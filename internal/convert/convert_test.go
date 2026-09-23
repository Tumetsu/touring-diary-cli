package convert

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tumetsu/touring-diary-cli/internal/media"
)

const fixtureDir = "../../testdata/media"

func TestID(t *testing.T) {
	// sha1("IMG_1932.JPG") starts with 514b15b870.
	if got := ID("IMG_1932.JPG"); got != "514b15b870" {
		t.Errorf("ID = %s", got)
	}
	if ID("a/b.jpg") != ID(filepath.FromSlash("a/b.jpg")) {
		t.Error("ID must not depend on the path separator")
	}
	if ID("a.jpg") == ID("A.jpg") || len(ID("x")) != 10 {
		t.Error("unexpected ID")
	}
}

func TestFitLong(t *testing.T) {
	for _, tc := range []struct{ w, h, long, ww, wh int }{
		{4032, 3024, 1600, 1600, 1200},
		{3024, 4032, 1600, 1200, 1600},
		{800, 600, 1600, 800, 600},
		{1600, 1200, 320, 320, 240},
		{5000, 10, 100, 100, 1},
	} {
		if w, h := fitLong(tc.w, tc.h, tc.long); w != tc.ww || h != tc.wh {
			t.Errorf("fitLong(%d,%d,%d) = %d,%d want %d,%d", tc.w, tc.h, tc.long, w, h, tc.ww, tc.wh)
		}
	}
}

func TestOrient(t *testing.T) {
	// 3x2 image with a marker in the top-left pixel.
	src := image.NewRGBA(image.Rect(0, 0, 3, 2))
	mark := color.RGBA{255, 0, 0, 255}
	src.SetRGBA(0, 0, mark)
	// Where the stored top-left pixel ends up after each orientation.
	want := map[int]struct{ w, h, x, y int }{
		1: {3, 2, 0, 0}, 2: {3, 2, 2, 0}, 3: {3, 2, 2, 1}, 4: {3, 2, 0, 1},
		5: {2, 3, 0, 0}, 6: {2, 3, 1, 0}, 7: {2, 3, 1, 2}, 8: {2, 3, 0, 2},
	}
	for o, w := range want {
		got := orient(src, o)
		if got.Rect.Dx() != w.w || got.Rect.Dy() != w.h || got.RGBAAt(w.x, w.y) != mark {
			t.Errorf("orientation %d: size %v, marker not at %d,%d", o, got.Rect.Size(), w.x, w.y)
		}
	}
}

// fixture returns a media.File for a testdata/media file with its real
// size and mtime.
func fixture(t *testing.T, name, format string, kind media.Kind) media.File {
	t.Helper()
	p := filepath.Join(fixtureDir, name)
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return media.File{Path: p, Rel: name, Name: name, Kind: kind, Format: format, Size: st.Size(), ModTime: st.ModTime()}
}

func decodeJPEG(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func isRed(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	return r>>8 > 150 && g>>8 < 100 && b>>8 < 100
}

func TestRunPhotosAndCache(t *testing.T) {
	out := t.TempDir()
	jpg := fixture(t, "gps_offset.jpg", "jpeg", media.KindPhoto)
	jpg.Orientation = 6
	heic := fixture(t, "heic_gps.heic", "heic", media.KindPhoto)
	png := fixture(t, "nometa.png", "png", media.KindPhoto)
	files := []media.File{jpg, heic, png}
	c := &Converter{OutDir: out, Params: Params{PhotoSize: 40, ThumbSize: 16}}

	outs, st, err := c.Run(files)
	if err != nil {
		t.Fatal(err)
	}
	if st.Converted != 3 || st.Cached != 0 {
		t.Fatalf("first run stats %+v", st)
	}
	for i, o := range outs {
		if o.Err != nil {
			t.Fatalf("%s: %v", files[i].Name, o.Err)
		}
	}
	o := outs[0]
	id := ID("gps_offset.jpg")
	if o.Src != "media/"+id+".jpg" || o.Thumb != "media/"+id+"_thumb.jpg" {
		t.Errorf("paths %s %s", o.Src, o.Thumb)
	}
	// 64x48 stored, orientation 6 -> 48x64 upright, resized to 30x40.
	if o.Width != 48 || o.Height != 64 {
		t.Errorf("dims %dx%d, want 48x64", o.Width, o.Height)
	}
	img := decodeJPEG(t, filepath.Join(out, o.Src))
	if b := img.Bounds(); b.Dx() != 30 || b.Dy() != 40 {
		t.Errorf("photo size %v", b.Size())
	}
	// Stored top-left red quadrant ends up top-right after rotating 90° CW.
	if !isRed(img.At(25, 5)) || isRed(img.At(5, 5)) {
		t.Error("orientation 6 not applied")
	}
	if b := decodeJPEG(t, filepath.Join(out, o.Thumb)).Bounds(); b.Dx() != 12 || b.Dy() != 16 {
		t.Errorf("thumb size %v", b.Size())
	}
	// HEIC comes back upright from the decoder (magick auto-oriented it).
	if h := outs[1]; h.Width != 48 || h.Height != 64 {
		t.Errorf("heic dims %dx%d", h.Width, h.Height)
	}
	if !isRed(decodeJPEG(t, filepath.Join(out, outs[1].Src)).At(25, 5)) {
		t.Error("heic pixels not upright")
	}
	if b, err := os.ReadFile(filepath.Join(out, o.Src)); err != nil || strings.Contains(string(b), "Exif") {
		t.Error("output must not carry EXIF")
	}

	// Second run: everything cached.
	if _, st, _ = c.Run(files); st.Cached != 3 || st.Converted != 0 {
		t.Errorf("second run stats %+v", st)
	}
	// Changed mtime -> miss for that file only.
	files[2].ModTime = files[2].ModTime.Add(time.Second)
	if _, st, _ = c.Run(files); st.Cached != 2 || st.Converted != 1 {
		t.Errorf("mtime change stats %+v", st)
	}
	// Deleted output -> miss.
	os.Remove(filepath.Join(out, o.Thumb))
	if _, st, _ = c.Run(files); st.Cached != 2 || st.Converted != 1 {
		t.Errorf("missing output stats %+v", st)
	}
	// Changed params -> all miss.
	c.Params.ThumbSize = 20
	if _, st, _ = c.Run(files); st.Converted != 3 {
		t.Errorf("param change stats %+v", st)
	}
	// Force -> all miss.
	c.Force = true
	if _, st, _ = c.Run(files); st.Converted != 3 {
		t.Errorf("force stats %+v", st)
	}
	c.Force = false

	// Dropping a file prunes its outputs and cache entry.
	if _, _, err := c.Run(files[:1]); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(out, MediaDir))
	if len(entries) != 2 {
		t.Errorf("after prune: %d files", len(entries))
	}
	if cc := loadCache(out); len(cc.Entries) != 1 {
		t.Errorf("cache entries %d", len(cc.Entries))
	}
}

func TestRunDecodeFailure(t *testing.T) {
	bad := fixture(t, "img_0100.MOV", "jpeg", media.KindPhoto) // not a JPEG
	outs, st, err := (&Converter{OutDir: t.TempDir(), Params: Params{PhotoSize: 40, ThumbSize: 16}}).Run([]media.File{bad})
	if err != nil || st.Failed != 1 || outs[0].Err == nil {
		t.Fatalf("err %v stats %+v out %+v", err, st, outs[0])
	}
}

func TestRunVideoWithoutFFmpegCopies(t *testing.T) {
	out := t.TempDir()
	v := fixture(t, "clip.mov", "mov", media.KindVideo)
	outs, st, err := (&Converter{OutDir: out, Params: Params{PhotoSize: 40, ThumbSize: 16}}).Run([]media.File{v})
	if err != nil || st.Copied != 1 {
		t.Fatalf("err %v stats %+v", err, st)
	}
	if outs[0].Src != "media/"+ID("clip.mov")+".mov" || outs[0].Poster != "" {
		t.Errorf("out %+v", outs[0])
	}
	if _, err := os.Stat(filepath.Join(out, outs[0].Src)); err != nil {
		t.Error(err)
	}
}

func TestRunVideoTranscode(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	res, err := media.Scan(fixtureDir, media.Options{FFprobe: ffprobe})
	if err != nil {
		t.Fatal(err)
	}
	var files []media.File
	for _, f := range res.Files {
		if f.Name == "clip.mov" || f.Name == "IMG_0100.jpg" {
			files = append(files, f)
		}
	}
	out := t.TempDir()
	c := &Converter{OutDir: out, Params: Params{PhotoSize: 40, ThumbSize: 16, FFmpeg: ffmpeg, LivePhotos: true}}
	outs, st, err := c.Run(files)
	if err != nil {
		t.Fatal(err)
	}
	// The fake Live Photo video fails (warning only); photo and clip succeed.
	if st.Converted != 2 || st.Failed != 1 {
		t.Errorf("stats %+v", st)
	}
	var clip Output
	for i, f := range files {
		if outs[i].Err != nil {
			t.Fatalf("%s: %v", f.Name, outs[i].Err)
		}
		if f.Name == "clip.mov" {
			clip = outs[i]
		} else if outs[i].LivePhoto != "" {
			t.Errorf("failed live photo must not be attached: %+v", outs[i])
		}
	}
	id := ID("clip.mov")
	if clip.Src != "media/"+id+".mp4" || clip.Poster != "media/"+id+"_poster.jpg" || clip.Width != 48 || clip.Height != 64 {
		t.Errorf("clip %+v", clip)
	}
	// Output is upright (rotation applied to pixels) with no GPS metadata.
	probe, err := exec.Command(ffprobe, "-v", "quiet", "-show_streams", "-show_format",
		filepath.Join(out, clip.Src)).Output()
	if err != nil {
		t.Fatal(err)
	}
	p := string(probe)
	for _, want := range []string{"codec_name=h264", "codec_name=aac", "width=48", "height=64"} {
		if !strings.Contains(p, want) {
			t.Errorf("transcoded output missing %q", want)
		}
	}
	for _, bad := range []string{"rotation=", "ISO6709", "TAG:location", "creationdate"} {
		if strings.Contains(p, bad) {
			t.Errorf("transcoded output contains %q", bad)
		}
	}
	if b := decodeJPEG(t, filepath.Join(out, clip.Poster)).Bounds(); b.Dx() != 30 || b.Dy() != 40 {
		t.Errorf("poster size %v", b.Size())
	}
	if clip.Thumb != "media/"+id+"_thumb.jpg" {
		t.Errorf("video thumb %q", clip.Thumb)
	} else if b := decodeJPEG(t, filepath.Join(out, clip.Thumb)).Bounds(); b.Dx() != 12 || b.Dy() != 16 {
		t.Errorf("video thumb size %v", b.Size())
	}
	if _, st, _ = c.Run(files); st.Cached != 2 || st.Failed != 1 {
		t.Errorf("second run %+v", st)
	}
}

func TestEncodeImageFormats(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 32, 24))
	for i := range img.Pix {
		img.Pix[i] = byte(i * 7)
	}
	for _, tc := range []struct {
		format string
		magic  func([]byte) bool
	}{
		{FormatJPEG, func(b []byte) bool { return len(b) > 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF }},
		{FormatWebP, func(b []byte) bool { return len(b) > 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP" }},
	} {
		var lo, hi bytes.Buffer
		if err := encodeImage(&lo, img, tc.format, 10); err != nil {
			t.Fatalf("%s: %v", tc.format, err)
		}
		if err := encodeImage(&hi, img, tc.format, 100); err != nil {
			t.Fatalf("%s: %v", tc.format, err)
		}
		if !tc.magic(lo.Bytes()) || !tc.magic(hi.Bytes()) {
			t.Errorf("%s: wrong magic bytes % x", tc.format, lo.Bytes()[:12])
		}
		if lo.Len() >= hi.Len() {
			t.Errorf("%s: quality not applied: q10 %d bytes, q100 %d bytes", tc.format, lo.Len(), hi.Len())
		}
	}
	if err := encodeImage(io.Discard, img, "gif", 80); err == nil {
		t.Error("unknown format accepted")
	}
}

func TestRunWebPOutputsAndCacheParams(t *testing.T) {
	out := t.TempDir()
	files := []media.File{fixture(t, "gps_offset.jpg", "jpeg", media.KindPhoto)}
	id := ID("gps_offset.jpg")
	c := &Converter{OutDir: out, Params: Params{PhotoSize: 40, ThumbSize: 16}}
	if _, _, err := c.Run(files); err != nil {
		t.Fatal(err)
	}
	c.Params.Format = FormatWebP
	outs, st, err := c.Run(files)
	if err != nil || outs[0].Err != nil {
		t.Fatal(err, outs[0].Err)
	}
	if st.Converted != 1 {
		t.Errorf("format change did not reconvert: %+v", st)
	}
	if outs[0].Src != "media/"+id+".webp" || outs[0].Thumb != "media/"+id+"_thumb.webp" {
		t.Errorf("paths %s %s", outs[0].Src, outs[0].Thumb)
	}
	for _, p := range []string{outs[0].Src, outs[0].Thumb} {
		b, err := os.ReadFile(filepath.Join(out, p))
		if err != nil || string(b[8:12]) != "WEBP" {
			t.Errorf("%s is not WebP: %v", p, err)
		}
	}
	// The JPEG outputs of the previous format are removed.
	if _, err := os.Stat(filepath.Join(out, "media", id+".jpg")); !os.IsNotExist(err) {
		t.Errorf("stale jpeg kept: %v", err)
	}
	// Quality changes invalidate the cache too.
	c.Params.ThumbQuality = 50
	if _, st, _ := c.Run(files); st.Converted != 1 {
		t.Errorf("quality change did not reconvert: %+v", st)
	}
	if _, st, _ := c.Run(files); st.Cached != 1 {
		t.Errorf("unchanged run not cached: %+v", st)
	}
}

func TestParamsHashJPEGUnchanged(t *testing.T) {
	// Defaults (zero values) and explicit JPEG settings hash alike, and the
	// JPEG hash keeps the pre-format-option string so old caches stay valid.
	c := &Converter{Params: Params{PhotoSize: 1600, ThumbSize: 320}}
	tk := task{kind: "photo"}
	def := c.paramsHash(tk, false)
	c.Params = Params{PhotoSize: 1600, ThumbSize: 320, Format: FormatJPEG, PhotoQuality: 85, ThumbQuality: 80}
	if c.paramsHash(tk, false) != def {
		t.Error("explicit defaults change the hash")
	}
	sum := sha1.Sum([]byte("photo v1 size=1600 q=85 thumb=320 q=80"))
	if def != hex.EncodeToString(sum[:8]) {
		t.Error("JPEG params hash changed; existing caches would be invalidated")
	}
	c.Params.Format = FormatWebP
	if c.paramsHash(tk, false) == def {
		t.Error("format not in the hash")
	}
}
