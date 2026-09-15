// schedgen reads a GameSheet team schedule (the schedule web page, a saved
// copy of that page, or a CSV export) and writes an ICS calendar file.
//
//	go run . -mode url -url https://gamesheetstats.com/seasons/15870/teams/560065/schedule -team "Skateful Dead" -out season/2026/winter/team/skateful-dead.ics
//	go run . -mode html -in skateful-dead.html -team "Skateful Dead" -out season/2026/winter/team/skateful-dead.ics
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
	defaultURL = "https://gamesheetstats.com/seasons/14869/teams/511656/schedule"
)

type Game struct {
	Start                   time.Time
	Visitor, Home, Location string
}

func main() {
	mode := flag.String("mode", "url", "input mode: 'url' (fetch the GameSheet schedule page), 'html' (read a saved copy of that page), or 'csv' (read a GameSheet CSV export)")
	url := flag.String("url", defaultURL, "GameSheet team schedule page URL (used when -mode=url)")
	in := flag.String("in", "temp.csv", "input file: saved schedule page (-mode=html) or CSV export (-mode=csv)")
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
	case "html":
		src = *in
		games, err = readPage(*in, loc, *team)
	case "csv":
		src = *in
		games, err = readGames(*in, loc, *team)
	default:
		must(fmt.Errorf("invalid -mode %q (want 'url', 'html' or 'csv')", *mode))
	}
	must(err)
	if len(games) == 0 {
		must(fmt.Errorf("no games from %s", src))
	}

	must(writeICS(*out, games, *team))
	fmt.Printf("Wrote %s (%d games)\n", *out, len(games))
}

// fetchGames downloads the team schedule page and parses it.
// Note: gamesheetstats.com sits behind a Cloudflare browser check, which
// usually rejects plain HTTP clients with a 403. Use -mode html with a page
// saved from a browser when that happens.
func fetchGames(url string, loc *time.Location, team string) ([]Game, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		msg := string(body)
		if strings.Contains(msg, "Just a moment") {
			msg = "blocked by Cloudflare browser check; save the page from a browser and use -mode html"
		}
		return nil, fmt.Errorf("GET %s: %d %s", url, resp.StatusCode, msg)
	}
	return parsePage(body, loc, team)
}

// readPage parses a schedule page saved from a browser (File > Save Page As).
func readPage(path string, loc *time.Location, team string) ([]Game, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parsePage(body, loc, team)
}

// parsePage extracts games from the schedule page HTML. The page is a Next.js
// app that streams its data as a series of
//
//	self.__next_f.push([1,"<json string>"])
//
// script chunks. Joined together, the strings contain a row like
// 19:[{"gameId":...},...] holding the schedule. Rows can be split across chunks,
// so all chunks are decoded and concatenated before searching.
func parsePage(html []byte, loc *time.Location, team string) ([]Game, error) {
	const marker = `self.__next_f.push([1,`
	var payload strings.Builder
	rest := string(html)
	for {
		i := strings.Index(rest, marker)
		if i < 0 {
			break
		}
		rest = rest[i+len(marker):]
		lit, n := jsonStringLiteral(rest)
		if n == 0 {
			return nil, fmt.Errorf("malformed __next_f chunk")
		}
		var s string
		if err := json.Unmarshal([]byte(lit), &s); err != nil {
			return nil, fmt.Errorf("decode __next_f chunk: %w", err)
		}
		payload.WriteString(s)
		rest = rest[n:]
	}

	const gamesKey = `[{"gameId":`
	text := payload.String()
	i := strings.Index(text, gamesKey)
	if i < 0 {
		return nil, fmt.Errorf("no game list found in page (is this a GameSheet team schedule page?)")
	}

	var rows []struct {
		TimeStampZulu string                 `json:"timeStampZulu"`
		Location      string                 `json:"location"`
		Visitor       struct{ Title string } `json:"visitor"`
		Home          struct{ Title string } `json:"home"`
	}
	if err := json.NewDecoder(strings.NewReader(text[i:])).Decode(&rows); err != nil {
		return nil, fmt.Errorf("decode game list: %w", err)
	}

	var games []Game
	for n, r := range rows {
		t, err := time.Parse(time.RFC3339, r.TimeStampZulu)
		if err != nil {
			return nil, fmt.Errorf("game %d: parse timeStampZulu %q: %w", n, r.TimeStampZulu, err)
		}
		visitor, home := r.Visitor.Title, r.Home.Title
		if team != "" && !strings.EqualFold(visitor, team) && !strings.EqualFold(home, team) {
			continue
		}
		games = append(games, Game{
			Start:    t.In(loc),
			Visitor:  visitor,
			Home:     home,
			Location: r.Location,
		})
	}
	return games, nil
}

// jsonStringLiteral returns the JSON string literal (quotes included) at the
// start of s, and the number of bytes it spans. n is 0 if s does not start
// with a complete string literal.
func jsonStringLiteral(s string) (lit string, n int) {
	if len(s) == 0 || s[0] != '"' {
		return "", 0
	}
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return s[:i+1], i + 1
		}
	}
	return "", 0
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
	calName := "Hockey Schedule"
	if team != "" {
		calName = team + " Schedule"
	}
	w("X-WR-CALNAME:" + escape(calName))
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
