package convert

import (
	"image"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tumetsu/touring-diary-cli/internal/media"
)

// fakeTool writes an executable shell script and returns its path.
func fakeTool(t *testing.T, name, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFFmpegTimeout(t *testing.T) {
	for _, tc := range []struct {
		dur  float64
		want time.Duration
	}{
		{0, 10 * time.Minute},
		{3, 11 * time.Minute},
		{60, 30 * time.Minute},
		{3600, 60 * time.Minute},
	} {
		if got := ffmpegTimeout(tc.dur); got != tc.want {
			t.Errorf("ffmpegTimeout(%v) = %s, want %s", tc.dur, got, tc.want)
		}
	}
	slow := fakeTool(t, "ffmpeg", "exec sleep 5\n")
	start := time.Now()
	err := runFFmpeg(slow, nil, nil, 200*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") || time.Since(start) > 2*time.Second {
		t.Errorf("err %v after %s", err, time.Since(start))
	}
}

func TestRunLivePhotoHDRAndOddNames(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "log")
	// Claims zscale/tonemap support, logs each run's args and writes the
	// output (last arg, "file:" stripped).
	ffmpeg := fakeTool(t, "ffmpeg", `case "$*" in *-filters*) printf ' ... zscale V->V x\n ... tonemap V->V x\n'; exit 0;; esac
printf '%s\n' "$@" >> `+logFile+`
for a; do last=$a; done
printf x > "${last#file:}"
`)
	dir := t.TempDir()
	video := filepath.Join(dir, "-x.mov")
	if err := os.WriteFile(video, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	photo := fixture(t, "IMG_0100.jpg", "jpeg", media.KindPhoto)
	photo.LivePhoto = &media.File{Path: video, Rel: "-x.mov", Name: "-x.mov", Kind: media.KindVideo, Format: "mov", HDR: true}
	out := t.TempDir()
	c := &Converter{OutDir: out, Params: Params{PhotoSize: 40, ThumbSize: 16, FFmpeg: ffmpeg, LivePhotos: true}}
	outs, st, err := c.Run([]media.File{photo})
	if err != nil || outs[0].Err != nil || st.Converted != 2 {
		t.Fatalf("err %v out %+v stats %+v", err, outs[0], st)
	}
	if outs[0].LivePhoto != "media/"+ID("-x.mov")+".mp4" {
		t.Errorf("live photo %q", outs[0].LivePhoto)
	}
	b, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs(video)
	log := string(b)
	if !strings.Contains(log, "-i\nfile:"+abs+"\n") {
		t.Errorf("input not passed as file:<abs>:\n%s", log)
	}
	if !strings.Contains(log, "tonemap=") {
		t.Errorf("HDR Live Photo not tone-mapped:\n%s", log)
	}
}

func TestPruneKeepsOutputsOfUnconvertedSources(t *testing.T) {
	out := t.TempDir()
	photo := fixture(t, "nometa.png", "png", media.KindPhoto)
	video := fixture(t, "clip.mov", "mov", media.KindVideo)
	c := &Converter{OutDir: out, Params: Params{PhotoSize: 40, ThumbSize: 16}}
	if _, _, err := c.Run([]media.File{photo, video}); err != nil {
		t.Fatal(err)
	}
	vOut := filepath.Join(out, MediaDir, ID("clip.mov")+".mov")

	// The video is left out of the run (e.g. --no-video) but still exists.
	c.Sources = []string{"nometa.png", "clip.mov"}
	if _, _, err := c.Run([]media.File{photo}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(vOut); err != nil {
		t.Errorf("output of an existing source was pruned: %v", err)
	}
	if _, ok := loadCache(out).Entries["clip.mov"]; !ok {
		t.Error("cache entry of an existing source was pruned")
	}

	// Gone from the scan: pruned.
	c.Sources = []string{"nometa.png"}
	if _, _, err := c.Run([]media.File{photo}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(vOut); !os.IsNotExist(err) {
		t.Errorf("stale output kept: %v", err)
	}
	if _, ok := loadCache(out).Entries["clip.mov"]; ok {
		t.Error("stale cache entry kept")
	}
}

func TestHEICDecodeConcurrencyCapped(t *testing.T) {
	var cur, peak atomic.Int32
	old := decodeHEIC
	decodeHEIC = func(r io.Reader) (image.Image, error) {
		n := cur.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		cur.Add(-1)
		return image.NewRGBA(image.Rect(0, 0, 4, 4)), nil
	}
	t.Cleanup(func() { decodeHEIC = old })
	src := filepath.Join(fixtureDir, "heic_gps.heic")
	out := t.TempDir()
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := filepath.Join(out, string(rune('a'+i)))
			if _, err := convertImage(src, "heic", 1, name+".jpg", "", Params{PhotoSize: 4}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if limit := int32(min(runtime.NumCPU(), 4)); peak.Load() > limit || peak.Load() < 1 {
		t.Errorf("peak concurrent HEIC decodes %d, limit %d", peak.Load(), limit)
	}
}
