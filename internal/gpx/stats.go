package gpx

import (
	"math"

	"github.com/tuomassalmi/touring-diary/internal/model"
)

// EarthRadiusM is the mean Earth radius used for distance calculations.
const EarthRadiusM = 6371008.8

// movingSpeedMS is the speed above which time counts as moving (1 km/h).
const movingSpeedMS = 1000.0 / 3600.0

// Haversine returns the great-circle distance in metres between two points.
func Haversine(lat1, lon1, lat2, lon2 float64) float64 {
	rad := math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLon := (lon2 - lon1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * EarthRadiusM * math.Asin(math.Min(1, math.Sqrt(a)))
}

// Stats computes the per-track statistics of spec section 4.4 over the full
// resolution points. Distances and deltas never cross segment boundaries.
func Stats(t Track) model.TrackStats {
	var st model.TrackStats
	var distM, gainM float64
	var moving float64
	var hrSum float64
	var hrN int
	for _, seg := range t.Segments {
		var eles []float64
		for i, p := range seg {
			if p.Ele != nil {
				eles = append(eles, *p.Ele)
				if st.MinElevationM == nil || *p.Ele < *st.MinElevationM {
					v := *p.Ele
					st.MinElevationM = &v
				}
				if st.MaxElevationM == nil || *p.Ele > *st.MaxElevationM {
					v := *p.Ele
					st.MaxElevationM = &v
				}
			}
			if p.HR != nil {
				hrSum += *p.HR
				hrN++
			}
			if i == 0 {
				continue
			}
			prev := seg[i-1]
			d := Haversine(prev.Lat, prev.Lon, p.Lat, p.Lon)
			distM += d
			if prev.HasTime() && p.HasTime() {
				dt := p.Time.Sub(prev.Time).Seconds()
				if dt > 0 && d/dt > movingSpeedMS {
					moving += dt
				}
			}
		}
		gainM += elevationGain(eles)
	}
	if start, end, ok := t.TimeSpan(); ok {
		st.ElapsedTimeS = int64(end.Sub(start).Seconds())
	}
	st.DistanceKm = model.Round(distM/1000, 2)
	st.MovingTimeS = int64(math.Round(moving))
	st.ElevationGainM = math.Round(gainM)
	if hrN > 0 {
		v := model.Round(hrSum/float64(hrN), 1)
		st.AvgHeartRate = &v
	}
	return st
}

// elevationGain sums positive deltas after a 3-point moving average.
func elevationGain(eles []float64) float64 {
	n := len(eles)
	if n < 2 {
		return 0
	}
	smooth := make([]float64, n)
	for i := range eles {
		if i == 0 || i == n-1 {
			smooth[i] = eles[i]
			continue
		}
		smooth[i] = (eles[i-1] + eles[i] + eles[i+1]) / 3
	}
	var gain float64
	for i := 1; i < n; i++ {
		if d := smooth[i] - smooth[i-1]; d > 0 {
			gain += d
		}
	}
	return gain
}
