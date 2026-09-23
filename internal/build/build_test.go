package build

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/tuomassalmi/touring-diary/internal/model"
)

const td = "../../testdata"

var noteIDRe = regexp.MustCompile(`^n[0-9a-f]{10}$`)

func runFixture(t *testing.T, opts Options) (*Summary, map[string]any) {
	t.Helper()
	opts.GPXDir = filepath.Join(td, "gpx")
	opts.NotesDir = filepath.Join(td, "notes")
	opts.OutDir = filepath.Join(t.TempDir(), "dist")
	opts.Now = func() time.Time { return time.Date(2026, 9, 23, 15, 0, 0, 0, time.UTC) }
	sum, err := Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(opts.OutDir, "trip.json"))
	if err != nil {
		t.Fatal(err)
	}
	var trip map[string]any
	if err := json.Unmarshal(b, &trip); err != nil {
		t.Fatal(err)
	}
	return sum, trip
}

func TestBuildFixture(t *testing.T) {
	sum, trip := runFixture(t, Options{
		Title:         "CLI title",
		ConfigPath:    filepath.Join(td, "trip.json"),
		OverridesPath: filepath.Join(td, "overrides.json"),
	})

	if trip["title"] != "CLI title" {
		t.Errorf("title = %v (CLI must win over config)", trip["title"])
	}
	if trip["timezone"] != "Europe/Helsinki" {
		t.Errorf("timezone = %v", trip["timezone"])
	}
	if trip["generatedAt"] != "2026-09-23T15:00:00Z" {
		t.Errorf("generatedAt = %v", trip["generatedAt"])
	}
	if sum.Tracks != 2 || sum.Notes != 5 {
		t.Errorf("tracks %d notes %d", sum.Tracks, sum.Notes)
	}
	wantBy := map[model.PlacementSource]int{model.SourceGPS: 1, model.SourceManual: 2, model.SourceSnapped: 2}
	for src, n := range wantBy {
		if sum.NotesBy[src] != n {
			t.Errorf("%s = %d, want %d (all: %v)", src, sum.NotesBy[src], n, sum.NotesBy)
		}
	}
	if len(sum.Skipped) != 6 {
		t.Errorf("skipped %d: %+v", len(sum.Skipped), sum.Skipped)
	}

	items := trip["items"].([]any)
	type row struct{ time, source, title string }
	var got []row
	for _, it := range items {
		m := it.(map[string]any)
		title, _ := m["title"].(string)
		if id := m["id"].(string); !noteIDRe.MatchString(id) {
			t.Errorf("note id %q", id)
		}
		got = append(got, row{m["time"].(string), m["placement"].(map[string]any)["source"].(string), title})
	}
	want := []row{
		{"2026-06-27T06:01:00Z", "manual", "Fixed start"},
		{"2026-06-27T12:00:00Z", "gps", "Viewpoint"},
		{"2026-06-27T20:50:00Z", "snapped", ""},
		{"2026-06-27T21:30:00Z", "manual", "Late"},
		{"2026-06-28T06:10:00Z", "snapped", ""},
	}
	if len(got) != len(want) {
		t.Fatalf("items = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("item %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	tent := items[2].(map[string]any)
	if tent["lat"] != 60.103 || tent["placement"].(map[string]any)["gapSeconds"] != 1200.0 {
		t.Errorf("tent note = %v", tent)
	}

	days := trip["days"].([]any)
	if len(days) != 2 {
		t.Fatalf("days = %v", days)
	}
	d1, d2 := days[0].(map[string]any), days[1].(map[string]any)
	if d1["date"] != "2026-06-27" || d1["title"] != "First day" || len(d1["itemIds"].([]any)) != 3 {
		t.Errorf("day1 = %v", d1)
	}
	if ids := d1["stats"].(map[string]any)["trackIds"].([]any); len(ids) != 2 {
		t.Errorf("day1 tracks = %v", ids)
	}
	// 00:30 local on 06-28 belongs to day 2 even though it is 06-27 in UTC.
	if d2["date"] != "2026-06-28" || d2["index"] != 2.0 || len(d2["itemIds"].([]any)) != 2 {
		t.Errorf("day2 = %v", d2)
	}
	bounds := trip["bounds"].([]any)
	if lo := bounds[0].([]any); lo[0] != 60.0 || lo[1] != 23.75 {
		t.Errorf("bounds = %v", bounds)
	}

	var buf bytes.Buffer
	if err := sum.Print(&buf); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Tracks: 2", "Notes: 5", "snapped:      2", "broken.gpx"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("summary missing %q:\n%s", want, buf.String())
		}
	}
}

func TestBuildInfersZoneAndDefaults(t *testing.T) {
	sum, trip := runFixture(t, Options{})
	if trip["timezone"] != "+03:00" || trip["title"] != DefaultTitle {
		t.Errorf("tz %v title %v", trip["timezone"], trip["title"])
	}
	// Without overrides the 07-10 note is far from every anchor.
	if sum.NotesBy[model.SourceNone] != 1 {
		t.Errorf("by source = %v", sum.NotesBy)
	}
}

func TestBuildUnreadableInputFails(t *testing.T) {
	_, err := Run(Options{GPXDir: filepath.Join(td, "missing"), OutDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error for missing gpx dir")
	}
}
