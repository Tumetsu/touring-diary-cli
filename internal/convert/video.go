package convert

import (
	"bytes"
	"fmt"
	"image/png"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// scaleFilter limits the long edge to 1920 px ("max 1080p") and keeps even
// dimensions for yuv420p. It runs after ffmpeg's automatic rotation, so it
// sees display dimensions.
const scaleFilter = `scale='if(gte(iw,ih),min(1920,iw),-2)':'if(gte(iw,ih),-2,min(1920,ih))'`

// toneMapFilter converts HLG/PQ HDR (iPhone default) to SDR BT.709. Without
// it the H.264 output looks washed out.
const toneMapFilter = `zscale=t=linear:npl=100,format=gbrpf32le,zscale=p=bt709,` +
	`tonemap=tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=tv,format=yuv420p`

// videoFilter returns the -vf chain for a source.
func videoFilter(hdr, canToneMap bool) string {
	if hdr && canToneMap {
		return scaleFilter + "," + toneMapFilter
	}
	return scaleFilter + ",format=yuv420p"
}

// transcodeArgs returns the ffmpeg arguments that turn src into a
// browser-playable H.264/AAC MP4 at dst. Metadata (including GPS) is not
// copied. ffmpeg rotates the pixels upright by default (autorotate) and
// drops the display matrix, so the output needs no rotation flag.
func transcodeArgs(src, dst string, hdr, canToneMap bool) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-i", src,
		"-map", "0:v:0", "-map", "0:a:0?",
		"-map_metadata", "-1", "-map_chapters", "-1",
		"-c:v", "libx264", "-crf", "23", "-preset", "medium",
		"-maxrate", "8M", "-bufsize", "16M",
		"-vf", videoFilter(hdr, canToneMap),
		"-pix_fmt", "yuv420p",
	}
	if hdr && canToneMap {
		args = append(args, "-colorspace", "bt709", "-color_primaries", "bt709", "-color_trc", "bt709")
	}
	args = append(args, "-c:a", "aac", "-b:a", "128k", "-movflags", "+faststart", "-f", "mp4", dst)
	return args
}

// transcode runs ffmpeg; dst is written atomically.
func transcode(ffmpeg, src, dst string, hdr, canToneMap bool) error {
	tmp := dst + ".tmp"
	if err := runFFmpeg(ffmpeg, transcodeArgs(src, tmp, hdr, canToneMap), nil); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// posterAt returns the poster time: 1 s, or 0 for clips of 1 s or less.
func posterAt(durationS float64) float64 {
	if durationS > 1 {
		return 1
	}
	return 0
}

// extractPoster grabs one upright frame as PNG and writes it resized to the
// photo size as a JPEG (dst) and to the thumb size (thumb).
func extractPoster(ffmpeg, src, dst, thumb string, durationS float64, hdr, canToneMap bool, p Params) error {
	vf := "format=rgb24"
	if hdr && canToneMap {
		vf = toneMapFilter + ",format=rgb24"
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin",
		"-ss", strconv.FormatFloat(posterAt(durationS), 'f', 3, 64), "-i", src,
		"-map", "0:v:0", "-frames:v", "1", "-vf", vf,
		"-f", "image2pipe", "-c:v", "png", "-"}
	var out bytes.Buffer
	if err := runFFmpeg(ffmpeg, args, &out); err != nil {
		return err
	}
	if out.Len() == 0 {
		return fmt.Errorf("ffmpeg produced no poster frame")
	}
	img, err := png.Decode(&out)
	if err != nil {
		return fmt.Errorf("decode poster frame: %w", err)
	}
	_, err = writeStill(img, 1, dst, thumb, p)
	return err
}

func runFFmpeg(ffmpeg string, args []string, stdout io.Writer) error {
	cmd := exec.Command(ffmpeg, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = stdout
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[len(msg)-300:]
		}
		return fmt.Errorf("ffmpeg: %v: %s", err, msg)
	}
	return nil
}

// hasFilter reports whether ffmpeg was built with the named filter.
func hasFilter(ffmpeg, name string) bool {
	out, err := exec.Command(ffmpeg, "-hide_banner", "-filters").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[1] == name {
			return true
		}
	}
	return false
}

// copyFile copies src to dst atomically.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
