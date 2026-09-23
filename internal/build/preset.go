package build

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Tumetsu/touring-diary-cli/internal/convert"
)

// Preset is a named set of defaults. Its values apply only to settings that
// neither the command line nor the config file sets.
type Preset struct {
	Format                     string
	PhotoSize                  int
	PhotoQuality, ThumbQuality int
	NoVideo                    bool
}

// Presets are the known presets by name.
var Presets = map[string]Preset{
	// web keeps the output small for static hosts such as GitHub Pages.
	"web": {Format: convert.FormatWebP, PhotoSize: 1400, PhotoQuality: 80, ThumbQuality: 75, NoVideo: true},
}

// PresetNames returns the preset names, sorted.
func PresetNames() []string {
	names := make([]string, 0, len(Presets))
	for n := range Presets {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// resolveImageOptions fills the image, video and size-limit options from,
// in order: the command line, the config file, the preset, the defaults.
// It rejects out-of-range values wherever they come from.
func (b *builder) resolveImageOptions() error {
	o, c := &b.opts, b.cfg
	for _, v := range []struct {
		name string
		q    int
	}{{"--photo-quality", o.PhotoQuality}, {"--thumb-quality", o.ThumbQuality},
		{"config photoQuality", c.PhotoQuality}, {"config thumbQuality", c.ThumbQuality}} {
		if v.q < 0 || v.q > 100 {
			return fmt.Errorf("%s must be between 1 and 100, got %d", v.name, v.q)
		}
	}
	if o.MaxOutputMB < 0 || c.MaxOutputMB < 0 {
		return fmt.Errorf("--max-output-mb must not be negative")
	}
	name := strings.ToLower(firstNonEmpty(o.Preset, c.Preset))
	var p Preset
	if name != "" {
		var ok bool
		if p, ok = Presets[name]; !ok {
			return fmt.Errorf("unknown preset %q (known: %s)", name, strings.Join(PresetNames(), ", "))
		}
	}
	o.Preset = name
	o.Format = normalizeFormat(firstNonEmpty(o.Format, c.Format, p.Format, convert.DefaultFormat))
	if !convert.ValidFormat(o.Format) {
		return fmt.Errorf("unknown image format %q (use jpeg or webp)", o.Format)
	}
	o.PhotoSize = firstPositive(o.PhotoSize, c.PhotoSize, p.PhotoSize, convert.DefaultPhotoSize)
	o.ThumbSize = firstPositive(o.ThumbSize, c.ThumbSize, convert.DefaultThumbSize)
	o.PhotoQuality = firstPositive(o.PhotoQuality, c.PhotoQuality, p.PhotoQuality, convert.DefaultPhotoQuality)
	o.ThumbQuality = firstPositive(o.ThumbQuality, c.ThumbQuality, p.ThumbQuality, convert.DefaultThumbQuality)
	switch {
	case o.NoVideo || o.NoVideoSet:
	case c.NoVideo != nil:
		o.NoVideo = *c.NoVideo
	default:
		o.NoVideo = p.NoVideo
	}
	if o.MaxOutputMB == 0 {
		o.MaxOutputMB = c.MaxOutputMB
	}
	return nil
}

// normalizeFormat lower-cases a format name and accepts "jpg" for "jpeg".
func normalizeFormat(f string) string {
	f = strings.ToLower(f)
	if f == "jpg" {
		return convert.FormatJPEG
	}
	return f
}
