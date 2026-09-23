// Package gpx reads GPX files into tracks and waypoints and computes track
// statistics and display simplification (spec sections 2.1 and 4.4).
package gpx

import (
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	gpxgo "github.com/tkrajina/gpxgo/gpx"

	"github.com/Tumetsu/touring-diary-cli/internal/model"
)

// Point is a full-resolution trackpoint. Time is zero when the GPX point has
// no <time>; such points are drawn but not used for placement.
type Point struct {
	Lat, Lon float64
	Ele      *float64
	Time     time.Time
	HR       *float64
}

// HasTime reports whether the point carries a timestamp.
func (p Point) HasTime() bool { return !p.Time.IsZero() }

// Segment is one <trkseg>.
type Segment []Point

// Track is one <trk> with its segments at full resolution.
type Track struct {
	Name     string
	Type     string
	File     string
	Segments []Segment
}

// TimeSpan returns the first and last point times of the track. ok is false
// when no point has a time.
func (t Track) TimeSpan() (start, end time.Time, ok bool) {
	for _, seg := range t.Segments {
		for _, p := range seg {
			if !p.HasTime() {
				continue
			}
			if !ok || p.Time.Before(start) {
				start = p.Time
			}
			if !ok || p.Time.After(end) {
				end = p.Time
			}
			ok = true
		}
	}
	return start, end, ok
}

// Waypoint is a <wpt> element; it becomes a note.
type Waypoint struct {
	Name, Description string
	Lat, Lon          float64
	Time              time.Time
	File              string
}

// Result is the outcome of reading a GPX folder.
type Result struct {
	Tracks    []Track
	Waypoints []Waypoint
	Skipped   []model.Skipped
}

// LoadDir parses every *.gpx file (case-insensitive) in dir. Files that fail
// to parse are reported in Result.Skipped; only an unreadable dir is an error.
func LoadDir(dir string) (Result, error) {
	var res Result
	entries, err := os.ReadDir(dir)
	if err != nil {
		return res, fmt.Errorf("read gpx dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".gpx") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(dir, name)
		tracks, wpts, err := parseFile(path)
		if err != nil {
			res.Skipped = append(res.Skipped, model.Skipped{Path: path, Reason: err.Error()})
			continue
		}
		if len(tracks) == 0 && len(wpts) == 0 {
			res.Skipped = append(res.Skipped, model.Skipped{Path: path, Reason: "no tracks or waypoints"})
			continue
		}
		res.Tracks = append(res.Tracks, tracks...)
		res.Waypoints = append(res.Waypoints, wpts...)
	}
	return res, nil
}

func parseFile(path string) ([]Track, []Waypoint, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open: %w", err)
	}
	defer f.Close()
	return Parse(f, path)
}

// Parse reads one GPX document. file is recorded on the returned tracks and
// waypoints. Tracks without any points are dropped.
func Parse(r io.Reader, file string) ([]Track, []Waypoint, error) {
	doc, err := gpxgo.Parse(r)
	if err != nil {
		return nil, nil, fmt.Errorf("parse gpx: %w", err)
	}
	var tracks []Track
	for i, trk := range doc.Tracks {
		t := Track{Name: strings.TrimSpace(trk.Name), Type: strings.TrimSpace(trk.Type), File: file}
		if t.Name == "" {
			base := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
			t.Name = strings.ReplaceAll(base, "_", " ")
			if len(doc.Tracks) > 1 {
				t.Name += " " + strconv.Itoa(i+1)
			}
		}
		for _, seg := range trk.Segments {
			if len(seg.Points) == 0 {
				continue
			}
			s := make(Segment, 0, len(seg.Points))
			for _, p := range seg.Points {
				if pt, ok := convertPoint(p); ok {
					s = append(s, pt)
				}
			}
			if len(s) == 0 {
				continue
			}
			t.Segments = append(t.Segments, s)
		}
		if len(t.Segments) > 0 {
			tracks = append(tracks, t)
		}
	}
	var wpts []Waypoint
	for _, w := range doc.Waypoints {
		if !finite(w.Latitude) || !finite(w.Longitude) {
			continue
		}
		wpts = append(wpts, Waypoint{
			Name:        strings.TrimSpace(w.Name),
			Description: strings.TrimSpace(w.Description),
			Lat:         w.Latitude,
			Lon:         w.Longitude,
			Time:        w.Timestamp.UTC(),
			File:        file,
		})
	}
	return tracks, wpts, nil
}

// convertPoint converts one trackpoint. ok is false when lat/lon are not
// finite; non-finite elevation or heart rate is treated as missing (NaN
// would make trip.json unencodable).
func convertPoint(p gpxgo.GPXPoint) (pt Point, ok bool) {
	if !finite(p.Latitude) || !finite(p.Longitude) {
		return Point{}, false
	}
	out := Point{Lat: p.Latitude, Lon: p.Longitude}
	if p.Elevation.NotNull() {
		if v := p.Elevation.Value(); finite(v) {
			out.Ele = &v
		}
	}
	if !p.Timestamp.IsZero() {
		out.Time = p.Timestamp.UTC()
	}
	out.HR = findHR(p.Extensions.Nodes)
	return out, true
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// findHR searches extension nodes for a heart-rate element (<gpxtpx:hr> or
// any element with local name "hr").
func findHR(nodes []gpxgo.ExtensionNode) *float64 {
	for _, n := range nodes {
		if strings.EqualFold(n.LocalName(), "hr") {
			if v, err := strconv.ParseFloat(strings.TrimSpace(n.Data), 64); err == nil && finite(v) {
				return &v
			}
		}
		if hr := findHR(n.Nodes); hr != nil {
			return hr
		}
	}
	return nil
}
