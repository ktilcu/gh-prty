package main

import (
	"fmt"
	"strings"
	"time"
)

// PR is one open pull request, flattened out of the GitHub search response into
// only the fields a sidebar can actually show.
type PR struct {
	Number    int
	Title     string
	Repo      string // owner/name
	Author    string
	URL       string
	IsDraft   bool
	CreatedAt time.Time
	UpdatedAt time.Time

	// ReviewDecision is GitHub's own verdict on the review: APPROVED,
	// CHANGES_REQUESTED, REVIEW_REQUIRED, or "" on repos that require no review.
	ReviewDecision string
	// Checks is the status rollup of the head commit: SUCCESS, FAILURE, ERROR,
	// PENDING, EXPECTED, or "" when the repo runs nothing.
	Checks string

	Additions int
	Deletions int

	// Base and Head are the branch names, which are all a stack is: a PR whose
	// base is another open PR's head. CrossRepo marks a head on a fork, whose
	// branch names say nothing about this repo's branches.
	Base          string
	Head          string
	DefaultBranch string
	CrossRepo     bool

	// StackRoot is the key of the bottom PR of the stack this one is in, or ""
	// when it is not stacked. The rest place it within that stack: StackOn is
	// the PR directly under it (0 at the bottom), StackDepth counts up from 1 at
	// the bottom, and StackSize is how many PRs the stack holds.
	StackRoot  string
	StackOn    int
	StackDepth int
	StackSize  int

	// Mine is set for your own PRs, which changes what the state means: an
	// approved PR of yours is a merge waiting to happen, an approved PR of
	// someone else's is just done.
	Mine bool
	// NeedsMe is set when GitHub says this PR is waiting on your review, either
	// requested from you directly or from a team you are in. It clears itself
	// once you submit a review, which is what makes it trustworthy as a queue.
	NeedsMe bool
}

// Key identifies a PR across refreshes. The cursor tracks this rather than a row
// index, so a PR merging while you read does not slide the cursor onto its
// neighbour.
func (p PR) Key() string { return fmt.Sprintf("%s#%d", p.Repo, p.Number) }

// RepoName is the bare repository name. The owner is the same for every row in
// practice, so showing it would spend columns to say nothing.
func (p PR) RepoName() string {
	if i := strings.LastIndex(p.Repo, "/"); i >= 0 {
		return p.Repo[i+1:]
	}
	return p.Repo
}

// State is what a row is waiting on, which is the one thing worth reading off a
// sidebar. Each state carries a glyph so a narrow pane can drop the word.
type State struct {
	Glyph string
	Label string
	kind  stateKind
}

type stateKind int

const (
	kindNeutral stateKind = iota
	kindGood
	kindRunning
	kindActOnIt // blocked on a human: you, or the author
	kindBroken
)

var (
	stateNeedsYou = State{"!", "needs you", kindActOnIt}
	stateDraft    = State{"d", "draft", kindNeutral}
	stateChanges  = State{"!", "changes requested", kindActOnIt}
	stateFailing  = State{"x", "ci failed", kindBroken}
	stateReady    = State{"+", "ready to merge", kindGood}
	stateApproved = State{"+", "approved", kindGood}
	stateRunning  = State{"~", "checks running", kindRunning}
	stateWaiting  = State{"o", "awaiting review", kindNeutral}
)

// State resolves the one thing to say about a PR. The order is a priority, not a
// classification: a PR can be draft and failing and unreviewed at once, and the
// first match is the one you would act on first.
func (p PR) State() State {
	switch {
	// Your review is the only thing here that is blocked on you specifically, so
	// it outranks everything else the PR might also be.
	case p.NeedsMe:
		return stateNeedsYou
	case p.IsDraft:
		return stateDraft
	case p.ReviewDecision == "CHANGES_REQUESTED":
		return stateChanges
	case p.Checks == "FAILURE" || p.Checks == "ERROR":
		return stateFailing
	case p.ReviewDecision == "APPROVED":
		// Approved is the finish line on your own PR and merely a fact on
		// someone else's.
		if p.Mine {
			return stateReady
		}
		return stateApproved
	case p.Checks == "PENDING" || p.Checks == "EXPECTED":
		return stateRunning
	default:
		return stateWaiting
	}
}

// Age is how long the PR has been open, which is the number that ages badly. A
// PR sitting unreviewed for two weeks is exactly what a sidebar exists to
// surface.
func (p PR) Age() time.Duration {
	if p.CreatedAt.IsZero() {
		return 0
	}
	return time.Since(p.CreatedAt)
}

func (p PR) Idle() time.Duration {
	if p.UpdatedAt.IsZero() {
		return 0
	}
	return time.Since(p.UpdatedAt)
}
