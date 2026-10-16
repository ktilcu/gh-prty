package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

func node1(repo string, number int, created string) node {
	n := node{Number: number}
	n.Repository.NameWithOwner = repo
	n.CreatedAt, _ = time.Parse(time.RFC3339, created)
	return n
}

// The searches overlap on purpose, so the flags have to accumulate across them
// rather than the last write winning.
func TestMergeAccumulatesFlagsAcrossSearches(t *testing.T) {
	var r response
	r.Data.Team.Nodes = []node{node1("org/app", 1, "2026-08-01T00:00:00Z")}
	r.Data.Review.Nodes = []node{node1("org/app", 1, "2026-08-01T00:00:00Z")}

	prs := r.merge()
	if len(prs) != 1 {
		t.Fatalf("got %d PRs, want the two copies deduped into 1", len(prs))
	}
	if !prs[0].NeedsMe {
		t.Error("a teammate's PR awaiting your review lost its NeedsMe flag")
	}
	if prs[0].Mine {
		t.Error("a teammate's PR was marked as yours")
	}
}

func TestMergeKeepsOwnPRsOutOfYourReviewQueue(t *testing.T) {
	var r response
	r.Data.Mine.Nodes = []node{node1("org/app", 1, "2026-08-01T00:00:00Z")}
	// A stale request can leave you as a reviewer on your own PR, which is not
	// something you can act on.
	r.Data.Review.Nodes = []node{node1("org/app", 1, "2026-08-01T00:00:00Z")}

	prs := r.merge()
	if len(prs) != 1 {
		t.Fatalf("got %d PRs, want 1", len(prs))
	}
	if !prs[0].Mine {
		t.Error("your own PR lost its Mine flag")
	}
	if prs[0].NeedsMe {
		t.Error("you cannot review your own PR, but it was queued for you")
	}
}

// Ordering must depend only on fields that do not change while a PR is open,
// or rows slide under the cursor as CI finishes.
func TestMergeSortsNewestFirst(t *testing.T) {
	var r response
	r.Data.Mine.Nodes = []node{
		node1("org/app", 1, "2026-01-01T00:00:00Z"),
		node1("org/app", 3, "2026-08-01T00:00:00Z"),
		node1("org/app", 2, "2026-04-01T00:00:00Z"),
	}

	got := []int{}
	for _, p := range r.merge() {
		got = append(got, p.Number)
	}
	if want := []int{3, 2, 1}; !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestToPRSurvivesNullAuthorAndChecks(t *testing.T) {
	// A deleted account leaves author null and a repo without CI leaves the
	// rollup null. Both are normal, and neither may panic.
	p := node1("org/app", 1, "2026-08-01T00:00:00Z").toPR()
	if p.Author != "" || p.Checks != "" {
		t.Errorf("got author %q checks %q, want both empty", p.Author, p.Checks)
	}
}

func TestToPRReadsTheCheckRollup(t *testing.T) {
	n := node1("org/app", 1, "2026-08-01T00:00:00Z")
	n.Author = &struct{ Login string }{Login: "someone"}
	n.Commits.Nodes = []struct {
		Commit struct {
			StatusCheckRollup *struct{ State string } `json:"statusCheckRollup"`
		} `json:"commit"`
	}{{}}
	n.Commits.Nodes[0].Commit.StatusCheckRollup = &struct{ State string }{State: "FAILURE"}

	p := n.toPR()
	if p.Checks != "FAILURE" || p.Author != "someone" {
		t.Errorf("got author %q checks %q", p.Author, p.Checks)
	}
}

func TestTeamScope(t *testing.T) {
	got := teamScope("acme", []string{"ann", "bob"})
	for _, want := range []string{"org:acme", "author:ann", "author:bob", "-author:@me"} {
		if !strings.Contains(got, want) {
			t.Errorf("teamScope() = %q, missing %q", got, want)
		}
	}

	// With no members there is nobody to name, so the org is the only scope left.
	if got := teamScope("acme", nil); got != "org:acme -author:@me" {
		t.Errorf("orgless fallback = %q", got)
	}
	// With neither there is no team feed to ask for at all.
	if got := teamScope("", nil); got != "" {
		t.Errorf("teamScope(\"\", nil) = %q, want empty", got)
	}
}

func TestSearchQueryOmitsTeamAliasesWithoutATeam(t *testing.T) {
	q := searchQuery("", "", nil, 10)
	if !strings.Contains(q, "mine:") || !strings.Contains(q, "review:") {
		t.Error("the always-available searches are missing")
	}
	// Asking for an alias with an empty qualifier would make GitHub reject the
	// whole request, taking your own PRs down with it.
	if strings.Contains(q, "team:") || strings.Contains(q, "teamReview:") {
		t.Errorf("team aliases survived without a team:\n%s", q)
	}
}

func TestTrimDropsOldButKeepsUndated(t *testing.T) {
	c := &Client{MaxAge: 30 * 24 * time.Hour}
	got := c.trim([]PR{
		{Number: 1, CreatedAt: time.Now().Add(-time.Hour)},
		{Number: 2, CreatedAt: time.Now().Add(-90 * 24 * time.Hour)},
		{Number: 3}, // no date is not evidence of being old
	})

	nums := []int{}
	for _, p := range got {
		nums = append(nums, p.Number)
	}
	if want := []int{1, 3}; !slices.Equal(nums, want) {
		t.Errorf("trim kept %v, want %v", nums, want)
	}

	// Off by default.
	all := []PR{{CreatedAt: time.Now().Add(-9999 * time.Hour)}}
	if got := (&Client{}).trim(all); len(got) != 1 {
		t.Error("MaxAge 0 dropped a row")
	}
}

func TestTransientOnlyRetriesGatewayErrors(t *testing.T) {
	for _, msg := range []string{"gh: HTTP 502", "HTTP 503", "gh timed out after 20s"} {
		if !transient(errString(msg)) {
			t.Errorf("%q should be retried", msg)
		}
	}
	// Retrying these would only slow down telling you what is actually wrong.
	for _, msg := range []string{"gh: HTTP 401 Bad credentials", "HTTP 404 Not Found"} {
		if transient(errString(msg)) {
			t.Errorf("%q should not be retried", msg)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestToPRReadsBranches(t *testing.T) {
	n := node1("org/app", 2, "2026-08-01T00:00:00Z")
	n.BaseRefName, n.HeadRefName = "parent-branch", "child-branch"
	n.IsCrossRepository = true
	n.Repository.DefaultBranchRef = &struct{ Name string }{Name: "trunk"}

	p := n.toPR()
	if p.Base != "parent-branch" || p.Head != "child-branch" ||
		p.DefaultBranch != "trunk" || !p.CrossRepo {
		t.Errorf("got base %q head %q default %q cross %v",
			p.Base, p.Head, p.DefaultBranch, p.CrossRepo)
	}

	// An empty repo has no default branch, and that must not panic either.
	if p := node1("org/app", 3, "2026-08-01T00:00:00Z").toPR(); p.DefaultBranch != "" {
		t.Errorf("default branch = %q, want empty", p.DefaultBranch)
	}
}

// A stack can span sections — yours stacked on a teammate's — so stacking has
// to happen on the merged list, not per search.
func TestMergeLinksStacksAcrossSearches(t *testing.T) {
	parent := node1("org/app", 1, "2026-08-01T00:00:00Z")
	parent.BaseRefName, parent.HeadRefName = "main", "theirs"
	child := node1("org/app", 2, "2026-08-02T00:00:00Z")
	child.BaseRefName, child.HeadRefName = "theirs", "mine"
	for _, n := range []*node{&parent, &child} {
		n.Repository.DefaultBranchRef = &struct{ Name string }{Name: "main"}
	}

	var r response
	r.Data.Mine.Nodes = []node{child}
	r.Data.Team.Nodes = []node{parent}

	prs := r.merge()
	if len(prs) != 2 || prs[0].Number != 2 || prs[0].StackOn != 1 {
		t.Errorf("merge did not stack #2 on #1: %+v", prs)
	}
}

func decode(t *testing.T, raw string) node {
	t.Helper()
	var n node
	if err := json.Unmarshal([]byte(raw), &n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestToPRCountsUnresolvedThreads(t *testing.T) {
	p := decode(t, `{"reviewThreads": {"nodes": [
		{"isResolved": false, "isOutdated": false},
		{"isResolved": false, "isOutdated": true},
		{"isResolved": true,  "isOutdated": true},
		{"isResolved": true,  "isOutdated": false}
	]}}`).toPR()

	// An outdated thread still counts: the code moved under it, which usually
	// means it was addressed, but the reviewer still sees it open.
	if p.Unresolved != 2 || p.Outdated != 1 {
		t.Errorf("unresolved %d outdated %d, want 2 and 1", p.Unresolved, p.Outdated)
	}
}

// A repo that requires no review leaves reviewDecision empty even after an
// approval, which used to read as "awaiting review" on an approved PR.
func TestToPRFallsBackToTheLatestReviews(t *testing.T) {
	for _, tt := range []struct {
		name, raw, want string
	}{{
		name: "an approval",
		raw: `{"latestReviews": {"nodes": [
			{"state": "COMMENTED"}, {"state": "APPROVED"}]}}`,
		want: "APPROVED",
	}, {
		name: "changes requested outweighs an approval",
		raw: `{"latestReviews": {"nodes": [
			{"state": "APPROVED"}, {"state": "CHANGES_REQUESTED"}]}}`,
		want: "CHANGES_REQUESTED",
	}, {
		name: "comments alone decide nothing",
		raw:  `{"latestReviews": {"nodes": [{"state": "COMMENTED"}]}}`,
		want: "",
	}, {
		// GitHub's own verdict wins when there is one: a repo needing two
		// approvals says REVIEW_REQUIRED after the first.
		name: "an explicit decision is kept",
		raw: `{"reviewDecision": "REVIEW_REQUIRED",
			"latestReviews": {"nodes": [{"state": "APPROVED"}]}}`,
		want: "REVIEW_REQUIRED",
	}} {
		t.Run(tt.name, func(t *testing.T) {
			if got := decode(t, tt.raw).toPR().ReviewDecision; got != tt.want {
				t.Errorf("ReviewDecision = %q, want %q", got, tt.want)
			}
		})
	}
}

// An approval with a written note has nothing to resolve, but the note might
// still matter, so it is kept for the detail line.
func TestToPRKeepsWhoLeftANoteWithTheirApproval(t *testing.T) {
	p := decode(t, `{"latestReviews": {"nodes": [
		{"state": "APPROVED", "body": "one nit before merging", "author": {"login": "ann"}},
		{"state": "APPROVED", "body": "", "author": {"login": "bob"}},
		{"state": "COMMENTED", "body": "a question", "author": {"login": "cat"}},
		{"state": "APPROVED", "body": "lgtm", "author": null}
	]}}`).toPR()
	if !slices.Equal(p.Notes, []string{"ann"}) {
		t.Errorf("notes = %v, want [ann]", p.Notes)
	}
}

// Threads are fetched for your own PRs only. Asking for them on every search
// multiplies the cost of a query GitHub already 502s on now and then.
func TestSearchQueryAsksForThreadsOnlyOnYourPRs(t *testing.T) {
	q := searchQuery("acme", "eng", []string{"ann"}, 10)
	if n := strings.Count(q, "reviewThreads"); n != 1 {
		t.Fatalf("reviewThreads appears %d times, want once", n)
	}
	mine := q[strings.Index(q, "mine:"):strings.Index(q, "review:")]
	if !strings.Contains(mine, "reviewThreads") {
		t.Errorf("threads are not on the mine search:\n%s", q)
	}
	if strings.Count(q, "latestReviews") != 4 {
		t.Errorf("every search needs latestReviews for the decision fallback")
	}
}
