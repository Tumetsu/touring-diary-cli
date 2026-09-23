// Package build orchestrates the pipeline: ingest, time normalisation,
// anchors, placement, day grouping and writing trip.json (spec section 4).
package build

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tuomassalmi/touring-diary/internal/convert"
	"github.com/tuomassalmi/touring-diary/internal/gpx"
	"github.com/tuomassalmi/touring-diary/internal/model"
	"github.com/tuomassalmi/touring-diary/internal/notes"
	"github.com/tuomassalmi/touring-diary/internal/placement"
	"github.com/tuomassalmi/touring-diary/internal/timeutil"
)

// DefaultTitle is used when neither the CLI nor the config gives a title.
const DefaultTitle = "Trip"

// Options configures a build. Zero values mean "not set on the command
// line"; they are then filled from the config file or defaults.
type Options struct {
	GPXDir, NotesDir, MediaDir, OutDir string
	Title                              string
	TZ                                 string
	ConfigPath, OverridesPath          string
	MaxGap                             time.Duration
	Verbose                            bool
	// Media conversion (spec 4.5). Zero sizes mean the defaults.
	PhotoSize, ThumbSize int
	LivePhotos, NoVideo  bool
	Force                bool
	// FFmpeg/FFprobe are tool paths; empty means look up on PATH, ToolOff
	// means treat as missing.
	FFmpeg, FFprobe string
	// Now returns the build time; defaults to time.Now.
	Now func() time.Time
	// Log receives warnings and verbose output; defaults to discarding.
	Log *log.Logger
}

// Summary describes what a build did; see (Summary).Print.
type Summary struct {
	OutPath     string
	Timezone    string
	Tracks      int
	TrackPoints int
	Days        int
	Notes       int
	NotesBy     map[model.PlacementSource]int
	// Media counts items in trip.json; MediaScanned every media file found
	// (Live Photo videos included); LivePairs the videos merged into photos.
	Media, Photos, Videos, LivePhotos int
	MediaScanned, LivePairs           int
	MediaBy                           map[model.PlacementSource]int
	MediaConversion                   convert.Stats
	Skipped                           []model.Skipped
}

// Run executes a build and writes <out>/trip.json. It returns an error only
// for unusable configuration, unreadable input folders or a failed write;
// bad individual inputs are reported in Summary.Skipped.
func Run(opts Options) (*Summary, error) {
	b, err := newBuilder(opts)
	if err != nil {
		return nil, err
	}
	return b.run()
}

type builder struct {
	opts      Options
	cfg       Config
	log       *log.Logger
	zone      timeutil.Zone
	overrides map[string]Override
	// usedOverrides records override keys that matched a note or media file.
	usedOverrides map[string]bool
	// mediaRels are the relative paths of every scanned media file.
	mediaRels []string
	sum       *Summary
}

// noteRec is a note or waypoint being prepared as an item.
type noteRec struct {
	time        time.Time // UTC
	timeAssumed bool
	title, text string
	lat, lon    *float64
	source      model.PlacementSource // gps or manual when positioned itself
	order       int
	// idKey identifies the note across builds: source file, timestamp as
	// written and text. Its hash is the item id.
	idKey string
}

func newBuilder(opts Options) (*builder, error) {
	b := &builder{opts: opts, usedOverrides: map[string]bool{},
		sum: &Summary{NotesBy: map[model.PlacementSource]int{}, MediaBy: map[model.PlacementSource]int{}}}
	b.log = opts.Log
	if b.log == nil {
		b.log = log.New(io.Discard, "", 0)
	}
	if b.opts.Now == nil {
		b.opts.Now = time.Now
	}
	if opts.ConfigPath != "" {
		c, err := LoadConfig(opts.ConfigPath)
		if err != nil {
			return nil, err
		}
		b.cfg = c
	}
	o := &b.opts
	o.GPXDir = firstNonEmpty(o.GPXDir, b.cfg.GPX)
	o.NotesDir = firstNonEmpty(o.NotesDir, b.cfg.Notes)
	o.MediaDir = firstNonEmpty(o.MediaDir, b.cfg.Media)
	o.OutDir = firstNonEmpty(o.OutDir, b.cfg.Out)
	o.OverridesPath = firstNonEmpty(o.OverridesPath, b.cfg.Overrides)
	o.Title = firstNonEmpty(o.Title, b.cfg.Title, DefaultTitle)
	o.TZ = firstNonEmpty(o.TZ, b.cfg.Timezone)
	if o.MaxGap == 0 {
		o.MaxGap = time.Duration(b.cfg.MaxGap)
	}
	if o.MaxGap <= 0 {
		o.MaxGap = placement.DefaultMaxGap
	}
	o.PhotoSize = firstPositive(o.PhotoSize, b.cfg.PhotoSize, convert.DefaultPhotoSize)
	o.ThumbSize = firstPositive(o.ThumbSize, b.cfg.ThumbSize, convert.DefaultThumbSize)
	o.LivePhotos = o.LivePhotos || b.cfg.LivePhotos
	o.NoVideo = o.NoVideo || b.cfg.NoVideo
	if o.OutDir == "" {
		return nil, errors.New("--out is required")
	}
	if o.GPXDir == "" && o.NotesDir == "" && o.MediaDir == "" {
		return nil, errors.New("at least one of --gpx, --notes or --media is required")
	}
	if o.OverridesPath != "" {
		ov, err := LoadOverrides(o.OverridesPath)
		if err != nil {
			return nil, err
		}
		b.overrides = ov
		b.validateOverrides()
	}
	return b, nil
}

// validateOverrides drops override positions that are incomplete or out of
// range, with a warning.
func (b *builder) validateOverrides() {
	keys := make([]string, 0, len(b.overrides))
	for k := range b.overrides {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		ov := b.overrides[k]
		switch {
		case (ov.Lat == nil) != (ov.Lon == nil):
			b.log.Printf("warning: override %q: lat and lon must be given together; ignoring the position", k)
		case ov.Lat != nil && (math.Abs(*ov.Lat) > 90 || math.Abs(*ov.Lon) > 180):
			b.log.Printf("warning: override %q: position %v,%v is out of range (lat ±90, lon ±180); ignoring it", k, *ov.Lat, *ov.Lon)
		default:
			continue
		}
		ov.Lat, ov.Lon = nil, nil
		b.overrides[k] = ov
	}
}

func (b *builder) verbosef(format string, args ...any) {
	if b.opts.Verbose {
		b.log.Printf(format, args...)
	}
}

func (b *builder) skip(path, reason string) {
	b.sum.Skipped = append(b.sum.Skipped, model.Skipped{Path: path, Reason: reason})
	b.log.Printf("warning: skipped %s: %s", path, reason)
}

func (b *builder) run() (*Summary, error) {
	var gres gpx.Result
	if b.opts.GPXDir != "" {
		var err error
		if gres, err = gpx.LoadDir(b.opts.GPXDir); err != nil {
			return nil, err
		}
		for _, s := range gres.Skipped {
			b.skip(s.Path, s.Reason)
		}
	}
	var nres notes.Result
	if b.opts.NotesDir != "" {
		var err error
		if nres, err = notes.LoadDir(b.opts.NotesDir); err != nil {
			return nil, err
		}
		for _, s := range nres.Skipped {
			b.skip(s.Path, s.Reason)
		}
		for _, w := range nres.Warnings {
			b.log.Printf("warning: %s", w)
		}
	}
	mres, err := b.scanMedia()
	if err != nil {
		return nil, err
	}

	if err := b.resolveZone(nres.Notes, mediaOffsets(mres)); err != nil {
		return nil, err
	}
	b.sum.Timezone = b.zone.Name

	mrecs := b.mediaRecords(mres)
	recs := b.noteRecords(nres.Notes, gres.Waypoints)
	if mrecs, err = b.convertMedia(mrecs); err != nil {
		return nil, err
	}
	tracks, anchors := b.prepareTracks(gres.Tracks)
	for _, r := range recs {
		if r.source == model.SourceManual {
			anchors = append(anchors, placement.Anchor{Time: r.time, Lat: *r.lat, Lon: *r.lon, Kind: placement.KindManual})
		}
	}
	anchors = append(anchors, mediaAnchors(mrecs)...)
	placer := placement.New(anchors, b.opts.MaxGap)
	b.verbosef("anchors: %d, max gap %s", placer.Len(), b.opts.MaxGap)

	items := mergeItems(b.placeNotes(recs, placer), b.placeMedia(mrecs, placer))
	days := b.groupDays(items, tracks)

	trip := model.Trip{
		Title:       b.opts.Title,
		Timezone:    b.zone.Name,
		GeneratedAt: b.opts.Now().UTC().Truncate(time.Second),
		Bounds:      bounds(gres.Tracks, items, nil),
		Days:        days,
		Tracks:      tracks,
		Items:       items,
	}
	trip.FitBounds = b.fitBounds(gres.Tracks, items, days, trip.Bounds)
	if trip.Tracks == nil {
		trip.Tracks = []model.Track{}
	}
	if trip.Items == nil {
		trip.Items = []model.Item{}
	}
	out, err := writeTrip(b.opts.OutDir, trip)
	if err != nil {
		return nil, err
	}
	// --- web assets (milestone 2) ---
	if err := copyWebAssets(b.opts.OutDir); err != nil {
		return nil, err
	}
	// --- end web assets ---
	b.sum.OutPath = out
	b.sum.Days = len(days)
	return b.sum, nil
}

// resolveZone picks the trip zone: CLI/config, else the most common offset
// among notes and media metadata.
func (b *builder) resolveZone(ns []notes.Note, mediaOffsets []int) error {
	if b.opts.TZ != "" {
		z, err := timeutil.ParseZone(b.opts.TZ)
		if err != nil {
			return fmt.Errorf("--tz: %w", err)
		}
		b.zone = z
		return nil
	}
	var offsets []int
	for _, n := range ns {
		if !n.Naive {
			offsets = append(offsets, timeutil.Offset(n.Time))
		}
	}
	offsets = append(offsets, mediaOffsets...)
	b.zone = timeutil.InferZone(offsets)
	b.verbosef("timezone inferred from %d note and media offsets: %s", len(offsets), b.zone.Name)
	return nil
}

// noteRecords applies overrides, normalises times to UTC and turns
// waypoints into notes.
func (b *builder) noteRecords(ns []notes.Note, wpts []gpx.Waypoint) []noteRec {
	byInstant := map[int64]string{}
	for key := range b.overrides {
		if t, naive, err := notes.ParseTimestamp(key); err == nil && !naive {
			byInstant[t.UnixNano()] = key
		}
	}
	used := b.usedOverrides
	var recs []noteRec
	for i, n := range ns {
		r := noteRec{title: n.Title, text: n.Text, lat: n.Lat, lon: n.Lon, order: i,
			idKey: relPath(b.opts.NotesDir, n.File) + "\n" + n.Key + "\n" + n.Text}
		t := n.Time
		if n.Naive {
			t = reinterpret(t, b.zone.Loc)
			r.timeAssumed = true
		}
		key, ok := n.Key, false
		if _, ok = b.overrides[key]; !ok && !n.Naive {
			key, ok = byInstant[t.UnixNano()]
		}
		if ok {
			used[key] = true
			ov := b.overrides[key]
			where := fmt.Sprintf("note %s (%s)", n.Key, n.File)
			if ov.Skip {
				b.skip(where, "skipped by override")
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
					t = ot
				}
			}
			if ov.Title != nil {
				r.title = *ov.Title
			}
			if ov.Lat != nil && ov.Lon != nil {
				r.lat, r.lon = ov.Lat, ov.Lon
			}
		}
		r.time = t.UTC()
		if r.lat != nil {
			r.source = model.SourceManual
		}
		recs = append(recs, r)
	}
	for key := range b.overrides {
		if used[key] {
			continue
		}
		if _, _, err := notes.ParseTimestamp(key); err == nil {
			b.log.Printf("warning: override %q matches no note", key)
		} else {
			b.log.Printf("warning: override %q matches no media file", key)
		}
	}
	for i, w := range wpts {
		where := fmt.Sprintf("waypoint %q (%s)", w.Name, w.File)
		if w.Time.IsZero() {
			b.skip(where, "waypoint has no time")
			continue
		}
		lat, lon := w.Lat, w.Lon
		recs = append(recs, noteRec{
			time: w.Time, title: w.Name, text: w.Description,
			lat: &lat, lon: &lon, source: model.SourceGPS, order: len(ns) + i,
			idKey: relPath(b.opts.GPXDir, w.File) + "\n" + w.Time.Format(time.RFC3339Nano) + "\n" + w.Name + "\n" + w.Description,
		})
	}
	return recs
}

// reinterpret treats the wall clock of t (parsed as UTC) as local time in loc.
func reinterpret(t time.Time, loc *time.Location) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), loc)
}

type timedTrack struct {
	src   gpx.Track
	start time.Time
	end   time.Time
	timed bool
}

// prepareTracks sorts tracks by start, assigns ids, computes stats and
// display points, and returns trackpoint anchors.
func (b *builder) prepareTracks(in []gpx.Track) ([]model.Track, []placement.Anchor) {
	tts := make([]timedTrack, len(in))
	for i, t := range in {
		s, e, ok := t.TimeSpan()
		tts[i] = timedTrack{src: t, start: s, end: e, timed: ok}
	}
	sort.SliceStable(tts, func(i, j int) bool {
		if tts[i].timed != tts[j].timed {
			return tts[i].timed
		}
		return tts[i].start.Before(tts[j].start)
	})
	var out []model.Track
	var anchors []placement.Anchor
	segID := 0
	for i, tt := range tts {
		mt := model.Track{
			ID:    "t" + strconv.Itoa(i+1),
			Name:  tt.src.Name,
			Type:  tt.src.Type,
			Stats: gpx.Stats(tt.src),
		}
		if tt.timed {
			s, e := tt.start, tt.end
			mt.Start, mt.End = &s, &e
		}
		mt.Points, mt.SegmentStarts = gpx.Display(tt.src, tt.start, gpx.DisplayTolerance)
		full := 0
		for _, seg := range tt.src.Segments {
			segID++
			full += len(seg)
			for _, p := range seg {
				if p.HasTime() {
					anchors = append(anchors, placement.Anchor{Time: p.Time, Lat: p.Lat, Lon: p.Lon,
						Kind: placement.KindTrackpoint, Segment: segID})
				}
			}
		}
		b.sum.TrackPoints += full
		b.verbosef("track %s %q (%s): %d points, %d displayed, %.1f km", mt.ID, mt.Name, filepath.Base(tt.src.File),
			full, len(mt.Points), mt.Stats.DistanceKm)
		out = append(out, mt)
	}
	b.sum.Tracks = len(out)
	return out, anchors
}

// placeNotes positions note records, sorts them chronologically and assigns
// ids: "n" + the first 10 hex chars of sha1(idKey), so ids stay stable
// when notes are added.
func (b *builder) placeNotes(recs []noteRec, placer *placement.Placer) []model.Item {
	sort.SliceStable(recs, func(i, j int) bool {
		if !recs[i].time.Equal(recs[j].time) {
			return recs[i].time.Before(recs[j].time)
		}
		return recs[i].order < recs[j].order
	})
	items := make([]model.Item, 0, len(recs))
	seen := map[string]bool{}
	for _, r := range recs {
		sum := sha1.Sum([]byte(r.idKey))
		it := model.Item{
			ID: uniqueID(seen, "n"+hex.EncodeToString(sum[:])[:10]), Kind: model.KindNote, Time: r.time, TimeAssumed: r.timeAssumed,
			Title: r.title, Text: r.text,
		}
		if r.lat != nil {
			it.Lat, it.Lon = r.lat, r.lon
			it.Placement = model.Placement{Source: r.source}
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
		b.sum.NotesBy[it.Placement.Source]++
		b.verbosef("%s %s %-12s %s", it.ID, b.zone.DateKey(it.Time)+" "+it.Time.In(b.zone.Loc).Format("15:04"),
			it.Placement.Source, oneLine(firstNonEmpty(it.Title, it.Text), 50))
		items = append(items, it)
	}
	b.sum.Notes = len(items)
	return items
}

// uniqueID returns base, or base-2, base-3, ... if base is already in seen,
// and records the result. Callers assign ids in a deterministic order.
func uniqueID(seen map[string]bool, base string) string {
	id := base
	for n := 2; seen[id]; n++ {
		id = base + "-" + strconv.Itoa(n)
	}
	seen[id] = true
	return id
}

// relPath returns file relative to dir, slash-separated; the base name if
// that fails.
func relPath(dir, file string) string {
	if r, err := filepath.Rel(dir, file); err == nil {
		return filepath.ToSlash(r)
	}
	return filepath.Base(file)
}

// maxSpanDays is the trip length beyond which the build warns about outliers
// (a stray old note, a camera with an unset clock).
const maxSpanDays = 60

// groupDays buckets items and tracks into calendar days in the trip zone.
// Only dates with content get a day; indexes are sequential.
func (b *builder) groupDays(items []model.Item, tracks []model.Track) []model.Day {
	type content struct {
		t    time.Time
		what string
	}
	var all []content
	for _, it := range items {
		what := "media " + it.Original
		if it.Kind == model.KindNote {
			what = fmt.Sprintf("note %q", oneLine(firstNonEmpty(it.Title, it.Text), 40))
		}
		all = append(all, content{it.Time, what})
	}
	for _, t := range tracks {
		if t.Start != nil {
			all = append(all, content{*t.Start, fmt.Sprintf("track %q", t.Name)})
		}
	}
	if len(all) == 0 {
		return []model.Day{}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].t.Before(all[j].t) })
	first, last := all[0], all[len(all)-1]
	if span := last.t.Sub(first.t); span > maxSpanDays*24*time.Hour {
		median := all[len(all)/2].t
		days := func(d time.Duration) int { return int(math.Round(d.Hours() / 24)) }
		b.log.Printf("warning: the trip spans %d days (%s to %s); check the outliers: earliest is %s on %s (%d days before the median date %s), latest is %s on %s (%d days after it). Fix a wrong time with an override or remove the file.",
			days(span), b.zone.DateKey(first.t), b.zone.DateKey(last.t),
			first.what, b.zone.DateKey(first.t), days(median.Sub(first.t)), b.zone.DateKey(median),
			last.what, b.zone.DateKey(last.t), days(last.t.Sub(median)))
	}
	var days []model.Day
	index := map[string]int{}
	for _, c := range all {
		k := b.zone.DateKey(c.t)
		if _, ok := index[k]; ok {
			continue
		}
		index[k] = len(days)
		days = append(days, model.Day{Date: k, Index: len(days) + 1, ItemIDs: []string{}, Stats: model.DayStats{TrackIDs: []string{}}})
	}
	for _, it := range items {
		d := &days[index[b.zone.DateKey(it.Time)]]
		d.ItemIDs = append(d.ItemIDs, it.ID)
	}
	names := make([][]string, len(days))
	for _, t := range tracks {
		if t.Start == nil {
			continue
		}
		i := index[b.zone.DateKey(*t.Start)]
		st := &days[i].Stats
		st.DistanceKm += t.Stats.DistanceKm
		st.ElevationGainM += t.Stats.ElevationGainM
		st.MovingTimeS += t.Stats.MovingTimeS
		st.TrackIDs = append(st.TrackIDs, t.ID)
		names[i] = append(names[i], t.Name)
	}
	for i := range days {
		days[i].Stats.DistanceKm = model.Round(days[i].Stats.DistanceKm, 2)
		dc := b.cfg.Days[days[i].Date]
		if dc.Title != "" {
			days[i].Title = dc.Title
		} else {
			days[i].Title = strings.Join(names[i], " · ")
		}
		days[i].ExcludeFromFit = dc.ExcludeFromFit
	}
	dates := make([]string, 0, len(b.cfg.Days))
	for date := range b.cfg.Days {
		if _, ok := index[date]; !ok {
			dates = append(dates, date)
		}
	}
	sort.Strings(dates)
	for _, date := range dates {
		b.log.Printf("warning: config day %q matches no day of the trip", date)
	}
	return days
}

// fitBounds is the initial map view: the bounds of the tracks and placed
// items of the days not marked excludeFromFit. It is all when every day is
// excluded or there are no days.
func (b *builder) fitBounds(tracks []gpx.Track, items []model.Item, days []model.Day, all *model.Bounds) *model.Bounds {
	excluded := map[string]bool{}
	for _, d := range days {
		if d.ExcludeFromFit {
			excluded[d.Date] = true
		}
	}
	if len(excluded) == 0 || len(excluded) == len(days) {
		return all
	}
	skip := func(t time.Time) bool { return excluded[b.zone.DateKey(t)] }
	if fb := bounds(tracks, items, skip); fb != nil {
		return fb
	}
	return all
}

// bounds is the box around all track points and placed items, leaving out
// tracks that start and items that fall on a time skip reports (nil: none).
func bounds(tracks []gpx.Track, items []model.Item, skip func(time.Time) bool) *model.Bounds {
	var bb *model.Bounds
	add := func(lat, lon float64) {
		if bb == nil {
			bb = model.NewBounds(lat, lon)
		} else {
			bb.Extend(lat, lon)
		}
	}
	for _, t := range tracks {
		if start, _, ok := t.TimeSpan(); ok && skip != nil && skip(start) {
			continue
		}
		for _, seg := range t.Segments {
			for _, p := range seg {
				add(p.Lat, p.Lon)
			}
		}
	}
	for _, it := range items {
		if it.Lat != nil && (skip == nil || !skip(it.Time)) {
			add(*it.Lat, *it.Lon)
		}
	}
	return bb
}

func writeTrip(dir string, trip model.Trip) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create output dir: %w", err)
	}
	data, err := json.Marshal(trip)
	if err != nil {
		return "", fmt.Errorf("encode trip.json: %w", err)
	}
	path := filepath.Join(dir, "trip.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("write trip.json: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", fmt.Errorf("write trip.json: %w", err)
	}
	return path, nil
}

func firstPositive(v ...int) int {
	for _, x := range v {
		if x > 0 {
			return x
		}
	}
	return 0
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}
