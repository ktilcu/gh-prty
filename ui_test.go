package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestShortNeverExceedsFourColumns(t *testing.T) {
	const day = 24 * time.Hour
	for _, d := range []time.Duration{
		0, -time.Hour, time.Second, 59 * time.Second, time.Minute,
		59 * time.Minute, time.Hour, 23 * time.Hour, day, 364 * day,
		365 * day, 5153 * day, // a real 14-year-old PR blew the column before this
		100 * 365 * day,
	} {
		if got := short(d); len(got) > 4 {
			t.Errorf("short(%v) = %q, %d columns, want at most 4", d, got, len(got))
		}
	}
}

func TestShortUnits(t *testing.T) {
	const day = 24 * time.Hour
	for _, tt := range []struct {
		in   time.Duration
		want string
	}{
		{0, "-"},
		{30 * time.Second, "30s"},
		{90 * time.Minute, "1h"},
		{3 * day, "3d"},
		{364 * day, "364d"},
		{400 * day, "1y"},
		{5153 * day, "14y"},
	} {
		if got := short(tt.in); got != tt.want {
			t.Errorf("short(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// Every row must fit the pane exactly. A row one column too wide wraps, and a
// single wrapped row desynchronises every row under it.
func TestRowsFitTheirWidth(t *testing.T) {
	prs := []PR{{
		Repo: "acme/api", Number: 1367, Mine: true,
		Title:     strings.Repeat("a long pull request title ", 6),
		Author:    "someone-with-a-long-login",
		CreatedAt: time.Now().Add(-5153 * 24 * time.Hour),
		UpdatedAt: time.Now().Add(-time.Hour),
	}, {
		Repo: "acme/web", Number: 99999, NeedsMe: true,
		Title:     "short",
		Author:    "teammate",
		CreatedAt: time.Now().Add(-time.Minute),
	}}

	for _, width := range []int{30, 40, 46, 60, 75, 76, 80, 99, 100, 140, 200} {
		m := model{client: &Client{}, prs: prs, loaded: true, width: width}
		m.ensureSelection()

		for line := range strings.SplitSeq(m.View(), "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Errorf("width %d: line is %d columns:\n%q", width, got, line)
			}
		}
	}
}

// View clips every line, so it would hide a row that is genuinely too wide by
// eating the age column instead of wrapping. Check the rows before the clip.
func TestRowsFitBeforeClipping(t *testing.T) {
	prs := []PR{
		{Repo: "org/app", Number: 999999, Mine: true, Title: "a six figure number",
			CreatedAt: time.Now().Add(-5153 * 24 * time.Hour)},
		{Repo: "org/a-rather-long-repo-name", Number: 1, Author: "a-long-login-here",
			Title:          "changes requested is the longest state label",
			ReviewDecision: "CHANGES_REQUESTED", CreatedAt: time.Now()},
	}

	for _, width := range []int{40, 46, 60, 76, 80, 100, 140} {
		m := model{client: &Client{}, prs: prs, loaded: true, width: width}
		m.ensureSelection()
		l := layoutFor(width)

		for _, r := range m.rows(l, width) {
			line := m.render(r, width, l)
			if got := lipgloss.Width(line); got > width {
				t.Errorf("width %d: unclipped row is %d columns:\n%q", width, got, line)
			}
		}
	}
}

func TestLayoutSwitchesAtCompactBelow(t *testing.T) {
	if !layoutFor(compactBelow - 1).compact {
		t.Error("narrow pane did not get the compact layout")
	}
	if layoutFor(compactBelow).compact {
		t.Error("wide pane got the compact layout")
	}
	// Grouping by repo freed the repo column, and the author took its place, so
	// every non-compact width now has room for it.
	if layoutFor(compactBelow - 1).showAuthor {
		t.Error("compact layout claimed room for an author column")
	}
	if !layoutFor(compactBelow).showAuthor {
		t.Error("author column missing at the narrowest table width")
	}
	// The title column has to stay legible however narrow the pane gets.
	if got := layoutFor(20).title; got < 8 {
		t.Errorf("title width collapsed to %d at 20 columns", got)
	}
}

// Group order must derive only from fields that hold still while a PR is open,
// or a check going green would reorder the groups under the cursor.
func TestGroupByRepoOrdersByFirstAppearance(t *testing.T) {
	prs := []PR{
		{Repo: "org/beta", Number: 9},
		{Repo: "org/alpha", Number: 8},
		{Repo: "org/beta", Number: 7},
		{Repo: "org/gamma", Number: 6},
		{Repo: "org/alpha", Number: 5},
	}

	got := groupByRepo(prs, repoOrder(prs))
	if len(got) != 3 {
		t.Fatalf("got %d groups, want 3", len(got))
	}
	if want := []string{"beta", "alpha", "gamma"}; !slices.Equal(
		[]string{got[0].repo, got[1].repo, got[2].repo}, want) {
		t.Errorf("group order = %v, want %v", got, want)
	}
	// Rows keep the order they arrived in within their group.
	if nums := []int{got[0].prs[0].Number, got[0].prs[1].Number}; !slices.Equal(
		nums, []int{9, 7}) {
		t.Errorf("beta rows = %v, want [9 7]", nums)
	}
	if len(got[1].prs) != 2 || len(got[2].prs) != 1 {
		t.Errorf("group sizes = %d, %d, %d",
			len(got[0].prs), len(got[1].prs), len(got[2].prs))
	}
}

// A repo must sit in the same place under MINE as under TEAM. Ordering each
// section by its own newest PR would put the same few repos in two different
// orders, which is harder to scan than no grouping at all.
func TestRepoOrderIsTheSameInEverySection(t *testing.T) {
	now := time.Now()
	// api holds the newest PR overall, but only mine in app and only the team's
	// in api — so per-section ordering would disagree between the sections.
	m := model{client: &Client{}, loaded: true, width: 100, prs: []PR{
		{Repo: "org/api", Number: 4, Author: "t", CreatedAt: now},
		{Repo: "org/app", Number: 3, Mine: true, CreatedAt: now.Add(-time.Hour)},
		{Repo: "org/api", Number: 2, Mine: true, CreatedAt: now.Add(-2 * time.Hour)},
		{Repo: "org/app", Number: 1, Author: "t", CreatedAt: now.Add(-3 * time.Hour)},
	}}
	m.ensureSelection()

	perSection := map[string][]string{}
	section := ""
	for _, r := range m.rows(layoutFor(100), 100) {
		switch r.kind {
		case rowHeader:
			section = r.text
		case rowGroup:
			perSection[section] = append(perSection[section], r.text)
		}
	}

	want := []string{"api", "app"}
	for _, s := range []string{"MINE", "TEAM"} {
		if !slices.Equal(perSection[s], want) {
			t.Errorf("%s repo order = %v, want %v", s, perSection[s], want)
		}
	}
}

// The invariant that repo grouping broke: j/k walk visible(), the screen draws
// rows(), and if those two disagree the cursor jumps several rows at a time.
// Interleaving repos is what makes them disagree, so the fixture does that.
func TestCursorOrderMatchesDrawnOrder(t *testing.T) {
	repos := []string{"org/app", "org/api", "org/web"}
	var prs []PR
	for i := range 18 {
		prs = append(prs, PR{
			Repo: repos[i%3], Number: 100 - i, Mine: i%2 == 0,
			Author: "teammate", Title: "title",
			CreatedAt: time.Now().Add(-time.Duration(i) * time.Hour),
		})
	}

	for _, sc := range []scope{scopeAll, scopeMine, scopeTeam} {
		m := model{client: &Client{}, prs: prs, loaded: true, width: 100, scope: sc}
		m.ensureSelection()

		var drawn []string
		for _, r := range m.rows(layoutFor(100), 100) {
			if r.kind == rowPR {
				drawn = append(drawn, r.pr.Key())
			}
		}
		var cursor []string
		for _, p := range m.visible() {
			cursor = append(cursor, p.Key())
		}

		if !slices.Equal(cursor, drawn) {
			t.Fatalf("scope %s: cursor order != drawn order\ncursor %v\ndrawn  %v",
				sc, cursor, drawn)
		}

		// And walking with j must land on each drawn row in turn.
		m.selected = drawn[0]
		for i := 1; i < len(drawn); i++ {
			m.move(1)
			if m.selected != drawn[i] {
				t.Fatalf("scope %s: %d presses of j reached %s, want %s",
					sc, i, m.selected, drawn[i])
			}
		}
	}
}

// 1-9 read from visible() while the printed numbers come from rows(), so the
// same divergence sent the number keys to the wrong PR.
func TestJumpNumbersMatchThePrintedOnes(t *testing.T) {
	repos := []string{"org/app", "org/api"}
	var prs []PR
	for i := range 12 {
		prs = append(prs, PR{
			Repo: repos[i%2], Number: 50 - i, Mine: i < 6, Author: "teammate",
			CreatedAt: time.Now().Add(-time.Duration(i) * time.Hour),
		})
	}

	m := model{client: &Client{}, prs: prs, loaded: true, width: 100}
	m.ensureSelection()

	visible := m.visible()
	for _, r := range m.rows(layoutFor(100), 100) {
		if r.kind != rowPR || r.idx == 0 {
			continue
		}
		// The row prints idx; pressing that key selects visible[idx-1].
		if got := visible[r.idx-1].Key(); got != r.pr.Key() {
			t.Errorf("row printed %d for %s, but that key selects %s",
				r.idx, r.pr.Key(), got)
		}
	}
}

func TestRowsNestReposUnderSections(t *testing.T) {
	m := model{client: &Client{}, loaded: true, width: 100, prs: []PR{
		{Repo: "org/app", Number: 3, Mine: true, CreatedAt: time.Now()},
		{Repo: "org/api", Number: 2, Mine: true, CreatedAt: time.Now()},
		{Repo: "org/app", Number: 1, Author: "teammate", CreatedAt: time.Now()},
	}}
	m.ensureSelection()

	var shape []string
	for _, r := range m.rows(layoutFor(100), 100) {
		switch r.kind {
		case rowHeader:
			shape = append(shape, "S:"+r.text)
		case rowGroup:
			shape = append(shape, "R:"+r.text)
		case rowPR:
			shape = append(shape, fmt.Sprintf("P:%d", r.pr.Number))
		}
	}

	want := []string{"S:MINE", "R:app", "P:3", "R:api", "P:2", "S:TEAM", "R:app", "P:1"}
	if !slices.Equal(shape, want) {
		t.Errorf("shape = %v\nwant   %v", shape, want)
	}
}

// The same repo appears under both sections, and each header counts only its own
// section's rows.
func TestGroupNotesCountOnlyTheirOwnGroup(t *testing.T) {
	m := model{client: &Client{}, loaded: true, width: 100, prs: []PR{
		{Repo: "org/app", Number: 4, Mine: true, ReviewDecision: "APPROVED"},
		{Repo: "org/api", Number: 3, Mine: true, Checks: "FAILURE"},
		{Repo: "org/app", Number: 2, Author: "t", NeedsMe: true},
	}}
	m.ensureSelection()

	notes := map[string]string{}
	section := ""
	for _, r := range m.rows(layoutFor(100), 100) {
		switch r.kind {
		case rowHeader:
			section = r.text
			notes[section] = r.note
		case rowGroup:
			notes[section+"/"+r.text] = r.note
		}
	}

	for key, want := range map[string]string{
		"MINE":     "1 ready · 1 blocked",
		"MINE/app": "1 ready",
		"MINE/api": "1 blocked",
		"TEAM":     "1 for you",
		"TEAM/app": "1 for you",
	} {
		if notes[key] != want {
			t.Errorf("note for %s = %q, want %q", key, notes[key], want)
		}
	}
}

// Grouping only helps while you can see which group you are in.
func TestScrolledRowsKeepTheirRepoHeader(t *testing.T) {
	var prs []PR
	for i := 1; i <= 30; i++ {
		repo := "org/app"
		if i%2 == 0 {
			repo = "org/api"
		}
		prs = append(prs, PR{
			Repo: repo, Number: i, Mine: true, Title: "title",
			CreatedAt: time.Now().Add(-time.Duration(i) * time.Hour),
		})
	}

	m := model{client: &Client{}, prs: prs, loaded: true, width: 60, height: 10}
	m.ensureSelection()

	for range 29 {
		m.move(1)
		first := strings.Split(m.View(), "\n")[2]
		if strings.TrimSpace(first) == "" {
			t.Fatalf("no context line while on %s", m.selected)
		}
		// Whatever is at the top, it must be a header rather than an orphaned
		// PR row: those start with a cursor or blank margin then a glyph.
		if strings.Contains(first, "MINE") || strings.Contains(first, "app") ||
			strings.Contains(first, "api") {
			continue
		}
		t.Fatalf("top line lost its group while on %s: %q", m.selected, first)
	}
}

func TestWindowPrependsTheBreadcrumbOnlyWhenNeeded(t *testing.T) {
	rows := []row{
		{kind: rowHeader, text: "MINE"},
		{kind: rowGroup, text: "app"},
		{kind: rowPR, crumb: "MINE · app"},
		{kind: rowPR, crumb: "MINE · app"},
	}

	// Starting on a header needs no help, and must not lose a row to a sticky.
	if got := window(rows, 0, 3); len(got) != 3 || got[0].text != "MINE" {
		t.Errorf("unscrolled window = %v", got)
	}
	// Starting mid-group, the breadcrumb replaces the lost headers.
	got := window(rows, 2, 2)
	if len(got) != 2 || got[0].kind != rowGroup || got[0].text != "MINE · app" {
		t.Errorf("scrolled window = %v, want a breadcrumb first", got)
	}
}

func TestFitDropsTrailingPartsRatherThanTruncatingAll(t *testing.T) {
	parts := []string{"repo", "ready to merge", "1h idle"}

	if got := fit(parts, " · ", 100); got != "repo · ready to merge · 1h idle" {
		t.Errorf("full width = %q", got)
	}
	if got := fit(parts, " · ", 21); got != "repo · ready to merge" {
		t.Errorf("tight width = %q, want the last part dropped", got)
	}
	// Showing nothing would be worse than showing a truncated first fact.
	if got := fit(parts, " · ", 3); got != "re~" {
		t.Errorf("very tight = %q", got)
	}
}

// The cursor tracks a key, so a PR merging out from under it must not silently
// move the selection onto a neighbour.
func TestSelectionSurvivesAPRDisappearing(t *testing.T) {
	a := PR{Repo: "org/app", Number: 1, Mine: true}
	b := PR{Repo: "org/app", Number: 2, Mine: true}

	m := model{client: &Client{}, prs: []PR{a, b}, loaded: true}
	m.ensureSelection()
	m.move(1)
	if m.selected != b.Key() {
		t.Fatalf("selected %q, want %q", m.selected, b.Key())
	}

	// b merges; the cursor should fall back to a rather than to whatever now
	// sits at b's old index.
	m.prs = []PR{a}
	m.ensureSelection()
	if m.selected != a.Key() {
		t.Errorf("selected %q after the row vanished, want %q", m.selected, a.Key())
	}

	// And with nothing left there is nothing to act on.
	m.prs = nil
	m.ensureSelection()
	if _, ok := m.current(); ok {
		t.Error("current() returned a PR from an empty list")
	}
}

func TestScopeFiltersSections(t *testing.T) {
	mine := PR{Repo: "org/app", Number: 1, Mine: true}
	theirs := PR{Repo: "org/app", Number: 2, Author: "teammate"}
	m := model{client: &Client{}, prs: []PR{mine, theirs}, loaded: true, width: 100}

	m.scope = scopeMine
	if got := m.visible(); len(got) != 1 || got[0].Key() != mine.Key() {
		t.Errorf("scopeMine showed %d PRs", len(got))
	}
	if strings.Contains(m.View(), "TEAM") {
		t.Error("scopeMine still drew the TEAM section")
	}

	m.scope = scopeTeam
	if got := m.visible(); len(got) != 1 || got[0].Key() != theirs.Key() {
		t.Errorf("scopeTeam showed %d PRs", len(got))
	}
	if strings.Contains(m.View(), "MINE") {
		t.Error("scopeTeam still drew the MINE section")
	}

	m.scope = scopeAll
	if got := m.visible(); len(got) != 2 {
		t.Errorf("scopeAll showed %d PRs, want 2", len(got))
	}
}

// A short pane must scroll rather than quietly cut the list off, which would
// read as "that is all of them".
func TestShortPaneKeepsTheCursorVisible(t *testing.T) {
	var prs []PR
	for i := 1; i <= 40; i++ {
		prs = append(prs, PR{
			Repo: "org/app", Number: i, Mine: true, Title: "title",
			CreatedAt: time.Now().Add(-time.Duration(i) * time.Hour),
		})
	}

	m := model{client: &Client{}, prs: prs, loaded: true, width: 46, height: 12}
	m.ensureSelection()
	m.clamp()

	for range 39 {
		m.move(1)
		body := m.View()
		if lines := strings.Count(body, "\n") + 1; lines > m.height {
			t.Fatalf("rendered %d lines into a %d-line pane", lines, m.height)
		}
		if !strings.Contains(body, "> ") {
			t.Fatalf("cursor scrolled off screen at PR %s", m.selected)
		}
	}
}

func TestEmptyStateDistinguishesLoadingFromEmpty(t *testing.T) {
	m := model{client: &Client{}, width: 46}
	if !strings.Contains(m.View(), "loading") {
		t.Error("a pane that has not fetched yet claimed to be empty")
	}

	m.loaded = true
	if !strings.Contains(m.View(), "nothing open") {
		t.Error("a fetched, empty pane did not say so")
	}

	// A failed refresh must not claim there is nothing open.
	m.err = "HTTP 502"
	view := m.View()
	if strings.Contains(view, "nothing open") || !strings.Contains(view, "502") {
		t.Errorf("error state reads wrong:\n%s", view)
	}
}

func TestNotesSummariseWhatNeedsDoing(t *testing.T) {
	mine := []PR{
		{Mine: true, ReviewDecision: "APPROVED"},
		{Mine: true, Checks: "FAILURE"},
		{Mine: true, ReviewDecision: "REVIEW_REQUIRED"},
	}
	if got := mineNote(mine); got != "1 ready · 1 blocked" {
		t.Errorf("mineNote() = %q", got)
	}
	// Open threads are neither ready nor blocked: the work left is yours, and
	// small.
	withThreads := append(mine,
		PR{Mine: true, ReviewDecision: "APPROVED", Unresolved: 1},
		PR{Mine: true, Unresolved: 2})
	if got := mineNote(withThreads); got != "1 ready · 2 to resolve · 1 blocked" {
		t.Errorf("mineNote() with threads = %q", got)
	}
	// With nothing to act on, the count is the only useful thing to say.
	if got := mineNote([]PR{{Mine: true}}); got != "1 open" {
		t.Errorf("mineNote() with nothing pending = %q", got)
	}

	if got := teamNote([]PR{{NeedsMe: true}, {}}); got != "1 for you" {
		t.Errorf("teamNote() = %q", got)
	}
	if got := teamNote([]PR{{}, {}}); got != "2 open" {
		t.Errorf("teamNote() with none for you = %q", got)
	}
}

// lineFor finds the drawn row for a PR number. Numbers are right-aligned in a
// five-column field, so " 1402 " cannot match inside a longer number.
func lineFor(t *testing.T, view string, number int) string {
	t.Helper()
	needle := fmt.Sprintf(" %d ", number)
	for line := range strings.SplitSeq(view, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	t.Fatalf("no row for #%d in:\n%s", number, view)
	return ""
}

func stackedModel(width int) model {
	prs := stack([]PR{
		branch(1567, 22, "honor-status", "revoke-tokens"),
		branch(1565, 23, "provenance", "honor-status"),
		branch(1562, 24, "main", "rasterize"),
		branch(1507, 400, "accept-logins", "provenance"),
		branch(1402, 1200, "main", "accept-logins"),
	})
	for i := range prs {
		prs[i].Mine = true
		prs[i].Title = "title"
	}
	m := model{client: &Client{}, prs: prs, loaded: true, width: width}
	m.ensureSelection()
	return m
}

// The connector sits between the number and the title, in a column every row
// reserves, so stacked and unstacked titles still line up.
func TestStackConnectorsDrawTheChain(t *testing.T) {
	for _, width := range []int{50, 100} {
		view := stackedModel(width).View()
		for number, want := range map[int]string{
			1567: "1567 ┌ title",
			1565: "1565 │ title",
			1507: "1507 │ title",
			1402: "1402 └ title",
			1562: "1562   title",
		} {
			if line := lineFor(t, view, number); !strings.Contains(line, want) {
				t.Errorf("width %d: #%d row = %q, want it to contain %q",
					width, number, line, want)
			}
		}
	}
}

// Connectors come from the neighbouring rows, not from the stack alone. A stack
// split between MINE and TEAM must not leave a line pointing at nothing.
func TestStackConnectorsDoNotDangleAcrossSections(t *testing.T) {
	prs := stack([]PR{
		branch(42, 1, "theirs", "mine"),
		branch(41, 2, "main", "theirs"),
	})
	prs[0].Mine, prs[0].Title = true, "title"
	prs[1].Author, prs[1].Title = "teammate", "title"

	m := model{client: &Client{}, prs: prs, loaded: true, width: 100}
	m.ensureSelection()
	view := m.View()
	for _, number := range []int{41, 42} {
		want := fmt.Sprintf("%d   title", number)
		if line := lineFor(t, view, number); !strings.Contains(line, want) {
			t.Errorf("#%d row = %q, want no connector", number, line)
		}
	}
}

// Where a PR sits in its stack goes on the detail line, ahead of the idle time,
// since it is what explains the connector.
func TestDetailSaysWhereAPRSitsInItsStack(t *testing.T) {
	prs := stackedModel(100).prs
	l := layoutFor(100)

	if got := detailOf(byNumber(prs, 1507), l, 100); !strings.HasPrefix(got, "2 of 4 · on #1402") {
		t.Errorf("middle of a stack: detail = %q", got)
	}
	// The bottom has nothing loaded under it, but is still part of a stack.
	if got := detailOf(byNumber(prs, 1402), l, 100); !strings.HasPrefix(got, "1 of 4 ·") ||
		strings.Contains(got, "on ") {
		t.Errorf("bottom of a stack: detail = %q", got)
	}
	if got := detailOf(byNumber(prs, 1562), l, 100); strings.Contains(got, " of ") {
		t.Errorf("unstacked PR claimed a stack position: %q", got)
	}
}

// A parent outside the feed still means the PR does not target main, and
// letting it read as though it did is the mistake that merges into the wrong
// branch.
func TestDetailNamesAnUnloadedBase(t *testing.T) {
	p := stack([]PR{branch(5, 1, "someone-elses-branch", "mine")})[0]
	if got := detailOf(p, layoutFor(100), 100); !strings.HasPrefix(got, "on someone-elses-branch") {
		t.Errorf("detail = %q, want it to name the base", got)
	}
}

func TestStackedRowsFitTheirWidth(t *testing.T) {
	for _, width := range []int{30, 40, 46, 60, 75, 76, 80, 100, 140} {
		m := stackedModel(width)
		for i := range m.prs {
			m.prs[i].Title = strings.Repeat("a long pull request title ", 6)
		}
		l := layoutFor(width)
		for _, r := range m.rows(l, width) {
			if got := lipgloss.Width(m.render(r, width, l)); got > width {
				t.Errorf("width %d: unclipped row is %d columns", width, got)
			}
		}
	}
}

func TestDetailMentionsOutdatedThreadsAndNotes(t *testing.T) {
	p := PR{Mine: true, ReviewDecision: "APPROVED", Unresolved: 2, Outdated: 1,
		Notes: []string{"ann", "bob"}}
	got := detailOf(p, layoutFor(100), 100)
	for _, want := range []string{"1 outdated", "notes from ann, bob"} {
		if !strings.Contains(got, want) {
			t.Errorf("detail = %q, missing %q", got, want)
		}
	}

	one := PR{Mine: true, ReviewDecision: "APPROVED", Notes: []string{"ann"}}
	if got := detailOf(one, layoutFor(100), 100); !strings.Contains(got, "note from ann") {
		t.Errorf("detail = %q, want a note from ann", got)
	}
	if got := detailOf(PR{Mine: true}, layoutFor(100), 100); strings.Contains(got, "outdated") ||
		strings.Contains(got, "note") {
		t.Errorf("a PR with neither mentioned them: %q", got)
	}
}

func press(m model, key string) (model, tea.Cmd) {
	var msg tea.KeyMsg
	switch key {
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	next, cmd := m.Update(msg)
	return next.(model), cmd
}

func quits(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestHelpTogglesAndEscClosesBeforeQuitting(t *testing.T) {
	m := model{client: &Client{}, loaded: true, width: 100}

	m, _ = press(m, "?")
	if !m.help {
		t.Fatal("? did not open help")
	}
	m, _ = press(m, "?")
	if m.help {
		t.Fatal("? did not close help")
	}

	// esc is how most people leave a help screen, so there it must not quit.
	m, _ = press(m, "?")
	m, cmd := press(m, "esc")
	if m.help || quits(cmd) {
		t.Errorf("esc in help: help %v, quit %v; want closed and still running",
			m.help, quits(cmd))
	}
	if _, cmd := press(m, "esc"); !quits(cmd) {
		t.Error("esc on the list no longer quits")
	}
	m, _ = press(m, "?")
	if _, cmd := press(m, "q"); !quits(cmd) {
		t.Error("q in help did not quit")
	}
}

// The list is hidden behind help, so moving the cursor there would move it
// somewhere you cannot see.
func TestHelpIgnoresListKeys(t *testing.T) {
	m := model{client: &Client{}, loaded: true, width: 100, prs: []PR{
		{Repo: "org/app", Number: 2, Mine: true},
		{Repo: "org/app", Number: 1, Mine: true},
	}}
	m.ensureSelection()
	before := m.selected

	m, _ = press(m, "?")
	for _, key := range []string{"j", "G", "2"} {
		var cmd tea.Cmd
		if m, cmd = press(m, key); cmd != nil {
			t.Errorf("%s in help returned a command", key)
		}
	}
	if m.selected != before {
		t.Errorf("cursor moved behind help: %s -> %s", before, m.selected)
	}
}

// Refreshing carries on behind help, and must not close it.
func TestRefreshKeepsHelpOpen(t *testing.T) {
	m := model{client: &Client{}, help: true, width: 100}
	next, _ := m.Update(feedMsg{feed: Feed{PRs: []PR{{Repo: "org/app", Number: 1}}}})
	if !next.(model).help {
		t.Error("a refresh closed help")
	}
}

// The help is the legend, so a state missing from it is a glyph nobody can
// decode.
func TestHelpListsEveryState(t *testing.T) {
	view := model{client: &Client{}, help: true, width: 100}.View()
	for _, s := range []State{stateNeedsYou, stateDraft, stateChanges, stateFailing,
		stateApprovedOpen(2), stateReady, stateUnresolved(3), stateRunning,
		stateWaiting} {
		if !strings.Contains(view, s.Glyph+"  "+s.Label) {
			t.Errorf("help is missing %q %q", s.Glyph, s.Label)
		}
	}
	for _, want := range []string{"┌", "open in browser", "copy url"} {
		if !strings.Contains(view, want) {
			t.Errorf("help is missing %q", want)
		}
	}
}

func TestHelpFitsItsWidth(t *testing.T) {
	for _, width := range []int{30, 40, 50, 60, 76, 100, 140} {
		m := model{client: &Client{}, help: true, width: width}
		for _, line := range m.helpLines(width) {
			if got := lipgloss.Width(line); got > width {
				t.Errorf("width %d: help line is %d columns:\n%q", width, got, line)
			}
		}
	}
}

func TestFooterMentionsHelp(t *testing.T) {
	m := model{client: &Client{}}
	if got := m.footer(layoutFor(100)); !strings.Contains(got, "? help") {
		t.Errorf("wide footer = %q", got)
	}
	if got := m.footer(layoutFor(50)); !strings.Contains(got, "?") {
		t.Errorf("compact footer = %q", got)
	}
}

// A style rendered inside another ends with a full reset, which strips the
// outer colour from everything after it. The dim jump number did exactly that,
// so the wide layout drew every row after its number in the default colour.
func TestRowColourSurvivesTheJumpNumber(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	m := model{client: &Client{}, loaded: true, prs: []PR{{
		Repo: "org/app", Number: 5, Mine: true, Title: "title",
		ReviewDecision: "APPROVED", Unresolved: 1,
	}}}
	m.ensureSelection()

	for _, width := range []int{50, 100} {
		m.width = width
		l := layoutFor(width)
		for _, r := range m.rows(l, width) {
			if r.kind != rowPR {
				continue
			}
			line := m.render(r, width, l)
			// The title is on every layout; whatever escape precedes it must
			// be the row's colour, not a reset.
			at := strings.Index(line, "title")
			last := strings.LastIndex(line[:at], "\x1b[")
			if last < 0 || strings.HasPrefix(line[last:], "\x1b[0m") {
				t.Errorf("width %d: title is not coloured: %q", width, line)
			}
		}
	}
}
