package media

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/evanoberholster/imagemeta"
)

// readImage fills f from the image's EXIF. A file without EXIF is not an
// error: it simply has no time or position (the build decides whether to
// skip it). Only unreadable files fail.
func readImage(f *File) error {
	r, err := os.Open(f.Path)
	if err != nil {
		return err
	}
	defer r.Close()
	ex, err := imagemeta.Decode(r)
	if err != nil {
		// No or unparseable EXIF (common for PNG, WebP, screenshots).
		return nil
	}
	if t := ex.OriginalDate(); !t.IsZero() && t.Year() > 1900 {
		f.Time = t
		f.Naive = ex.ExifIFD.OffsetTimeOriginal == nil
	}
	lat, lon := ex.GPS.Latitude(), ex.GPS.Longitude()
	if validPosition(lat, lon) {
		f.Lat, f.Lon = &lat, &lon
		if acc := ex.GPS.HPositioningError(); acc > 0 {
			f.AccuracyM = &acc
		}
	}
	f.Orientation = int(ex.IFD0.Orientation)
	w, h := int(ex.ExifIFD.PixelXDimension), int(ex.ExifIFD.PixelYDimension)
	if w == 0 || h == 0 {
		w, h = int(ex.IFD0.ImageWidth), int(ex.IFD0.ImageHeight)
	}
	// EXIF dimensions describe the stored image; swap for 90-degree turns.
	// These are a hint only: conversion reports the decoded dimensions.
	if f.Orientation >= 5 && f.Orientation <= 8 {
		w, h = h, w
	}
	f.Width, f.Height = w, h
	return nil
}

// validPosition rejects 0,0 (imagemeta's "absent") and out-of-range values.
func validPosition(lat, lon float64) bool {
	if lat == 0 && lon == 0 {
		return false
	}
	return !math.IsNaN(lat) && !math.IsNaN(lon) && math.Abs(lat) <= 90 && math.Abs(lon) <= 180
}

// probeOutput is the subset of `ffprobe -print_format json -show_format
// -show_streams` that is used.
type probeOutput struct {
	Format struct {
		Duration string            `json:"duration"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
	Streams []struct {
		CodecType     string            `json:"codec_type"`
		Width         int               `json:"width"`
		Height        int               `json:"height"`
		ColorTransfer string            `json:"color_transfer"`
		Tags          map[string]string `json:"tags"`
		Disposition   map[string]int    `json:"disposition"`
		SideDataList  []struct {
			Rotation *float64 `json:"rotation"`
		} `json:"side_data_list"`
	} `json:"streams"`
}

func readVideo(f *File, ffprobe string) error {
	cmd := exec.Command(ffprobe, "-v", "quiet", "-print_format", "json", "-show_format", "-show_streams", f.Path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("ffprobe failed: %v %s", err, strings.TrimSpace(stderr.String()))
	}
	return applyProbe(f, out)
}

// applyProbe fills f from ffprobe JSON output.
func applyProbe(f *File, data []byte) error {
	var p probeOutput
	if err := json.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("parse ffprobe output: %w", err)
	}
	tags := p.Format.Tags
	if s := tags["com.apple.quicktime.creationdate"]; s != "" {
		if t, err := parseQuickTimeDate(s); err == nil {
			f.Time = t
		}
	}
	if f.Time.IsZero() {
		if t, err := time.Parse(time.RFC3339Nano, tags["creation_time"]); err == nil && t.Year() > 1904 {
			f.Time = t.UTC()
		}
	}
	loc := tags["com.apple.quicktime.location.ISO6709"]
	if loc == "" {
		loc = tags["location"] // written by Android and ffmpeg
	}
	if s := loc; s != "" {
		if lat, lon, err := ParseISO6709(s); err == nil && validPosition(lat, lon) {
			f.Lat, f.Lon = &lat, &lon
			if a, err := strconv.ParseFloat(tags["com.apple.quicktime.location.accuracy.horizontal"], 64); err == nil && a > 0 {
				f.AccuracyM = &a
			}
		}
	}
	if d, err := strconv.ParseFloat(p.Format.Duration, 64); err == nil {
		f.DurationS = d
	}
	for _, s := range p.Streams {
		if s.CodecType != "video" || s.Disposition["attached_pic"] == 1 {
			continue
		}
		rot := 0
		for _, sd := range s.SideDataList {
			if sd.Rotation != nil {
				rot = int(math.Round(*sd.Rotation))
			}
		}
		if r, err := strconv.Atoi(s.Tags["rotate"]); err == nil && rot == 0 {
			rot = -r // the legacy tag is clockwise, side data counter-clockwise
		}
		f.Rotation = rot
		f.Width, f.Height = s.Width, s.Height
		if ((rot%180)+180)%180 == 90 {
			f.Width, f.Height = s.Height, s.Width
		}
		f.HDR = s.ColorTransfer == "arib-std-b67" || s.ColorTransfer == "smpte2084"
		break
	}
	if f.Width == 0 {
		return errors.New("no video stream")
	}
	return nil
}

// parseQuickTimeDate parses com.apple.quicktime.creationdate, e.g.
// "2026-06-27T10:39:15+0300".
func parseQuickTimeDate(s string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02T15:04:05-0700", time.RFC3339Nano, "2006-01-02T15:04:05.000-0700"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised date %q", s)
}

var iso6709Re = regexp.MustCompile(`^([+-])(\d+(?:\.\d*)?)([+-])(\d+(?:\.\d*)?)(?:[+-]\d+(?:\.\d*)?)?(?:CRS[^/]*)?/?$`)

// ParseISO6709 parses an ISO 6709 point such as "+66.7003+027.5555/" or
// "+65.0078+025.5037+009.489/". Degrees, degrees-minutes (DDMM.MM) and
// degrees-minutes-seconds (DDMMSS.SS) forms are accepted; altitude is
// ignored.
func ParseISO6709(s string) (lat, lon float64, err error) {
	m := iso6709Re.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, 0, fmt.Errorf("invalid ISO 6709 location %q", s)
	}
	lat, err = iso6709Part(m[1], m[2], 2)
	if err != nil {
		return 0, 0, err
	}
	lon, err = iso6709Part(m[3], m[4], 3)
	if err != nil {
		return 0, 0, err
	}
	if math.Abs(lat) > 90 || math.Abs(lon) > 180 {
		return 0, 0, fmt.Errorf("ISO 6709 location out of range %q", s)
	}
	return lat, lon, nil
}

// iso6709Part converts one coordinate; degDigits is 2 for latitude and 3
// for longitude.
func iso6709Part(sign, num string, degDigits int) (float64, error) {
	intPart := num
	if i := strings.IndexByte(num, '.'); i >= 0 {
		intPart = num[:i]
	}
	v, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, err
	}
	var deg float64
	switch len(intPart) {
	case degDigits:
		deg = v
	case degDigits + 2: // DDMM.MM
		d := math.Floor(v / 100)
		deg = d + (v-d*100)/60
	case degDigits + 4: // DDMMSS.SS
		d := math.Floor(v / 10000)
		mm := math.Floor((v - d*10000) / 100)
		deg = d + mm/60 + (v-d*10000-mm*100)/3600
	default:
		return 0, fmt.Errorf("invalid ISO 6709 coordinate %q", num)
	}
	if sign == "-" {
		deg = -deg
	}
	return deg, nil
}
