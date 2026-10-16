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
