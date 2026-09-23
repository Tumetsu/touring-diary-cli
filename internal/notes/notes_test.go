package notes

import (
	"strings"
	"testing"
	"time"
)

func TestLoadDir(t *testing.T) {
	res, err := LoadDir("../../testdata/notes")
	if err != nil {
		t.Fatal(err)
	}
	// more.json (1) + notes.json (4 usable); zbroken.json skipped.
	if len(res.Notes) != 5 {
		t.Fatalf("notes = %d, want 5", len(res.Notes))
	}
	var reasons []string
	for _, s := range res.Skipped {
		reasons = append(reasons, s.Path+": "+s.Reason)
	}
	joined := strings.Join(reasons, "\n")
	for _, want := range []string{"notes.json[3]: invalid timestamp", "notes.json[4]: empty note", "zbroken.json: decode notes json"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing skip %q in\n%s", want, joined)
		}
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "notes.json[5]") {
		t.Errorf("warnings = %v", res.Warnings)
	}

	n := res.Notes[2] // second note of notes.json
	if n.Text != "In the tent.\nSecond line." {
		t.Errorf("text = %q", n.Text)
	}
	if !n.Time.Equal(time.Date(2026, 6, 27, 20, 50, 0, 0, time.UTC)) {
		t.Errorf("time = %s", n.Time)
	}
	if _, off := n.Time.Zone(); off != 3*3600 {
		t.Errorf("offset not kept: %d", off)
	}
	m := res.Notes[3]
	if m.Title != "Late" || m.Lat == nil || *m.Lat != 60.5 || *m.Lon != 25.5 {
		t.Errorf("manual note = %+v", m)
	}
	if res.Notes[4].Lat != nil {
		t.Error("incomplete position should be dropped")
	}
}

func TestParseTimestamp(t *testing.T) {
	cases := []struct {
		in    string
		naive bool
		utc   string
	}{
		{"2026-06-27T10:10:00+03:00", false, "2026-06-27T07:10:00Z"},
		{"2026-06-27T10:10+03:00", false, "2026-06-27T07:10:00Z"},
		{"2026-06-27T07:10:00Z", false, "2026-06-27T07:10:00Z"},
		{"2026-06-27T10:10:00", true, "2026-06-27T10:10:00Z"},
	}
	for _, c := range cases {
		tm, naive, err := ParseTimestamp(c.in)
		if err != nil {
			t.Errorf("%s: %v", c.in, err)
			continue
		}
		if naive != c.naive || tm.UTC().Format(time.RFC3339) != c.utc {
			t.Errorf("%s: got %s naive=%v", c.in, tm.UTC().Format(time.RFC3339), naive)
		}
	}
	if _, _, err := ParseTimestamp("yesterday"); err == nil {
		t.Error("expected error")
	}
}
