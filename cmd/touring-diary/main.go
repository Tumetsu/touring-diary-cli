// Command touring-diary turns GPX tracks, timestamped notes and media from a
// trip into a static website. See SPEC.md.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/Tumetsu/touring-diary-cli/internal/build"
	"github.com/Tumetsu/touring-diary-cli/internal/convert"
	"github.com/Tumetsu/touring-diary-cli/internal/placement"
	"github.com/Tumetsu/touring-diary-cli/internal/serve"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `touring-diary turns GPX tracks, timestamped notes and photos/videos from a
trip into a static website with a map and a timeline.

usage:
  touring-diary build --gpx DIR --notes DIR --media DIR --out DIR [flags]
  touring-diary serve [--addr HOST:PORT] [DIR]
  touring-diary version

commands:
  build     build the site from GPX, notes and media folders
  serve     preview a built site over local HTTP (default dir: dist)
  version   print the version

main build flags:
  --gpx DIR, --notes DIR, --media DIR   input folders (at least one)
  --out DIR                             output folder (required)
  --title TEXT                          trip title
  --tz ZONE                             trip timezone, e.g. Europe/Helsinki
  --config FILE, --overrides FILE       trip config and per-item overrides (JSON)
  --preset web                          smaller output for static hosting
  --cloudflare-auth                     password-protect the site on Cloudflare Pages
  --force                               ignore the conversion cache

run "touring-diary build -h" for all flags with their defaults.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "build":
		return runBuild(args[1:], stdout, stderr)
	case "serve":
		return runServe(args[1:], stdout, stderr)
	case "version", "-version", "--version":
		fmt.Fprintf(stdout, "touring-diary %s\n", version)
		return 0
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

// buildDefaults are the effective defaults shown by "build -h". The flags
// themselves default to zero values so that a config file can fill them
// (the CLI wins only when a flag is set).
var buildDefaults = map[string]string{
	"gpx": "none", "notes": "none", "media": "none", "out": "none, required",
	"title": `"` + build.DefaultTitle + `"`, "tz": "most common UTC offset in notes and EXIF",
	"config": "none", "overrides": "none",
	"max-gap":    strings.TrimSuffix(strings.TrimSuffix(placement.DefaultMaxGap.String(), "0s"), "0m"),
	"photo-size": strconv.Itoa(convert.DefaultPhotoSize), "thumb-size": strconv.Itoa(convert.DefaultThumbSize),
	"format":        convert.DefaultFormat,
	"photo-quality": strconv.Itoa(convert.DefaultPhotoQuality), "thumb-quality": strconv.Itoa(convert.DefaultThumbQuality),
	"preset": "none", "max-output-mb": "none",
}

// presetHelp documents the presets in "build -h".
const presetHelp = `
presets:
  --preset web   for static hosts such as GitHub Pages: sets
                 --format webp --photo-size 1400 --photo-quality 80
                 --thumb-quality 75 --no-video
                 Each of these given on the command line or in the config
                 file wins over the preset.
`

func runBuild(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if wantsHelp(args) {
		fs.SetOutput(stdout)
	}
	var o build.Options
	fs.StringVar(&o.GPXDir, "gpx", "", "folder of .gpx files")
	fs.StringVar(&o.NotesDir, "notes", "", "folder of .json note files")
	fs.StringVar(&o.MediaDir, "media", "", "folder of photos and videos (scanned recursively)")
	fs.StringVar(&o.OutDir, "out", "", "output folder")
	fs.StringVar(&o.Title, "title", "", "trip title")
	fs.StringVar(&o.TZ, "tz", "", "trip timezone for display and day boundaries (IANA name or +HH:MM)")
	fs.StringVar(&o.ConfigPath, "config", "", "trip config JSON; CLI flags win over it")
	fs.StringVar(&o.OverridesPath, "overrides", "", "overrides JSON keyed by media filename or note timestamp")
	fs.DurationVar(&o.MaxGap, "max-gap", 0, "max time distance to a position anchor for placement")
	fs.IntVar(&o.PhotoSize, "photo-size", 0, "long edge of converted photos and video posters in px")
	fs.IntVar(&o.ThumbSize, "thumb-size", 0, "long edge of thumbnails in px")
	fs.StringVar(&o.Format, "format", "", "image format of photos, thumbnails and video posters: jpeg or webp")
	fs.IntVar(&o.PhotoQuality, "photo-quality", 0, "encoder quality of photos and video posters, 1-100")
	fs.IntVar(&o.ThumbQuality, "thumb-quality", 0, "encoder quality of thumbnails, 1-100")
	fs.StringVar(&o.Preset, "preset", "", "preset of defaults: web (see below)")
	fs.Float64Var(&o.MaxOutputMB, "max-output-mb", 0, "warn when the output folder is larger than this many MB")
	fs.BoolVar(&o.LivePhotos, "live-photos", false, "convert and attach Live Photo motion videos")
	fs.BoolVar(&o.NoVideo, "no-video", false, "skip videos entirely")
	fs.BoolVar(&o.CloudflareAuth, "cloudflare-auth", false, "write _worker.js: HTTP Basic Auth on Cloudflare Pages (secrets AUTH_USER, AUTH_PASSWORD)")
	fs.BoolVar(&o.Force, "force", false, "ignore the conversion cache and rebuild all media")
	fs.BoolVar(&o.Verbose, "verbose", false, "log every media file and placement detail")
	fs.Usage = func() {
		w := fs.Output()
		fmt.Fprint(w, "usage: touring-diary build --out DIR [--gpx DIR] [--notes DIR] [--media DIR] [flags]\n\nflags:\n")
		printFlags(w, fs, buildDefaults)
		fmt.Fprint(w, presetHelp)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "build: unexpected arguments %v\n", fs.Args())
		return 2
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "no-video" {
			o.NoVideoSet = true
		}
	})
	o.Log = log.New(stderr, "", 0)
	sum, err := build.Run(o)
	if err != nil {
		fmt.Fprintf(stderr, "build failed: %v\n", err)
		return 1
	}
	if err := sum.Print(stdout); err != nil {
		fmt.Fprintf(stderr, "write summary: %v\n", err)
		return 1
	}
	return 0
}

func runServe(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if wantsHelp(args) {
		fs.SetOutput(stdout)
	}
	dir := fs.String("dir", "dist", "built site folder to serve (may also be given as an argument)")
	addr := fs.String("addr", "127.0.0.1:8090", "listen address")
	fs.Usage = func() {
		w := fs.Output()
		fmt.Fprint(w, "usage: touring-diary serve [--addr HOST:PORT] [DIR]\n\nflags:\n")
		printFlags(w, fs, nil)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	switch fs.NArg() {
	case 0:
	case 1:
		*dir = fs.Arg(0)
	default:
		fmt.Fprintf(stderr, "serve: unexpected arguments %v\n", fs.Args()[1:])
		return 2
	}
	if err := serve.Run(*dir, *addr, stdout); err != nil {
		fmt.Fprintf(stderr, "serve: %v\n", err)
		return 1
	}
	return 0
}

// wantsHelp reports whether args ask for help, so it goes to stdout.
func wantsHelp(args []string) bool {
	for _, a := range args {
		switch a {
		case "-h", "-help", "--help", "--h":
			return true
		case "--":
			return false
		}
	}
	return false
}

// printFlags lists every flag with its default. defaults overrides the
// flag's own default value where the effective default is computed later.
func printFlags(w io.Writer, fs *flag.FlagSet, defaults map[string]string) {
	fs.VisitAll(func(f *flag.Flag) {
		name, usage := flag.UnquoteUsage(f)
		head := "  --" + f.Name
		if name != "" {
			head += " " + name
		}
		def, ok := defaults[f.Name]
		if !ok {
			def = f.DefValue
			if name == "string" {
				def = strconv.Quote(def)
			}
		}
		fmt.Fprintf(w, "%s\n      %s (default: %s)\n", head, usage, def)
	})
}
