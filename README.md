# prty

Your open pull requests and your team's, in a terminal sidebar. What is ready to
merge, what is blocked, and what is waiting on your review.

No token, no config, no daemon — everything comes from `gh`, which already holds
your GitHub auth.

Yours and your team's, each grouped by repo.

```
 PRTY                                                       platform · 6 mine · 2 team

 MINE                                                              2 ready · 3 to resolve
  worker                                                      1 ready · 1 to resolve
   1 +   196   Pick up Debian security fixes in agen~ ready to merge       4h
   2 c   191   Hold the agent to the schema's requir~ 5 unresolved         9d
  api                                                    1 ready · 2 to resolve
>  3 d  1563 ┌ Rename legacy raw_file attachments on~ draft                1d
       3 of 3 · on #1562 · 2h idle · +88-14
   4 *  1562 │ Rasterize outlined-text pages in uplo~ approved · 1 open    1d
   5 +  1561 └ Separate document uploads from normal~ ready to merge       1d
   6 *  1539   Create new records instead of amendments  approved · 1 open    8d

 TEAM                                                                           1 for you
  worker                                                                   1 for you
   7 !   127   Add a zoom tool to the extractio~ needs you           10d ana
  api                                                                    1 open
   8 d  1208   let customers export reports as adde~ draft              121d ben

  jk select · enter open · tab all · r refresh 4s ago · ? help · q quit
```

| | |
| --- | --- |
| `!` | waiting on you — your review, or changes requested on yours |
| `x` | CI failed |
| `*` | approved, with review threads still open (purple) |
| `+` | approved with nothing left open; on your own PR that means ready to merge |
| `c` | review threads open and no approval yet (yellow) |
| `~` | checks still running |
| `o` | awaiting review |
| `d` | draft |
| `┌│└` | a stack, tip on top |

`?` shows this legend inside prty, each state in its own colour.

**AGE** is how long the PR has been open, which is the number that ages badly.
A PR sitting unreviewed for two weeks is exactly what a sidebar exists to
surface.

## Install

```sh
go install github.com/ktilcu/prty@latest
```

Then run `prty` in a pane and leave it there. It needs `gh` on PATH and logged in
(`gh auth status`), with the `read:org` scope so it can resolve your team.

| flag | |
| --- | --- |
| `--team` | team whose PRs to show, as `org/slug`; defaults to your largest GitHub team |
| `--org` | organisation to scope the team feed to; implied by `--team` |
| `--members` | extra logins to count as teammates, comma separated |
| `--max-age` | hide PRs opened longer ago than this, e.g. `2160h` for 90 days; `0` shows everything |
| `--limit` | maximum PRs to request per search (default 40) |
| `--every` | how often to refresh (default `1m`) |
| `--once` | print one frame and exit; needs no terminal, so it works in scripts and cron |
| `--width` | render `--once` at a given width, for previewing the narrow layout |

## Keys

| | |
| --- | --- |
| `j` `k` / arrows | move the cursor |
| `enter` / `o` | open the selected PR in your browser |
| `y` | copy the selected PR's URL |
| `1`-`9` | select and open in one keystroke |
| `tab` | cycle all → mine → team |
| `g` `G` | first, last |
| `r` | force a refresh |
| `?` | help: the legend and these keys; `?` or `esc` goes back |
| `q` | quit |

The footer lists only the keys you reach for most. The rest are in `?`, because
a footer naming every key ran past 90 columns and got clipped, starting with the
hint for where to find the others.

## Whose PRs count as the team's

With no flags, `prty` reads your GitHub team memberships and takes the largest
one — the small ones tend to be owners and admin groups. It shows which team it
picked in the header, so the guess is never a mystery. `--team org/slug` overrides
it, and `--members` adds people the team does not include.

Naming the members is deliberate. Scoping to the org alone would sweep up every
bot PR in every repo and spend the whole row budget on dependency bumps. Only if
there is no team to resolve does it fall back to `org:<org>`.

## What is shown

Four searches, in a single GraphQL request so one refresh costs one round trip:

| | |
| --- | --- |
| `author:@me` | your open PRs |
| `review-requested:@me` | anything waiting on your review |
| `team-review-requested:org/team` | anything waiting on your team's review |
| `author:` per member | your teammates' open PRs |

They overlap on purpose — a teammate's PR can be both theirs and awaiting your
review — so the same PR arrives more than once and the flags accumulate rather
than the last search winning.

The two review searches are why "needs you" is trustworthy. GitHub's
`reviewDecision` reports the PR's overall verdict, not whose turn it is, so it
cannot answer "is this waiting on me". The `review-requested` qualifiers can, and
they clear themselves the moment you submit a review.

A PR of your own is never queued for your review. A stale request can leave you
as a reviewer on your own PR, and that is not something you can act on.

## What a row says

Each row shows one thing: the first state that applies, in priority order.

1. **needs you** — your review. It outranks everything else the PR might also
   be, including draft and failing: those are the author's problem, yours is the
   review.
2. **draft**
3. **changes requested**
4. **ci failed**
5. **approved · 2 open** — approved, with review threads still open
6. **approved** / **ready to merge** — approved, with nothing left open
7. **3 unresolved** — review threads open, no approval yet
8. **checks running**
9. **awaiting review**

The order is a priority, not a classification. A PR can be draft and failing and
unreviewed at once, and the first match is the one you would act on first.
Approved-but-failing reports the failure, because the approval went stale the
moment CI went red. Open threads outrank running checks, because checks finish on
their own and threads wait for you.

## Review threads

On your own PRs, prty counts the review threads nobody has resolved. That is
what tells a clean approval from one that came with things to address: "ready to
merge" means approved, green, and nothing left open, and an approval with
threads still open is purple `approved · 2 open` instead. The count is in the
label because how many are left is what says how close the PR is.

An outdated thread still counts. The code moved under it, which usually means it
was addressed, but the reviewer still sees it open until someone resolves it.
The detail line says how many are outdated, and names anyone who approved with a
written note, which has nothing to resolve but may still be worth reading.

Threads are fetched for your own PRs only. GitHub cannot count unresolved threads
in a query, so every thread comes back, and asking for them on all four searches
would multiply the cost of a request that already earns the odd `502`.

Repos that require no review leave GitHub's `reviewDecision` empty even after an
approval, so where it is empty prty reads the latest reviews instead: any
changes requested wins, then any approval. Where GitHub does give a decision it
is kept, since a repo needing two approvals says `REVIEW_REQUIRED` after the
first and means it.

## Grouping

Sections are the top level, repos nest inside them. Both headers carry the same
summary of what needs doing, so the section says how much of it there is and each
repo says where it is.

Repos are ranked by their newest PR, worked out **once across every section**
rather than per section. That is what keeps a repo in the same place under MINE
as it is under TEAM — ordering each section by its own newest PR would put the
same handful of repos into two different orders, which is harder to scan than no
grouping at all. Like the row order, the rank derives from nothing but
`createdAt`, so a check going green cannot reorder the groups under your cursor.

Grouping pays for itself twice. It buys the vertical space back immediately: the
repo column is gone from every row, because a header two lines up already said
it, and those fourteen columns go to the title instead. It also means the author
column now fits at every table width instead of only past 100 columns.

Grouping is only useful while you can see which group you are in, so a repo
header that scrolls off the top is replaced by a breadcrumb — `MINE ·
api` — rather than leaving the rows under it unattributable.

## Stacks

Stacked PRs are drawn as one run, tip first, with a connector between the number
and the title:

```
  api                                                    1 ready · 3 to resolve
>  1 d  1567 ┌ Revoke user tokens when deactivat~ draft               23h
       4 of 4 · on #1565 · 23h idle · +45-2
   2 d  1565 │ Honor customer user status on every l~ draft               23h
   3 o  1507 │ Record which connection granted an ac~ awaiting review     17d
   4 o  1402 └ Accept logins from any connection  awaiting review     52d
   5 *  1539   Create new records instead of amendments  approved · 1 open    8d
```

A PR is stacked on another when its base branch is the other's head branch, in
the same repo. That is the signal rather than the gh-stack footer, because a
stack made by hand, or adopted with `gh stack link` after the fact, has no footer
but always has the branches. A fork's head is never a parent: its branch names
belong to the fork, and a fork PR from its own `main` would otherwise sit under
every PR in the repo.

A stack sits where its newest member would have, the same rule that ranks repo
groups, so it still moves for nothing but `createdAt`. Under the cursor the
detail line says where the PR sits — `2 of 4 · on #1402` — and names the base
branch when the PR under it is not in the feed, so a PR never reads as targeting
`main` when it does not.

Connectors come from the neighbouring rows rather than the stack, so a stack split
between MINE and TEAM — yours on a teammate's — draws each half on its own, and
only the detail line ties them together.

## Narrow panes

Under 76 columns the table switches to a compact layout, which is what a sidebar
of a few hundred pixels gets:

```
 PRTY                                         6/2

 MINE                      2 ready · 3 to resolve
  worker              1 ready · 1 to resolve
   +   196   Pick up Debian security fixes i~   4h
   c   191   Hold the agent to the schema's ~   9d
  api            1 ready · 2 to resolve
>  d  1563 ┌ Rename legacy raw_file attachme~   1d
       draft · 3 of 3 · on #1562 · 2h idle
   *  1562 │ Rasterize outlined-text pages i~   1d
   +  1561 └ Separate document uploads from ~   1d
   *  1539   Create new cases instead of add~   8d
```

The state word and the author are what go — the glyph says what the word does.
What is dropped reappears under the cursor, spending vertical space, which a
sidebar has, to save horizontal, which it does not. The detail line drops its own
trailing facts rather than truncating all of them, so a very narrow pane still
says something true.

The cursor stays out at the left margin rather than indenting with its row, so it
is still findable at a glance once the rows sit under a repo.

Every line is clipped to the pane width, and every row is built to fit before
that clip. Both matter: an overflowing line wraps, and one wrapped row would
desynchronise every row under it. A six-figure PR number takes its columns from
the title for the same reason. `--width` renders a frame at any width for
checking this.

A pane too short for the list scrolls to keep the cursor visible rather than
quietly cutting the list off at the bottom, which would read as "that is all of
them". Scrolling keeps the breadcrumb line described under
[Grouping](#grouping).

## A few load-bearing details

**Stable ordering.** Rows are sorted newest-first and never by state, and repo
groups are ranked the same way. Sorting either by state would reshuffle them
under the cursor the moment a check went green, and `createdAt` is the only field
here that never changes while a PR is open. The cursor tracks `repo#number`
rather than a row index for the same reason: a PR merging while you read must not
slide the cursor onto its neighbour.

**Failed refreshes keep the last good frame.** A flaky network dims the
timestamp in the footer; it does not blank the list you were reading. The
GraphQL query is expensive enough that GitHub answers `502` on it now and then,
so it is retried once — but only the gateway errors are. Retrying bad
credentials would just make the UI slow to tell you what is wrong.

**Nothing is hidden silently.** `--max-age` is off by default, because hiding
rows is worse than showing stale ones and the list is newest-first so the stale
ones sink out of the way anyway. When it is on, the header says so.

## Keeping it visible

`prty` is just a command, so run it wherever suits you — a split pane, a tab, or
its own window.

Be aware that a split pane is only visible on its own tab, so it vanishes the
moment you switch tabs. Keeping it genuinely on screen is a window management
problem rather than a terminal one, and it is better solved there: a window
manager that can reserve screen space for one window — such as Rectangle's Todo
Mode — will keep a `prty` window parked and tile everything else around it.

For a status line or a cron job, `--once` prints a single frame and needs no
terminal.

## Requirements

Go 1.24+ to build, and `gh` logged in. `enter` uses `open` and `y` uses `pbcopy`,
so those two keys are macOS-only; everything else is portable.
