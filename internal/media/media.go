package media

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tuomassalmi/touring-diary/internal/model"
)

// Kind is the kind of a media file.
type Kind string

// Media kinds; the values match the item kinds in trip.json.
const (
	KindPhoto Kind = "photo"
	KindVideo Kind = "video"
)

// Formats by lower-case extension.
var formats = map[string]struct {
	kind   Kind
	format string
}{
	".heic": {KindPhoto, "heic"},
	".heif": {KindPhoto, "heic"},
	".jpg":  {KindPhoto, "jpeg"},
	".jpeg": {KindPhoto, "jpeg"},
	".png":  {KindPhoto, "png"},
	".webp": {KindPhoto, "webp"},
	".mov":  {KindVideo, "mov"},
	".mp4":  {KindVideo, "mp4"},
}

// File is one media file with the metadata read from it.
type File struct {
	// Path is the file path (media dir joined with Rel).
	Path string
	// Rel is the slash-separated path relative to the media dir. It keys the
	// output id and the conversion cache.
	Rel string
	// Name is the base filename, used to match overrides.
	Name    string
	Kind    Kind
	Format  string // heic, jpeg, png, webp, mov, mp4
	Size    int64
	ModTime time.Time

	// Time is the capture time, zero when the file has none. It keeps the
	// offset from the metadata. When Naive is true it holds the wall clock
	// as if it were UTC and must be reinterpreted in the trip zone.
	Time  time.Time
	Naive bool
	// HasOffset is true when the time came with an explicit UTC offset from
	// the device (EXIF OffsetTimeOriginal, QuickTime
	// com.apple.quicktime.creationdate). Only these vote in timezone
	// inference: a UTC creation_time says nothing about the local zone.
	HasOffset bool
	// Lat/Lon are nil when the file has no position.
	Lat, Lon *float64
	// AccuracyM is the reported horizontal accuracy in metres, nil if unknown.
	AccuracyM *float64
	// Orientation is the EXIF orientation (1..8) of photos; 0 when absent.
	Orientation int
	// Width/Height are display (upright) pixel dimensions when known.
	Width, Height int

	// Video only.
	DurationS float64
	Rotation  int  // display rotation in degrees from the container
	HDR       bool // HLG or PQ transfer; needs tone mapping for SDR output

	// LivePhoto is the paired motion video of a photo (spec 2.3). Its
	// metadata is read only with Options.LivePhotos (for the HDR flag).
	LivePhoto *File
}

// Options configures Scan.
type Options struct {
	// FFprobe is the ffprobe executable; empty means unavailable, in which
	// case videos are skipped.
	FFprobe string
	// NoVideo skips videos (not Live Photo motion files) without probing.
	NoVideo bool
	// LivePhotos also probes paired Live Photo videos, so they are
	// converted with the right settings (HDR tone mapping).
	LivePhotos bool
}

// Result is the outcome of scanning a media folder.
type Result struct {
	// Files are sorted by Rel. Live Photo videos appear only as LivePhoto of
	// their photo.
	Files    []File
	Skipped  []model.Skipped
	Warnings []string
	// Scanned counts every media file found, Live Photo videos included.
	Scanned int
	// Rels lists the relative path of every media file found, Live Photo
	// videos and skipped files included.
	Rels []string
	// LivePairs counts photos with a paired Live Photo video.
	LivePairs int
}

// IsMedia reports whether name has a supported media extension.
func IsMedia(name string) bool {
	_, ok := formats[strings.ToLower(filepath.Ext(name))]
	return ok
}

// Scan walks dir recursively and reads metadata from every supported file.
// Only an unreadable dir is an error; bad files end up in Result.Skipped.
func Scan(dir string, opts Options) (Result, error) {
	var res Result
	var found []File
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == dir {
				return err
			}
			res.Skipped = append(res.Skipped, model.Skipped{Path: path, Reason: err.Error()})
			return nil
		}
		name := d.Name()
		if path != dir && strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		f, ok := formats[strings.ToLower(filepath.Ext(name))]
		if !ok {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			res.Skipped = append(res.Skipped, model.Skipped{Path: path, Reason: err.Error()})
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		found = append(found, File{
			Path: path, Rel: filepath.ToSlash(rel), Name: name, Kind: f.kind, Format: f.format,
			Size: info.Size(), ModTime: info.ModTime(),
		})
		return nil
	})
	if err != nil {
		return res, fmt.Errorf("read media dir: %w", err)
	}
	res.Scanned = len(found)
	for _, f := range found {
		res.Rels = append(res.Rels, f.Rel)
	}
	files, pairs := pairLivePhotos(found)
	res.LivePairs = pairs

	// Read metadata in parallel; results go back into files by index.
	type outcome struct{ skip, warn string }
	outcomes := make([]outcome, len(files))
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.NumCPU())
	for i := range files {
		f := &files[i]
		if f.Kind == KindVideo {
			switch {
			case opts.NoVideo:
				outcomes[i].skip = "videos disabled (--no-video)"
				continue
			case opts.FFprobe == "":
				outcomes[i].skip = "ffprobe not found; cannot read video metadata"
				continue
			}
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			var err error
			if f.Kind == KindPhoto {
				err = readImage(f)
				if lp := f.LivePhoto; err == nil && lp != nil && opts.LivePhotos && opts.FFprobe != "" {
					if perr := readVideo(lp, opts.FFprobe); perr != nil {
						outcomes[i].warn = fmt.Sprintf("%s: Live Photo video: %v", lp.Path, perr)
					}
				}
			} else {
				err = readVideo(f, opts.FFprobe)
			}
			if err != nil {
				outcomes[i].skip = err.Error()
			}
		}()
	}
	wg.Wait()
	for i, f := range files {
		if w := outcomes[i].warn; w != "" {
			res.Warnings = append(res.Warnings, w)
		}
		if o := outcomes[i]; o.skip != "" {
			res.Skipped = append(res.Skipped, model.Skipped{Path: f.Path, Reason: o.skip})
			continue
		}
		res.Files = append(res.Files, f)
	}
	return res, nil
}

// pairLivePhotos merges videos into photos with the same directory and stem
// (case-insensitive). The result is sorted by Rel.
func pairLivePhotos(in []File) ([]File, int) {
	stem := func(f File) string {
		return strings.ToLower(strings.TrimSuffix(f.Rel, filepath.Ext(f.Rel)))
	}
	photos := map[string]int{}
	var out []File
	for _, f := range in {
		if f.Kind == KindPhoto {
			if _, dup := photos[stem(f)]; !dup {
				photos[stem(f)] = len(out)
			}
			out = append(out, f)
		}
	}
	pairs := 0
	for _, f := range in {
		if f.Kind != KindVideo {
			continue
		}
		if i, ok := photos[stem(f)]; ok && out[i].LivePhoto == nil {
			v := f
			out[i].LivePhoto = &v
			pairs++
			continue
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rel < out[j].Rel })
	return out, pairs
}
