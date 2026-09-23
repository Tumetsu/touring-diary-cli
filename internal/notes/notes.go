// Package notes reads timestamped JSON notes (spec section 2.2).
package notes

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Tumetsu/touring-diary-cli/internal/model"
)

// Note is one parsed note.
type Note struct {
	// Time is the note's instant. It keeps the offset given in the file so
	// callers can infer the trip timezone; convert with .UTC() for output.
	Time time.Time
	// Naive is true when the timestamp had no offset. Time then holds the
	// wall clock as if it were UTC and must be reinterpreted in the trip zone.
	Naive bool
	// Key is the timestamp exactly as written, used to match overrides.
	Key      string
	Text     string
	Title    string
	Lat, Lon *float64
	File     string
}

// Result is the outcome of reading a notes folder.
type Result struct {
	Notes    []Note
	Skipped  []model.Skipped
	Warnings []string
}

// LoadDir reads every *.json file in dir and concatenates the notes. Files
// or entries that cannot be used are reported in Result.Skipped; only an
// unreadable dir is an error.
func LoadDir(dir string) (Result, error) {
	var res Result
	entries, err := os.ReadDir(dir)
	if err != nil {
		return res, fmt.Errorf("read notes dir: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(dir, name)
		f, err := os.Open(path)
		if err != nil {
			res.Skipped = append(res.Skipped, model.Skipped{Path: path, Reason: err.Error()})
			continue
		}
		r, err := Parse(f, path)
		f.Close()
		if err != nil {
			res.Skipped = append(res.Skipped, model.Skipped{Path: path, Reason: err.Error()})
			continue
		}
		res.Notes = append(res.Notes, r.Notes...)
		res.Skipped = append(res.Skipped, r.Skipped...)
		res.Warnings = append(res.Warnings, r.Warnings...)
	}
	return res, nil
}

type rawNote struct {
	Timestamp string   `json:"timestamp"`
	Text      string   `json:"text"`
	Title     string   `json:"title"`
	Lat       *float64 `json:"lat"`
	Lon       *float64 `json:"lon"`
}

// Parse reads one notes file: a JSON array of note objects. A malformed
// document is an error; individual unusable entries are reported in
// Result.Skipped with the path suffixed by the entry index.
func Parse(r io.Reader, file string) (Result, error) {
	var res Result
	var raws []rawNote
	if err := json.NewDecoder(r).Decode(&raws); err != nil {
		return res, fmt.Errorf("decode notes json: %w", err)
	}
	for i, rn := range raws {
		where := fmt.Sprintf("%s[%d]", file, i)
		t, naive, err := ParseTimestamp(rn.Timestamp)
		if err != nil {
			res.Skipped = append(res.Skipped, model.Skipped{Path: where, Reason: err.Error()})
			continue
		}
		if strings.TrimSpace(rn.Text) == "" && strings.TrimSpace(rn.Title) == "" {
			res.Skipped = append(res.Skipped, model.Skipped{Path: where, Reason: "empty note"})
			continue
		}
		n := Note{Time: t, Naive: naive, Key: rn.Timestamp, Text: rn.Text, Title: rn.Title, File: file}
		switch {
		case rn.Lat != nil && rn.Lon != nil && validLatLon(*rn.Lat, *rn.Lon):
			n.Lat, n.Lon = rn.Lat, rn.Lon
		case rn.Lat != nil || rn.Lon != nil:
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: ignoring incomplete or invalid lat/lon", where))
		}
		res.Notes = append(res.Notes, n)
	}
	return res, nil
}

var offsetLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04Z07:00", "2006-01-02T15:04:05.999999999Z0700"}
var naiveLayouts = []string{"2006-01-02T15:04:05.999999999", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02 15:04"}

// ParseTimestamp parses an ISO 8601 timestamp. With an offset the result
// keeps that offset. Without one, naive is true and the wall clock is
// returned in UTC for the caller to reinterpret.
func ParseTimestamp(s string) (t time.Time, naive bool, err error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "z") {
		s = s[:len(s)-1] + "Z"
	}
	for _, l := range offsetLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, false, nil
		}
	}
	for _, l := range naiveLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true, nil
		}
	}
	return time.Time{}, false, fmt.Errorf("invalid timestamp %q", s)
}

func validLatLon(lat, lon float64) bool {
	return lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180
}
