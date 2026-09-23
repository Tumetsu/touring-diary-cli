package build

import (
	"fmt"
	"io"

	"github.com/Tumetsu/touring-diary-cli/internal/convert"
	"github.com/Tumetsu/touring-diary-cli/internal/model"
)

var sourceOrder = []model.PlacementSource{
	model.SourceGPS, model.SourceManual, model.SourceInterpolated, model.SourceSnapped, model.SourceNone,
}

// Print writes the end-of-run summary (spec section 3).
func (s *Summary) Print(w io.Writer) error {
	ew := &errWriter{w: w}
	ew.printf("Wrote %s\n", s.OutPath)
	ew.printf("Timezone: %s, days: %d\n", s.Timezone, s.Days)
	ew.printf("Tracks: %d (%d points)\n", s.Tracks, s.TrackPoints)
	ew.printf("Notes: %d\n", s.Notes)
	for _, src := range sourceOrder {
		ew.printf("  %-13s %d\n", string(src)+":", s.NotesBy[src])
	}
	ew.printf("Media: %d (%d photos, %d videos", s.Media, s.Photos, s.Videos)
	if s.LivePairs > 0 {
		ew.printf("; %d Live Photo videos merged into their photos", s.LivePairs)
	}
	ew.printf("; %d files scanned)\n", s.MediaScanned)
	for _, src := range sourceOrder {
		ew.printf("  %-13s %d\n", string(src)+":", s.MediaBy[src])
	}
	if c := s.MediaConversion; c != (convert.Stats{}) {
		ew.printf("Conversion: %d converted, %d cached, %d copied, %d failed\n", c.Converted, c.Cached, c.Copied, c.Failed)
		if c.VideoOutputsRemoved > 0 {
			ew.printf("removed %d video outputs (--no-video)\n", c.VideoOutputsRemoved)
		}
	}
	if s.Format != "" {
		ew.printf("Images: %s, photos and posters %d px q%d, thumbnails %d px q%d\n",
			s.Format, s.PhotoSize, s.PhotoQuality, s.ThumbSize, s.ThumbQuality)
	}
	if len(s.Skipped) == 0 {
		ew.printf("Skipped: none\n")
	} else {
		ew.printf("Skipped: %d\n", len(s.Skipped))
		for _, sk := range s.Skipped {
			ew.printf("  %s: %s\n", sk.Path, sk.Reason)
		}
	}
	if s.CloudflareAuth != "" {
		ew.printf("cloudflare auth: %s %s\n", CloudflareWorker, s.CloudflareAuth)
	}
	s.printSize(ew)
	return ew.err
}

// printSize writes the output size report (spec section 3).
func (s *Summary) printSize(ew *errWriter) {
	z := s.Size
	ew.printf("Output size: %.1f MB in %d files (without %s)\n", MB(z.Total), z.Files, convert.CacheFile)
	rows := []struct {
		name string
		n    int64
	}{
		{"photos", z.Photos}, {"thumbnails", z.Thumbs}, {"videos", z.Videos}, {"posters", z.Posters},
		{"trip.json + site assets", z.Site},
	}
	for _, r := range rows {
		ew.printf("  %-24s %8.1f MB\n", r.name+":", MB(r.n))
	}
	if z.Unused > 0 {
		ew.printf("  %-24s %8.1f MB (kept for the cache, not in trip.json; no need to publish)\n", "unused media:", MB(z.Unused))
	}
	if z.Largest != "" {
		ew.printf("  largest file: %s (%.1f MB)\n", z.Largest, MB(z.LargestSize))
	}
	if s.MaxOutputMB > 0 && MB(z.Total) > s.MaxOutputMB {
		ew.printf("warning: output is %.1f MB, over the --max-output-mb limit of %g MB\n", MB(z.Total), s.MaxOutputMB)
	}
}

type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) printf(format string, args ...any) {
	if e.err == nil {
		_, e.err = fmt.Fprintf(e.w, format, args...)
	}
}
