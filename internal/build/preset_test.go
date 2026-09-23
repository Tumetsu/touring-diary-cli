package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resolved runs option resolution only (no build) and returns the options.
func resolved(t *testing.T, o Options, config string) (Options, error) {
	t.Helper()
	o.OutDir = firstNonEmpty(o.OutDir, "out")
	o.NotesDir = "notes"
	if config != "" {
		p := filepath.Join(t.TempDir(), "cfg.json")
		writeTestFile(t, p, config)
		o.ConfigPath = p
	}
	b, err := newBuilder(o)
	if err != nil {
		return Options{}, err
	}
	return b.opts, nil
}

func TestImageOptionResolution(t *testing.T) {
	type want struct {
		format               string
		photoSize, thumbSize int
		photoQ, thumbQ       int
		noVideo              bool
	}
	for _, tc := range []struct {
		name   string
		opts   Options
		config string
		want   want
	}{
		{"defaults", Options{}, "", want{"jpeg", 1600, 320, 85, 80, false}},
		{"web preset", Options{Preset: "web"}, "", want{"webp", 1400, 320, 80, 75, true}},
		{"explicit flags win over preset",
			Options{Preset: "WEB", Format: "jpg", PhotoSize: 2000, PhotoQuality: 90, ThumbQuality: 70, NoVideoSet: true},
			"", want{"jpeg", 2000, 320, 90, 70, false}},
		{"config preset", Options{}, `{"preset": "web"}`, want{"webp", 1400, 320, 80, 75, true}},
		{"config values win over preset", Options{Preset: "web"},
			`{"format": "jpeg", "photoSize": 1200, "photoQuality": 70, "thumbQuality": 60, "noVideo": false}`,
			want{"jpeg", 1200, 320, 70, 60, false}},
		{"config equivalents", Options{},
			`{"format": "webp", "photoQuality": 77, "thumbQuality": 66, "noVideo": true, "maxOutputMb": 250}`,
			want{"webp", 1600, 320, 77, 66, true}},
		{"CLI wins over config", Options{Format: "jpeg", PhotoQuality: 50, ThumbQuality: 40},
			`{"format": "webp", "photoQuality": 77, "thumbQuality": 66}`, want{"jpeg", 1600, 320, 50, 40, false}},
		{"no-video flag stays on over config false", Options{NoVideo: true, NoVideoSet: true},
			`{"noVideo": false}`, want{"jpeg", 1600, 320, 85, 80, true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, err := resolved(t, tc.opts, tc.config)
			if err != nil {
				t.Fatal(err)
			}
			got := want{o.Format, o.PhotoSize, o.ThumbSize, o.PhotoQuality, o.ThumbQuality, o.NoVideo}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
	o, _ := resolved(t, Options{}, `{"maxOutputMb": 250}`)
	if o.MaxOutputMB != 250 {
		t.Errorf("config maxOutputMb: %v", o.MaxOutputMB)
	}
	o, _ = resolved(t, Options{MaxOutputMB: 100}, `{"maxOutputMb": 250}`)
	if o.MaxOutputMB != 100 {
		t.Errorf("CLI --max-output-mb: %v", o.MaxOutputMB)
	}
}

func TestImageOptionErrors(t *testing.T) {
	for _, tc := range []struct {
		opts   Options
		config string
		want   string
	}{
		{Options{Format: "gif"}, "", `unknown image format "gif"`},
		{Options{Preset: "tiny"}, "", `unknown preset "tiny" (known: web)`},
		{Options{PhotoQuality: 101}, "", "--photo-quality must be between 1 and 100"},
		{Options{ThumbQuality: -1}, "", "--thumb-quality must be between 1 and 100"},
		{Options{}, `{"thumbQuality": 200}`, "config thumbQuality must be between 1 and 100"},
		{Options{MaxOutputMB: -5}, "", "--max-output-mb must not be negative"},
	} {
		if _, err := resolved(t, tc.opts, tc.config); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v %s: error %v, want %q", tc.opts, tc.config, err, tc.want)
		}
	}
}

func TestMeasureOutput(t *testing.T) {
	dir := t.TempDir()
	files := map[string]int{
		"index.html": 100, "app.js": 200, "vendor/leaflet.js": 300, "trip.json": 400,
		".cache.json": 5000, "media/aaaaaaaaaa.webp.tmp": 7000,
		"media/aaaaaaaaaa.webp": 1000, "media/aaaaaaaaaa_thumb.webp": 50,
		"media/bbbbbbbbbb.mp4": 9000, "media/bbbbbbbbbb_poster.jpg": 800, "media/bbbbbbbbbb_thumb.jpg": 40,
		"media/cccccccccc.jpg": 600, "media/dddddddddd.mov": 3000,
	}
	for name, n := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ref := map[string]bool{}
	for _, p := range []string{"media/aaaaaaaaaa.webp", "media/aaaaaaaaaa_thumb.webp", "media/bbbbbbbbbb.mp4",
		"media/bbbbbbbbbb_poster.jpg", "media/bbbbbbbbbb_thumb.jpg", "media/cccccccccc.jpg"} {
		ref[p] = true
	}
	r, err := MeasureOutput(dir, ref)
	if err != nil {
		t.Fatal(err)
	}
	want := SizeReport{Total: 15490, Photos: 1600, Thumbs: 90, Videos: 9000, Posters: 800, Site: 1000,
		Unused: 3000, Files: 11, Largest: "media/bbbbbbbbbb.mp4", LargestSize: 9000}
	if r != want {
		t.Errorf("got  %+v\nwant %+v", r, want)
	}
	// Without a reference set every media file counts by its name.
	if r, _ := MeasureOutput(dir, nil); r.Videos != 12000 || r.Unused != 0 {
		t.Errorf("nil referenced: %+v", r)
	}
}

func TestPrintSizeReport(t *testing.T) {
	var sb strings.Builder
	s := &Summary{Size: SizeReport{Total: 123_456_789, Photos: 100_000_000, Site: 23_456_789, Files: 3,
		Largest: "media/x.webp", LargestSize: 2_345_678}, MaxOutputMB: 100}
	if err := s.Print(&sb); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Output size: 123.5 MB in 3 files", "photos:", "100.0 MB", "trip.json + site assets:",
		"23.5 MB", "largest file: media/x.webp (2.3 MB)", "warning: output is 123.5 MB, over the --max-output-mb limit of 100 MB"} {
		if !strings.Contains(sb.String(), want) {
			t.Errorf("summary lacks %q:\n%s", want, sb.String())
		}
	}
	s.MaxOutputMB = 200
	sb.Reset()
	s.Print(&sb)
	if strings.Contains(sb.String(), "warning") {
		t.Errorf("warning under the limit:\n%s", sb.String())
	}
}
