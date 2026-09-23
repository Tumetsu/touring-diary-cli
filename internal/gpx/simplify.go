package gpx

import (
	"math"
	"time"

	"github.com/tuomassalmi/touring-diary/internal/model"
)

// DisplayTolerance is the Ramer–Douglas–Peucker tolerance for display tracks.
const DisplayTolerance = 5.0

// Simplify returns the indices of seg kept by Ramer–Douglas–Peucker with the
// given tolerance in metres. The first and last points are always kept.
// Distances use a local equirectangular projection, which is accurate to well
// under a metre at track scale.
func Simplify(seg Segment, toleranceM float64) []int {
	n := len(seg)
	if n <= 2 {
		idx := make([]int, n)
		for i := range idx {
			idx[i] = i
		}
		return idx
	}
	lat0 := seg[0].Lat * math.Pi / 180
	kx := EarthRadiusM * math.Cos(lat0) * math.Pi / 180
	ky := EarthRadiusM * math.Pi / 180
	xs := make([]float64, n)
	ys := make([]float64, n)
	for i, p := range seg {
		xs[i] = p.Lon * kx
		ys[i] = p.Lat * ky
	}
	keep := make([]bool, n)
	keep[0], keep[n-1] = true, true
	type span struct{ a, b int }
	stack := []span{{0, n - 1}}
	for len(stack) > 0 {
		s := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		maxD, maxI := -1.0, -1
		for i := s.a + 1; i < s.b; i++ {
			if d := segDist(xs[i], ys[i], xs[s.a], ys[s.a], xs[s.b], ys[s.b]); d > maxD {
				maxD, maxI = d, i
			}
		}
		if maxI >= 0 && maxD > toleranceM {
			keep[maxI] = true
			stack = append(stack, span{s.a, maxI}, span{maxI, s.b})
		}
	}
	var idx []int
	for i, k := range keep {
		if k {
			idx = append(idx, i)
		}
	}
	return idx
}

// segDist is the distance from (px, py) to the segment (ax, ay)-(bx, by).
func segDist(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	l2 := dx*dx + dy*dy
	if l2 == 0 {
		return math.Hypot(px-ax, py-ay)
	}
	t := ((px-ax)*dx + (py-ay)*dy) / l2
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(px-(ax+t*dx), py-(ay+t*dy))
}

// Display simplifies every segment of t and returns the concatenated display
// points plus the start index of each segment after the first. Seconds are
// measured from start (the track's first timed point).
func Display(t Track, start time.Time, toleranceM float64) (points []model.Point, segmentStarts []int) {
	for si, seg := range t.Segments {
		if si > 0 {
			segmentStarts = append(segmentStarts, len(points))
		}
		for _, i := range Simplify(seg, toleranceM) {
			p := seg[i]
			mp := model.Point{Lat: p.Lat, Lon: p.Lon, Ele: p.Ele}
			if p.HasTime() && !start.IsZero() {
				s := int64(p.Time.Sub(start).Seconds())
				mp.Seconds = &s
			}
			points = append(points, mp)
		}
	}
	return points, segmentStarts
}
