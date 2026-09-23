package media

import (
	"math"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func approx(a, b, eps float64) bool { return math.Abs(a-b) < eps }

func byName(t *testing.T, res Result) map[string]File {
	t.Helper()
	m := map[string]File{}
	for _, f := range res.Files {
		m[f.Name] = f
	}
	return m
}

func TestScanFixtures(t *testing.T) {
	ffprobe, _ := exec.LookPath("ffprobe")
	res, err := Scan(fixtureDir, Options{FFprobe: ffprobe})
	if err != nil {
		t.Fatal(err)
	}
	if res.Scanned != 7 || res.LivePairs != 1 {
		t.Errorf("scanned %d, pairs %d; want 7, 1", res.Scanned, res.LivePairs)
	}
	files := byName(t, res)

	g := files["gps_offset.jpg"]
	want := time.Date(2026, 6, 27, 7, 39, 15, 0, time.UTC)
	if !g.Time.Equal(want) || g.Naive || !g.HasOffset {
		t.Errorf("gps_offset time %v naive %v, want %v", g.Time, g.Naive, want)
	}
	if _, off := g.Time.Zone(); off != 3*3600 {
		t.Errorf("gps_offset offset = %d, want +3h", off)
	}
	if g.Lat == nil || !approx(*g.Lat, 66.7003, 1e-6) || !approx(*g.Lon, 27.5555, 1e-6) {
		t.Errorf("gps_offset position %v,%v", g.Lat, g.Lon)
	}
	if g.AccuracyM == nil || !approx(*g.AccuracyM, 4.75, 1e-9) {
		t.Errorf("gps_offset accuracy %v", g.AccuracyM)
	}
	if g.Orientation != 6 || g.Kind != KindPhoto || g.Format != "jpeg" {
		t.Errorf("gps_offset orientation %d kind %s format %s", g.Orientation, g.Kind, g.Format)
	}

	n := files["naive.jpg"]
	if !n.Naive || n.HasOffset || n.Time.Hour() != 21 || n.Lat != nil || n.Orientation != 3 {
		t.Errorf("naive.jpg: %+v", n)
	}

	if m := files["nometa.png"]; !m.Time.IsZero() || m.Lat != nil {
		t.Errorf("nometa.png should have no time or position: %+v", m)
	}

	h, ok := files["heic_gps.heic"]
	if !ok {
		t.Fatalf("heic fixture missing; skipped: %v", res.Skipped)
	}
	// ImageMagick writes the Exif item without the iref link imagemeta
	// follows, so this fixture has no readable metadata; HEIC EXIF parsing
	// is verified on real iPhone files. The fixture still exercises HEIC
	// detection and, in the convert tests, decoding.
	if h.Format != "heic" || h.Kind != KindPhoto {
		t.Errorf("heic: %+v", h)
	}

	live := files["IMG_0100.jpg"]
	if live.LivePhoto == nil || live.LivePhoto.Name != "img_0100.MOV" {
		t.Errorf("Live Photo not paired: %+v", live.LivePhoto)
	}
	if _, ok := files["img_0100.MOV"]; ok {
		t.Error("Live Photo video must not be a separate file")
	}

	c, ok := files["clip.mov"]
	if ffprobe == "" {
		if ok || len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0].Reason, "ffprobe") {
			t.Errorf("without ffprobe clip.mov must be skipped: %+v", res.Skipped)
		}
		return
	}
	if !ok {
		t.Fatalf("clip.mov missing; skipped: %v", res.Skipped)
	}
	if !c.Time.Equal(time.Date(2026, 6, 27, 7, 45, 0, 0, time.UTC)) || c.Naive {
		t.Errorf("clip time %v", c.Time)
	}
	if c.Lat == nil || *c.Lat != 66.701 || *c.Lon != 27.56 || c.AccuracyM == nil || *c.AccuracyM != 12.5 {
		t.Errorf("clip position %v,%v acc %v", c.Lat, c.Lon, c.AccuracyM)
	}
	if c.Width != 48 || c.Height != 64 || c.Rotation != 90 || !approx(c.DurationS, 1.5, 0.05) {
		t.Errorf("clip dims %dx%d rot %d dur %v", c.Width, c.Height, c.Rotation, c.DurationS)
	}
	if len(res.Skipped) != 0 {
		t.Errorf("skipped: %v", res.Skipped)
	}
}

func TestScanWithoutFFprobeSkipsVideos(t *testing.T) {
	res, err := Scan(fixtureDir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skipped) != 1 || filepath.Base(res.Skipped[0].Path) != "clip.mov" ||
		!strings.Contains(res.Skipped[0].Reason, "ffprobe not found") {
		t.Errorf("skipped = %+v", res.Skipped)
	}
	// Live Photo pairing does not need ffprobe.
	if res.LivePairs != 1 {
		t.Errorf("pairs = %d", res.LivePairs)
	}
}

func TestScanMissingDir(t *testing.T) {
	if _, err := Scan(filepath.Join(fixtureDir, "nope"), Options{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestPairLivePhotos(t *testing.T) {
	in := []File{
		{Rel: "a/IMG_1.HEIC", Kind: KindPhoto},
		{Rel: "a/img_1.mp4", Kind: KindVideo},
		{Rel: "b/IMG_1.MOV", Kind: KindVideo}, // other folder: not a pair
		{Rel: "IMG_2.MOV", Kind: KindVideo},
		{Rel: "IMG_3.JPG", Kind: KindPhoto},
	}
	out, pairs := pairLivePhotos(in)
	if pairs != 1 || len(out) != 4 {
		t.Fatalf("pairs %d, files %d: %+v", pairs, len(out), out)
	}
	var rels []string
	for _, f := range out {
		rels = append(rels, f.Rel)
	}
	if got := strings.Join(rels, " "); got != "IMG_2.MOV IMG_3.JPG a/IMG_1.HEIC b/IMG_1.MOV" {
		t.Errorf("order = %s", got)
	}
	if out[2].LivePhoto == nil || out[2].LivePhoto.Rel != "a/img_1.mp4" {
		t.Errorf("live = %+v", out[2].LivePhoto)
	}
}

func TestParseISO6709(t *testing.T) {
	for _, tc := range []struct {
		in       string
		lat, lon float64
	}{
		{"+66.7003+027.5555/", 66.7003, 27.5555},
		{"+65.0078+025.5037+009.489/", 65.0078, 25.5037},
		{"-33.8688+151.2093/", -33.8688, 151.2093},
		{"+40.7128-074.0060-010.5/", 40.7128, -74.006},
		{"+4042.77-07400.36/", 40 + 42.77/60, -(74 + 0.36/60)},                // DDMM.MM
		{"+404246.2-0740021.6/", 40 + 42.0/60 + 46.2/3600, -(74 + 21.6/3600)}, // DDMMSS
		{"+66.7003+027.5555", 66.7003, 27.5555},
		{"+66.7003+027.5555+120CRSWGS_84/", 66.7003, 27.5555},
		{"+60.1699+24.9384/", 60.1699, 24.9384}, // unpadded longitude
		{"+5.12+100.1/", 5.12, 100.1},           // unpadded latitude
		{"-5.5-7.25/", -5.5, -7.25},
	} {
		lat, lon, err := ParseISO6709(tc.in)
		if err != nil || !approx(lat, tc.lat, 1e-9) || !approx(lon, tc.lon, 1e-9) {
			t.Errorf("%q = %v,%v,%v; want %v,%v", tc.in, lat, lon, err, tc.lat, tc.lon)
		}
	}
	for _, bad := range []string{"", "66.7,27.5", "+95.0+027.0/", "+66.7003/", "+6+027.5/", "+66.7+27/"} {
		if _, _, err := ParseISO6709(bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func TestApplyProbe(t *testing.T) {
	const js = `{
	 "streams": [
	  {"codec_type": "video", "width": 1920, "height": 1080, "color_transfer": "arib-std-b67",
	   "side_data_list": [{"side_data_type": "DOVI configuration record"},
	                      {"side_data_type": "Display Matrix", "rotation": -90}]},
	  {"codec_type": "audio"}
	 ],
	 "format": {"duration": "5.131700", "tags": {
	  "creation_time": "2026-06-27T07:39:15.000000Z",
	  "com.apple.quicktime.location.accuracy.horizontal": "5607.551226",
	  "com.apple.quicktime.location.ISO6709": "+66.7003+027.5555/",
	  "com.apple.quicktime.creationdate": "2026-06-27T10:39:15+0300"}}
	}`
	var f File
	if err := applyProbe(&f, []byte(js)); err != nil {
		t.Fatal(err)
	}
	if _, off := f.Time.Zone(); !f.Time.Equal(time.Date(2026, 6, 27, 7, 39, 15, 0, time.UTC)) || off != 3*3600 {
		t.Errorf("time %v", f.Time)
	}
	if !f.HasOffset {
		t.Error("creationdate must set HasOffset")
	}
	if f.Width != 1080 || f.Height != 1920 || f.Rotation != -90 || !f.HDR {
		t.Errorf("dims %dx%d rot %d hdr %v", f.Width, f.Height, f.Rotation, f.HDR)
	}
	if *f.Lat != 66.7003 || *f.AccuracyM != 5607.551226 || f.DurationS != 5.1317 {
		t.Errorf("pos %v acc %v dur %v", *f.Lat, *f.AccuracyM, f.DurationS)
	}

	// Only creation_time (UTC), no location, legacy rotate tag.
	const js2 = `{"streams": [{"codec_type": "video", "width": 640, "height": 480, "tags": {"rotate": "270"}}],
	 "format": {"duration": "1.0", "tags": {"creation_time": "2026-06-28T05:00:00.000000Z"}}}`
	var g File
	if err := applyProbe(&g, []byte(js2)); err != nil {
		t.Fatal(err)
	}
	if !g.Time.Equal(time.Date(2026, 6, 28, 5, 0, 0, 0, time.UTC)) || g.Naive || g.HasOffset || g.Lat != nil {
		t.Errorf("fallback: %+v", g)
	}
	if g.Width != 480 || g.Height != 640 || g.HDR {
		t.Errorf("fallback dims %dx%d", g.Width, g.Height)
	}

	var h File
	if err := applyProbe(&h, []byte(`{"streams": [{"codec_type": "audio"}], "format": {}}`)); err == nil {
		t.Error("expected error for a file without video stream")
	}
}
