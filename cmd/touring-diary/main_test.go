package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestNoArgsPrintsUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	for _, want := range []string{"build", "serve", "version", "--gpx", "--out"} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("usage lacks %q", want)
		}
	}
}

func TestVersion(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"version"}, &out, &out); code != 0 || out.String() != "touring-diary dev\n" {
		t.Errorf("exit %d, output %q", code, out.String())
	}
}

func TestBuildHelpListsEveryFlagWithDefault(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"build", "--help"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	help := out.String()
	for _, name := range []string{"gpx", "notes", "media", "out", "title", "tz", "config", "overrides",
		"max-gap", "photo-size", "thumb-size", "live-photos", "no-video", "force", "verbose",
		"format", "photo-quality", "thumb-quality", "preset", "max-output-mb", "cloudflare-auth"} {
		if !strings.Contains(help, "--"+name) {
			t.Errorf("help lacks --%s", name)
		}
	}
	if got, want := strings.Count(help, "(default: "), 21; got != want {
		t.Errorf("%d defaults listed, want %d", got, want)
	}
	for _, want := range []string{"(default: 12h)", "(default: 1600)", "(default: 320)", "(default: jpeg)",
		"(default: 85)", "(default: 80)", "--preset web", "--format webp --photo-size 1400 --photo-quality 80"} {
		if !strings.Contains(help, want) {
			t.Errorf("help lacks %q", want)
		}
	}
}

// TestBuildPresetExplicitFlagsWin runs a real build with the web preset and
// flags that override part of it.
func TestBuildPresetExplicitFlagsWin(t *testing.T) {
	out := t.TempDir()
	var stdout, stderr bytes.Buffer
	args := []string{"build", "--media", "../../testdata/media", "--notes", "../../testdata/notes", "--out", out,
		"--preset", "web", "--format", "jpeg", "--thumb-quality", "60", "--no-video=false", "--max-output-mb", "0.001"}
	if code := run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	s := stdout.String()
	for _, want := range []string{"Images: jpeg, photos and posters 1400 px q80, thumbnails 320 px q60",
		"Output size: ", "trip.json + site assets:", "largest file: ", "warning: output is "} {
		if !strings.Contains(s, want) {
			t.Errorf("summary lacks %q:\n%s", want, s)
		}
	}
	// --no-video=false beats the preset: videos are not skipped for it.
	if strings.Contains(s, "--no-video") {
		t.Errorf("videos disabled despite --no-video=false:\n%s", s)
	}
}
