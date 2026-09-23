package build

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tuomassalmi/touring-diary/internal/convert"
	"github.com/tuomassalmi/touring-diary/internal/model"
)

func TestBuildWithMedia(t *testing.T) {
	dir := t.TempDir()
	ov := filepath.Join(dir, "overrides.json")
	if err := os.WriteFile(ov, []byte(`{
		"nometa.png": { "time": "2026-06-26T18:00:00+03:00", "caption": "Waiting for the train" },
		"IMG_0100.jpg": { "lat": 60.0, "lon": 25.0, "title": "Fixed" }
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "dist")
	opts := Options{
		GPXDir: filepath.Join(td, "gpx"), MediaDir: filepath.Join(td, "media"), OutDir: out,
		OverridesPath: ov, TZ: "Europe/Helsinki",
		PhotoSize: 40, ThumbSize: 16, FFmpeg: ToolOff, FFprobe: ToolOff,
		Now: func() time.Time { return time.Date(2026, 9, 23, 15, 0, 0, 0, time.UTC) },
	}
	sum, err := Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	if sum.MediaScanned != 7 || sum.LivePairs != 1 || sum.Media != 4 || sum.Photos != 4 || sum.Videos != 0 {
		t.Errorf("summary %+v", sum)
	}
	reasons := map[string]string{}
	for _, s := range sum.Skipped {
		reasons[filepath.Base(s.Path)] = s.Reason
	}
	if !strings.Contains(reasons["clip.mov"], "ffprobe not found") {
		t.Errorf("clip.mov reason %q", reasons["clip.mov"])
	}
	if !strings.Contains(reasons["heic_gps.heic"], "no timestamp") {
		t.Errorf("heic reason %q", reasons["heic_gps.heic"])
	}

	b, err := os.ReadFile(filepath.Join(out, "trip.json"))
	if err != nil {
		t.Fatal(err)
	}
	var trip struct {
		Days  []model.Day
		Items []model.Item
	}
	if err := json.Unmarshal(b, &trip); err != nil {
		t.Fatal(err)
	}
	media := map[string]model.Item{}
	var order []string
	for _, it := range trip.Items {
		order = append(order, it.ID[:1])
		if it.Kind != model.KindNote {
			media[it.Original] = it
		}
	}
	// Chronological, notes (a waypoint) and media interleaved.
	if got := strings.Join(order, " "); got != "m m m n m" {
		t.Errorf("order = %s", got)
	}

	png := media["nometa.png"]
	if png.ID != "m"+convert.ID("nometa.png") || png.Caption != "Waiting for the train" || !png.Time.Equal(time.Date(2026, 6, 26, 15, 0, 0, 0, time.UTC)) {
		t.Errorf("png %+v", png)
	}
	g := media["gps_offset.jpg"]
	if g.Kind != "photo" || g.Placement.Source != model.SourceGPS || *g.Lat != 66.7003 || *g.AccuracyM != 4.8 {
		t.Errorf("gps photo %+v", g)
	}
	id := "media/" + g.Src[len("media/"):len("media/")+10]
	if g.Src != id+".jpg" || g.Thumb != id+"_thumb.jpg" || g.Width != 48 || g.Height != 64 || g.Original != "gps_offset.jpg" {
		t.Errorf("gps photo files %+v", g)
	}
	for _, p := range []string{g.Src, g.Thumb} {
		if _, err := os.Stat(filepath.Join(out, p)); err != nil {
			t.Error(err)
		}
	}
	l := media["IMG_0100.jpg"]
	if l.Placement.Source != model.SourceManual || l.Title != "Fixed" || l.LivePhoto != nil {
		t.Errorf("live/manual photo %+v", l)
	}
	n := media["naive.jpg"]
	if !n.TimeAssumed || !n.Time.Equal(time.Date(2026, 6, 27, 18, 0, 0, 0, time.UTC)) {
		t.Errorf("naive photo %+v", n)
	}

	if trip.Days[0].Date != "2026-06-26" || len(trip.Days[0].ItemIDs) != 1 {
		t.Errorf("first day %+v", trip.Days[0])
	}

	var buf bytes.Buffer
	if err := sum.Print(&buf); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Media: 4 (4 photos, 0 videos; 1 Live Photo videos merged", "7 files scanned",
		"Conversion: 4 converted", "heic_gps.heic: no timestamp"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("summary missing %q:\n%s", want, buf.String())
		}
	}

	// Second build is fully cached.
	sum, err = Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	if c := sum.MediaConversion; c.Cached != 4 || c.Converted != 0 {
		t.Errorf("second run %+v", c)
	}
}
