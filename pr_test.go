package main

import "testing"

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
		"no-slash":                     "no-slash",
		"":                             "",
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
