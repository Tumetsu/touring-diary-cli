// Package model defines the trip data structures written to trip.json
// (spec section 5.1) and their JSON encoding.
package model

import (
	"encoding/json"
	"math"
	"time"
)

// PlacementSource says where an item's map position came from.
type PlacementSource string

// Placement sources, see spec section 4.3.
const (
	SourceGPS          PlacementSource = "gps"
	SourceManual       PlacementSource = "manual"
	SourceInterpolated PlacementSource = "interpolated"
	SourceSnapped      PlacementSource = "snapped"
	SourceNone         PlacementSource = "none"
)

// Trip is the root object of trip.json.
type Trip struct {
	Title       string    `json:"title"`
	Timezone    string    `json:"timezone"`
	GeneratedAt time.Time `json:"generatedAt"`
	Bounds      *Bounds   `json:"bounds"`
	// FitBounds is the initial map view: Bounds without the days marked
	// excludeFromFit in the config. Equals Bounds when no day remains.
	FitBounds *Bounds `json:"fitBounds"`
	Days      []Day   `json:"days"`
	Tracks    []Track `json:"tracks"`
	Items     []Item  `json:"items"`
}

// Bounds is a lat/lon bounding box, encoded as [[minLat, minLon], [maxLat, maxLon]].
type Bounds struct {
	MinLat, MinLon, MaxLat, MaxLon float64
}

// Extend grows the box to contain (lat, lon). A nil receiver is not allowed;
// use NewBounds for the first point.
func (b *Bounds) Extend(lat, lon float64) {
	b.MinLat = math.Min(b.MinLat, lat)
	b.MinLon = math.Min(b.MinLon, lon)
	b.MaxLat = math.Max(b.MaxLat, lat)
	b.MaxLon = math.Max(b.MaxLon, lon)
}

// NewBounds returns a box containing a single point.
func NewBounds(lat, lon float64) *Bounds {
	return &Bounds{MinLat: lat, MinLon: lon, MaxLat: lat, MaxLon: lon}
}

// MarshalJSON encodes the box as [[minLat, minLon], [maxLat, maxLon]].
func (b Bounds) MarshalJSON() ([]byte, error) {
	return json.Marshal([2][2]float64{{Round(b.MinLat, 6), Round(b.MinLon, 6)}, {Round(b.MaxLat, 6), Round(b.MaxLon, 6)}})
}

// Day is one calendar date in the trip timezone.
type Day struct {
	Date  string `json:"date"`
	Index int    `json:"index"`
	Title string `json:"title"`
	// ExcludeFromFit marks a day left out of the trip's FitBounds.
	ExcludeFromFit bool     `json:"excludeFromFit,omitempty"`
	Stats          DayStats `json:"stats"`
	ItemIDs        []string `json:"itemIds"`
}

// DayStats sums the stats of the tracks that start on a day.
type DayStats struct {
	DistanceKm     float64  `json:"distanceKm"`
	ElevationGainM float64  `json:"elevationGainM"`
	MovingTimeS    int64    `json:"movingTimeS"`
	TrackIDs       []string `json:"trackIds"`
}

// Track is one GPX track prepared for display.
type Track struct {
	ID    string     `json:"id"`
	Name  string     `json:"name"`
	Type  string     `json:"type"`
	Start *time.Time `json:"start"`
	End   *time.Time `json:"end"`
	Stats TrackStats `json:"stats"`
	// Points is the simplified polyline of all segments concatenated.
	Points []Point `json:"points"`
	// SegmentStarts holds the index into Points where each segment after the
	// first begins. Omitted for single-segment tracks.
	SegmentStarts []int `json:"segmentStarts,omitempty"`
}

// TrackStats are per-track statistics (spec section 4.4). Pointer fields are
// null when the input has no data for them.
type TrackStats struct {
	DistanceKm     float64  `json:"distanceKm"`
	ElapsedTimeS   int64    `json:"elapsedTimeS"`
	MovingTimeS    int64    `json:"movingTimeS"`
	ElevationGainM float64  `json:"elevationGainM"`
	MinElevationM  *float64 `json:"minElevationM"`
	MaxElevationM  *float64 `json:"maxElevationM"`
	AvgHeartRate   *float64 `json:"avgHeartRate"`
}

// Point is a display trackpoint, encoded as [lat, lon, ele, secondsSinceStart].
// Ele and Seconds are encoded as null when missing.
type Point struct {
	Lat, Lon float64
	Ele      *float64
	Seconds  *int64
}

// MarshalJSON encodes the point as a compact array with lat/lon rounded to 6
// decimals and elevation to 1 decimal.
func (p Point) MarshalJSON() ([]byte, error) {
	arr := [4]any{Round(p.Lat, 6), Round(p.Lon, 6), nil, nil}
	if p.Ele != nil {
		arr[2] = Round(*p.Ele, 1)
	}
	if p.Seconds != nil {
		arr[3] = *p.Seconds
	}
	return json.Marshal(arr)
}

// Item kinds.
const (
	KindNote  = "note"
	KindPhoto = "photo"
	KindVideo = "video"
)

// Item is a timeline entry: a note, photo or video. Media fields are
// omitted for notes; paths are relative to the site root.
type Item struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Time        time.Time `json:"time"`
	TimeAssumed bool      `json:"timeAssumed,omitempty"`
	Title       string    `json:"title,omitempty"`
	Text        string    `json:"text,omitempty"`
	Lat         *float64  `json:"lat"`
	Lon         *float64  `json:"lon"`
	Placement   Placement `json:"placement"`

	// Src is the web-ready file: media/<id>.jpg (or .webp) for photos, media/<id>.mp4
	// for videos (or the copied original when ffmpeg is unavailable).
	Src string `json:"src,omitempty"`
	// Thumb is the thumbnail, media/<id>_thumb.jpg (or .webp): of the photo, or of the
	// video poster (omitted when videos were copied without ffmpeg).
	Thumb string `json:"thumb,omitempty"`
	// Poster is the video poster frame, media/<id>_poster.jpg (or .webp); omitted when
	// videos were copied without ffmpeg.
	Poster string `json:"poster,omitempty"`
	// Width/Height are the upright source dimensions (display dimensions for
	// videos); use them for aspect ratio.
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
	// DurationS is the video length in seconds.
	DurationS *float64 `json:"durationS,omitempty"`
	// LivePhoto is the converted Live Photo motion video (media/<id>.mp4),
	// set only when built with --live-photos.
	LivePhoto *string `json:"livePhoto,omitempty"`
	// Original is the source filename.
	Original string `json:"original,omitempty"`
	// Caption comes from the overrides file.
	Caption string `json:"caption,omitempty"`
	// AccuracyM is the reported horizontal accuracy of the item's own GPS
	// position in metres, when known.
	AccuracyM *float64 `json:"accuracyM,omitempty"`
}

// Placement describes how an item's position was derived.
type Placement struct {
	Source PlacementSource `json:"source"`
	// GapSeconds is the time distance to the anchor(s) used; nil for gps and
	// manual placements.
	GapSeconds *int64 `json:"gapSeconds,omitempty"`
}

// Skipped records an input that was not used, with the reason.
type Skipped struct {
	Path   string
	Reason string
}

// Round rounds x to the given number of decimals.
func Round(x float64, decimals int) float64 {
	p := math.Pow(10, float64(decimals))
	return math.Round(x*p) / p
}
