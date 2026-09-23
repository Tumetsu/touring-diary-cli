// Milestone 0 spike: HEIC/JPEG decode + metadata extraction without cgo.
// Throwaway code; see SPEC.md section 8.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/evanoberholster/imagemeta"
	"github.com/gen2brain/heic"
	"golang.org/x/image/draw"
)

func main() {
	wasm := flag.Bool("wasm", false, "force the WASM decoder (heic.ForceWasmMode)")
	outDir := flag.String("out", "out", "output directory")
	flag.Parse()
	heic.ForceWasmMode = *wasm

	if err := heic.Dynamic(); err != nil {
		fmt.Printf("heic dynamic lib: unavailable (%v)\n", err)
	} else {
		fmt.Printf("heic dynamic lib: loaded\n")
	}
	fmt.Printf("ForceWasmMode=%v\n\n", heic.ForceWasmMode)

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	failed := false
	for _, p := range flag.Args() {
		if err := process(p, *outDir); err != nil {
			fmt.Printf("  ERROR: %v\n\n", err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

func process(path, outDir string) error {
	fmt.Println("==", path)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	isHEIC := isHEICExt(path)

	// --- metadata via gen2brain/heic (HEIC only) ---
	if isHEIC {
		hx, err := heic.DecodeExif(bytes.NewReader(data))
		if err != nil {
			fmt.Printf("  heic.DecodeExif: %v\n", err)
		} else {
			fmt.Printf("  heic.DecodeExif: orient=%d DTO=%q GPS=%.6f,%.6f (no OffsetTimeOriginal field)\n",
				hx.Orientation, hx.DateTimeOriginal, hx.GPSLatitude, hx.GPSLongitude)
		}
	}

	// --- metadata via imagemeta (HEIC + JPEG) ---
	orientation := 1
	ex, err := imagemeta.Decode(bytes.NewReader(data))
	if err != nil {
		fmt.Printf("  imagemeta.Decode: %v\n", err)
	} else {
		orientation = int(ex.IFD0.Orientation)
		dto := ex.OriginalDate()
		off := "<absent>"
		if loc := ex.ExifIFD.OffsetTimeOriginal; loc != nil {
			_, secs := dto.In(loc).Zone()
			off = fmt.Sprintf("%s (%+ds)", loc.String(), secs)
		}
		fmt.Printf("  imagemeta: type=%v orient=%d\n", ex.ImageType, orientation)
		fmt.Printf("  DateTimeOriginal: %s\n", dto.Format(time.RFC3339))
		fmt.Printf("  OffsetTimeOriginal: %s\n", off)
		lat, lon := ex.GPS.Latitude(), ex.GPS.Longitude()
		if lat == 0 && lon == 0 {
			fmt.Printf("  GPS: <none>\n")
		} else {
			fmt.Printf("  GPS: %.6f, %.6f (hpe=%.1fm)\n", lat, lon, ex.GPS.HPositioningError())
		}
	}

	// --- pixel decode ---
	t0 := time.Now()
	var img image.Image
	if isHEIC {
		img, err = heic.Decode(bytes.NewReader(data))
	} else {
		img, _, err = image.Decode(bytes.NewReader(data))
	}
	dt := time.Since(t0)
	if err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	b := img.Bounds()
	fmt.Printf("  decode: %v  size=%dx%d (%T)\n", dt.Round(time.Millisecond), b.Dx(), b.Dy(), img)

	// gen2brain/heic applies irot/imir itself (DecodeExif doc: "Orientation is
	// already applied by the decoder"). Only apply EXIF orientation for non-HEIC.
	applied := 1
	if !isHEIC {
		applied = orientation
		img = orient(img, orientation)
	}

	t1 := time.Now()
	img = resize(img, 1600)
	out := filepath.Join(outDir, strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))+".jpg")
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 85}); err != nil {
		return err
	}
	ob := img.Bounds()
	fmt.Printf("  wrote %s %dx%d (orientation applied by us: %d; resize+encode %v)\n\n",
		out, ob.Dx(), ob.Dy(), applied, time.Since(t1).Round(time.Millisecond))
	return nil
}

func isHEICExt(p string) bool {
	e := strings.ToLower(filepath.Ext(p))
	return e == ".heic" || e == ".heif"
}

func resize(src image.Image, long int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= long && h <= long {
		return src
	}
	var nw, nh int
	if w >= h {
		nw, nh = long, int(float64(h)*float64(long)/float64(w)+0.5)
	} else {
		nh, nw = long, int(float64(w)*float64(long)/float64(h)+0.5)
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	return dst
}

// orient returns an upright copy of src for EXIF orientation o (1..8).
func orient(src image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch o {
			case 2: // mirror horizontal
				dx, dy = w-1-x, y
			case 3: // rotate 180
				dx, dy = w-1-x, h-1-y
			case 4: // mirror vertical
				dx, dy = x, h-1-y
			case 5: // transpose
				dx, dy = y, x
			case 6: // rotate 90 CW
				dx, dy = h-1-y, x
			case 7: // transverse
				dx, dy = h-1-y, w-1-x
			case 8: // rotate 90 CCW
				dx, dy = y, w-1-x
			}
			dst.Set(dx, dy, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}
