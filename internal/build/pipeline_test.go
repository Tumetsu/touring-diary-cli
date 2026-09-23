package build

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tumetsu/touring-diary-cli/internal/convert"
	"github.com/Tumetsu/touring-diary-cli/internal/media"
	"github.com/Tumetsu/touring-diary-cli/internal/model"
)

func writeTestFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// testTrip is the part of trip.json the tests read.
type testTrip struct {
	Days  []model.Day
	Items []model.Item
}

// buildTrip runs a build and returns the decoded trip.json and the log.
func buildTrip(t *testing.T, opts Options) (*Summary, testTrip, string) {
	t.Helper()
	var logBuf bytes.Buffer
	opts.Log = log.New(&logBuf, "", 0)
	if opts.OutDir == "" {
		opts.OutDir = filepath.Join(t.TempDir(), "dist")
	}
	if opts.TZ == "" {
		opts.TZ = "+03:00"
	}
	sum, err := Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(opts.OutDir, "trip.json"))
	if err != nil {
		t.Fatal(err)
	}
	var trip testTrip
	if err := json.Unmarshal(b, &trip); err != nil {
		t.Fatal(err)
	}
	return sum, trip, logBuf.String()
}

func TestNoteIDsStableWhenNotesAreAdded(t *testing.T) {
	dir := t.TempDir()
	notesFile := filepath.Join(dir, "notes", "log.json")
	writeTestFile(t, notesFile, `[
		{"timestamp": "2026-06-27T10:00:00+03:00", "text": "a"},
		{"timestamp": "2026-06-27T11:00:00+03:00", "text": "b"},
		{"timestamp": "2026-06-27T11:00:00+03:00", "text": "b"}]`)
	_, trip, _ := buildTrip(t, Options{NotesDir: filepath.Join(dir, "notes")})
	var before []string
	for _, it := range trip.Items {
		before = append(before, it.ID)
	}
	if len(before) != 3 || !noteIDRe.MatchString(before[0]) || before[2] != before[1]+"-2" {
		t.Fatalf("ids %v", before)
	}

	writeTestFile(t, notesFile, `[
		{"timestamp": "2026-06-27T09:00:00+03:00", "text": "earlier"},
		{"timestamp": "2026-06-27T10:00:00+03:00", "text": "a"},
		{"timestamp": "2026-06-27T11:00:00+03:00", "text": "b"},
		{"timestamp": "2026-06-27T11:00:00+03:00", "text": "b"}]`)
	_, trip, _ = buildTrip(t, Options{NotesDir: filepath.Join(dir, "notes")})
	var after []string
	for _, it := range trip.Items[1:] {
		after = append(after, it.ID)
	}
	if strings.Join(after, " ") != strings.Join(before, " ") {
		t.Errorf("ids changed: %v -> %v", before, after)
	}
}

func TestDaysOnlyForDatesWithContent(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "notes", "log.json"), `[
		{"timestamp": "2001-01-01T12:00:00+03:00", "text": "stray"},
		{"timestamp": "2026-06-27T10:00:00+03:00", "text": "a"},
		{"timestamp": "2026-06-28T10:00:00+03:00", "text": "b"},
		{"timestamp": "2026-06-30T10:00:00+03:00", "text": "c"}]`)
	sum, trip, logs := buildTrip(t, Options{NotesDir: filepath.Join(dir, "notes")})
	var got []string
	for i, d := range trip.Days {
		if d.Index != i+1 {
			t.Errorf("day %s index %d, want %d", d.Date, d.Index, i+1)
		}
		got = append(got, d.Date)
	}
	if strings.Join(got, " ") != "2001-01-01 2026-06-27 2026-06-28 2026-06-30" || sum.Days != 4 {
		t.Errorf("days %v", got)
	}
	if !strings.Contains(logs, "warning: the trip spans") || !strings.Contains(logs, `earliest is note "stray" on 2001-01-01`) {
		t.Errorf("no outlier warning:\n%s", logs)
	}

	// A normal trip gets no warning.
	_, _, logs = buildTrip(t, Options{NotesDir: filepath.Join(td, "notes")})
	if strings.Contains(logs, "trip spans") {
		t.Errorf("unexpected warning:\n%s", logs)
	}
}

func TestOverridePositionValidation(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "notes", "log.json"), `[
		{"timestamp": "2026-06-27T10:00:00+03:00", "text": "a"},
		{"timestamp": "2026-06-27T11:00:00+03:00", "text": "b"},
		{"timestamp": "2026-06-27T12:00:00+03:00", "text": "c"}]`)
	ov := filepath.Join(dir, "overrides.json")
	writeTestFile(t, ov, `{
		"2026-06-27T10:00:00+03:00": {"lat": 60.1},
		"2026-06-27T11:00:00+03:00": {"lat": 95, "lon": 25},
		"2026-06-27T12:00:00+03:00": {"lat": 60.1, "lon": 25}}`)
	sum, trip, logs := buildTrip(t, Options{NotesDir: filepath.Join(dir, "notes"), OverridesPath: ov})
	// The two rejected positions are ignored: those notes snap to the third.
	if sum.NotesBy[model.SourceManual] != 1 || trip.Items[2].Placement.Source != model.SourceManual {
		t.Errorf("by %v items %+v", sum.NotesBy, trip.Items)
	}
	for _, want := range []string{`override "2026-06-27T10:00:00+03:00": lat and lon must be given together`,
		`override "2026-06-27T11:00:00+03:00": position 95,25 is out of range`} {
		if !strings.Contains(logs, want) {
			t.Errorf("log lacks %q:\n%s", want, logs)
		}
	}
}

func TestAmbiguousMediaOverrideName(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(td, "media", "gps_offset.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, rel := range []string{"a/x.jpg", "b/x.jpg", "b/y.jpg"} {
		writeTestFile(t, filepath.Join(dir, "media", filepath.FromSlash(rel)), string(src))
	}
	ov := filepath.Join(dir, "overrides.json")
	writeTestFile(t, ov, `{"x.jpg": {"title": "bare"}, "b/x.jpg": {"caption": "by path"}, "y.jpg": {"title": "unique"}}`)
	_, trip, logs := buildTrip(t, Options{MediaDir: filepath.Join(dir, "media"), OverridesPath: ov,
		PhotoSize: 40, ThumbSize: 16, FFmpeg: ToolOff, FFprobe: ToolOff})
	byID := map[string]model.Item{}
	for _, it := range trip.Items {
		byID[it.ID] = it
	}
	a, b, y := byID["m"+convert.ID("a/x.jpg")], byID["m"+convert.ID("b/x.jpg")], byID["m"+convert.ID("b/y.jpg")]
	if a.Title != "" || b.Title != "" || b.Caption != "by path" || y.Title != "unique" {
		t.Errorf("a %+v\nb %+v\ny %+v", a, b, y)
	}
	if !strings.Contains(logs, `override "x.jpg" matches 2 files in different folders`) || strings.Contains(logs, "matches no media") {
		t.Errorf("log:\n%s", logs)
	}
}

func TestMediaOffsetsOnlyFromDeviceOffsets(t *testing.T) {
	eest := time.FixedZone("", 3*3600)
	res := &media.Result{Files: []media.File{
		{Time: time.Date(2026, 6, 27, 10, 0, 0, 0, eest), HasOffset: true},
		{Time: time.Date(2026, 6, 27, 7, 0, 0, 0, time.UTC)}, // QuickTime creation_time
		{Time: time.Date(2026, 6, 27, 7, 0, 0, 0, time.UTC)},
		{Time: time.Date(2026, 6, 27, 7, 0, 0, 0, time.UTC), Naive: true},
	}}
	if got := mediaOffsets(res); len(got) != 1 || got[0] != 3*3600 {
		t.Errorf("offsets %v", got)
	}
}

func TestConfigPathsRelativeToConfigFile(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "trip", "trip.json")
	abs := filepath.Join(dir, "elsewhere")
	writeTestFile(t, cfg, `{"gpx": "gpx", "notes": "../notes", "media": "`+filepath.ToSlash(abs)+`", "out": "dist", "overrides": "ov.json"}`)
	c, err := LoadConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, "trip")
	for got, want := range map[string]string{
		c.GPX: filepath.Join(base, "gpx"), c.Notes: filepath.Join(dir, "notes"), c.Media: abs,
		c.Out: filepath.Join(base, "dist"), c.Overrides: filepath.Join(base, "ov.json"),
	} {
		if got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	}
}

func TestNoVideoRemovesVideoOutputs(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	out := filepath.Join(t.TempDir(), "dist")
	opts := Options{MediaDir: filepath.Join(td, "media"), OutDir: out, PhotoSize: 40, ThumbSize: 16}
	buildTrip(t, opts)
	id := convert.ID("clip.mov")
	files := []string{id + ".mp4", id + "_poster.jpg", id + "_thumb.jpg"}
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(out, "media", f)); err != nil {
			t.Fatal(err)
		}
	}
	opts.NoVideo = true
	sum, trip, _ := buildTrip(t, opts)
	for _, it := range trip.Items {
		if it.Kind == "video" {
			t.Errorf("--no-video left a video item: %+v", it)
		}
	}
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(out, "media", f)); !os.IsNotExist(err) {
			t.Errorf("--no-video kept %s: %v", f, err)
		}
	}
	if sum.MediaConversion.VideoOutputsRemoved != len(files) {
		t.Errorf("removed count %+v", sum.MediaConversion)
	}
	if sum.Size.Videos != 0 || sum.Size.Posters != 0 || sum.Size.Unused != 0 {
		t.Errorf("size report %+v", sum.Size)
	}
	var sb strings.Builder
	sum.Print(&sb)
	if !strings.Contains(sb.String(), "removed 3 video outputs (--no-video)") {
		t.Errorf("summary lacks the removal line:\n%s", sb.String())
	}
	cache, err := os.ReadFile(filepath.Join(out, convert.CacheFile))
	if err != nil || strings.Contains(string(cache), "clip.mov") {
		t.Errorf("cache entry of clip.mov kept: %v", err)
	}
	// Turning videos back on converts the clip again.
	opts.NoVideo = false
	sum, _, _ = buildTrip(t, opts)
	if sum.MediaConversion.Converted != 1 {
		t.Errorf("rebuild after --no-video: %+v", sum.MediaConversion)
	}
}

func TestMissingFFprobeKeepsVideoOutputs(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	out := filepath.Join(t.TempDir(), "dist")
	opts := Options{MediaDir: filepath.Join(td, "media"), OutDir: out, PhotoSize: 40, ThumbSize: 16}
	buildTrip(t, opts)
	opts.FFprobe = ToolOff
	sum, _, _ := buildTrip(t, opts)
	if _, err := os.Stat(filepath.Join(out, "media", convert.ID("clip.mov")+".mp4")); err != nil || sum.MediaConversion.VideoOutputsRemoved != 0 {
		t.Errorf("missing ffprobe removed video outputs: %v %+v", err, sum.MediaConversion)
	}
}

func TestConfigExcludeFromFit(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "cfg.json")
	writeTestFile(t, cfg, `{"days": {"2026-06-26": {"title": "Train", "excludeFromFit": true}, "2026-06-27": {"excludeFromFit": false}}}`)
	c, err := LoadConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if d := c.Days["2026-06-26"]; !d.ExcludeFromFit || d.Title != "Train" || c.Days["2026-06-27"].ExcludeFromFit {
		t.Errorf("days %+v", c.Days)
	}
}

func TestFitBounds(t *testing.T) {
	dir := t.TempDir()
	notes := filepath.Join(dir, "notes")
	writeTestFile(t, filepath.Join(notes, "log.json"), `[
		{"timestamp": "2026-06-26T10:00:00+03:00", "text": "south", "lat": 61.5, "lon": 23.8},
		{"timestamp": "2026-06-27T10:00:00+03:00", "text": "a", "lat": 66.7, "lon": 27.4},
		{"timestamp": "2026-06-28T10:00:00+03:00", "text": "b", "lat": 66.9, "lon": 28.9}]`)
	all := `[[61.5,23.8],[66.9,28.9]]`
	for _, tc := range []struct {
		name, days, want string
		excluded         []string
	}{
		{"none excluded", `{}`, all, nil},
		{"one excluded", `{"2026-06-26": {"excludeFromFit": true}}`, `[[66.7,27.4],[66.9,28.9]]`, []string{"2026-06-26"}},
		{"all excluded", `{"2026-06-26": {"excludeFromFit": true}, "2026-06-27": {"excludeFromFit": true}, "2026-06-28": {"excludeFromFit": true}}`,
			all, []string{"2026-06-26", "2026-06-27", "2026-06-28"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := filepath.Join(t.TempDir(), "cfg.json")
			writeTestFile(t, cfg, `{"days": `+tc.days+`}`)
			opts := Options{NotesDir: notes, ConfigPath: cfg, OutDir: filepath.Join(t.TempDir(), "dist")}
			_, trip, _ := buildTrip(t, opts)
			raw, err := os.ReadFile(filepath.Join(opts.OutDir, "trip.json"))
			if err != nil {
				t.Fatal(err)
			}
			var got struct{ Bounds, FitBounds json.RawMessage }
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if string(got.Bounds) != all || string(got.FitBounds) != tc.want {
				t.Errorf("bounds %s fitBounds %s, want %s", got.Bounds, got.FitBounds, tc.want)
			}
			var excluded []string
			for _, d := range trip.Days {
				if d.ExcludeFromFit {
					excluded = append(excluded, d.Date)
				}
			}
			if strings.Join(excluded, " ") != strings.Join(tc.excluded, " ") {
				t.Errorf("excluded days %v, want %v", excluded, tc.excluded)
			}
			if tc.excluded == nil && strings.Contains(string(raw), "excludeFromFit") {
				t.Error("excludeFromFit written for a day that is not excluded")
			}
		})
	}
}

func TestConfigUnknownDayWarns(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "notes", "log.json"), `[{"timestamp": "2026-06-27T10:00:00+03:00", "text": "a"}]`)
	cfg := filepath.Join(dir, "cfg.json")
	writeTestFile(t, cfg, `{"days": {"2026-06-27": {"title": "ok"}, "2026-07-01": {"excludeFromFit": true}}}`)
	_, trip, logs := buildTrip(t, Options{NotesDir: filepath.Join(dir, "notes"), ConfigPath: cfg})
	if !strings.Contains(logs, `warning: config day "2026-07-01" matches no day of the trip`) {
		t.Errorf("no warning:\n%s", logs)
	}
	if strings.Contains(logs, `"2026-06-27" matches no day`) || trip.Days[0].Title != "ok" {
		t.Errorf("days %+v logs:\n%s", trip.Days, logs)
	}
}

func TestWebPresetBuild(t *testing.T) {
	sum, trip, _ := buildTrip(t, Options{MediaDir: filepath.Join(td, "media"), Preset: "web", ThumbSize: 16})
	photos := 0
	for _, it := range trip.Items {
		switch it.Kind {
		case "video":
			t.Errorf("web preset left a video item: %+v", it)
		case "photo":
			photos++
			if !strings.HasSuffix(it.Src, ".webp") || !strings.HasSuffix(it.Thumb, "_thumb.webp") {
				t.Errorf("photo paths %s %s", it.Src, it.Thumb)
			}
		}
	}
	if photos == 0 || sum.Size.Photos == 0 || sum.Size.Thumbs == 0 || sum.Size.Videos != 0 || sum.Format != "webp" {
		t.Errorf("photos %d, summary %+v", photos, sum)
	}
}
