package build

import (
	"io/fs"
	"path"
	"path/filepath"
	"strings"

	"github.com/Tumetsu/touring-diary-cli/internal/convert"
)

// SizeReport is the size of an output folder by category, in bytes. The
// conversion cache and temporary files are not counted: they are not
// published.
type SizeReport struct {
	Total, Photos, Thumbs, Videos, Posters, Site int64
	// Unused are media outputs that trip.json does not reference: outputs
	// kept so that the cache survives (--no-video, a skip override, a
	// failed conversion, Live Photo videos without --live-photos). They are
	// included in Total because they are in the folder.
	Unused int64
	Files  int
	// Largest is the biggest file, slash-separated relative to the folder.
	Largest     string
	LargestSize int64
}

// MeasureOutput walks dir and sums file sizes by category. referenced holds
// the media paths used in trip.json (slash-separated, relative to dir); a
// nil map treats every media file as referenced.
func MeasureOutput(dir string, referenced map[string]bool) (SizeReport, error) {
	var r SizeReport
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == convert.CacheFile || strings.HasSuffix(rel, ".tmp") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		n := info.Size()
		r.Total += n
		r.Files++
		if n > r.LargestSize {
			r.Largest, r.LargestSize = rel, n
		}
		*r.bucket(rel, referenced) += n
		return nil
	})
	return r, err
}

// bucket returns the counter for a file relative to the output folder.
func (r *SizeReport) bucket(rel string, referenced map[string]bool) *int64 {
	if !strings.HasPrefix(rel, convert.MediaDir+"/") {
		return &r.Site
	}
	if referenced != nil && !referenced[rel] {
		return &r.Unused
	}
	name := path.Base(rel)
	switch {
	case strings.Contains(name, "_thumb."):
		return &r.Thumbs
	case strings.Contains(name, "_poster."):
		return &r.Posters
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".mp4", ".mov", ".m4v":
		return &r.Videos
	}
	return &r.Photos
}

// MB converts bytes to megabytes (1 MB = 1,000,000 bytes).
func MB(n int64) float64 { return float64(n) / 1e6 }
