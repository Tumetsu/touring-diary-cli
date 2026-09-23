package media

import (
	"bytes"
	"encoding/binary"
	"flag"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Fixtures in testdata/media are committed. Regenerate them with
//
//	go test ./internal/media -run TestGenerateFixtures -update-fixtures
//
// The HEIC and MOV fixtures need ImageMagick (magick) and ffmpeg.
var updateFixtures = flag.Bool("update-fixtures", false, "regenerate testdata/media fixtures")

const fixtureDir = "../../testdata/media"

// TIFF field types.
const (
	tASCII    = 2
	tShort    = 3
	tLong     = 4
	tRational = 5
)

// tiffEntry is one IFD entry. For sub-IFD pointers set ifdRef to the index
// of the referenced IFD (1-based); data is then ignored.
type tiffEntry struct {
	tag, typ uint16
	count    uint32
	data     []byte
	ifdRef   int
}

func ascii(tag uint16, s string) tiffEntry {
	return tiffEntry{tag: tag, typ: tASCII, count: uint32(len(s) + 1), data: append([]byte(s), 0)}
}

func short(tag uint16, v uint16) tiffEntry {
	b := make([]byte, 2)
	binary.LittleEndian.PutUint16(b, v)
	return tiffEntry{tag: tag, typ: tShort, count: 1, data: b}
}

func rationals(tag uint16, v ...[2]uint32) tiffEntry {
	var b []byte
	for _, r := range v {
		b = binary.LittleEndian.AppendUint32(b, r[0])
		b = binary.LittleEndian.AppendUint32(b, r[1])
	}
	return tiffEntry{tag: tag, typ: tRational, count: uint32(len(v)), data: b}
}

func ifdPointer(tag uint16, ref int) tiffEntry {
	return tiffEntry{tag: tag, typ: tLong, count: 1, ifdRef: ref}
}

// buildTIFF serialises IFDs (IFD0 first; entries sorted by tag) into a
// little-endian TIFF blob with values that do not fit 4 bytes placed after
// the IFDs.
func buildTIFF(ifds [][]tiffEntry) []byte {
	le := binary.LittleEndian
	offsets := make([]uint32, len(ifds))
	pos := uint32(8)
	for i, ifd := range ifds {
		offsets[i] = pos
		pos += 2 + 12*uint32(len(ifd)) + 4
	}
	dataStart := pos
	var head, data []byte
	head = append(head, 'I', 'I', 42, 0)
	head = le.AppendUint32(head, 8)
	for _, ifd := range ifds {
		head = le.AppendUint16(head, uint16(len(ifd)))
		for _, e := range ifd {
			head = le.AppendUint16(head, e.tag)
			head = le.AppendUint16(head, e.typ)
			head = le.AppendUint32(head, e.count)
			switch {
			case e.ifdRef > 0:
				head = le.AppendUint32(head, offsets[e.ifdRef])
			case len(e.data) <= 4:
				v := make([]byte, 4)
				copy(v, e.data)
				head = append(head, v...)
			default:
				head = le.AppendUint32(head, dataStart+uint32(len(data)))
				data = append(data, e.data...)
				if len(data)%2 == 1 {
					data = append(data, 0)
				}
			}
		}
		head = le.AppendUint32(head, 0) // no next IFD
	}
	return append(head, data...)
}

// jpegWithExif encodes a small image and inserts an APP1 Exif segment.
func jpegWithExif(t *testing.T, w, h int, tiff []byte) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// Top-left quadrant red so orientation handling can be verified.
			c := color.RGBA{40, 90, 160, 255}
			if x < w/2 && y < h/2 {
				c = color.RGBA{220, 30, 30, 255}
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	enc := buf.Bytes()
	if tiff == nil {
		return enc
	}
	payload := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	seg = append(seg, payload...)
	out := append([]byte{}, enc[:2]...) // SOI
	out = append(out, seg...)
	return append(out, enc[2:]...)
}

func fixtureExif(dto, offset string, orientation uint16, gps bool) []byte {
	ifd0 := []tiffEntry{short(0x0112, orientation), ifdPointer(0x8769, 1)}
	exif := []tiffEntry{ascii(0x9003, dto)}
	if offset != "" {
		exif = append(exif, ascii(0x9011, offset))
	}
	ifds := [][]tiffEntry{ifd0, exif}
	if gps {
		ifds[0] = append(ifds[0], ifdPointer(0x8825, 2))
		ifds = append(ifds, []tiffEntry{
			ascii(0x0001, "N"),
			rationals(0x0002, [2]uint32{66, 1}, [2]uint32{42, 1}, [2]uint32{108, 100}), // 66.7003
			ascii(0x0003, "E"),
			rationals(0x0004, [2]uint32{27, 1}, [2]uint32{33, 1}, [2]uint32{1980, 100}), // 27.5555
			rationals(0x001F, [2]uint32{475, 100}),                                      // 4.75 m
		})
	}
	return buildTIFF(ifds)
}

func TestGenerateFixtures(t *testing.T) {
	if !*updateFixtures {
		t.Skip("run with -update-fixtures to regenerate")
	}
	if err := os.MkdirAll(fixtureDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(fixtureDir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Stored landscape 64x48 with orientation 6: displays as 48x64 portrait.
	write("gps_offset.jpg", jpegWithExif(t, 64, 48, fixtureExif("2026:06:27 10:39:15", "+03:00", 6, true)))
	// No offset: time is assumed to be in the trip zone.
	write("naive.jpg", jpegWithExif(t, 64, 48, fixtureExif("2026:06:27 21:00:00", "", 3, false)))
	// Live Photo pair; the video is never probed so any bytes do.
	write("IMG_0100.jpg", jpegWithExif(t, 64, 48, fixtureExif("2026:06:27 12:00:00", "+03:00", 1, false)))
	write("img_0100.MOV", []byte("not a real video"))
	// No metadata at all.
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 32, 24))); err != nil {
		t.Fatal(err)
	}
	write("nometa.png", buf.Bytes())

	run := func(name string, args ...string) {
		if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	run("magick", filepath.Join(fixtureDir, "gps_offset.jpg"), "-auto-orient", "-quality", "50",
		filepath.Join(fixtureDir, "heic_gps.heic"))
	run("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-noautorotate", "-display_rotation", "90", "-f", "lavfi", "-i", "testsrc=size=64x48:rate=10:duration=1.5",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1.5",
		"-c:v", "libx264", "-crf", "35", "-c:a", "aac", "-b:a", "32k", "-shortest",
		"-movflags", "use_metadata_tags",
		"-metadata", "com.apple.quicktime.creationdate=2026-06-27T10:45:00+0300",
		"-metadata", "com.apple.quicktime.location.ISO6709=+66.7010+027.5600+120.5/",
		"-metadata", "com.apple.quicktime.location.accuracy.horizontal=12.5",
		filepath.Join(fixtureDir, "clip.mov"))
}
