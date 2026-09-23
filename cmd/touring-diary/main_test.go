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
		"max-gap", "photo-size", "thumb-size", "live-photos", "no-video", "force", "verbose"} {
		if !strings.Contains(help, "--"+name) {
			t.Errorf("help lacks --%s", name)
		}
	}
	if got, want := strings.Count(help, "(default: "), 15; got != want {
		t.Errorf("%d defaults listed, want %d", got, want)
	}
	for _, want := range []string{"(default: 12h)", "(default: 1600)", "(default: 320)"} {
		if !strings.Contains(help, want) {
			t.Errorf("help lacks %q", want)
		}
	}
}
