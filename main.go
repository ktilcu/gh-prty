// prty shows your open pull requests and your team's in a terminal sidebar.
//
// Everything comes from gh, which already holds your GitHub auth, so there is no
// token to configure and nothing to store. Point it at a team or let it work one
// out from your memberships.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	team := flag.String("team", "", "team whose PRs to show, as org/slug; "+
		"defaults to your largest GitHub team")
	org := flag.String("org", "", "organisation to scope the team feed to; "+
		"implied by --team")
	members := flag.String("members", "", "extra logins to count as teammates, "+
		"comma separated")
	limit := flag.Int("limit", 40, "maximum PRs to request per search")
	maxAge := flag.Duration("max-age", 0, "hide PRs opened longer ago than this, "+
		"e.g. 2160h for 90 days; 0 shows everything")
	every := flag.Duration("every", time.Minute, "how often to refresh")
	once := flag.Bool("once", false, "print a single frame and exit")
	width := flag.Int("width", 0, "render --once at this width, for previewing "+
		"narrow layouts")
	themeName := flag.String("theme", "default", "colour scheme: "+themeNames())
	flag.Parse()

	t, err := lookupTheme(*themeName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "prty:", err)
		os.Exit(1)
	}
	useTheme(t)

	client := &Client{Org: *org, Limit: *limit, MaxAge: *maxAge}
	if *team != "" {
		owner, slug, ok := strings.Cut(*team, "/")
		if !ok {
			fmt.Fprintln(os.Stderr, "prty: --team wants org/slug, e.g. acme/backend")
			os.Exit(1)
		}
		client.Org, client.Team = owner, slug
	}
	for login := range strings.SplitSeq(*members, ",") {
		if login = strings.TrimSpace(login); login != "" {
			client.Extra = append(client.Extra, login)
		}
	}

	m := newModel(client, *every)

	// --once needs no terminal, which makes it the mode for scripting, for cron,
	// and for checking the render without taking over a pane.
	if *once {
		feed, err := client.Load()
		if err != nil {
			fmt.Fprintln(os.Stderr, "prty:", err)
			os.Exit(1)
		}
		m.prs, m.team = feed.PRs, feed.Team
		m.loaded = true
		m.lastLoad = time.Now()
		m.width = *width
		m.ensureSelection()
		fmt.Println(m.View())
		return
	}

	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "prty:", err)
		os.Exit(1)
	}
}
