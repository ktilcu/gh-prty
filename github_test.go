package main

import (
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
