package main

import (
	"fmt"
	"maps"
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
	// From catppuccin/palette, the mocha flavour.
	"catppuccin-mocha": {
		dim:    "#7f849c", // overlay1; overlay0 is too faint for text
		cursor: "#a6e3a1", // green
		err:    "#f38ba8", // red
		header: "#89b4fa", // blue
		group:  "#b4befe", // lavender

		neutral:      "#cdd6f4", // text
		good:         "#a6e3a1", // green
		running:      "#89dceb", // sky
		act:          "#fab387", // peach
		broken:       "#f38ba8", // red
		approvedOpen: "#cba6f7", // mauve
		unresolved:   "#f9e2af", // yellow
	},
	// From morhetz/gruvbox, the bright accents meant for a dark background.
	"gruvbox-dark": {
		dim:    "#928374", // gray
		cursor: "#b8bb26", // bright green
		err:    "#fb4934", // bright red
		header: "#83a598", // bright blue
		group:  "#8ec07c", // bright aqua

		neutral:      "#ebdbb2", // light1
		good:         "#b8bb26", // bright green
		running:      "#83a598", // bright blue
		act:          "#fe8019", // bright orange
		broken:       "#fb4934", // bright red
		approvedOpen: "#d3869b", // bright purple
		unresolved:   "#fabd2f", // bright yellow
	},
	// Classic Monokai, as Sublime Text shipped it. It has no plain red; its
	// pink is what it uses for errors, so that is what failing gets.
	"monokai": {
		dim:    "#75715e", // comment
		cursor: "#a6e22e", // green
		err:    "#f92672", // pink
		header: "#f8f8f2", // foreground; its one blue is spent on running
		group:  "#66d9ef", // cyan

		neutral:      "#f8f8f2", // foreground
		good:         "#a6e22e", // green
		running:      "#66d9ef", // cyan
		act:          "#fd971f", // orange
		broken:       "#f92672", // pink
		approvedOpen: "#ae81ff", // purple
		unresolved:   "#e6db74", // yellow
	},
	// Ethan Schoonover's Solarized, light. Text is base00 on base3 as the
	// palette intends; secondary content is base1.
	"solarized-light": {
		dim:    "#93a1a1", // base1
		cursor: "#859900", // green
		err:    "#dc322f", // red
		header: "#268bd2", // blue
		group:  "#586e75", // base01, the emphasised tone

		neutral:      "#657b83", // base00
		good:         "#859900", // green
		running:      "#2aa198", // cyan
		act:          "#cb4b16", // orange
		broken:       "#dc322f", // red
		approvedOpen: "#6c71c4", // violet
		unresolved:   "#b58900", // yellow
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

// themeNames lists the themes for the flag's help and for errors, so neither
// can fall out of step with the map.
func themeNames() string {
	return strings.Join(slices.Sorted(maps.Keys(themes)), ", ")
}

func lookupTheme(name string) (theme, error) {
	if t, ok := themes[name]; ok {
		return t, nil
	}
	return theme{}, fmt.Errorf("unknown theme %q; choose from %s", name, themeNames())
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
