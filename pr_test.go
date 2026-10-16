package main

import (
	"fmt"
	"testing"
	"unicode/utf8"
)

func TestStatePriority(t *testing.T) {
	tests := []struct {
		name string
		pr   PR
		want State
	}{{
		// A review request outranks everything, including the PR being a draft
		// or failing: those are the author's problem, your review is yours.
		name: "your review outranks a failing draft",
		pr:   PR{NeedsMe: true, IsDraft: true, Checks: "FAILURE"},
		want: stateNeedsYou,
	}, {
		name: "draft",
		pr:   PR{IsDraft: true, Mine: true},
		want: stateDraft,
	}, {
		name: "changes requested outranks a failing build",
		pr:   PR{Mine: true, ReviewDecision: "CHANGES_REQUESTED", Checks: "FAILURE"},
		want: stateChanges,
	}, {
		// The approval is stale the moment CI goes red, so saying "ready to
		// merge" here would be actively wrong.
		name: "approved but failing is not ready",
		pr:   PR{Mine: true, ReviewDecision: "APPROVED", Checks: "FAILURE"},
		want: stateFailing,
	}, {
		name: "errored checks count as failing",
		pr:   PR{Mine: true, Checks: "ERROR"},
		want: stateFailing,
	}, {
		name: "approved on yours is ready to merge",
		pr:   PR{Mine: true, ReviewDecision: "APPROVED", Checks: "SUCCESS"},
		want: stateReady,
	}, {
		name: "approved on theirs is just approved",
		pr:   PR{ReviewDecision: "APPROVED", Checks: "SUCCESS"},
		want: stateApproved,
	}, {
		name: "checks still running",
		pr:   PR{Mine: true, Checks: "PENDING"},
		want: stateRunning,
	}, {
		// A repo with no CI leaves the rollup empty, which must not read as
		// pending forever.
		name: "no checks configured is not pending",
		pr:   PR{Mine: true, ReviewDecision: "REVIEW_REQUIRED"},
		want: stateWaiting,
	}, {
		// The approval is the good news; the open threads are what is left
		// before it is really ready.
		name: "approved with open threads is not ready to merge",
		pr:   PR{Mine: true, ReviewDecision: "APPROVED", Checks: "SUCCESS", Unresolved: 2},
		want: stateApprovedOpen(2),
	}, {
		name: "a failing build outranks open threads on an approval",
		pr:   PR{Mine: true, ReviewDecision: "APPROVED", Checks: "FAILURE", Unresolved: 2},
		want: stateFailing,
	}, {
		name: "draft outranks open threads",
		pr:   PR{Mine: true, IsDraft: true, Unresolved: 1},
		want: stateDraft,
	}, {
		name: "open threads without an approval",
		pr:   PR{Mine: true, ReviewDecision: "REVIEW_REQUIRED", Unresolved: 3},
		want: stateUnresolved(3),
	}, {
		// Checks finish on their own; the threads wait for you.
		name: "open threads outrank running checks",
		pr:   PR{Mine: true, Checks: "PENDING", Unresolved: 1},
		want: stateUnresolved(1),
	}, {
		name: "changes requested outranks open threads",
		pr:   PR{Mine: true, ReviewDecision: "CHANGES_REQUESTED", Unresolved: 4},
		want: stateChanges,
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.pr.State(); got != tt.want {
				t.Errorf("State() = %q, want %q", got.Label, tt.want.Label)
			}
		})
	}
}

func TestRepoName(t *testing.T) {
	for in, want := range map[string]string{
		"acme/api": "api",
		"no-slash": "no-slash",
		"":         "",
	} {
		if got := (PR{Repo: in}).RepoName(); got != want {
			t.Errorf("RepoName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKeyDistinguishesReposAndNumbers(t *testing.T) {
	a := PR{Repo: "org/one", Number: 5}
	b := PR{Repo: "org/two", Number: 5}
	c := PR{Repo: "org/one", Number: 6}

	if a.Key() == b.Key() {
		t.Error("same number in different repos collided")
	}
	if a.Key() == c.Key() {
		t.Error("different numbers in the same repo collided")
	}
}

// The count lives in the label, and the state column is a fixed width. A label
// one column too wide pushes the age and author out of line on that row only.
func TestCountedLabelsFitTheStateColumn(t *testing.T) {
	for _, n := range []int{1, 9, 10, 99, 100, 1000} {
		for _, s := range []State{stateApprovedOpen(n), stateUnresolved(n)} {
			if w := utf8.RuneCountInString(s.Label); w > stateWidth {
				t.Errorf("%q is %d columns, the state column is %d", s.Label,
					w, stateWidth)
			}
		}
	}
	if got := stateApprovedOpen(2).Label; got != "approved · 2 open" {
		t.Errorf("label = %q", got)
	}
	if got := stateUnresolved(3).Label; got != "3 unresolved" {
		t.Errorf("label = %q", got)
	}
}

// Approved with comments is good news and reads as its own colour, not as a
// shade of either ready or blocked.
func TestReviewStatesHaveTheirOwnColours(t *testing.T) {
	kinds := []stateKind{kindGood, kindActOnIt, kindBroken, kindRunning,
		kindApprovedOpen, kindUnresolved}
	seen := map[string]stateKind{}
	for _, k := range kinds {
		fg := fmt.Sprint(styleFor(k).GetForeground())
		if other, dup := seen[fg]; dup {
			t.Errorf("kinds %d and %d share colour %s", k, other, fg)
		}
		seen[fg] = k
	}
	// Narrow panes drop the label, so the glyph alone has to tell them apart.
	glyphs := map[string]bool{}
	for _, s := range []State{stateNeedsYou, stateDraft, stateFailing, stateReady,
		stateRunning, stateWaiting, stateApprovedOpen(1), stateUnresolved(1)} {
		glyphs[s.Glyph] = true
	}
	if len(glyphs) != 8 {
		t.Errorf("only %d distinct glyphs across 8 states", len(glyphs))
	}
}
