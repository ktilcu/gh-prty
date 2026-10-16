package main

import "slices"

// stack links PRs stacked on one another and pulls each stack into one
// contiguous run, tip first, so it reads top to bottom like git log.
//
// The signal is the base branch, not the gh-stack footer: a PR whose base is
// another open PR's head is stacked on it. That covers stacks made by hand or
// adopted after the fact, which carry no footer at all.
//
// A stack sits where its newest member would have, which is the same rule
// repoOrder uses for groups. Like everything else that orders rows, it derives
// from createdAt alone, so a check going green cannot move a stack.
func stack(prs []PR) []PR {
	parent := parents(prs)

	// Walk each PR down to the bottom of its stack, counting as it goes.
	root := make([]int, len(prs))
	depth := make([]int, len(prs))
	size := make(map[int]int)
	for i := range prs {
		r, d := i, 1
		for parent[r] >= 0 {
			r, d = parent[r], d+1
		}
		root[i], depth[i] = r, d
		size[r]++
	}

	out := make([]PR, 0, len(prs))
	placed := make(map[int]bool)
	for i, p := range prs {
		r := root[i]
		// A bottom PR with nothing on top of it is not a stack, just a PR.
		if size[r] == 1 {
			out = append(out, p)
			continue
		}
		if placed[r] {
			continue
		}
		placed[r] = true

		// The first member reached is the newest, so this is where the whole
		// stack goes. Within it, deepest first puts the tip at the top.
		var members []int
		for j := range prs {
			if root[j] == r {
				members = append(members, j)
			}
		}
		slices.SortStableFunc(members, func(a, b int) int { return depth[b] - depth[a] })

		for _, j := range members {
			m := prs[j]
			m.StackRoot = prs[r].Key()
			m.StackDepth = depth[j]
			m.StackSize = size[r]
			if parent[j] >= 0 {
				m.StackOn = prs[parent[j]].Number
			}
			out = append(out, m)
		}
	}
	return out
}

// parents finds the index of the PR each one is stacked on, or -1.
func parents(prs []PR) []int {
	type branch struct{ repo, name string }

	// A fork's head names a branch on the fork, so it can never be what a PR in
	// this repo is based on — and a fork PR from its own main would otherwise
	// become the parent of every PR in the repo.
	heads := make(map[branch]int, len(prs))
	for i, p := range prs {
		if !p.CrossRepo && p.Head != "" {
			heads[branch{p.Repo, p.Head}] = i
		}
	}

	parent := make([]int, len(prs))
	for i, p := range prs {
		parent[i] = -1
		if p.Base == "" || p.Base == p.DefaultBranch {
			continue
		}
		if j, ok := heads[branch{p.Repo, p.Base}]; ok && j != i {
			parent[i] = j
		}
	}

	// gh-stack cannot make a cycle, but a retarget or force-push can leave one
	// behind. Cut each where it closes, or walking down a stack never ends.
	for i := range prs {
		seen := map[int]bool{i: true}
		for r := i; parent[r] >= 0; r = parent[r] {
			if seen[parent[r]] {
				parent[r] = -1
				break
			}
			seen[parent[r]] = true
		}
	}
	return parent
}
