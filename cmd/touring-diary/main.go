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

	"github.com/tuomassalmi/touring-diary/internal/build"
	"github.com/tuomassalmi/touring-diary/internal/serve"
)

const usage = `usage: touring-diary <command> [flags]

commands:
  build   build the site from GPX, notes and media folders
  serve   preview a built site over local HTTP

run "touring-diary <command> -h" for flags.
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
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func runBuild(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o build.Options
	fs.StringVar(&o.GPXDir, "gpx", "", "folder of .gpx files")
	fs.StringVar(&o.NotesDir, "notes", "", "folder of .json note files")
	fs.StringVar(&o.MediaDir, "media", "", "folder of photos and videos")
	fs.StringVar(&o.OutDir, "out", "", "output folder")
	fs.StringVar(&o.Title, "title", "", "trip title")
	fs.StringVar(&o.TZ, "tz", "", "trip timezone (IANA name or +HH:MM); default: most common note/EXIF offset")
	fs.StringVar(&o.ConfigPath, "config", "", "trip config JSON")
	fs.StringVar(&o.OverridesPath, "overrides", "", "overrides JSON keyed by note timestamp or media filename")
	fs.DurationVar(&o.MaxGap, "max-gap", 0, "max time distance to a position anchor (default 12h)")
	fs.IntVar(&o.PhotoSize, "photo-size", 0, "long edge of converted photos and posters in px (default 1600)")
	fs.IntVar(&o.ThumbSize, "thumb-size", 0, "long edge of thumbnails in px (default 320)")
	fs.BoolVar(&o.LivePhotos, "live-photos", false, "convert and attach Live Photo motion videos")
	fs.BoolVar(&o.NoVideo, "no-video", false, "skip videos entirely")
	fs.BoolVar(&o.Force, "force", false, "ignore the conversion cache and rebuild all media")
	fs.BoolVar(&o.Verbose, "verbose", false, "verbose logging")
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
	dir := fs.String("dir", "dist", "built site folder to serve (may also be given as an argument)")
	addr := fs.String("addr", "127.0.0.1:8090", "listen address")
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
