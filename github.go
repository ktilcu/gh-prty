package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ghTimeout bounds every gh call. These are network calls behind a subprocess,
// and a hung one must never stall the UI.
const ghTimeout = 20 * time.Second

// Client fetches open PRs by shelling out to gh. That is the whole integration:
// gh already holds the auth, so there is no token to store, refresh, or leak.
type Client struct {
	// Org and Team scope the team feed. Either can be empty: with no team the
	// whole org is used, and with neither the team is resolved from your GitHub
	// team memberships on first load.
	Org  string
	Team string
	// Extra are logins to treat as teammates on top of the resolved team.
	Extra []string
	// Limit caps each search. GitHub allows 100 per search and a sidebar cannot
	// show anywhere near that.
	Limit int
	// MaxAge drops PRs opened longer ago than this, or keeps everything when
	// zero. Off by default: hiding rows is worse than showing stale ones, and
	// the list is newest-first so the stale ones sink out of the way anyway.
	MaxAge time.Duration

	mu       sync.Mutex
	resolved bool
	members  []string
}

// Feed is one refresh: the PRs, and the team they were resolved against so the
// header can say which team it is showing without a second lookup.
type Feed struct {
	PRs  []PR
	Team string
}

// Load fetches every open PR worth showing: yours, your team's, and anything
// waiting on your review.
func (c *Client) Load() (Feed, error) {
	org, team, members, err := c.resolveTeam()
	if err != nil {
		return Feed{}, err
	}

	limit := c.Limit
	if limit <= 0 {
		limit = 40
	}

	// One retry, because this query is expensive enough that GitHub answers 502
	// on it now and then. Without the retry a single blip shows as an error in
	// the pane; with it, the refresh just takes a second longer.
	raw, err := ghRetry(2, "api", "graphql", "-f",
		"query="+searchQuery(org, team, members, limit))
	if err != nil {
		return Feed{}, err
	}

	var resp response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Feed{}, fmt.Errorf("could not parse the gh response: %w", err)
	}
	if len(resp.Errors) > 0 {
		return Feed{}, errors.New("github: " + resp.Errors[0].Message)
	}

	label := team
	if label == "" {
		label = org
	}
	return Feed{PRs: c.trim(resp.merge()), Team: label}, nil
}

// trim applies MaxAge. A PR with no creation date is kept: an unparseable
// timestamp is not evidence the PR is old.
func (c *Client) trim(prs []PR) []PR {
	if c.MaxAge <= 0 {
		return prs
	}
	return slices.DeleteFunc(prs, func(p PR) bool {
		return !p.CreatedAt.IsZero() && time.Since(p.CreatedAt) > c.MaxAge
	})
}

// resolveTeam works out whose PRs count as the team's, once, and caches it. It
// is deliberately lazy rather than done at startup: a sidebar should paint
// immediately and fill in, not sit on a blank screen waiting for two round
// trips.
func (c *Client) resolveTeam() (org, team string, members []string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.resolved {
		return c.Org, c.Team, c.members, nil
	}

	// An explicit --team is taken at its word. Only an unconfigured run guesses.
	if c.Org == "" && c.Team == "" {
		org, team, err := largestTeam()
		if err != nil {
			return "", "", nil, err
		}
		c.Org, c.Team = org, team
	}

	if c.Team != "" && c.Org != "" {
		logins, err := teamMembers(c.Org, c.Team)
		if err != nil {
			return "", "", nil, err
		}
		c.members = logins
	}
	c.members = append(c.members, c.Extra...)
	slices.Sort(c.members)
	c.members = slices.Compact(c.members)

	c.resolved = true
	return c.Org, c.Team, c.members, nil
}

// largestTeam picks a default team from your memberships. Where there are
// several, the biggest one is the best guess at "my team" — the small ones tend
// to be owners and admin groups.
func largestTeam() (org, team string, err error) {
	raw, err := gh("api", "user/teams", "--paginate", "--jq",
		`.[] | "\(.organization.login)\t\(.slug)\t\(.members_count)"`)
	if err != nil {
		return "", "", err
	}

	best := -1
	for line := range strings.Lines(string(raw)) {
		fields := strings.Split(strings.TrimSpace(line), "\t")
		if len(fields) != 3 {
			continue
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil {
			continue
		}
		if size > best {
			org, team, best = fields[0], fields[1], size
		}
	}
	if best < 0 {
		return "", "", errors.New("no GitHub teams found; pass --team org/slug or --org")
	}
	return org, team, nil
}

func teamMembers(org, team string) ([]string, error) {
	raw, err := gh("api", fmt.Sprintf("orgs/%s/teams/%s/members", org, team),
		"--paginate", "--jq", ".[].login")
	if err != nil {
		return nil, err
	}

	var logins []string
	for line := range strings.Lines(string(raw)) {
		if login := strings.TrimSpace(line); login != "" {
			logins = append(logins, login)
		}
	}
	return logins, nil
}

// --- query ------------------------------------------------------------------

// prFields is the per-PR selection, shared by every search alias.
const prFields = `
    number title url isDraft createdAt updatedAt
    baseRefName headRefName isCrossRepository
    author { login }
    repository { nameWithOwner defaultBranchRef { name } }
    reviewDecision
    additions deletions
    commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }`

// searchQuery builds one GraphQL request holding every search at once. Four
// aliased searches cost a single HTTP round trip, which is what keeps the
// refresh cheap enough to run on a timer.
//
// The review searches exist because reviewDecision cannot answer "is this
// waiting on me" — it reports the PR's overall verdict, not whose turn it is.
// GitHub's own review-requested qualifiers can, and they clear themselves the
// moment you submit a review.
func searchQuery(org, team string, members []string, limit int) string {
	searches := []struct{ alias, query string }{
		{"mine", "author:@me"},
		{"review", "review-requested:@me"},
	}
	if org != "" && team != "" {
		searches = append(searches, struct{ alias, query string }{
			"teamReview", fmt.Sprintf("team-review-requested:%s/%s", org, team),
		})
	}
	if q := teamScope(org, members); q != "" {
		searches = append(searches, struct{ alias, query string }{"team", q})
	}

	var b strings.Builder
	b.WriteString("{\n")
	for _, s := range searches {
		fmt.Fprintf(&b, `  %s: search(query: "is:open is:pr archived:false %s", type: ISSUE, first: %d) {
    nodes { ... on PullRequest {%s
    } }
  }
`, s.alias, s.query, limit, prFields)
	}
	b.WriteString("}")
	return b.String()
}

// teamScope is the search fragment for the team feed. Naming the members is much
// better than scoping to the org alone, which sweeps up every bot PR in every
// repo and spends the row budget on dependency bumps.
func teamScope(org string, members []string) string {
	if len(members) == 0 {
		if org == "" {
			return ""
		}
		return "org:" + org + " -author:@me"
	}

	parts := make([]string, 0, len(members)+1)
	if org != "" {
		parts = append(parts, "org:"+org)
	}
	for _, m := range members {
		// Your own PRs have their own section, so asking for them again would
		// only burn the team search's row budget.
		parts = append(parts, "author:"+m)
	}
	return strings.Join(parts, " ") + " -author:@me"
}

// --- response ---------------------------------------------------------------

type response struct {
	Data struct {
		Mine       search `json:"mine"`
		Review     search `json:"review"`
		TeamReview search `json:"teamReview"`
		Team       search `json:"team"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type search struct {
	Nodes []node `json:"nodes"`
}

type node struct {
	Number     int                     `json:"number"`
	Title      string                  `json:"title"`
	URL        string                  `json:"url"`
	IsDraft    bool                    `json:"isDraft"`
	CreatedAt  time.Time               `json:"createdAt"`
	UpdatedAt  time.Time               `json:"updatedAt"`
	Author     *struct{ Login string } `json:"author"`
	Repository struct {
		NameWithOwner    string                 `json:"nameWithOwner"`
		DefaultBranchRef *struct{ Name string } `json:"defaultBranchRef"`
	} `json:"repository"`
	BaseRefName       string `json:"baseRefName"`
	HeadRefName       string `json:"headRefName"`
	IsCrossRepository bool   `json:"isCrossRepository"`
	ReviewDecision    string `json:"reviewDecision"`
	Additions         int    `json:"additions"`
	Deletions         int    `json:"deletions"`
	Commits           struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct{ State string } `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

func (n node) toPR() PR {
	p := PR{
		Number:         n.Number,
		Title:          n.Title,
		Repo:           n.Repository.NameWithOwner,
		URL:            n.URL,
		IsDraft:        n.IsDraft,
		CreatedAt:      n.CreatedAt,
		UpdatedAt:      n.UpdatedAt,
		ReviewDecision: n.ReviewDecision,
		Additions:      n.Additions,
		Deletions:      n.Deletions,
		Base:           n.BaseRefName,
		Head:           n.HeadRefName,
		CrossRepo:      n.IsCrossRepository,
	}
	// A deleted account leaves author null, a repo with no CI leaves the rollup
	// null, and an empty repo has no default branch. All are normal, so none may
	// panic.
	if n.Author != nil {
		p.Author = n.Author.Login
	}
	if n.Repository.DefaultBranchRef != nil {
		p.DefaultBranch = n.Repository.DefaultBranchRef.Name
	}
	if c := n.Commits.Nodes; len(c) > 0 && c[0].Commit.StatusCheckRollup != nil {
		p.Checks = c[0].Commit.StatusCheckRollup.State
	}
	return p
}

// merge folds the four searches into one list. The searches overlap by design —
// a teammate's PR can be both theirs and awaiting your review — so the same PR
// arrives more than once and the flags have to accumulate rather than overwrite.
func (r response) merge() []PR {
	byKey := make(map[string]*PR)
	var order []string

	add := func(nodes []node, mine, needsMe bool) {
		for _, n := range nodes {
			p := n.toPR()
			existing, seen := byKey[p.Key()]
			if !seen {
				byKey[p.Key()] = &p
				order = append(order, p.Key())
				existing = &p
			}
			existing.Mine = existing.Mine || mine
			existing.NeedsMe = existing.NeedsMe || needsMe
		}
	}

	add(r.Data.Mine.Nodes, true, false)
	add(r.Data.Review.Nodes, false, true)
	add(r.Data.TeamReview.Nodes, false, true)
	add(r.Data.Team.Nodes, false, false)

	prs := make([]PR, 0, len(order))
	for _, key := range order {
		p := *byKey[key]
		// You cannot review your own PR, so a review request landing on one is
		// noise from a stale request rather than something to act on.
		if p.Mine {
			p.NeedsMe = false
		}
		prs = append(prs, p)
	}

	// Newest first, and never by state. Sorting on state would reshuffle rows
	// under the cursor the moment a check went green, and createdAt is the only
	// field here that never changes while a PR is open.
	slices.SortFunc(prs, func(a, b PR) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Repo, b.Repo); c != 0 {
			return c
		}
		return cmp.Compare(a.Number, b.Number)
	})
	// Stacking runs on the merged list rather than per search, because a stack
	// can span them: yours stacked on a teammate's.
	return stack(prs)
}

// --- process ----------------------------------------------------------------

// ghRetry runs gh, retrying only the failures that a second attempt can fix.
// Retrying a real error — bad credentials, an unknown team — would just make the
// UI slow to tell you what is wrong.
func ghRetry(attempts int, args ...string) ([]byte, error) {
	var err error
	for i := range attempts {
		var out []byte
		if out, err = gh(args...); err == nil {
			return out, nil
		}
		if !transient(err) {
			return nil, err
		}
		if i < attempts-1 {
			time.Sleep(time.Second)
		}
	}
	return nil, err
}

// transient covers the gateway errors GitHub returns when a query takes too long
// server-side, plus our own timeout.
func transient(err error) bool {
	msg := err.Error()
	for _, s := range []string{"HTTP 502", "HTTP 503", "HTTP 504", "timed out"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

func gh(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "gh", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err == nil {
		return out, nil
	}

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("gh timed out after %s", ghTimeout)
	}
	if errors.Is(err, exec.ErrNotFound) {
		return nil, errors.New("gh is not installed; see cli.github.com")
	}
	// gh puts the useful part on stderr: a GraphQL error, or a prompt to log in.
	if msg := firstLine(stderr.String()); msg != "" {
		return nil, errors.New(msg)
	}
	return nil, err
}

func firstLine(s string) string {
	for line := range strings.Lines(s) {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// openURL and copyURL are the two things worth doing to a selected row, and both
// are fire-and-forget: the UI reports failure but never waits on either.
func openURL(url string) error {
	return exec.Command("open", url).Run()
}

func copyURL(url string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(url)
	return cmd.Run()
}
