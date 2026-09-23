package media

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeFFprobe puts an executable shell script named ffprobe first on PATH
// and returns its path.
func fakeFFprobe(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ffprobe"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	p, err := exec.LookPath("ffprobe")
	if err != nil || filepath.Dir(p) != dir {
		t.Fatalf("fake ffprobe not first on PATH: %v %s", err, p)
	}
	return p
}

const hdrProbeJSON = `{"streams": [{"codec_type": "video", "width": 64, "height": 48, "color_transfer": "arib-std-b67"}],
 "format": {"duration": "1.0", "tags": {"com.apple.quicktime.creationdate": "2026-06-27T10:45:00+0300"}}}`

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestProbeTimeout(t *testing.T) {
	ffprobe := fakeFFprobe(t, "exec sleep 5\n")
	old := probeTimeout
	probeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { probeTimeout = old })
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "slow.mov"), []byte("x"))
	start := time.Now()
	res, err := Scan(dir, Options{FFprobe: ffprobe})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("probe not cut off: took %s", time.Since(start))
	}
	if len(res.Files) != 0 || len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0].Reason, "timed out") {
		t.Errorf("result %+v", res)
	}
}

func TestProbeOddFilenames(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(t.TempDir(), "args")
	ffprobe := fakeFFprobe(t, `printf '%s\n' "$@" > `+argsFile+"\ncat <<'EOF'\n"+hdrProbeJSON+"\nEOF\n")
	writeFile(t, filepath.Join(dir, "-x.mov"), []byte("x"))
	res, err := Scan(dir, Options{FFprobe: ffprobe})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 1 {
		t.Fatalf("result %+v", res)
	}
	b, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(b)), "\n")
	abs, _ := filepath.Abs(filepath.Join(dir, "-x.mov"))
	if last := args[len(args)-1]; last != "file:"+abs {
		t.Errorf("input arg = %q, want file:%s", last, abs)
	}
	if !strings.Contains(string(b), "-v\nerror\n") {
		t.Errorf("args %q lack -v error", args)
	}
}

func TestProbeOddFilenamesRealFFprobe(t *testing.T) {
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	src, err := os.ReadFile(filepath.Join(fixtureDir, "clip.mov"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	names := []string{"-x.mov", "a:b.mov"}
	if runtime.GOOS == "windows" {
		names = names[:1]
	}
	for _, n := range names {
		writeFile(t, filepath.Join(dir, n), src)
	}
	writeFile(t, filepath.Join(dir, "broken.mov"), []byte("not a video"))
	res, err := Scan(dir, Options{FFprobe: ffprobe})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != len(names) {
		t.Errorf("files %d, skipped %+v", len(res.Files), res.Skipped)
	}
	// With -v error the skip reason carries ffprobe's own message.
	if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0].Reason, "Invalid data") {
		t.Errorf("skipped %+v", res.Skipped)
	}
}

func TestScanProbesLivePhotoVideos(t *testing.T) {
	ffprobe := fakeFFprobe(t, "cat <<'EOF'\n"+hdrProbeJSON+"\nEOF\n")
	jpg, err := os.ReadFile(filepath.Join(fixtureDir, "IMG_0100.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "IMG_1.jpg"), jpg)
	writeFile(t, filepath.Join(dir, "IMG_1.mov"), []byte("x"))
	for _, live := range []bool{false, true} {
		res, err := Scan(dir, Options{FFprobe: ffprobe, LivePhotos: live})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Files) != 1 || res.Files[0].LivePhoto == nil {
			t.Fatalf("live %v: %+v", live, res)
		}
		if lp := res.Files[0].LivePhoto; lp.HDR != live {
			t.Errorf("live %v: Live Photo HDR = %v", live, lp.HDR)
		}
		if len(res.Rels) != 2 {
			t.Errorf("rels %v", res.Rels)
		}
	}
}

func TestReadImageIFD0OffsetFallback(t *testing.T) {
	// DateTimeOriginal without OffsetTimeOriginal, but with OffsetTime.
	tiff := buildTIFF([][]tiffEntry{
		{short(0x0112, 1), ifdPointer(0x8769, 1)},
		{ascii(0x9003, "2026:06:27 10:00:00"), ascii(0x9010, "+03:00")},
	})
	p := filepath.Join(t.TempDir(), "a.jpg")
	writeFile(t, p, jpegWithExif(t, 8, 8, tiff))
	f := File{Path: p}
	if err := readImage(&f); err != nil {
		t.Fatal(err)
	}
	if f.Naive || !f.Time.Equal(time.Date(2026, 6, 27, 7, 0, 0, 0, time.UTC)) {
		t.Errorf("time %v naive %v", f.Time, f.Naive)
	}
}
