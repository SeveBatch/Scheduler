// schedgen reads a GameSheet schedule (from the public API or a CSV export)
// and writes an ICS calendar file.
//
//	go run . -mode url -out schedule.ics
//	go run . -mode csv -in temp.csv -out schedule.ics
package main

import (
	"crypto/sha1"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	tzName     = "America/Denver"
	duration   = 90 * time.Minute
	defaultURL = "https://gamesheetstats.com/api/useUnifiedGames/14869?filter[gametype]=overall&filter[limit]=100&filter[offset]=0&filter[teams]=511656&filter[timeZoneOffset]=-360"
)

type Game struct {
	Start                   time.Time
	Visitor, Home, Location string
}

func main() {
	mode := flag.String("mode", "url", "input mode: 'url' (fetch from GameSheet API) or 'csv' (read local CSV export)")
	url := flag.String("url", defaultURL, "GameSheet useUnifiedGames API URL (used when -mode=url)")
	in := flag.String("in", "temp.csv", "input CSV from GameSheet export (used when -mode=csv)")
	out := flag.String("out", "schedule.ics", "output ICS file path")
	team := flag.String("team", "", "filter to games involving this team (case-insensitive); home games render as 'Team vs Opponent'")
	flag.Parse()

	loc, err := time.LoadLocation(tzName)
	must(err)

	var games []Game
	var src string
	switch *mode {
	case "url":
		src = *url
		games, err = fetchGames(*url, loc, *team)
	case "csv":
		src = *in
		games, err = readGames(*in, loc, *team)
	default:
		must(fmt.Errorf("invalid -mode %q (want 'url' or 'csv')", *mode))
	}
	must(err)
	if len(games) == 0 {
		must(fmt.Errorf("no games from %s", src))
	}

	must(writeICS(*out, games, *team))
	fmt.Printf("Wrote %s (%d games)\n", *out, len(games))
}

// fetchGames retrieves games from the GameSheet API. The response is columnar:
// parallel arrays where index i across each describes one game.
// GameSheet tags wall-clock local times with a Z suffix; we reinterpret them in loc.
func fetchGames(url string, loc *time.Location, team string) ([]Game, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GET %s: %d %s", url, resp.StatusCode, string(body))
	}

	var data struct {
		Date     []string                 `json:"date"`
		Visitor  []struct{ Title string } `json:"visitor"`
		Home     []struct{ Title string } `json:"home"`
		Location []string                 `json:"location"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	n := len(data.Date)
	if len(data.Visitor) != n || len(data.Home) != n || len(data.Location) != n {
		return nil, fmt.Errorf("inconsistent array lengths: date=%d visitor=%d home=%d location=%d",
			n, len(data.Visitor), len(data.Home), len(data.Location))
	}

	var games []Game
	for i := 0; i < n; i++ {
		t, err := time.Parse("2006-01-02T15:04:05.000Z", data.Date[i])
		if err != nil {
			return nil, fmt.Errorf("game %d: parse date %q: %w", i, data.Date[i], err)
		}
		visitor, home := data.Visitor[i].Title, data.Home[i].Title
		if team != "" && !strings.EqualFold(visitor, team) && !strings.EqualFold(home, team) {
			continue
		}
		games = append(games, Game{
			Start:    time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, loc),
			Visitor:  visitor,
			Home:     home,
			Location: data.Location[i],
		})
	}
	return games, nil
}

func subject(g Game, team string) string {
	if team != "" && strings.EqualFold(g.Home, team) {
		return fmt.Sprintf("%s vs %s", g.Home, g.Visitor)
	}
	return fmt.Sprintf("%s @ %s", g.Visitor, g.Home)
}

// readGames expects the GameSheet column layout: Date, Visitor, _, Details, _, Home, Location.
// GameSheet tags wall-clock local times with a Z suffix; we reinterpret them in loc.
func readGames(path string, loc *time.Location, team string) ([]Game, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1

	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	if len(header) < 7 || header[0] != "Date" || header[1] != "Visitor" || header[5] != "Home" || header[6] != "Location" {
		return nil, fmt.Errorf("unexpected header %v", header)
	}

	var games []Game
	for line := 2; ; line++ {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		if len(row) < 7 || row[0] == "" {
			continue
		}
		t, err := time.Parse("2006-01-02T15:04:05.000Z", row[0])
		if err != nil {
			return nil, fmt.Errorf("line %d: parse date %q: %w", line, row[0], err)
		}
		visitor, home := row[1], row[5]
		if team != "" && !strings.EqualFold(visitor, team) && !strings.EqualFold(home, team) {
			continue
		}
		games = append(games, Game{
			Start:    time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, loc),
			Visitor:  visitor,
			Home:     home,
			Location: row[6],
		})
	}
	return games, nil
}

func writeCSV(path string, games []Game, team string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	w.Write([]string{"Subject", "Start Date", "Start Time", "End Date", "End Time", "Description", "Location"})
	for _, g := range games {
		end := g.Start.Add(duration)
		w.Write([]string{
			subject(g, team),
			g.Start.Format("01/02/2006"),
			strings.TrimLeft(g.Start.Format("03:04 PM"), "0"),
			end.Format("01/02/2006"),
			strings.TrimLeft(end.Format("03:04 PM"), "0"),
			fmt.Sprintf("%s (visitor) at %s (home)", g.Visitor, g.Home),
			g.Location,
		})
	}
	return w.Error()
}

func writeICS(path string, games []Game, team string) error {
	var b strings.Builder
	w := func(s string) { b.WriteString(s); b.WriteString("\r\n") }

	w("BEGIN:VCALENDAR")
	w("VERSION:2.0")
	w("PRODID:-//schedgen//EN")
	w("CALSCALE:GREGORIAN")
	w("METHOD:PUBLISH")
	w("X-WR-CALNAME:Hockey Schedule")
	w("X-WR-TIMEZONE:" + tzName)
	w("BEGIN:VTIMEZONE")
	w("TZID:" + tzName)
	w("BEGIN:DAYLIGHT")
	w("TZOFFSETFROM:-0700")
	w("TZOFFSETTO:-0600")
	w("TZNAME:MDT")
	w("DTSTART:19700308T020000")
	w("RRULE:FREQ=YEARLY;BYMONTH=3;BYDAY=2SU")
	w("END:DAYLIGHT")
	w("BEGIN:STANDARD")
	w("TZOFFSETFROM:-0600")
	w("TZOFFSETTO:-0700")
	w("TZNAME:MST")
	w("DTSTART:19701101T020000")
	w("RRULE:FREQ=YEARLY;BYMONTH=11;BYDAY=1SU")
	w("END:STANDARD")
	w("END:VTIMEZONE")

	for _, g := range games {
		end := g.Start.Add(duration)
		w("BEGIN:VEVENT")
		w("UID:" + uid(g))
		w("DTSTAMP:" + g.Start.UTC().Format("20060102T150405Z"))
		w(fmt.Sprintf("DTSTART;TZID=%s:%s", tzName, g.Start.Format("20060102T150405")))
		w(fmt.Sprintf("DTEND;TZID=%s:%s", tzName, end.Format("20060102T150405")))
		w("SUMMARY:" + escape(subject(g, team)))
		w("DESCRIPTION:" + escape(fmt.Sprintf("%s (visitor) at %s (home)", g.Visitor, g.Home)))
		if g.Location != "" {
			w("LOCATION:" + escape(g.Location))
		}
		w("END:VEVENT")
	}
	w("END:VCALENDAR")
	return os.WriteFile(path, []byte(b.String()), 0644)
}

func escape(s string) string {
	return strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\n", `\n`).Replace(s)
}

// uid derives a stable UID from game fields so that re-running the generator
// produces the same identifier for the same game (avoids duplicate events on
// calendar refresh).
func uid(g Game) string {
	h := sha1.Sum([]byte(strings.Join([]string{
		g.Start.Format(time.RFC3339), g.Visitor, g.Home, g.Location,
	}, "|")))
	return hex.EncodeToString(h[:]) + "@schedgen"
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
