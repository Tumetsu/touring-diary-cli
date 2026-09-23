// Package placement positions timed items on the map from time-sorted
// position anchors (spec sections 4.2 and 4.3).
package placement

import (
	"sort"
	"time"

	"github.com/tuomassalmi/touring-diary/internal/gpx"
	"github.com/tuomassalmi/touring-diary/internal/model"
)

// DefaultMaxGap is the default maximum time distance to a usable anchor.
const DefaultMaxGap = 12 * time.Hour

// NearbyM is the distance under which two anchors from different sources
// are interpolated between rather than snapped to.
const NearbyM = 2000.0

// MaxAnchorAccuracyM is the worst reported horizontal accuracy with which a
// media GPS position still becomes an anchor (spec 4.2).
const MaxAnchorAccuracyM = 500.0

// SanityDistanceM is how far an item's own GPS position may be from the
// interpolated track position before a warning is logged (spec 4.3).
const SanityDistanceM = 1000.0

// UsableAccuracy reports whether a media position with reported horizontal
// accuracy acc (nil when unknown) may be used as an anchor.
func UsableAccuracy(acc *float64) bool {
	return acc == nil || *acc <= MaxAnchorAccuracyM
}

// AnchorKind says where an anchor came from.
type AnchorKind int

// Anchor kinds.
const (
	KindTrackpoint AnchorKind = iota + 1
	KindMedia                 // media item with its own GPS position
	KindManual                // manual lat/lon from notes or overrides
)

// Anchor is a known (time, position) pair.
type Anchor struct {
	Time     time.Time
	Lat, Lon float64
	Kind     AnchorKind
	// Segment identifies the track segment of a trackpoint anchor. Zero means
	// the anchor belongs to no segment. Use distinct positive values per
	// segment across all tracks.
	Segment int
}

// Result is the placement of one item.
type Result struct {
	Source   model.PlacementSource
	Lat, Lon float64
	// Positioned is false for SourceNone.
	Positioned bool
	// Gap is the time distance to the anchor(s) used; for SourceNone it is the
	// distance to the nearest anchor. HasGap is false when there are no anchors.
	Gap    time.Duration
	HasGap bool
}

// Placer answers placement queries against a fixed set of anchors.
type Placer struct {
	anchors []Anchor
	// track holds the trackpoint anchors (Segment != 0) only, for TrackPosition.
	track  []Anchor
	maxGap time.Duration
}

// New returns a Placer over a copy of anchors sorted by time.
func New(anchors []Anchor, maxGap time.Duration) *Placer {
	a := make([]Anchor, len(anchors))
	copy(a, anchors)
	sort.SliceStable(a, func(i, j int) bool { return a[i].Time.Before(a[j].Time) })
	var track []Anchor
	for _, x := range a {
		if x.Segment != 0 {
			track = append(track, x)
		}
	}
	return &Placer{anchors: a, track: track, maxGap: maxGap}
}

// TrackPosition returns the position interpolated along a track segment at
// t. ok is false unless t lies within the time span of one segment.
func (p *Placer) TrackPosition(t time.Time) (lat, lon float64, ok bool) {
	n := len(p.track)
	ai := sort.Search(n, func(i int) bool { return !p.track[i].Time.Before(t) })
	bi := sort.Search(n, func(i int) bool { return p.track[i].Time.After(t) }) - 1
	if bi < 0 || ai >= n {
		return 0, 0, false
	}
	a, b := p.track[bi], p.track[ai]
	if a.Segment != b.Segment {
		return 0, 0, false
	}
	lat, lon = interpolate(a, b, t)
	return lat, lon, true
}

// CheckOwnPosition compares an item's own position with the track
// position at t. It returns the distance and whether it exceeds
// SanityDistanceM; inTrack is false when t is outside every track segment.
func (p *Placer) CheckOwnPosition(t time.Time, lat, lon float64) (distM float64, suspicious, inTrack bool) {
	tl, to, ok := p.TrackPosition(t)
	if !ok {
		return 0, false, false
	}
	d := gpx.Haversine(lat, lon, tl, to)
	return d, d > SanityDistanceM, true
}

// Len returns the number of anchors.
func (p *Placer) Len() int { return len(p.anchors) }

// Place positions an item at time t that has no position of its own.
func (p *Placer) Place(t time.Time) Result {
	n := len(p.anchors)
	if n == 0 {
		return Result{Source: model.SourceNone}
	}
	// after: first anchor at or after t; before: last anchor at or before t.
	ai := sort.Search(n, func(i int) bool { return !p.anchors[i].Time.Before(t) })
	bi := sort.Search(n, func(i int) bool { return p.anchors[i].Time.After(t) }) - 1
	var a, b *Anchor
	if bi >= 0 {
		a = &p.anchors[bi]
	}
	if ai < n {
		b = &p.anchors[ai]
	}

	if a != nil && b != nil {
		ga, gb := t.Sub(a.Time), b.Time.Sub(t)
		sameSeg := a.Segment != 0 && a.Segment == b.Segment
		near := ga <= p.maxGap && gb <= p.maxGap &&
			gpx.Haversine(a.Lat, a.Lon, b.Lat, b.Lon) < NearbyM
		if sameSeg || near || a == b {
			lat, lon := interpolate(*a, *b, t)
			return Result{Source: model.SourceInterpolated, Lat: lat, Lon: lon, Positioned: true,
				Gap: min(ga, gb), HasGap: true}
		}
	}

	nearest, gap := nearer(a, b, t)
	if gap <= p.maxGap {
		return Result{Source: model.SourceSnapped, Lat: nearest.Lat, Lon: nearest.Lon, Positioned: true,
			Gap: gap, HasGap: true}
	}
	return Result{Source: model.SourceNone, Gap: gap, HasGap: true}
}

// nearer returns whichever of a and b (at least one non-nil) is closer in
// time to t, preferring a on ties.
func nearer(a, b *Anchor, t time.Time) (*Anchor, time.Duration) {
	switch {
	case a == nil:
		return b, b.Time.Sub(t)
	case b == nil:
		return a, t.Sub(a.Time)
	}
	ga, gb := t.Sub(a.Time), b.Time.Sub(t)
	if gb < ga {
		return b, gb
	}
	return a, ga
}

func interpolate(a, b Anchor, t time.Time) (lat, lon float64) {
	span := b.Time.Sub(a.Time)
	if span <= 0 {
		return a.Lat, a.Lon
	}
	f := float64(t.Sub(a.Time)) / float64(span)
	return a.Lat + (b.Lat-a.Lat)*f, a.Lon + (b.Lon-a.Lon)*f
}
