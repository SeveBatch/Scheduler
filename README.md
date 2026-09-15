# Scheduler

Turns a GameSheet team schedule into an `.ics` calendar file you can subscribe to from Google Calendar, Apple Calendar, or Outlook.

Calendars in this repo:

- `schedule.ics` - Jedi Knights (Apex Adult Hockey League, Summer 2026)
- `season/2026/winter/team/skateful-dead.ics` - Skateful Dead (Ice Centre Adult Hockey League, Winter 2026-2027)

New calendars go under `season/<year>/<season>/team/<team-name>.ics`.

Subscribe with the raw GitHub URL of the file, for example
`https://raw.githubusercontent.com/SeveBatch/Scheduler/main/season/2026/winter/team/skateful-dead.ics`.

## Requirements

Go 1.24 or newer. There are no other dependencies.

## Run it

The tool has three input modes.

### 1. Saved page (`-mode html`) - the one that works today

gamesheetstats.com sits behind a Cloudflare browser check, so a plain script cannot download the page. Save it from a real browser instead:

1. Open the team's schedule page in Chrome, for example
   `https://gamesheetstats.com/seasons/15870/teams/560065/schedule`.
2. Wait for the schedule table to show.
3. File > Save Page As, format "Webpage, HTML Only". Save it as `skateful-dead.html` next to `sched.go`.
4. Run:

```
go run sched.go -mode html -in skateful-dead.html -team "Skateful Dead" -out season/2026/winter/team/skateful-dead.ics
```

The `-team` flag does two things. It filters to that team's games and it flips home games to "Team vs Opponent" instead of "Visitor @ Home". It also names the calendar "<Team> Schedule".

### 2. Fetch the page (`-mode url`)

```
go run sched.go -mode url -url https://gamesheetstats.com/seasons/15870/teams/560065/schedule -team "Skateful Dead" -out season/2026/winter/team/skateful-dead.ics
```

Same parser as `-mode html`, but downloads the page first. As of September 2026 this returns `403 blocked by Cloudflare browser check`. Use mode 1 until that changes.

### 3. CSV export (`-mode csv`)

Reads a GameSheet CSV export with columns `Date, Visitor, _, Details, _, Home, Location` (see `temp.csv`).

```
go run sched.go -mode csv -in temp.csv -team "Jedi Knights" -out schedule.ics
```

## Adding another team

1. Find the team's schedule page on gamesheetstats.com. The URL looks like `/seasons/<season>/teams/<team>/schedule`.
2. Make the folder first. The tool does not create it: `mkdir -p season/<year>/<season>/team`.
3. Follow mode 1 above with that page, the team's exact name for `-team`, and `-out season/<year>/<season>/team/<team-name>.ics`.
4. Commit the new `.ics` file and add it to the list at the top of this file.

## GitHub workflow

`.github/workflows/schedule.yml` runs `-mode url` for the Jedi Knights and commits `schedule.ics` if it changed. The daily timer is off because the Cloudflare check blocks the fetch (it failed every day from June 12, 2026). The workflow can still be started by hand from the Actions tab (workflow_dispatch) to test whether GameSheet lets scripts through again.

The normal flow now is: save the page from a browser, run `-mode html` locally, commit the `.ics` file, and push.

## Notes

- All times are written in `America/Denver`. Game length is fixed at 90 minutes.
- Each event gets a stable UID built from its date, teams, and rink, so re-running the tool and re-committing does not create duplicate events in subscribed calendars.
