package main

import (
	"fmt"
	"strings"
	"testing"
)

// A theme that leaves a colour unset would draw that role in the terminal's
// default colour, which reads as a deliberate choice and is not one.
func TestEveryThemeSetsEveryColour(t *testing.T) {
	for name, th := range themes {
		for role, c := range th.colours() {
			if c == "" {
				t.Errorf("theme %s leaves %s unset", name, role)
			}
		}
	}
}

// The states are the whole point of the colours, so no theme may let two of
// them collapse into one.
func TestEveryThemeKeepsStatesDistinct(t *testing.T) {
	kinds := []stateKind{kindNeutral, kindGood, kindRunning, kindActOnIt,
		kindBroken, kindApprovedOpen, kindUnresolved}

	defer useTheme(themes["default"])
	for name, th := range themes {
		useTheme(th)
		seen := map[string]stateKind{}
		for _, k := range kinds {
			fg := fmt.Sprint(styleFor(k).GetForeground())
			if other, dup := seen[fg]; dup {
				t.Errorf("theme %s: kinds %d and %d share %s", name, k, other, fg)
			}
			seen[fg] = k
		}
	}
}

func TestUseThemeRecolours(t *testing.T) {
	defer useTheme(themes["default"])

	useTheme(themes["tokyonight-moon"])
	if got := fmt.Sprint(styleFor(kindApprovedOpen).GetForeground()); !strings.Contains(got, "c099ff") {
		t.Errorf("approved-open under tokyonight-moon = %s, want its magenta", got)
	}
	useTheme(themes["default"])
	if got := fmt.Sprint(styleFor(kindApprovedOpen).GetForeground()); !strings.Contains(got, "141") {
		t.Errorf("approved-open after switching back = %s, want 141", got)
	}
}

func TestLookupThemeNamesTheChoicesOnAMiss(t *testing.T) {
	if _, err := lookupTheme("tokyonight-moon"); err != nil {
		t.Errorf("known theme: %v", err)
	}
	_, err := lookupTheme("solarized")
	if err == nil {
		t.Fatal("unknown theme was accepted")
	}
	for _, name := range []string{"catppuccin-mocha", "default", "gruvbox-dark",
		"monokai", "solarized-light", "tokyonight-moon"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not offer %s", err, name)
		}
	}
}

// T walks every theme in the order the flag lists them and wraps around, so
// pressing it len(themes) times comes back to where you started.
func TestThemeKeyCyclesThroughEveryTheme(t *testing.T) {
	defer useTheme(themes["default"])
	useTheme(themes["default"])

	m := model{client: &Client{}, loaded: true, width: 100, theme: "default"}
	seen := map[string]bool{}
	for range themes {
		m, _ = press(m, "T")
		if seen[m.theme] {
			t.Fatalf("%s came round twice before every theme was shown", m.theme)
		}
		seen[m.theme] = true

		// The styles must actually change, not just the name.
		want := fmt.Sprint(themes[m.theme].approvedOpen)
		if got := fmt.Sprint(styleFor(kindApprovedOpen).GetForeground()); got != want {
			t.Errorf("on %s approved-open is %s, want %s", m.theme, got, want)
		}
	}
	if m.theme != "default" {
		t.Errorf("after a full cycle the theme is %s, want default", m.theme)
	}
}

// Cycling is a preview and saves nothing, so it has to say how to keep the one
// you land on.
func TestThemeKeySaysHowToKeepIt(t *testing.T) {
	defer useTheme(themes["default"])

	m := model{client: &Client{}, loaded: true, width: 100, theme: "default"}
	m, _ = press(m, "T")
	if !strings.Contains(m.message, m.theme) || !strings.Contains(m.message, "--theme "+m.theme) {
		t.Errorf("message = %q, want the theme and the flag that keeps it", m.message)
	}
}

func TestHelpListsTheThemeKey(t *testing.T) {
	view := model{client: &Client{}, help: true, width: 100}.View()
	if !strings.Contains(view, "next theme") {
		t.Error("help does not mention the theme key")
	}
}
