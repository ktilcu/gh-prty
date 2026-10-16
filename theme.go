package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// theme is every colour prty draws with. Only foregrounds: the background is
// the terminal's, and painting over it would fight whatever the pane is set to.
//
// Colours may be 256-colour indices or hex. lipgloss scales hex down on
// terminals that cannot show it, so a truecolor theme still works over ssh.
type theme struct {
	dim, cursor, err, header, group lipgloss.Color

	neutral, good, running, act, broken lipgloss.Color
	approvedOpen, unresolved            lipgloss.Color
}

var themes = map[string]theme{
	"default": {
		dim:    "245",
		cursor: "42",
		err:    "203",
		header: "111",
		// Repo groups sit visually between a section header and a row, so they
		// get the header's hue without its weight.
		group: "74",

		neutral: "252",
		good:    "42",
		running: "39",
		act:     "214",
		broken:  "203",
		// Approved with open threads is good news with a little left to do,
		// so it gets a colour of its own rather than a shade of ready or
		// blocked.
		approvedOpen: "141",
		unresolved:   "220",
	},
	// From folke/tokyonight.nvim's moon palette. The theme has a real orange,
	// which is why "needs you" can keep the colour it has everywhere else.
	"tokyonight-moon": {
		dim:    "#636da6", // comment
		cursor: "#c3e88d", // green
		err:    "#ff757f", // red
		header: "#82aaff", // blue
		group:  "#65bcff", // blue1

		neutral:      "#c8d3f5", // fg
		good:         "#c3e88d", // green
		running:      "#86e1fc", // cyan
		act:          "#ff966c", // orange
		broken:       "#ff757f", // red
		approvedOpen: "#c099ff", // magenta
		unresolved:   "#ffc777", // yellow
	},
}

// colours names every role, so a test can check that no theme leaves one out.
func (t theme) colours() map[string]lipgloss.Color {
	return map[string]lipgloss.Color{
		"dim": t.dim, "cursor": t.cursor, "err": t.err, "header": t.header,
		"group": t.group, "neutral": t.neutral, "good": t.good,
		"running": t.running, "act": t.act, "broken": t.broken,
		"approvedOpen": t.approvedOpen, "unresolved": t.unresolved,
	}
}

func lookupTheme(name string) (theme, error) {
	if t, ok := themes[name]; ok {
		return t, nil
	}
	names := make([]string, 0, len(themes))
	for n := range themes {
		names = append(names, n)
	}
	slices.Sort(names)
	return theme{}, fmt.Errorf("unknown theme %q; choose from %s", name,
		strings.Join(names, ", "))
}

func init() { useTheme(themes["default"]) }

// useTheme sets the package styles. It runs once, before the UI starts, which is
// what makes package-level styles safe: nothing reads them while they change.
func useTheme(t theme) {
	dimStyle = lipgloss.NewStyle().Foreground(t.dim)
	cursorStyle = lipgloss.NewStyle().Foreground(t.cursor).Bold(true)
	errorStyle = lipgloss.NewStyle().Foreground(t.err)
	headerStyle = lipgloss.NewStyle().Foreground(t.header).Bold(true)
	groupStyle = lipgloss.NewStyle().Foreground(t.group)

	neutralStyle = lipgloss.NewStyle().Foreground(t.neutral)
	goodStyle = lipgloss.NewStyle().Foreground(t.good)
	runningStyle = lipgloss.NewStyle().Foreground(t.running)
	actStyle = lipgloss.NewStyle().Foreground(t.act).Bold(true)
	brokenStyle = lipgloss.NewStyle().Foreground(t.broken)
	approvedOpenStyle = lipgloss.NewStyle().Foreground(t.approvedOpen)
	unresolvedStyle = lipgloss.NewStyle().Foreground(t.unresolved)
}
