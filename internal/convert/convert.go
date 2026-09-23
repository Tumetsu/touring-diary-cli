package convert

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tuomassalmi/touring-diary/internal/media"
)

// Default output sizes (long edge, pixels).
const (
	DefaultPhotoSize = 1600
	DefaultThumbSize = 320
)

// MediaDir is the output subfolder, also the URL prefix in trip.json.
const MediaDir = "media"

// pipelineVersion is bumped when conversion output changes in a way the
// parameters do not capture, invalidating every cache entry.
const pipelineVersion = 1

// ID returns the output id for a source path relative to the media dir:
// the first 10 hex chars of its SHA-1 (spec 4.5).
func ID(rel string) string {
	sum := sha1.Sum([]byte(filepath.ToSlash(rel)))
	return hex.EncodeToString(sum[:])[:10]
}

// Params are the conversion settings.
type Params struct {
	PhotoSize, ThumbSize int
	// FFmpeg is the ffmpeg executable; empty means unavailable, in which case
	// videos are copied unchanged.
	FFmpeg string
	// LivePhotos converts the motion video paired with a photo.
	LivePhotos bool
}

// Output is what conversion produced for one media file. Paths are
// relative to the output dir and slash-separated (as used in trip.json).
type Output struct {
	Src    string
	Thumb  string // photos, and videos with a poster
	Poster string // videos; empty when copied without ffmpeg
	// LivePhoto is the converted motion video; empty unless Params.LivePhotos.
	LivePhoto string
	// Width/Height are the upright source dimensions (photos) or display
	// dimensions (videos).
	Width, Height int
	Err           error
}

// Stats counts conversion work.
type Stats struct {
	Converted, Cached, Copied, Failed int
}

// Converter converts media into <OutDir>/media.
type Converter struct {
	OutDir  string
	Params  Params
	Force   bool
	Verbose bool
	Log     *log.Logger
	// Workers overrides the worker count (default runtime.NumCPU()).
	Workers int
}

// task is one unit of conversion work, cached independently.
type task struct {
	file   media.File
	kind   string // "photo", "video", "live"
	id     string
	parent int // index into files
}

func (t task) key() string { return t.file.Rel }

// Run converts files and returns one Output per file, in order. Individual
// failures are reported in Output.Err; the error return is for failures
// that affect the whole run (output dir not writable).
func (c *Converter) Run(files []media.File) ([]Output, Stats, error) {
	var st Stats
	lg := c.Log
	if lg == nil {
		lg = log.New(io.Discard, "", 0)
	}
	dir := filepath.Join(c.OutDir, MediaDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, st, fmt.Errorf("create media dir: %w", err)
	}
	cache := loadCache(c.OutDir)
	if c.Force {
		cache = newCache()
	}
	canToneMap := c.Params.FFmpeg != "" && hasFilter(c.Params.FFmpeg, "zscale") && hasFilter(c.Params.FFmpeg, "tonemap")
	if c.Params.FFmpeg == "" && hasVideo(files, c.Params.LivePhotos) {
		lg.Printf("warning: ffmpeg not found; videos are copied unconverted and may not play in browsers")
	}

	var tasks []task
	for i, f := range files {
		switch f.Kind {
		case media.KindPhoto:
			tasks = append(tasks, task{file: f, kind: "photo", id: ID(f.Rel), parent: i})
			if c.Params.LivePhotos && f.LivePhoto != nil {
				tasks = append(tasks, task{file: *f.LivePhoto, kind: "live", id: ID(f.LivePhoto.Rel), parent: i})
			}
		case media.KindVideo:
			tasks = append(tasks, task{file: f, kind: "video", id: ID(f.Rel), parent: i})
		}
	}
	// Videos first: they take longest, so starting them early shortens the tail.
	sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].kind != "photo" && tasks[j].kind == "photo" })

	outs := make([]Output, len(files))
	for i, f := range files {
		outs[i].Width, outs[i].Height = f.Width, f.Height
	}
	results := make([]taskResult, len(tasks))
	workers := c.Workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	// ffmpeg is itself multi-threaded; a couple at a time is enough.
	ffmpegSem := make(chan struct{}, max(1, min(2, workers)))
	var done atomic.Int64
	var wg sync.WaitGroup
	ch := make(chan int)
	var mu sync.Mutex
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range ch {
				t := tasks[i]
				params := c.paramsHash(t, canToneMap)
				mu.Lock()
				ent, hit := cache.lookup(t.key(), t.file, params, c.OutDir)
				mu.Unlock()
				var r taskResult
				start := time.Now()
				if hit {
					r = taskResult{entry: ent, cached: true}
				} else {
					if t.kind != "photo" && c.Params.FFmpeg != "" {
						ffmpegSem <- struct{}{}
						r = c.runTask(t, params, canToneMap)
						<-ffmpegSem
					} else {
						r = c.runTask(t, params, canToneMap)
					}
					if r.err == nil {
						mu.Lock()
						cache.Entries[t.key()] = r.entry
						mu.Unlock()
					}
				}
				results[i] = r
				n := done.Add(1)
				switch {
				case c.Verbose:
					state := "converted"
					switch {
					case r.err != nil:
						state = "FAILED"
					case r.cached:
						state = "cached"
					}
					lg.Printf("media %d/%d %s %s (%s)", n, len(tasks), state, t.file.Rel, time.Since(start).Round(time.Millisecond))
				case n%25 == 0 || int(n) == len(tasks):
					lg.Printf("converted %d/%d", n, len(tasks))
				}
			}
		}()
	}
	for i := range tasks {
		ch <- i
	}
	close(ch)
	wg.Wait()

	keep := map[string]bool{}
	for i, t := range tasks {
		r := results[i]
		o := &outs[t.parent]
		if r.err != nil {
			st.Failed++
			if t.kind == "live" {
				lg.Printf("warning: %s: live photo video: %v", t.file.Rel, r.err)
			} else {
				o.Err = r.err
			}
			continue
		}
		switch {
		case r.cached:
			st.Cached++
		case r.copied:
			st.Copied++
		default:
			st.Converted++
		}
		for _, p := range r.entry.Outputs {
			keep[p] = true
		}
		e := r.entry
		switch t.kind {
		case "photo":
			o.Src, o.Thumb = e.Outputs[0], e.Outputs[1]
			o.Width, o.Height = e.Width, e.Height
		case "video":
			o.Src = e.Outputs[0]
			if len(e.Outputs) > 1 {
				o.Poster = e.Outputs[1]
			}
			if len(e.Outputs) > 2 {
				o.Thumb = e.Outputs[2]
			}
		case "live":
			o.LivePhoto = e.Outputs[0]
		}
	}
	cache.prune(keep)
	if err := cache.save(c.OutDir); err != nil {
		return outs, st, err
	}
	if n := pruneOutputs(dir, keep); n > 0 {
		lg.Printf("media: removed %d stale output files", n)
	}
	return outs, st, nil
}

type taskResult struct {
	entry  cacheEntry
	cached bool
	copied bool
	err    error
}

// runTask performs one conversion and returns its cache entry.
func (c *Converter) runTask(t task, params string, canToneMap bool) taskResult {
	f := t.file
	out := func(suffix string) (rel, abs string) {
		rel = MediaDir + "/" + t.id + suffix
		return rel, filepath.Join(c.OutDir, filepath.FromSlash(rel))
	}
	ent := cacheEntry{Size: f.Size, ModTime: f.ModTime.UnixNano(), Params: params}
	switch t.kind {
	case "photo":
		pRel, pAbs := out(".jpg")
		tRel, tAbs := out("_thumb.jpg")
		res, err := convertImage(f.Path, f.Format, f.Orientation, pAbs, tAbs, c.Params)
		if err != nil {
			return taskResult{err: err}
		}
		ent.Outputs = []string{pRel, tRel}
		ent.Width, ent.Height = res.width, res.height
		return taskResult{entry: ent}
	default: // video, live
		if c.Params.FFmpeg == "" {
			rel, abs := out("." + strings.ToLower(strings.TrimPrefix(filepath.Ext(f.Path), ".")))
			if err := copyFile(f.Path, abs); err != nil {
				return taskResult{err: err}
			}
			ent.Outputs = []string{rel}
			return taskResult{entry: ent, copied: true}
		}
		rel, abs := out(".mp4")
		if err := transcode(c.Params.FFmpeg, f.Path, abs, f.HDR, canToneMap); err != nil {
			return taskResult{err: err}
		}
		ent.Outputs = []string{rel}
		if t.kind == "video" {
			pRel, pAbs := out("_poster.jpg")
			tRel, tAbs := out("_thumb.jpg")
			if err := extractPoster(c.Params.FFmpeg, f.Path, pAbs, tAbs, f.DurationS, f.HDR, canToneMap, c.Params); err != nil {
				return taskResult{err: fmt.Errorf("poster: %w", err)}
			}
			ent.Outputs = append(ent.Outputs, pRel, tRel)
		}
		return taskResult{entry: ent}
	}
}

// paramsHash identifies the settings that affect a task's outputs.
func (c *Converter) paramsHash(t task, canToneMap bool) string {
	var s string
	switch {
	case t.kind == "photo":
		s = fmt.Sprintf("photo v%d size=%d q=%d thumb=%d q=%d", pipelineVersion,
			c.Params.PhotoSize, PhotoQuality, c.Params.ThumbSize, ThumbQuality)
	case c.Params.FFmpeg == "":
		s = fmt.Sprintf("%s v%d copy", t.kind, pipelineVersion)
	default:
		s = fmt.Sprintf("%s v%d %s", t.kind, pipelineVersion,
			strings.Join(transcodeArgs("SRC", "DST", t.file.HDR, canToneMap), " "))
		if t.kind == "video" {
			s += fmt.Sprintf(" poster size=%d q=%d at=%g thumb=%d q=%d", c.Params.PhotoSize, PhotoQuality,
				posterAt(t.file.DurationS), c.Params.ThumbSize, ThumbQuality)
		}
	}
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:8])
}

func hasVideo(files []media.File, live bool) bool {
	for _, f := range files {
		if f.Kind == media.KindVideo || (live && f.LivePhoto != nil) {
			return true
		}
	}
	return false
}

var outputNameRe = regexp.MustCompile(`^[0-9a-f]{10}(_thumb|_poster)?\.[a-z0-9]+(\.tmp)?$`)

// pruneOutputs removes files in dir that look like conversion outputs but
// are not in keep (paths relative to the out dir). Returns the count.
func pruneOutputs(dir string, keep map[string]bool) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() || !outputNameRe.MatchString(e.Name()) || keep[MediaDir+"/"+e.Name()] {
			continue
		}
		if os.Remove(filepath.Join(dir, e.Name())) == nil {
			n++
		}
	}
	return n
}
