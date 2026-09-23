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
	}
	if len(s.Skipped) == 0 {
		ew.printf("Skipped: none\n")
	} else {
		ew.printf("Skipped: %d\n", len(s.Skipped))
		for _, sk := range s.Skipped {
			ew.printf("  %s: %s\n", sk.Path, sk.Reason)
		}
	}
	return ew.err
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
