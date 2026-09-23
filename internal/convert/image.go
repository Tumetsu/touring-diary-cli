package convert

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"

	"github.com/gen2brain/heic"
	"golang.org/x/image/draw"
	"golang.org/x/image/webp"
)

// JPEG qualities (spec 4.5).
const (
	PhotoQuality = 85
	ThumbQuality = 80
)

// decodeImage decodes a source image by format ("heic", "jpeg", "png",
// "webp"). HEIC pixels come back upright; the others are as stored.
func decodeImage(r io.Reader, format string) (image.Image, error) {
	switch format {
	case "heic":
		return heic.Decode(r)
	case "jpeg":
		return jpeg.Decode(r)
	case "png":
		return png.Decode(r)
	case "webp":
		return webp.Decode(r)
	}
	return nil, fmt.Errorf("unsupported image format %q", format)
}

// fitLong returns w×h scaled so the long edge is at most long, keeping the
// aspect ratio. Images already small enough keep their size.
func fitLong(w, h, long int) (int, int) {
	if long <= 0 || (w <= long && h <= long) {
		return w, h
	}
	if w >= h {
		return long, max(1, int(float64(h)*float64(long)/float64(w)+0.5))
	}
	return max(1, int(float64(w)*float64(long)/float64(h)+0.5)), long
}

// resize scales src so its long edge is at most long, with Catmull-Rom.
// The result is always a fresh *image.RGBA with origin (0,0). Transparent
// areas are composited over white since JPEG has no alpha.
func resize(src image.Image, long int) *image.RGBA {
	b := src.Bounds()
	w, h := fitLong(b.Dx(), b.Dy(), long)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	op := draw.Src
	if o, ok := src.(interface{ Opaque() bool }); !ok || !o.Opaque() {
		draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
		op = draw.Over
	}
	if w == b.Dx() && h == b.Dy() {
		draw.Draw(dst, dst.Bounds(), src, b.Min, op)
		return dst
	}
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, op, nil)
	return dst
}

// orient returns src turned upright for EXIF orientation o (1..8). Other
// values return src unchanged.
func orient(src *image.RGBA, o int) *image.RGBA {
	if o <= 1 || o > 8 {
		return src
	}
	w, h := src.Rect.Dx(), src.Rect.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		row := src.Pix[y*src.Stride : y*src.Stride+w*4]
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
			copy(dst.Pix[dy*dst.Stride+dx*4:dy*dst.Stride+dx*4+4], row[x*4:x*4+4])
		}
	}
	return dst
}

// photoResult is what converting one still image produced.
type photoResult struct {
	width, height int // upright source dimensions
}

// convertImage decodes src, turns it upright, and writes the photo and
// thumbnail JPEGs. orientation is applied only when applyOrientation is set
// (never for HEIC, whose decoder already applies it).
func convertImage(src, format string, orientation int, photoPath, thumbPath string, p Params) (photoResult, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return photoResult{}, err
	}
	img, err := decodeImage(bytes.NewReader(data), format)
	if err != nil {
		return photoResult{}, fmt.Errorf("decode: %w", err)
	}
	if format == "heic" {
		orientation = 1
	}
	return writeStill(img, orientation, photoPath, thumbPath, p)
}

// writeStill resizes img to the photo size (then the thumb size from that),
// applies orientation to the small copies, and writes the JPEGs. thumbPath
// may be empty.
func writeStill(img image.Image, orientation int, photoPath, thumbPath string, p Params) (photoResult, error) {
	b := img.Bounds()
	res := photoResult{width: b.Dx(), height: b.Dy()}
	if orientation >= 5 && orientation <= 8 {
		res.width, res.height = res.height, res.width
	}
	// Resizing before orienting is equivalent and much cheaper.
	photo := resize(img, p.PhotoSize)
	if err := writeJPEG(photoPath, orient(photo, orientation), PhotoQuality); err != nil {
		return res, err
	}
	if thumbPath != "" {
		thumb := resize(photo, p.ThumbSize)
		if err := writeJPEG(thumbPath, orient(thumb, orientation), ThumbQuality); err != nil {
			return res, err
		}
	}
	return res, nil
}

// writeJPEG encodes img to path atomically. No metadata is written.
func writeJPEG(path string, img image.Image, quality int) error {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return fmt.Errorf("encode %s: %w", filepath.Base(path), err)
	}
	return writeFileAtomic(path, buf.Bytes())
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
