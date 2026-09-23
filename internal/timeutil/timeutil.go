// Package timeutil handles trip timezone selection, offset inference and
// bucketing of UTC instants into calendar days (spec section 4.1).
package timeutil

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DateLayout is the layout of a calendar date key, e.g. "2026-06-27".
const DateLayout = "2006-01-02"

// Zone is the trip timezone: a location plus the name written to trip.json.
type Zone struct {
	Loc  *time.Location
	Name string
}

var offsetRe = regexp.MustCompile(`^([+-])(\d{2}):?(\d{2})$`)

// ParseZone parses an IANA zone name ("Europe/Helsinki"), "UTC", or a fixed
// offset ("+03:00").
func ParseZone(s string) (Zone, error) {
	if m := offsetRe.FindStringSubmatch(s); m != nil {
		h, _ := strconv.Atoi(m[2])
		mi, _ := strconv.Atoi(m[3])
		secs := h*3600 + mi*60
		if m[1] == "-" {
			secs = -secs
		}
		return FixedZone(secs), nil
	}
	if strings.EqualFold(s, "Local") {
		return Zone{}, fmt.Errorf("%q is the build machine's zone and would make the site depend on where it is built; give an IANA name such as Europe/Helsinki or an offset such as +03:00", s)
	}
	loc, err := time.LoadLocation(s)
	if err != nil {
		return Zone{}, fmt.Errorf("load timezone %q: %w", s, err)
	}
	return Zone{Loc: loc, Name: loc.String()}, nil
}

// FixedZone returns a zone with a constant UTC offset, named like "+03:00".
func FixedZone(offsetSeconds int) Zone {
	name := FormatOffset(offsetSeconds)
	return Zone{Loc: time.FixedZone(name, offsetSeconds), Name: name}
}

// FormatOffset formats an offset in seconds as "+03:00".
func FormatOffset(offsetSeconds int) string {
	sign := '+'
	if offsetSeconds < 0 {
		sign = '-'
		offsetSeconds = -offsetSeconds
	}
	return fmt.Sprintf("%c%02d:%02d", sign, offsetSeconds/3600, offsetSeconds%3600/60)
}

// InferZone returns a fixed zone for the most common offset among offsets
// (seconds east of UTC). Ties go to the offset that reached the top count
// first. With no offsets the result is UTC ("+00:00").
func InferZone(offsets []int) Zone {
	counts := map[int]int{}
	best, bestCount := 0, 0
	for _, o := range offsets {
		counts[o]++
		if counts[o] > bestCount {
			best, bestCount = o, counts[o]
		}
	}
	return FixedZone(best)
}

// Offset returns the UTC offset in seconds that t carries.
func Offset(t time.Time) int {
	_, off := t.Zone()
	return off
}

// DateKey returns the calendar date of t in zone z, as "2006-01-02".
func (z Zone) DateKey(t time.Time) string {
	return t.In(z.Loc).Format(DateLayout)
}
