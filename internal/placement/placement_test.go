package placement

import (
	"math"
	"testing"
	"time"

	"github.com/Tumetsu/touring-diary-cli/internal/model"
)

var t0 = time.Date(2026, 6, 27, 6, 0, 0, 0, time.UTC)

func at(d time.Duration) time.Time { return t0.Add(d) }

// track returns trackpoint anchors moving north along lon 25 from lat0,
// one per minute, over n minutes.
func track(seg int, start time.Time, lat0 float64, n int) []Anchor {
	var out []Anchor
	for i := 0; i <= n; i++ {
		out = append(out, Anchor{Time: start.Add(time.Duration(i) * time.Minute),
			Lat: lat0 + float64(i)*0.001, Lon: 25, Kind: KindTrackpoint, Segment: seg})
	}
	return out
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestPlaceInsideTrackInterpolates(t *testing.T) {
	p := New(track(1, t0, 60, 10), DefaultMaxGap)
	r := p.Place(at(2*time.Minute + 30*time.Second))
	if r.Source != model.SourceInterpolated || !r.Positioned {
		t.Fatalf("source = %s, want interpolated", r.Source)
	}
	if !near(r.Lat, 60.0025) || !near(r.Lon, 25) {
		t.Errorf("pos = %v,%v want 60.0025,25", r.Lat, r.Lon)
	}
	if r.Gap != 30*time.Second {
		t.Errorf("gap = %s, want 30s", r.Gap)
	}
}

func TestPlaceSameSegmentIgnoresMaxGapAndDistance(t *testing.T) {
	// A long pause inside one segment, with a jump of ~11 km.
	anchors := []Anchor{
		{Time: t0, Lat: 60, Lon: 25, Kind: KindTrackpoint, Segment: 7},
		{Time: at(20 * time.Hour), Lat: 60.1, Lon: 25, Kind: KindTrackpoint, Segment: 7},
	}
	r := New(anchors, DefaultMaxGap).Place(at(15 * time.Hour))
	if r.Source != model.SourceInterpolated || !near(r.Lat, 60.075) {
		t.Fatalf("got %s at %v, want interpolated at 60.075", r.Source, r.Lat)
	}
	if r.Gap != 5*time.Hour {
		t.Errorf("gap = %s, want 5h", r.Gap)
	}
}

func TestPlaceOvernightTwoAnchorsCloseInterpolate(t *testing.T) {
	// Day 1 ends at the camp, day 2 starts ~100 m away 10 hours later.
	day1 := track(1, t0, 60, 10)                                   // ends 60.010 at 06:10
	day2 := track(2, at(10*time.Hour+10*time.Minute), 60.0109, 10) // starts 60.0109 at 16:20
	p := New(append(day1, day2...), DefaultMaxGap)
	r := p.Place(at(5*time.Hour + 10*time.Minute)) // halfway through the night
	if r.Source != model.SourceInterpolated {
		t.Fatalf("source = %s, want interpolated", r.Source)
	}
	if !near(r.Lat, (60.010+60.0109)/2) {
		t.Errorf("lat = %v", r.Lat)
	}
	if r.Gap != 5*time.Hour {
		t.Errorf("gap = %s, want 5h", r.Gap)
	}
}

func TestPlaceFarApartSnapsToNearer(t *testing.T) {
	day1 := track(1, t0, 60, 10)                              // ends 60.010 at 06:10
	day2 := track(2, at(10*time.Hour+10*time.Minute), 61, 10) // ~110 km away
	p := New(append(day1, day2...), DefaultMaxGap)

	r := p.Place(at(2 * time.Hour)) // 1h50m after day1 end, 8h20m before day2
	if r.Source != model.SourceSnapped || !near(r.Lat, 60.010) {
		t.Fatalf("got %s at %v, want snapped to 60.010", r.Source, r.Lat)
	}
	if r.Gap != time.Hour+50*time.Minute {
		t.Errorf("gap = %s", r.Gap)
	}

	r = p.Place(at(9 * time.Hour)) // closer to day2 start
	if r.Source != model.SourceSnapped || !near(r.Lat, 61) {
		t.Fatalf("got %s at %v, want snapped to 61", r.Source, r.Lat)
	}
}

func TestPlaceCloseButBeyondMaxGapSnaps(t *testing.T) {
	// Anchors are close in space but one side is beyond max-gap: snap.
	a := []Anchor{
		{Time: t0, Lat: 60, Lon: 25, Kind: KindTrackpoint, Segment: 1},
		{Time: at(20 * time.Hour), Lat: 60.001, Lon: 25, Kind: KindTrackpoint, Segment: 2},
	}
	r := New(a, DefaultMaxGap).Place(at(3 * time.Hour))
	if r.Source != model.SourceSnapped || !near(r.Lat, 60) || r.Gap != 3*time.Hour {
		t.Fatalf("got %s at %v gap %s", r.Source, r.Lat, r.Gap)
	}
}

func TestPlaceBeyondMaxGapIsNone(t *testing.T) {
	p := New(track(1, t0, 60, 10), DefaultMaxGap)
	for _, tt := range []time.Time{at(-13 * time.Hour), at(10*time.Minute + 12*time.Hour + time.Second)} {
		r := p.Place(tt)
		if r.Source != model.SourceNone || r.Positioned {
			t.Errorf("at %s: got %s, want none", tt, r.Source)
		}
		if !r.HasGap || r.Gap <= DefaultMaxGap {
			t.Errorf("at %s: gap = %s", tt, r.Gap)
		}
	}
	if r := New(nil, DefaultMaxGap).Place(t0); r.Source != model.SourceNone || r.HasGap {
		t.Errorf("no anchors: got %+v", r)
	}
}

func TestPlaceBeforeFirstAnchorSnaps(t *testing.T) {
	p := New(track(1, t0, 60, 10), DefaultMaxGap)
	r := p.Place(at(-time.Hour))
	if r.Source != model.SourceSnapped || !near(r.Lat, 60) || r.Gap != time.Hour {
		t.Fatalf("got %+v", r)
	}
}

func TestPlaceManualAnchorsInterpolateWhenClose(t *testing.T) {
	a := []Anchor{
		{Time: t0, Lat: 60, Lon: 25, Kind: KindManual},
		{Time: at(2 * time.Hour), Lat: 60.01, Lon: 25, Kind: KindManual},
	}
	r := New(a, DefaultMaxGap).Place(at(time.Hour))
	if r.Source != model.SourceInterpolated || !near(r.Lat, 60.005) {
		t.Fatalf("got %s at %v", r.Source, r.Lat)
	}
}

func TestPlaceUnsortedInput(t *testing.T) {
	a := track(1, t0, 60, 10)
	a[0], a[len(a)-1] = a[len(a)-1], a[0]
	r := New(a, DefaultMaxGap).Place(at(30 * time.Second))
	if r.Source != model.SourceInterpolated || !near(r.Lat, 60.0005) {
		t.Fatalf("got %s at %v", r.Source, r.Lat)
	}
}

func TestPlaceNoteBetweenTrackEndAndMediaAnchorInterpolates(t *testing.T) {
	// The ride ends at 06:10; a GPS photo 1 km further north an hour later.
	anchors := track(1, t0, 60, 10) // ends 60.010 at 06:10
	photo := Anchor{Time: at(time.Hour + 10*time.Minute), Lat: 60.019, Lon: 25, Kind: KindMedia}
	p := New(append(anchors, photo), DefaultMaxGap)
	r := p.Place(at(40 * time.Minute)) // halfway between
	if r.Source != model.SourceInterpolated || !near(r.Lat, (60.010+60.019)/2) {
		t.Fatalf("got %s at %v, want interpolated at %v", r.Source, r.Lat, (60.010+60.019)/2)
	}
	if r.Gap != 30*time.Minute {
		t.Errorf("gap = %s, want 30m", r.Gap)
	}
	// Without the photo the note snaps to the track end.
	if r := New(anchors, DefaultMaxGap).Place(at(40 * time.Minute)); r.Source != model.SourceSnapped {
		t.Errorf("without media anchor: %s, want snapped", r.Source)
	}
}

func TestUsableAccuracy(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	for _, tc := range []struct {
		acc  *float64
		want bool
	}{{nil, true}, {f(4.7), true}, {f(500), true}, {f(500.1), false}, {f(5607), false}} {
		if got := UsableAccuracy(tc.acc); got != tc.want {
			t.Errorf("UsableAccuracy(%v) = %v, want %v", tc.acc, got, tc.want)
		}
	}
}

func TestCheckOwnPosition(t *testing.T) {
	anchors := append(track(1, t0, 60, 10), Anchor{Time: at(5 * time.Minute), Lat: 70, Lon: 25, Kind: KindMedia})
	p := New(anchors, DefaultMaxGap)
	// Track at 06:05 is at 60.005; a photo 100 m off is fine, one 5.5 km off is not.
	if d, bad, in := p.CheckOwnPosition(at(5*time.Minute), 60.0059, 25); !in || bad || d > 150 {
		t.Errorf("near: d=%v bad=%v in=%v", d, bad, in)
	}
	if d, bad, in := p.CheckOwnPosition(at(5*time.Minute+30*time.Second), 60.06, 25); !in || !bad || d < 5000 {
		t.Errorf("far: d=%v bad=%v in=%v", d, bad, in)
	}
	// Outside the track's time span: no check.
	if _, _, in := p.CheckOwnPosition(at(time.Hour), 70, 25); in {
		t.Error("expected no track position after the track ended")
	}
}
