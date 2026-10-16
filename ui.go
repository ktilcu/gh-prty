package main

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The styles are set from a theme by useTheme; see theme.go.
var (
	titleStyle = lipgloss.NewStyle().Bold(true)

	dimStyle, cursorStyle, errorStyle, headerStyle, groupStyle lipgloss.Style

	neutralStyle, goodStyle, runningStyle, actStyle, brokenStyle lipgloss.Style
	approvedOpenStyle, unresolvedStyle                           lipgloss.Style
)

func styleFor(k stateKind) lipgloss.Style {
	switch k {
	case kindGood:
		return goodStyle
	case kindRunning:
		return runningStyle
	case kindActOnIt:
		return actStyle
	case kindBroken:
		return brokenStyle
	case kindApprovedOpen:
		return approvedOpenStyle
	case kindUnresolved:
		return unresolvedStyle
	default:
		return neutralStyle
	}
}

type (
	feedMsg struct {
		feed Feed
		err  error
	}
	actionMsg struct {
		note string
		err  error
	}
	tickMsg time.Time
)

// scope is which sections to show. Cycling to one section at a time is what
// makes a short pane usable: a review queue and a "what of mine is stuck" list
// are two different questions, and you rarely ask both at once.
type scope int

const (
	scopeAll scope = iota
	scopeMine
	scopeTeam
)

func (s scope) String() string {
	switch s {
	case scopeMine:
		return "mine"
	case scopeTeam:
		return "team"
	default:
		return "all"
	}
}

type model struct {
	client *Client
	every  time.Duration

	prs  []PR
	team string
	// selected tracks a PR key rather than a row index. A PR merging while you
	// read must not slide the cursor onto a different one.
	selected string
	offset   int
	scope    scope
	// help swaps the list for the legend. It replaces the list rather than
	// floating over it, because an overlay has nowhere to go in a sidebar.
	help bool
	// theme is the name of the theme in use, so T knows which comes next.
	theme string

	// loaded separates "fetched, found nothing" from "has not fetched yet", so
	// startup does not flash an empty state that is not true.
	loaded   bool
	fetching bool
	lastLoad time.Time
	err      string
	message  string

	width  int
	height int
}

func newModel(client *Client, every time.Duration) model {
	return model{client: client, every: every}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.load(), scheduleTick(m.every))
}

// --- commands ---------------------------------------------------------------

func (m model) load() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		feed, err := client.Load()
		return feedMsg{feed: feed, err: err}
	}
}

func scheduleTick(every time.Duration) tea.Cmd {
	return tea.Tick(every, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func openCmd(p PR) tea.Cmd {
	return func() tea.Msg {
		if err := openURL(p.URL); err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{note: fmt.Sprintf("opened %s#%d", p.RepoName(), p.Number)}
	}
}

func copyCmd(p PR) tea.Cmd {
	return func() tea.Msg {
		if err := copyURL(p.URL); err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{note: "copied " + p.URL}
	}
}

// --- update -----------------------------------------------------------------

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clamp()
		return m, nil

	case tickMsg:
		return m, tea.Batch(m.load(), scheduleTick(m.every))

	case feedMsg:
		m.fetching = false
		if msg.err != nil {
			// Keep the last good frame on screen. A flaky network should dim the
			// timestamp, not blank the list you were reading.
			m.err = msg.err.Error()
			return m, nil
		}
		m.err = ""
		m.prs, m.team = msg.feed.PRs, msg.feed.Team
		m.loaded = true
		m.lastLoad = time.Now()
		m.ensureSelection()
		m.clamp()
		return m, nil

	case actionMsg:
		if msg.err != nil {
			m.message = msg.err.Error()
		} else {
			m.message = msg.note
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.message = ""

	// Help hides the list, so list keys would move a cursor you cannot see.
	// Everything but leaving is ignored, and esc leaves help before it leaves
	// the program, since that is what esc means on a help screen.
	if m.help {
		switch msg.String() {
		case "?", "esc":
			m.help = false
		case "q", "ctrl+c":
			return m, tea.Quit
		}
		return m, nil
	}

	switch key := msg.String(); key {
	case "q", "ctrl+c", "esc":
		return m, tea.Quit
	case "?":
		m.help = true
	case "T":
		// A preview, not a setting: nothing is saved, so the message says how
		// to keep the theme you land on.
		m.theme = nextTheme(m.theme)
		useTheme(themes[m.theme])
		m.message = "theme: " + m.theme + " · --theme " + m.theme + " keeps it"
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "g", "home":
		m.jump(0)
	case "G", "end":
		m.jump(len(m.visible()) - 1)
	case "enter", "o":
		if p, ok := m.current(); ok {
			return m, openCmd(p)
		}
		m.message = "nothing selected"
	case "y":
		if p, ok := m.current(); ok {
			return m, copyCmd(p)
		}
		m.message = "nothing selected"
	case "tab":
		m.scope = (m.scope + 1) % 3
		m.ensureSelection()
		m.clamp()
	case "r":
		m.fetching = true
		return m, m.load()
	default:
		// Number keys select and open in one keystroke.
		if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
			visible := m.visible()
			if idx := int(key[0] - '1'); idx < len(visible) {
				m.selected = visible[idx].Key()
				return m, openCmd(visible[idx])
			}
		}
	}
	return m, nil
}

// section is one top-level group, already split into its repo groups. Both the
// rendering and the cursor read their order from here, and that is the point:
// deriving the two independently is what let j/k walk a different order than the
// one on screen the moment repo grouping went in.
type section struct {
	title  string
	prs    []PR
	note   func([]PR) string
	groups []repoGroup
}

// sections is the single source of truth for display order.
func (m model) sections() []section {
	order := repoOrder(m.prs)

	var out []section
	add := func(title string, prs []PR, note func([]PR) string) {
		out = append(out, section{
			title: title, prs: prs, note: note,
			groups: groupByRepo(prs, order),
		})
	}
	if m.scope != scopeTeam {
		add("MINE", m.mine(), mineNote)
	}
	if m.scope != scopeMine {
		add("TEAM", m.theirs(), teamNote)
	}
	return out
}

// visible is the PRs the current scope shows, flattened in exactly the order
// they are drawn in, so moving the cursor by one always moves it one row.
func (m model) visible() []PR {
	var out []PR
	for _, s := range m.sections() {
		for _, g := range s.groups {
			out = append(out, g.prs...)
		}
	}
	return out
}

func (m model) mine() []PR {
	var out []PR
	for _, p := range m.prs {
		if p.Mine {
			out = append(out, p)
		}
	}
	return out
}

func (m model) theirs() []PR {
	var out []PR
	for _, p := range m.prs {
		if !p.Mine {
			out = append(out, p)
		}
	}
	return out
}

func (m model) indexOf(key string) int {
	return slices.IndexFunc(m.visible(), func(p PR) bool { return p.Key() == key })
}

func (m model) current() (PR, bool) {
	visible := m.visible()
	if i := m.indexOf(m.selected); i >= 0 {
		return visible[i], true
	}
	return PR{}, false
}

func (m *model) move(delta int) {
	visible := m.visible()
	if len(visible) == 0 {
		return
	}
	next := (max(m.indexOf(m.selected), 0) + delta) % len(visible)
	if next < 0 {
		next += len(visible)
	}
	m.selected = visible[next].Key()
	m.clamp()
}

func (m *model) jump(i int) {
	visible := m.visible()
	if i < 0 || i >= len(visible) {
		return
	}
	m.selected = visible[i].Key()
	m.clamp()
}

func (m *model) ensureSelection() {
	visible := m.visible()
	if len(visible) == 0 {
		m.selected = ""
		return
	}
	if m.indexOf(m.selected) < 0 {
		m.selected = visible[0].Key()
	}
}

// clamp scrolls the minimum amount needed to keep the cursor on screen. A team
// with thirty open PRs will not fit a sidebar, and silently cutting the list off
// at the bottom would read as "that is all of them".
func (m *model) clamp() {
	width := m.renderWidth()
	rows := m.rows(layoutFor(width), width)
	budget := m.bodyBudget()
	if budget <= 0 || len(rows) <= budget {
		m.offset = 0
		return
	}

	cursor := slices.IndexFunc(rows, func(r row) bool {
		return r.kind == rowPR && r.pr.Key() == m.selected
	})
	m.offset = min(m.offset, len(rows)-budget)
	m.offset = max(m.offset, 0)
	if cursor < 0 {
		return
	}
	if cursor < m.offset {
		m.offset = cursor
	}
	// +1 keeps the selected row's detail line on screen with it.
	if bottom := cursor - budget + 2; bottom > m.offset {
		m.offset = min(bottom, len(rows)-budget)
	}
}

// bodyBudget is how many list lines fit: the pane minus the title, the footer,
// and the blank lines around them.
func (m model) bodyBudget() int {
	if m.height <= 0 {
		return 0
	}
	chrome := 4
	if m.err != "" || m.message != "" {
		chrome++
	}
	return max(m.height-chrome, 1)
}

// --- layout -----------------------------------------------------------------

const (
	// compactBelow is the width at which the table stops fitting. A sidebar is
	// always below it; a full window is not.
	compactBelow = 76
	// authorFrom is where there is room for an author column. Below it the
	// author moves to the detail line under the cursor.
	stateWidth  = 18 // "approved · 12 open", the longest label
	authorWidth = 12
	numWidth    = 5
	// linkWidth is the stack connector and the gap after it. Every row reserves
	// it, stacked or not, so titles stay aligned and do not shift when a stack
	// appears or merges away.
	linkWidth = 2
	// rowIndent nests PR rows one column under their repo group, leaving the
	// cursor out at the margin where it stays easy to find.
	rowIndent = 1
)

type layout struct {
	compact bool
	title   int
	// showAuthor is simply "not compact". Grouping by repo freed the repo column
	// from every row, and the author is what fits in its place — so there is no
	// longer a width where the table fits but the author does not.
	showAuthor bool
}

func layoutFor(width int) layout {
	if width < compactBelow {
		// cursor(2) indent(1) glyph(1) gap(1) number(5) gap(1) link(2) title
		// gap(1) age(4). The number gets five columns even though most repos
		// never reach them: a six-figure PR in a big repo would push the age
		// column past the edge and get clipped, and a clipped row is worse than
		// a shorter title.
		return layout{compact: true, title: max(width-16-linkWidth, 8)}
	}

	// cursor(2) indent(1) index(1) gap(1) glyph(1) gap(1) number(5) gap(1)
	// link(2) title gap(1) state(18) gap(1) age(4) gap(1) author(12)
	fixed := 2 + rowIndent + 1 + 1 + 1 + 1 + numWidth + 1 + linkWidth + 1 +
		stateWidth + 1 + 4 + 1 + authorWidth
	return layout{showAuthor: true, title: min(max(width-fixed, 16), 60)}
}

func (m model) renderWidth() int {
	if m.width > 0 {
		return m.width
	}
	return 100
}

// --- rows -------------------------------------------------------------------

type rowKind int

const (
	rowHeader rowKind = iota
	rowGroup
	rowPR
	rowDetail
	rowNote
	rowBlank
)

type row struct {
	kind rowKind
	text string
	note string
	pr   PR
	idx  int // 1-9 jump key, 0 for none
	// link is the stack connector drawn between the number and the title.
	link string
	// crumb names the section and repo a PR row sits under, so a row scrolled
	// away from its headers can still say where it belongs.
	crumb string
}

// detailIndent aligns the detail line under the PR number: cursor(2) indent(1)
// glyph(1) gap(1) plus two, so it reads as a continuation rather than a row.
const detailIndent = "       "

// rows turns the sections into the exact lines to draw, detail line included, so
// scrolling has a single list to window over. It walks sections() in order and
// nothing else, which is what keeps the drawn order and the cursor's order the
// same list.
func (m model) rows(l layout, width int) []row {
	var rows []row
	jump := 0

	for _, s := range m.sections() {
		if len(rows) > 0 {
			rows = append(rows, row{kind: rowBlank})
		}
		rows = append(rows, row{kind: rowHeader, text: s.title, note: s.note(s.prs)})

		if len(s.prs) == 0 {
			rows = append(rows, row{kind: rowNote, text: m.emptyText()})
			continue
		}
		for _, g := range s.groups {
			rows = append(rows, row{
				kind: rowGroup, text: g.repo, note: s.note(g.prs),
			})
			crumb := s.title + " · " + g.repo
			for i, p := range g.prs {
				jump++
				r := row{kind: rowPR, pr: p, crumb: crumb, link: link(g.prs, i)}
				if jump <= 9 {
					r.idx = jump
				}
				rows = append(rows, r)
				// What the columns dropped reappears under the cursor. This
				// spends vertical space, which a sidebar has, to save
				// horizontal, which it does not.
				if p.Key() == m.selected {
					if detail := detailOf(p, l, width); detail != "" {
						rows = append(rows, row{
							kind: rowDetail, text: detail, crumb: crumb,
						})
					}
				}
			}
		}
	}
	return rows
}

// link draws the stack connector for prs[i] from its neighbours rather than
// from the stack alone. A stack split between MINE and TEAM leaves each half on
// its own, and a connector pointing at a row in another section would point at
// nothing.
func link(prs []PR, i int) string {
	same := func(j int) bool {
		return j >= 0 && j < len(prs) && prs[i].StackRoot != "" &&
			prs[j].StackRoot == prs[i].StackRoot
	}
	switch above, below := same(i-1), same(i+1); {
	case above && below:
		return "│"
	case below:
		return "┌"
	case above:
		return "└"
	default:
		return " "
	}
}

type repoGroup struct {
	repo string
	prs  []PR
}

// repoOrder ranks repos by their newest PR, across every section at once. Doing
// it globally rather than per section is what keeps a repo in the same place
// under MINE as it is under TEAM: ordering each section by its own newest PR
// would shuffle the same handful of repos into two different orders and make
// them harder to find than no grouping at all.
//
// The rank derives from nothing but arrival order, which is createdAt — so a
// check going green cannot reorder the groups under the cursor.
func repoOrder(prs []PR) map[string]int {
	order := make(map[string]int)
	for _, p := range prs {
		if _, seen := order[p.Repo]; !seen {
			order[p.Repo] = len(order)
		}
	}
	return order
}

// groupByRepo splits a section into per-repo runs, ordered by the given rank.
func groupByRepo(prs []PR, order map[string]int) []repoGroup {
	var groups []repoGroup
	at := make(map[string]int, len(prs))

	for _, p := range prs {
		i, seen := at[p.Repo]
		if !seen {
			i = len(groups)
			at[p.Repo] = i
			groups = append(groups, repoGroup{repo: p.RepoName()})
		}
		groups[i].prs = append(groups[i].prs, p)
	}

	// Rows within a group keep their newest-first order; only the groups move.
	slices.SortStableFunc(groups, func(a, b repoGroup) int {
		return cmp.Compare(order[a.prs[0].Repo], order[b.prs[0].Repo])
	})
	return groups
}

func (m model) emptyText() string {
	switch {
	case m.err != "":
		return "unavailable"
	case !m.loaded:
		return "loading..."
	default:
		return "nothing open"
	}
}

// detailOf is whatever the current layout could not fit into columns, in
// priority order so that a pane too narrow for all of it drops the least useful
// fact rather than truncating every fact into uselessness.
func detailOf(p PR, l layout, width int) string {
	var parts []string
	if p.Author != "" && !p.Mine && !l.showAuthor {
		parts = append(parts, p.Author)
	}
	if l.compact {
		// Not the repo: the group header states it, and the sticky header keeps
		// stating it once the group scrolls off.
		parts = append(parts, p.State().Label)
	}
	parts = append(parts, stackOf(p)...)
	if p.Outdated > 0 {
		parts = append(parts, fmt.Sprintf("%d outdated", p.Outdated))
	}
	switch len(p.Notes) {
	case 0:
	case 1:
		parts = append(parts, "note from "+p.Notes[0])
	default:
		parts = append(parts, "notes from "+strings.Join(p.Notes, ", "))
	}
	if idle := p.Idle(); idle > 0 {
		parts = append(parts, short(idle)+" idle")
	}
	parts = append(parts, fmt.Sprintf("+%d-%d", p.Additions, p.Deletions))
	return fit(parts, " · ", width-len(detailIndent))
}

// stackOf says where a PR sits in its stack, which is what explains the
// connector. A base that is not the default branch is worth saying even with no
// loaded PR under it, or the row reads as though it targets main.
func stackOf(p PR) []string {
	var parts []string
	if p.StackSize > 0 {
		parts = append(parts, fmt.Sprintf("%d of %d", p.StackDepth, p.StackSize))
	}
	switch {
	case p.StackOn > 0:
		parts = append(parts, fmt.Sprintf("on #%d", p.StackOn))
	case p.Base != "" && p.DefaultBranch != "" && p.Base != p.DefaultBranch:
		parts = append(parts, "on "+p.Base)
	}
	return parts
}

// fit joins the leading parts that fit the width and drops the rest. A single
// part too wide on its own is truncated, since showing nothing would be worse.
func fit(parts []string, sep string, width int) string {
	out := ""
	for _, part := range parts {
		next := part
		if out != "" {
			next = out + sep + part
		}
		if lipgloss.Width(next) > width {
			break
		}
		out = next
	}
	if out == "" && len(parts) > 0 {
		return truncate(parts[0], width)
	}
	return out
}

func mineNote(prs []PR) string {
	var ready, resolve, blocked int
	for _, p := range prs {
		switch p.State().kind {
		case kindGood:
			ready++
		case kindApprovedOpen, kindUnresolved:
			resolve++
		case kindActOnIt, kindBroken:
			blocked++
		}
	}

	var parts []string
	if ready > 0 {
		parts = append(parts, fmt.Sprintf("%d ready", ready))
	}
	if resolve > 0 {
		parts = append(parts, fmt.Sprintf("%d to resolve", resolve))
	}
	if blocked > 0 {
		parts = append(parts, fmt.Sprintf("%d blocked", blocked))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("%d open", len(prs))
	}
	return strings.Join(parts, " · ")
}

func teamNote(prs []PR) string {
	needs := 0
	for _, p := range prs {
		if p.NeedsMe {
			needs++
		}
	}
	if needs > 0 {
		return fmt.Sprintf("%d for you", needs)
	}
	return fmt.Sprintf("%d open", len(prs))
}

// --- view -------------------------------------------------------------------

func (m model) View() string {
	width := m.renderWidth()

	var lines []string
	if m.help {
		lines = m.helpLines(width)
		// The list scrolls to keep the cursor in view; help has no cursor, so
		// a short pane keeps the top, where the states are.
		if m.height > 0 && len(lines) > m.height {
			lines = lines[:m.height]
		}
	} else {
		lines = m.listLines(width)
	}

	// Clipping matters more than it looks: an overflowing line wraps, and one
	// wrapped row would desynchronise every row under it.
	for i, line := range lines {
		lines[i] = lipgloss.NewStyle().MaxWidth(width).Render(line)
	}
	return strings.Join(lines, "\n")
}

func (m model) listLines(width int) []string {
	l := layoutFor(width)
	lines := []string{m.title(width, l), ""}

	rows := m.rows(l, width)
	if budget := m.bodyBudget(); budget > 0 && len(rows) > budget {
		rows = window(rows, min(m.offset, len(rows)-budget), budget)
	}
	for _, r := range rows {
		lines = append(lines, m.render(r, width, l))
	}

	lines = append(lines, "", m.footer(l))
	if m.err != "" {
		lines = append(lines, errorStyle.Render("  "+m.err))
	} else if m.message != "" {
		lines = append(lines, dimStyle.Render("  "+m.message))
	}
	return lines
}

// helpStates is the legend, in the order State resolves them, so reading down
// the list is reading how a row decides what to say.
var helpStates = []struct {
	state State
	desc  string
}{
	{stateNeedsYou, "your review is requested"},
	{stateDraft, ""},
	{stateChanges, ""},
	{stateFailing, ""},
	{stateApprovedOpen(2), "approved, threads still open"},
	{stateReady, "approved, nothing left"},
	{stateUnresolved(3), "threads open, no approval yet"},
	{stateRunning, ""},
	{stateWaiting, ""},
}

var helpKeys = [][2]string{
	{"j k", "move"},
	{"enter o", "open in browser"},
	{"y", "copy url"},
	{"1-9", "select and open"},
	{"tab", "all, mine, team"},
	{"g G", "first, last"},
	{"r", "refresh"},
	{"T", "next theme"},
	{"?", "this help"},
	{"q", "quit"},
}

// helpLines draws each state in its own colour, so the legend is also the key
// to what the colours mean. Descriptions are the first thing a narrow pane
// drops, the same rule the detail line follows.
func (m model) helpLines(width int) []string {
	lines := []string{
		spread("", titleStyle.Render(" PRTY · help"), "? back", width),
		"",
		spread(" ", headerStyle.Render("STATES"), "first match wins", width),
	}
	for _, h := range helpStates {
		text := h.state.Glyph + "  " + h.state.Label
		if h.desc != "" && 2+3+stateWidth+2+len(h.desc) <= width {
			text = fmt.Sprintf("%s  %-*s  %s", h.state.Glyph, stateWidth, h.state.Label,
				dimStyle.Render(h.desc))
		}
		lines = append(lines, "  "+styleFor(h.state.kind).Render(text))
	}

	lines = append(lines, "", " "+headerStyle.Render("STACKS"),
		"  ┌│└  "+dimStyle.Render(fit([]string{"tip on top", "detail says 2 of 4"},
			" · ", width-7)))

	lines = append(lines, "", " "+headerStyle.Render("KEYS"))
	const keyWidth, descWidth = 9, 17
	perLine := 1
	if width >= 2*(2+keyWidth+descWidth) {
		perLine = 2
	}
	for keys := range slices.Chunk(helpKeys, perLine) {
		line := ""
		for _, k := range keys {
			desc := truncate(k[1], min(descWidth, width-2-keyWidth))
			line += fmt.Sprintf("  %-*s%-*s", keyWidth, k[0], descWidth, desc)
		}
		lines = append(lines, dimStyle.Render(strings.TrimRight(line, " ")))
	}
	return lines
}

func (m model) title(width int, l layout) string {
	mine, theirs := len(m.mine()), len(m.theirs())

	right := ""
	if m.loaded {
		if l.compact {
			right = fmt.Sprintf("%d/%d", mine, theirs)
			if m.client.MaxAge > 0 {
				right = "<" + short(m.client.MaxAge) + " " + right
			}
		} else {
			right = fmt.Sprintf("%d mine · %d team", mine, theirs)
			// A hidden row must be visible as a rule, or the counts read as
			// "that is all of them" when they are not.
			if m.client.MaxAge > 0 {
				right = "<" + short(m.client.MaxAge) + " · " + right
			}
			if m.team != "" {
				right = m.team + " · " + right
			}
		}
	} else if m.err == "" {
		right = "loading..."
	}
	if m.scope != scopeAll {
		right = m.scope.String() + " · " + right
	}

	name := " PRTY"
	pad := max(width-lipgloss.Width(name)-lipgloss.Width(right)-1, 1)
	return titleStyle.Render(name) + strings.Repeat(" ", pad) + dimStyle.Render(right)
}

func (m model) render(r row, width int, l layout) string {
	switch r.kind {
	case rowBlank:
		return ""
	case rowNote:
		return dimStyle.Render("    " + r.text)
	case rowDetail:
		return dimStyle.Render(detailIndent + r.text)
	case rowHeader:
		return spread(" ", headerStyle.Render(r.text), r.note, width)
	case rowGroup:
		return spread("  ", groupStyle.Render(r.text), r.note, width)
	}

	p := r.pr
	state := p.State()
	style := styleFor(state.kind)

	// The cursor stays out at the margin rather than indenting with the row, so
	// it is still findable at a glance once the rows are nested under a repo.
	cursor := "  "
	if p.Key() == m.selected {
		cursor = cursorStyle.Render("> ")
	}
	cursor += strings.Repeat(" ", rowIndent)

	// A PR number wider than its column steals the columns from the title rather
	// than from the row, which would otherwise wrap.
	num, over := numField(p.Number)
	title := max(l.title-over, 1)

	var body string
	if l.compact {
		body = fmt.Sprintf("%s %s %s %-*s %4s",
			state.Glyph, num, r.link,
			title, truncate(p.Title, title),
			short(p.Age()))
	} else {
		idx := " "
		if r.idx > 0 {
			idx = strconv.Itoa(r.idx)
		}
		// No repo column: the group header above the row already says it, and
		// spending fourteen columns to repeat it on every row is exactly what
		// grouping is meant to save.
		// The number is dim rather than the row's colour, so it goes ahead of
		// the row's style instead of inside it. A style nested in another ends
		// with a full reset, which would strip the state colour from the rest
		// of the row.
		cursor += dimStyle.Render(idx) + " "
		body = fmt.Sprintf("%s %s %s %-*s %-*s %4s",
			state.Glyph, num, r.link,
			title, truncate(p.Title, title),
			stateWidth, state.Label,
			short(p.Age()))
		if l.showAuthor {
			// Left blank on your own PRs: the section says whose they are, and
			// repeating it on every row would only add noise to scan past.
			author := ""
			if !p.Mine {
				author = truncate(p.Author, authorWidth)
			}
			body += " " + author
		}
	}
	return cursor + style.Render(strings.TrimRight(body, " "))
}

func (m model) footer(l layout) string {
	age := "never"
	if !m.lastLoad.IsZero() {
		age = short(time.Since(m.lastLoad)) + " ago"
	}
	if m.fetching {
		age = "refreshing..."
	}

	if l.compact {
		return dimStyle.Render("  jk  enter  y  tab  r " + age + "  ?  q")
	}
	// The rest of the keys live in help. A footer listing all of them ran past
	// 90 columns and got clipped, and the first thing clipped was the hint for
	// where to find the rest.
	return dimStyle.Render("  jk select · enter open · tab " + m.scope.String() +
		" · r refresh " + age + " · ? help · q quit")
}

// --- formatting -------------------------------------------------------------

// window slices the rows to what fits, keeping one line of context at the top.
// Grouping is only useful while you can see which group you are in, and a row
// scrolled away from its repo header would otherwise be unattributable — so when
// the top row has lost its headers, the breadcrumb takes their place.
func window(rows []row, start, budget int) []row {
	out := rows[start : start+budget]
	if out[0].kind != rowPR && out[0].kind != rowDetail {
		return out
	}
	sticky := row{kind: rowGroup, text: out[0].crumb}
	return append([]row{sticky}, out[:budget-1]...)
}

// spread pushes a label to the left margin and its note to the right edge,
// dropping the note entirely when the pane is too narrow to hold both rather
// than letting it collide with the label.
func spread(indent, label, note string, width int) string {
	line := indent + label
	pad := width - lipgloss.Width(line) - lipgloss.Width(note) - 1
	if pad < 1 {
		return line
	}
	return line + strings.Repeat(" ", pad) + dimStyle.Render(note)
}

// numField right-aligns a PR number in its column and reports how many columns
// it overflowed by. Truncating the number is not an option — a wrong number is
// worse than a short title — so the caller pays for the overflow elsewhere.
func numField(number int) (string, int) {
	s := strconv.Itoa(number)
	if len(s) >= numWidth {
		return s, len(s) - numWidth
	}
	return strings.Repeat(" ", numWidth-len(s)) + s, 0
}

// short renders a duration in the four columns a sidebar can spare. The year
// case is not hypothetical: a forgotten PR on an old repo is genuinely years
// old, and "5153" would silently overflow the column and shift every field after
// it out of alignment.
func short(d time.Duration) string {
	const day = 24 * time.Hour

	switch {
	case d <= 0:
		return "-"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < day:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 365*day:
		return fmt.Sprintf("%dd", int(d/day))
	default:
		return fmt.Sprintf("%dy", int(d/(365*day)))
	}
}

func truncate(s string, width int) string {
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width <= 1 {
		return string(r[:max(width, 0)])
	}
	return string(r[:width-1]) + "~"
}
