package build

import (
	"math"
	"os/exec"
	"path"
	"sort"
	"time"

	"github.com/Tumetsu/touring-diary-cli/internal/convert"
	"github.com/Tumetsu/touring-diary-cli/internal/media"
	"github.com/Tumetsu/touring-diary-cli/internal/model"
	"github.com/Tumetsu/touring-diary-cli/internal/notes"
	"github.com/Tumetsu/touring-diary-cli/internal/placement"
	"github.com/Tumetsu/touring-diary-cli/internal/timeutil"
)

// ToolOff disables ffmpeg or ffprobe when given as Options.FFmpeg/FFprobe.
const ToolOff = "off"

// mediaRec is a media file being prepared as an item.
type mediaRec struct {
	file           media.File
	time           time.Time // UTC
	timeAssumed    bool
	title, caption string
	lat, lon       *float64
	source         model.PlacementSource // gps or manual when positioned itself
	out            convert.Output
}

// tool resolves an external tool: an explicit path, ToolOff, or a PATH lookup.
func tool(explicit, name string) string {
	switch explicit {
	case ToolOff:
		return ""
	case "":
		p, err := exec.LookPath(name)
		if err != nil {
			return ""
		}
		return p
	}
	return explicit
}

// scanMedia reads media metadata. It returns nil when no media dir is set.
func (b *builder) scanMedia() (*media.Result, error) {
	if b.opts.MediaDir == "" {
		return nil, nil
	}
	ffprobe := tool(b.opts.FFprobe, "ffprobe")
	if ffprobe == "" && !b.opts.NoVideo {
		b.log.Printf("warning: ffprobe not found; videos will be skipped")
	}
	res, err := media.Scan(b.opts.MediaDir, media.Options{FFprobe: ffprobe, NoVideo: b.opts.NoVideo,
		LivePhotos: b.opts.LivePhotos})
	if err != nil {
		return nil, err
	}
	for _, s := range res.Skipped {
		b.skip(s.Path, s.Reason)
	}
	for _, w := range res.Warnings {
		b.log.Printf("warning: %s", w)
	}
	b.mediaRels = res.Rels
	b.sum.MediaScanned = res.Scanned
	b.sum.LivePairs = res.LivePairs
	b.verbosef("media: %d files scanned, %d Live Photo videos paired", res.Scanned, res.LivePairs)
	return &res, nil
}

// mediaOffsets returns the UTC offsets of media times that carry one from
// the device, for timezone inference. UTC-only times (QuickTime
// creation_time) do not count: they say nothing about the local zone.
func mediaOffsets(res *media.Result) []int {
	if res == nil {
		return nil
	}
	var out []int
	for _, f := range res.Files {
		if !f.Time.IsZero() && f.HasOffset {
			out = append(out, timeutil.Offset(f.Time))
		}
	}
	return out
}

// mediaRecords applies overrides (keyed by filename or relative path),
// normalises times to UTC and drops files without a time.
func (b *builder) mediaRecords(res *media.Result) []mediaRec {
	if res == nil {
		return nil
	}
	// A bare filename key is ambiguous when files in different folders
	// share the name; such keys apply to nothing (use the relative path).
	names := map[string]int{}
	for _, rel := range res.Rels {
		names[path.Base(rel)]++
	}
	ambiguous := map[string]bool{}
	var recs []mediaRec
	for _, f := range res.Files {
		r := mediaRec{file: f, time: f.Time, timeAssumed: f.Naive, lat: f.Lat, lon: f.Lon}
		if f.Naive {
			r.time = reinterpret(f.Time, b.zone.Loc)
		}
		if r.lat != nil {
			r.source = model.SourceGPS
		}
		key, ok := f.Rel, false
		if _, ok = b.overrides[key]; !ok {
			key = f.Name
			_, ok = b.overrides[key]
			if ok && names[key] > 1 {
				ok = false
				b.usedOverrides[key] = true
				if !ambiguous[key] {
					ambiguous[key] = true
					b.log.Printf("warning: override %q matches %d files in different folders; not applied (key it by the path relative to the media folder, e.g. %q)",
						key, names[key], f.Rel)
				}
			}
		}
		if ok {
			b.usedOverrides[key] = true
			ov := b.overrides[key]
			if ov.Skip {
				b.skip(f.Path, "skipped by override")
				continue
			}
			if ov.Time != nil {
				ot, naive, err := notes.ParseTimestamp(*ov.Time)
				if err != nil {
					b.log.Printf("warning: override %q: %v; keeping original time", key, err)
				} else {
					r.timeAssumed = naive
					if naive {
						ot = reinterpret(ot, b.zone.Loc)
					}
					r.time = ot
				}
			}
			if ov.Lat != nil && ov.Lon != nil {
				r.lat, r.lon = ov.Lat, ov.Lon
				r.source = model.SourceManual
			}
			if ov.Title != nil {
				r.title = *ov.Title
			}
			if ov.Caption != nil {
				r.caption = *ov.Caption
			}
		}
		if r.time.IsZero() {
			b.skip(f.Path, "no timestamp in metadata (add an override with \"time\")")
			continue
		}
		r.time = r.time.UTC()
		recs = append(recs, r)
	}
	return recs
}

// convertMedia runs the conversion stage and drops records that failed.
func (b *builder) convertMedia(recs []mediaRec) ([]mediaRec, error) {
	if len(recs) == 0 && b.opts.MediaDir == "" {
		return recs, nil
	}
	files := make([]media.File, len(recs))
	for i, r := range recs {
		files[i] = r.file
	}
	c := convert.Converter{
		OutDir:  b.opts.OutDir,
		Sources: b.mediaRels,
		Params: convert.Params{
			PhotoSize:  b.opts.PhotoSize,
			ThumbSize:  b.opts.ThumbSize,
			FFmpeg:     tool(b.opts.FFmpeg, "ffmpeg"),
			LivePhotos: b.opts.LivePhotos,
		},
		Force:   b.opts.Force,
		Verbose: b.opts.Verbose,
		Log:     b.log,
	}
	start := time.Now()
	outs, st, err := c.Run(files)
	if err != nil {
		return nil, err
	}
	b.sum.MediaConversion = st
	b.verbosef("media conversion: %d converted, %d cached, %d copied, %d failed in %s",
		st.Converted, st.Cached, st.Copied, st.Failed, time.Since(start).Round(time.Millisecond))
	kept := recs[:0]
	for i, r := range recs {
		if outs[i].Err != nil {
			b.skip(r.file.Path, "conversion failed: "+outs[i].Err.Error())
			continue
		}
		r.out = outs[i]
		kept = append(kept, r)
	}
	return kept, nil
}

// mediaAnchors returns anchors for media with their own usable GPS fix or a
// manual position.
func mediaAnchors(recs []mediaRec) []placement.Anchor {
	var out []placement.Anchor
	for _, r := range recs {
		switch r.source {
		case model.SourceManual:
			out = append(out, placement.Anchor{Time: r.time, Lat: *r.lat, Lon: *r.lon, Kind: placement.KindManual})
		case model.SourceGPS:
			if placement.UsableAccuracy(r.file.AccuracyM) {
				out = append(out, placement.Anchor{Time: r.time, Lat: *r.lat, Lon: *r.lon, Kind: placement.KindMedia})
			}
		}
	}
	return out
}

// placeMedia positions media records, sorts them chronologically and
// assigns ids "m" + the output id of the source path, which stay stable
// when other files are added.
func (b *builder) placeMedia(recs []mediaRec, placer *placement.Placer) []model.Item {
	sort.SliceStable(recs, func(i, j int) bool {
		if !recs[i].time.Equal(recs[j].time) {
			return recs[i].time.Before(recs[j].time)
		}
		return recs[i].file.Rel < recs[j].file.Rel
	})
	items := make([]model.Item, 0, len(recs))
	seen := map[string]bool{}
	for _, r := range recs {
		f := r.file
		it := model.Item{
			ID: uniqueID(seen, "m"+convert.ID(f.Rel)), Kind: string(f.Kind), Time: r.time, TimeAssumed: r.timeAssumed,
			Title: r.title, Caption: r.caption, Original: f.Name,
			Src: r.out.Src, Thumb: r.out.Thumb, Poster: r.out.Poster,
			Width: r.out.Width, Height: r.out.Height,
		}
		if f.Kind == media.KindVideo {
			d := model.Round(f.DurationS, 2)
			it.DurationS = &d
		}
		if r.out.LivePhoto != "" {
			lp := r.out.LivePhoto
			it.LivePhoto = &lp
		}
		if r.lat != nil {
			lat, lon := model.Round(*r.lat, 6), model.Round(*r.lon, 6)
			it.Lat, it.Lon = &lat, &lon
			it.Placement = model.Placement{Source: r.source}
			if r.source == model.SourceGPS {
				if f.AccuracyM != nil {
					acc := model.Round(*f.AccuracyM, 1)
					it.AccuracyM = &acc
				}
				if d, bad, _ := placer.CheckOwnPosition(r.time, *r.lat, *r.lon); bad {
					b.log.Printf("warning: %s: GPS position is %.1f km from the track at that time; keeping it (use an override to fix)",
						f.Rel, d/1000)
				}
			}
		} else {
			res := placer.Place(r.time)
			it.Placement = model.Placement{Source: res.Source}
			if res.HasGap {
				g := int64(math.Round(res.Gap.Seconds()))
				it.Placement.GapSeconds = &g
			}
			if res.Positioned {
				lat, lon := model.Round(res.Lat, 6), model.Round(res.Lon, 6)
				it.Lat, it.Lon = &lat, &lon
			}
		}
		if f.Kind == media.KindVideo {
			b.sum.Videos++
		} else {
			b.sum.Photos++
			if f.LivePhoto != nil {
				b.sum.LivePhotos++
			}
		}
		b.sum.MediaBy[it.Placement.Source]++
		b.verbosef("%s %s %-12s %s", it.ID, b.zone.DateKey(it.Time)+" "+it.Time.In(b.zone.Loc).Format("15:04"),
			it.Placement.Source, f.Rel)
		items = append(items, it)
	}
	b.sum.Media = len(items)
	return items
}

// mergeItems merges chronologically sorted notes and media into one
// chronological list; notes come first on equal times.
func mergeItems(ns, ms []model.Item) []model.Item {
	out := make([]model.Item, 0, len(ns)+len(ms))
	i, j := 0, 0
	for i < len(ns) || j < len(ms) {
		if j >= len(ms) || (i < len(ns) && !ms[j].Time.Before(ns[i].Time)) {
			out = append(out, ns[i])
			i++
		} else {
			out = append(out, ms[j])
			j++
		}
	}
	return out
}
