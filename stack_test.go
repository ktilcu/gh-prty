package main

import (
	"slices"
	"testing"
	"time"
)

// branch builds a PR in org/app opened `age` hours ago, on base and from head.
func branch(number, age int, base, head string) PR {
	return PR{
		Repo: "org/app", Number: number, Base: base, Head: head,
		DefaultBranch: "main",
		CreatedAt:     time.Now().Add(-time.Duration(age) * time.Hour),
	}
}

func numbers(prs []PR) []int {
	out := make([]int, 0, len(prs))
	for _, p := range prs {
		out = append(out, p.Number)
	}
	return out
}

func byNumber(prs []PR, n int) PR {
	i := slices.IndexFunc(prs, func(p PR) bool { return p.Number == n })
	if i < 0 {
		return PR{}
	}
	return prs[i]
}

// The real shape that prompted this: a four-deep stack whose bottom half is
// weeks older than its top, with an unrelated PR opened in between. Sorted
// newest-first alone, #1507 and #1402 land far below the rest of their stack.
func TestStackKeepsAStackTogetherAtItsNewestMember(t *testing.T) {
	prs := []PR{
		branch(1567, 22, "honor-status", "revoke-tokens"),
		branch(1565, 23, "provenance", "honor-status"),
		branch(1562, 24, "main", "rasterize"),
		branch(1507, 400, "accept-logins", "provenance"),
		branch(1402, 1200, "main", "accept-logins"),
	}

	got := stack(prs)
	if want := []int{1567, 1565, 1507, 1402, 1562}; !slices.Equal(numbers(got), want) {
		t.Fatalf("order = %v, want %v", numbers(got), want)
	}

	root := byNumber(got, 1402).Key()
	for _, tt := range []struct{ number, on, depth int }{
		{1567, 1565, 4},
		{1565, 1507, 3},
		{1507, 1402, 2},
		{1402, 0, 1},
	} {
		p := byNumber(got, tt.number)
		if p.StackRoot != root || p.StackOn != tt.on || p.StackDepth != tt.depth || p.StackSize != 4 {
			t.Errorf("#%d: root %q on %d depth %d size %d, want %q on %d depth %d size 4",
				tt.number, p.StackRoot, p.StackOn, p.StackDepth, p.StackSize,
				root, tt.on, tt.depth)
		}
	}

	if p := byNumber(got, 1562); p.StackRoot != "" || p.StackSize != 0 {
		t.Errorf("#1562 stands alone but got root %q size %d", p.StackRoot, p.StackSize)
	}
}

// Ordering must still rest on createdAt alone. With no stacks in play, stack
// must be a no-op on the newest-first order merge already produced.
func TestStackLeavesUnstackedOrderAlone(t *testing.T) {
	prs := []PR{
		branch(3, 1, "main", "c"),
		branch(2, 2, "main", "b"),
		branch(1, 3, "main", "a"),
	}
	if got := numbers(stack(prs)); !slices.Equal(got, []int{3, 2, 1}) {
		t.Errorf("order = %v, want unchanged", got)
	}
}

// A fork's PR from its own main has head "main", and every ordinary PR has base
// "main". Matching on branch name alone would stack the whole repo on the fork.
func TestStackIgnoresForkHeadsAndTheDefaultBranch(t *testing.T) {
	fork := branch(9, 1, "main", "main")
	fork.CrossRepo = true
	forkTopic := branch(8, 2, "main", "patch-1")
	forkTopic.CrossRepo = true

	got := stack([]PR{
		fork,
		forkTopic,
		branch(7, 3, "main", "feature"),
		branch(6, 4, "patch-1", "on-the-fork"), // same name as the fork's head
	})
	for _, p := range got {
		if p.StackRoot != "" || p.StackOn != 0 {
			t.Errorf("#%d was stacked (root %q on %d) on a fork or the default branch",
				p.Number, p.StackRoot, p.StackOn)
		}
	}
}

// Branch names repeat across repos, so a match only counts within one repo.
func TestStackDoesNotLinkAcrossRepos(t *testing.T) {
	parent := branch(2, 2, "main", "shared-name")
	parent.Repo = "org/other"
	child := branch(1, 1, "shared-name", "child")

	for _, p := range stack([]PR{child, parent}) {
		if p.StackRoot != "" {
			t.Errorf("#%d in %s stacked across repos", p.Number, p.Repo)
		}
	}
}

// A parent outside the feed — someone else's, or older than --max-age — leaves
// the child with no loaded PR to point at. It is not drawn as a stack, but its
// base survives so the detail line can still say it does not target main.
func TestStackWithAMissingParent(t *testing.T) {
	got := stack([]PR{branch(5, 1, "someone-elses-branch", "mine")})
	p := got[0]
	if p.StackRoot != "" || p.StackOn != 0 || p.StackSize != 0 {
		t.Errorf("orphan got root %q on %d size %d, want none", p.StackRoot, p.StackOn, p.StackSize)
	}
	if p.Base != "someone-elses-branch" {
		t.Errorf("base = %q, the detail line needs it", p.Base)
	}
}

// gh-stack keeps stacks linear, but a hand-made stack can branch. Every member
// must still come out, once, in one contiguous run.
func TestStackKeepsABranchingStackContiguous(t *testing.T) {
	got := stack([]PR{
		branch(4, 1, "base", "left"),
		branch(3, 2, "main", "elsewhere"),
		branch(2, 3, "base", "right"),
		branch(1, 4, "main", "base"),
	})

	if len(got) != 4 {
		t.Fatalf("got %d PRs, want 4: %v", len(got), numbers(got))
	}
	idx := map[int]int{}
	for i, p := range got {
		idx[p.Number] = i
	}
	span := max(idx[4], idx[2], idx[1]) - min(idx[4], idx[2], idx[1])
	if span != 2 {
		t.Errorf("stack split up: %v", numbers(got))
	}
	if byNumber(got, 4).StackSize != 3 || byNumber(got, 2).StackOn != 1 {
		t.Errorf("branch members misattributed: %+v", got)
	}
}

// Two PRs each based on the other's head cannot happen through gh-stack, but a
// force-push or a retarget can leave it behind. It must not hang the refresh.
func TestStackSurvivesACycle(t *testing.T) {
	done := make(chan []PR)
	go func() {
		done <- stack([]PR{
			branch(2, 1, "a", "b"),
			branch(1, 2, "b", "a"),
		})
	}()

	select {
	case got := <-done:
		if len(got) != 2 {
			t.Errorf("cycle dropped PRs: %v", numbers(got))
		}
	case <-time.After(time.Second):
		t.Fatal("stack hung on a cycle")
	}
}
