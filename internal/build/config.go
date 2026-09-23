package build

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Config is the optional trip config file (spec section 2.5). Every field
// mirrors a CLI flag; the CLI wins when both are set.
type Config struct {
	Title      string   `json:"title"`
	Timezone   string   `json:"timezone"`
	MaxGap     Duration `json:"maxGap"`
	GPX        string   `json:"gpx"`
	Notes      string   `json:"notes"`
	Media      string   `json:"media"`
	Out        string   `json:"out"`
	Overrides  string   `json:"overrides"`
	PhotoSize  int      `json:"photoSize"`
	ThumbSize  int      `json:"thumbSize"`
	LivePhotos bool     `json:"livePhotos"`
	// NoVideo is a pointer so that an explicit false wins over a preset.
	NoVideo      *bool   `json:"noVideo"`
	Format       string  `json:"format"`
	PhotoQuality int     `json:"photoQuality"`
	ThumbQuality int     `json:"thumbQuality"`
	Preset       string  `json:"preset"`
	MaxOutputMB  float64 `json:"maxOutputMb"`
	// CloudflareAuth mirrors --cloudflare-auth.
	CloudflareAuth bool                 `json:"cloudflareAuth"`
	Days           map[string]DayConfig `json:"days"`
	// Map settings are read by the frontend milestone; kept here so config
	// files validate.
	Map json.RawMessage `json:"map"`
}

// DayConfig holds per-day settings keyed by date ("2026-06-27").
type DayConfig struct {
	Title string `json:"title"`
	// ExcludeFromFit leaves the day out of the map's initial fit (fitBounds).
	ExcludeFromFit bool `json:"excludeFromFit"`
}

// Duration is a time.Duration that decodes from a Go duration string ("12h").
type Duration time.Duration

// UnmarshalJSON parses a duration string.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duration must be a string like \"12h\": %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("parse duration %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

// LoadConfig reads a trip config file. Relative folder and file paths in
// it are resolved against the config file's directory.
func LoadConfig(path string) (Config, error) {
	var c Config
	if err := readJSON(path, &c); err != nil {
		return c, fmt.Errorf("load config: %w", err)
	}
	base := filepath.Dir(path)
	for _, p := range []*string{&c.GPX, &c.Notes, &c.Media, &c.Out, &c.Overrides} {
		if *p != "" && !filepath.IsAbs(*p) {
			*p = filepath.Join(base, *p)
		}
	}
	return c, nil
}

// Override is one entry of the overrides file (spec section 2.4).
type Override struct {
	Time    *string  `json:"time"`
	Lat     *float64 `json:"lat"`
	Lon     *float64 `json:"lon"`
	Title   *string  `json:"title"`
	Caption *string  `json:"caption"`
	Skip    bool     `json:"skip"`
}

// LoadOverrides reads an overrides file keyed by media filename or note
// timestamp.
func LoadOverrides(path string) (map[string]Override, error) {
	o := map[string]Override{}
	if err := readJSON(path, &o); err != nil {
		return nil, fmt.Errorf("load overrides: %w", err)
	}
	return o, nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}
