package gpx

import (
	"encoding/json"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const testdata = "../../testdata/gpx"

func loadTiny(t *testing.T) Result {
	t.Helper()
	res, err := LoadDir(testdata)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestLoadDirParsesTracksAndSkipsBroken(t *testing.T) {
	res := loadTiny(t)
	if len(res.Tracks) != 2 {
		t.Fatalf("tracks = %d, want 2", len(res.Tracks))
	}
	if len(res.Skipped) != 1 || filepath.Base(res.Skipped[0].Path) != "broken.gpx" {
		t.Errorf("skipped = %+v", res.Skipped)
	}
	ride, walk := res.Tracks[0], res.Tracks[1]
	if ride.Name != "Ride" || ride.Type != "cycling" || len(ride.Segments) != 1 {
		t.Errorf("ride = %q %q %d segs", ride.Name, ride.Type, len(ride.Segments))
	}
	if walk.Type != "" || len(walk.Segments) != 2 {
		t.Errorf("walk type %q, %d segs", walk.Type, len(walk.Segments))
	}
	if p := walk.Segments[1][0]; p.HasTime() || p.Ele != nil {
		t.Errorf("untimed point parsed as %+v", p)
	}
	if len(res.Waypoints) != 2 || res.Waypoints[0].Name != "Viewpoint" || res.Waypoints[0].Description != "Nice view" {
		t.Errorf("waypoints = %+v", res.Waypoints)
	}
	if !res.Waypoints[1].Time.IsZero() {
		t.Errorf("untimed waypoint has time %s", res.Waypoints[1].Time)
	}
}

func TestStats(t *testing.T) {
	ride := loadTiny(t).Tracks[0]
	st := Stats(ride)
	wantKm := Haversine(60, 25, 60.0018, 25) / 1000
	if math.Abs(st.DistanceKm-wantKm) > 0.01 {
		t.Errorf("distance = %v, want %v", st.DistanceKm, wantKm)
	}
	if st.ElapsedTimeS != 180 {
		t.Errorf("elapsed = %d", st.ElapsedTimeS)
	}
	if st.MovingTimeS != 120 {
		t.Errorf("moving = %d, want 120 (last minute stationary)", st.MovingTimeS)
	}
	// Smoothed: 10, 15, 21.67, 30 -> gain 20.
	if st.ElevationGainM != 20 {
		t.Errorf("gain = %v, want 20", st.ElevationGainM)
	}
	if *st.MinElevationM != 10 || *st.MaxElevationM != 30 {
		t.Errorf("min/max = %v/%v", *st.MinElevationM, *st.MaxElevationM)
	}
	if st.AvgHeartRate == nil || *st.AvgHeartRate != 110 {
		t.Errorf("hr = %v", st.AvgHeartRate)
	}

	walk := Stats(loadTiny(t).Tracks[1])
	if walk.MinElevationM != nil || walk.AvgHeartRate != nil {
		t.Errorf("walk should have no ele/hr: %+v", walk)
	}
	if walk.ElapsedTimeS != 1800 {
		t.Errorf("walk elapsed = %d", walk.ElapsedTimeS)
	}
}

func TestHaversine(t *testing.T) {
	// One degree of latitude is about 111.2 km.
	if d := Haversine(60, 25, 61, 25); math.Abs(d-111195) > 10 {
		t.Errorf("1 deg = %v m", d)
	}
}

func TestSimplifyStraightLine(t *testing.T) {
	var seg Segment
	for i := 0; i <= 100; i++ {
		seg = append(seg, Point{Lat: 60 + float64(i)*0.0001, Lon: 25})
	}
	if idx := Simplify(seg, DisplayTolerance); len(idx) != 2 || idx[0] != 0 || idx[1] != 100 {
		t.Errorf("straight line kept %v", idx)
	}
	// A 50 m sideways step at the midpoint must survive, and the straight
	// runs on either side must collapse to a few points.
	for i := 50; i <= 100; i++ {
		seg[i].Lon = 25 + 50/(111195*math.Cos(60*math.Pi/180))
	}
	idx := Simplify(seg, DisplayTolerance)
	if len(idx) > 5 || !slices.Contains(idx, 49) || !slices.Contains(idx, 50) {
		t.Errorf("step kept %v", idx)
	}
}

func TestDisplayPointsEncoding(t *testing.T) {
	ride := loadTiny(t).Tracks[0]
	start, _, _ := ride.TimeSpan()
	pts, segStarts := Display(ride, start, DisplayTolerance)
	if len(segStarts) != 0 {
		t.Errorf("segStarts = %v", segStarts)
	}
	b, err := json.Marshal(pts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "[[60,25,10,0],") || !strings.HasSuffix(string(b), "[60.0018,25,30,180]]") {
		t.Errorf("encoded = %s", b)
	}

	walk := loadTiny(t).Tracks[1]
	ws, _, _ := walk.TimeSpan()
	pts, segStarts = Display(walk, ws, DisplayTolerance)
	if len(segStarts) != 1 || segStarts[0] != 2 {
		t.Errorf("walk segStarts = %v", segStarts)
	}
	b, _ = json.Marshal(pts[2])
	if string(b) != "[60.102,25.1,null,null]" {
		t.Errorf("untimed point = %s", b)
	}
	if ws != time.Date(2026, 6, 27, 20, 0, 0, 0, time.UTC) {
		t.Errorf("walk start = %s", ws)
	}
}

func TestParseDropsNonFiniteValues(t *testing.T) {
	const doc = `<?xml version="1.0"?>
<gpx version="1.1" creator="test" xmlns="http://www.topografix.com/GPX/1/1"
     xmlns:gpxtpx="http://www.garmin.com/xmlschemas/TrackPointExtension/v1">
 <wpt lat="NaN" lon="25"><name>Bad</name><time>2026-06-27T10:00:00Z</time></wpt>
 <trk><name>T</name><trkseg>
  <trkpt lat="60" lon="25"><ele>NaN</ele><time>2026-06-27T10:00:00Z</time>
   <extensions><gpxtpx:TrackPointExtension><gpxtpx:hr>Inf</gpxtpx:hr></gpxtpx:TrackPointExtension></extensions></trkpt>
  <trkpt lat="NaN" lon="25"><ele>12</ele><time>2026-06-27T10:01:00Z</time></trkpt>
  <trkpt lat="60.001" lon="25"><ele>-Inf</ele><time>2026-06-27T10:02:00Z</time></trkpt>
 </trkseg></trk>
</gpx>`
	tracks, wpts, err := Parse(strings.NewReader(doc), "nan.gpx")
	if err != nil {
		t.Fatal(err)
	}
	if len(wpts) != 0 {
		t.Errorf("waypoint with NaN lat kept: %+v", wpts)
	}
	if len(tracks) != 1 || len(tracks[0].Segments[0]) != 2 {
		t.Fatalf("tracks = %+v", tracks)
	}
	for _, p := range tracks[0].Segments[0] {
		if p.Ele != nil || p.HR != nil {
			t.Errorf("non-finite ele/hr kept: %+v", p)
		}
	}
	start, _, _ := tracks[0].TimeSpan()
	pts, _ := Display(tracks[0], start, DisplayTolerance)
	if _, err := json.Marshal(pts); err != nil {
		t.Error(err)
	}
	if _, err := json.Marshal(Stats(tracks[0])); err != nil {
		t.Error(err)
	}
}
