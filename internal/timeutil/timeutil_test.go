package timeutil

import (
	"reflect"
	"testing"
	"time"
)

func TestInferZoneMostCommonOffset(t *testing.T) {
	z := InferZone([]int{3 * 3600, 2 * 3600, 3 * 3600, 0})
	if z.Name != "+03:00" {
		t.Fatalf("name = %q", z.Name)
	}
	if _, off := time.Date(2026, 1, 1, 0, 0, 0, 0, z.Loc).Zone(); off != 3*3600 {
		t.Errorf("offset = %d", off)
	}
	if z := InferZone(nil); z.Name != "+00:00" {
		t.Errorf("empty: %q", z.Name)
	}
	if z := InferZone([]int{-5*3600 - 1800}); z.Name != "-05:30" {
		t.Errorf("negative: %q", z.Name)
	}
}

func TestInferZoneFromParsedTimestamps(t *testing.T) {
	var offs []int
	for _, s := range []string{"2026-06-27T10:10:00+03:00", "2026-06-27T23:50:00+03:00", "2026-07-05T21:50:00+02:00"} {
		tm, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		offs = append(offs, Offset(tm))
	}
	if z := InferZone(offs); z.Name != "+03:00" {
		t.Errorf("zone = %q", z.Name)
	}
}

func TestDateKeyAcrossMidnightPlus3(t *testing.T) {
	z := FixedZone(3 * 3600)
	cases := map[string]string{
		"2026-06-27T20:59:59Z": "2026-06-27", // 23:59:59 local
		"2026-06-27T21:00:00Z": "2026-06-28", // 00:00 local
		"2026-06-27T20:50:00Z": "2026-06-27", // the 23:50 tent note
		"2026-06-27T22:45:00Z": "2026-06-28", // 01:45 local, next day
	}
	for in, want := range cases {
		tm, _ := time.Parse(time.RFC3339, in)
		if got := z.DateKey(tm); got != want {
			t.Errorf("%s: got %s want %s", in, got, want)
		}
	}
}

func TestParseZone(t *testing.T) {
	z, err := ParseZone("Europe/Helsinki")
	if err != nil {
		t.Fatal(err)
	}
	if z.Name != "Europe/Helsinki" {
		t.Errorf("name = %q", z.Name)
	}
	summer, _ := time.Parse(time.RFC3339, "2026-06-27T21:30:00Z")
	if got := z.DateKey(summer); got != "2026-06-28" {
		t.Errorf("helsinki summer date = %s", got)
	}
	if z, err := ParseZone("+03:00"); err != nil || z.Name != "+03:00" {
		t.Errorf("offset zone: %v %v", z.Name, err)
	}
	if _, err := ParseZone("Mars/Olympus"); err == nil {
		t.Error("expected error for unknown zone")
	}
}

func TestDateRange(t *testing.T) {
	got, err := DateRange("2026-06-30", "2026-07-02")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"2026-06-30", "2026-07-01", "2026-07-02"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
}
