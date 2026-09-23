# touring-diary

A command-line tool that turns the GPX tracks, timestamped notes and photos/videos
from a trip into a static website: a map with the routes, and with every note and
photo pinned where it happened, next to a timeline for going through the trip day
by day. The output is plain HTML, CSS, JavaScript and JPEG/MP4 files that any
static host can serve.

![Screenshot](docs/screenshot.png) <!-- placeholder: add a screenshot -->

The design and the rules the tool follows are in [SPEC.md](SPEC.md).

## Requirements

- **Go 1.27** or newer, to build from source.
- **ffmpeg and ffprobe** (optional), on `PATH`. `ffprobe` reads video metadata;
  without it videos are skipped (with a warning). `ffmpeg` transcodes videos to
  browser-playable H.264 MP4 and extracts a poster frame; without it videos are
  copied unchanged, have no poster, and iPhone HEVC videos will not play in most
  browsers. A file that makes `ffprobe` run longer than 30 s, or `ffmpeg` longer
  than 10 min + 20 s per second of video (at most 60 min), is skipped with a reason.
- **libheif** (optional), the native library. The default (dynamic) build loads
  it at run time when it is installed and decodes HEIC about 15 times faster. Without
  it the built-in WebAssembly decoder is used: it needs no system libraries but
  takes about 1–2 s per 12-megapixel photo. Release binaries always use the
  WebAssembly decoder.

## Install

From a checkout:

```sh
go install ./cmd/touring-diary     # into $(go env GOPATH)/bin
# or
make build                         # into bin/touring-diary
```

Or download a release binary for your platform (`touring-diary` or
`touring-diary.exe`, statically linked) and put it on your `PATH`.
`touring-diary version` prints the version it was built from.

## Inputs

The tool reads three folders. Any of them can be left out, but at least one is
needed.

### Notes (`--notes`)

Every `*.json` file in the folder holds a JSON array of notes. Files are
concatenated.

```json
[
  { "timestamp": "2026-06-27T10:10:00+03:00", "text": "Left from the shop in Kemijärvi" },
  { "timestamp": "2026-06-27T21:15:00+03:00", "title": "Camp", "text": "Tent up.\nMosquitoes.",
    "lat": 66.83, "lon": 28.95 }
]
```

- `timestamp`: ISO 8601. Include the UTC offset (`+03:00`, `+0300` or `Z`). A time without an offset is read
  in the trip timezone and marked "Time assumed" on the site.
- `text`: shown as plain text. Line breaks are kept; there is no markdown.
- `title`, `lat`, `lon`: optional. `lat`/`lon` pin the note to that position
  (placement `manual`).

### GPX tracks (`--gpx`)

Every `*.gpx` file (GPX 1.1), for example Strava or Garmin exports. Files may hold
several tracks and segments. `<ele>` and extensions are optional. `<trk><name>` is
the track title and `<trk><type>` its activity type: `cycling` is drawn in one
colour, `hiking`/`walking`/`running` in another, anything else in a third.
Trackpoints without `<time>` are drawn but not used for placement. `<wpt>` elements
become notes.

### Media (`--media`)

The folder is scanned recursively for `.heic`, `.heif`, `.jpg`, `.jpeg`, `.png`,
`.webp`, `.mov` and `.mp4` (any case).

- **Time** comes from EXIF `DateTimeOriginal` + `OffsetTimeOriginal` (or
  `OffsetTime` when that is missing) for images,
  and from QuickTime `com.apple.quicktime.creationdate` or `creation_time` for
  videos. File modification times are never used, because copying files changes
  them. A file with no time in its metadata is skipped and listed in the summary,
  unless an override gives it a `time`.
- **Position** comes from EXIF GPS or QuickTime `ISO6709` location. A reported
  accuracy worse than 500 m is shown on the site as approximate.
- **Orientation** from EXIF is applied, so the output is upright.
- **Live Photos**: a photo and a video with the same name in the same folder
  (`IMG_2231.HEIC` + `IMG_2231.MOV`) are one item, the photo. The video is ignored
  unless `--live-photos` is given; then it is probed (so HDR clips are tone-mapped),
  converted and attached to the photo as
  `livePhoto` in `trip.json`. The site does not play it yet.

Published files carry no metadata: GPS and camera EXIF are not copied into the
output JPEGs and MP4s.

## Usage

With this repository's layout (`gpx/`, `events_log/`, `media/`):

```sh
touring-diary build --gpx gpx --notes events_log --media media --out dist --title "Lapland 2026"
touring-diary serve dist            # preview at http://127.0.0.1:8090/
```

or `make demo` and `make serve`. The site must be opened through a web server:
from `file://` the browser refuses to load `trip.json`.

A build prints warnings for skipped files and ends with a summary: tracks, notes and
media counted by how each was placed, conversion counts, and every skipped file
with the reason. It exits with 0 even when files were skipped, and with non-zero
only when an input folder cannot be read or the output cannot be written.

`touring-diary` with no arguments prints the commands and main flags.
`touring-diary build -h` lists every flag with its default.

### Flags (`build`)

| Flag | Default | Meaning |
|---|---|---|
| `--gpx DIR` | none | folder of `.gpx` files |
| `--notes DIR` | none | folder of `.json` note files |
| `--media DIR` | none | folder of photos and videos, scanned recursively |
| `--out DIR` | none, required | output folder |
| `--title TEXT` | `Trip` | trip title |
| `--tz ZONE` | most common UTC offset in the notes, EXIF and QuickTime metadata | timezone for display and day boundaries: an IANA name (`Europe/Helsinki`) or an offset (`+03:00`); not `Local` |
| `--config FILE` | none | trip config file (below) |
| `--overrides FILE` | none | overrides file (below) |
| `--max-gap DURATION` | `12h` | how far in time an item may be from the nearest position fix and still be placed |
| `--photo-size PX` | `1600` | long edge of converted photos and video posters |
| `--thumb-size PX` | `320` | long edge of thumbnails |
| `--live-photos` | off | convert and attach Live Photo videos |
| `--no-video` | off | skip videos entirely |
| `--force` | off | ignore the conversion cache and convert all media again |
| `--verbose` | off | log every media file and extra detail |

### Flags (`serve`)

`touring-diary serve [--addr 127.0.0.1:8090] [DIR]` serves `DIR` (default `dist`).
It is meant for previewing on your own machine, not for hosting.

### Config file (`--config`)

Optional. Holds the things that are not in the data (day titles) and an
equivalent of every build flag except `--config`, `--force` and `--verbose`. A flag
given on the command line wins over the file.

```json
{
  "title": "Lapland 2026",
  "timezone": "Europe/Helsinki",
  "gpx": "gpx", "notes": "events_log", "media": "media", "out": "dist",
  "overrides": "overrides.json",
  "maxGap": "12h",
  "photoSize": 1600,
  "thumbSize": 320,
  "livePhotos": false,
  "noVideo": false,
  "days": {
    "2026-06-26": { "excludeFromFit": true },
    "2026-06-27": { "title": "Kemijärvi → Salla" }
  }
}
```

`days` sets day titles, keyed by date in the trip timezone. Days without a title
get their tracks' names joined with " · ". `excludeFromFit: true` leaves a day (say,
the train ride to the start) out of the map's initial view and the "All" view; the
day is still drawn and listed, and selecting it zooms to it. Relative paths in the config file
(`gpx`, `notes`, `media`, `out`, `overrides`) are resolved from the folder the
config file is in; paths given as flags are resolved from the directory you run
the command in.

### Overrides file (`--overrides`)

Optional. An object keyed by a media file name (or its path relative to the media
folder) or by a note timestamp. Overrides win over everything read from the files.

```json
{
  "camphoto_758783491.jpg": { "time": "2026-06-27T12:30:00+03:00", "lat": 66.66, "lon": 28.36 },
  "IMG_1884.HEIC": { "lat": 66.51, "lon": 29.12 },
  "2026-07-05T21:50:00+03:00": { "lat": 61.4981, "lon": 23.7608, "title": "Tampere" },
  "2026-07-10T12:00:00+03:00": { "skip": true }
}
```

Fields: `time`, `lat`, `lon` (both needed; a position with only one of them, or
outside ±90/±180, is ignored with a warning), `title`, `caption`, `skip`. A bare
file name that occurs in more than one folder is ambiguous and is not applied (the
build warns); use the relative path, e.g. `day2/IMG_0001.HEIC`. A note key
matches the note with the same instant, whatever offset either is written with.
Keys that match nothing are reported as warnings. Use overrides for files without
a timestamp and for the odd wrong GPS fix; the build warns about photos whose GPS
position is more than 1 km off the track recorded at the same time.

## How placement works

Every timed trackpoint, every photo or video with a usable GPS position and every
manual position is a known point in time and space. An item without its own
position is placed between the known points just before and just after it in time.
If both belong to the same track, or are close together, the position is
interpolated. Otherwise the item is put at the nearer one, if that is within
`--max-gap`, and is marked approximate on the site ("2h 15min from nearest GPS
fix"). An item with no known point within `--max-gap` stays on the timeline only.
The details are in [SPEC.md section 4.3](SPEC.md#43-placement-of-an-item-without-its-own-position).

## Cache and `--force`

Converting HEIC photos and transcoding videos is slow, so results are cached in
`<out>/.cache.json`. A file is converted again only when its size or modification
time changes, or the conversion settings do (`--photo-size`, `--thumb-size`, the
ffmpeg arguments, whether ffmpeg and its tone-mapping filters are available). A rebuild with nothing changed
takes about a second. Outputs of media that no longer exist are deleted.

Use `--force` when the cache cannot notice a change: for example after upgrading
ffmpeg (same arguments, different encoder), or after replacing a file with one of
the same size and modification time. Deleting `.cache.json` has the same effect.

## Output

```
dist/
  index.html, app.js, style.css    the site (no build step, no framework)
  vendor/                          Leaflet and Leaflet.markercluster
  trip.json                        all data the site shows
  media/<id>.jpg                   photo, long edge --photo-size
  media/<id>_thumb.jpg             thumbnail of a photo or of a video poster
  media/<id>.mp4                   video, H.264/AAC, long edge at most 1920 px
  media/<id>_poster.jpg            video poster frame
  .cache.json                      conversion cache (need not be published)
```

`<id>` is derived from the source file's path inside the media folder, so output
names stay stable between builds. The format of `trip.json` is described in
[SPEC.md section 5.1](SPEC.md#51-tripjson).

## Deployment

Copy the output folder to any static host (GitHub Pages, Netlify, an S3 bucket, a
web server directory). `.cache.json` does not need to be uploaded. The site works
from a subdirectory; all paths are relative.

Map tiles are loaded from the public OpenStreetMap and OpenTopoMap servers. They
are free but not unlimited: for a site with more than light personal traffic, read
the [OpenStreetMap tile usage policy](https://operations.osmfoundation.org/policies/tiles/)
and consider a commercial or self-hosted tile provider. Photos and notes are public
to anyone with the link, and positions show where you slept.

## Development

```sh
make test      # go test ./...
make lint      # gofmt -l and go vet
make build     # bin/touring-diary (dynamic build, uses libheif when installed)
make release   # static binaries for linux/amd64, linux/arm64, darwin/arm64,
               # darwin/amd64 and windows/amd64 in bin/<os>-<arch>/
make demo      # build, then build the sample trip into dist/
make serve     # build, then serve dist/
```

`make release PLATFORMS=linux/amd64` builds one platform. Release builds use
`CGO_ENABLED=0 -tags nodynamic` and stamp the version from `git describe`.

Packages:

| Path | Contents |
|---|---|
| `cmd/touring-diary` | command-line entry point and subcommands |
| `internal/build` | the pipeline: config, overrides, ingest, placement, days, `trip.json`, summary |
| `internal/gpx` | GPX parsing, track statistics, simplification |
| `internal/notes` | notes JSON parsing |
| `internal/media` | media scan, EXIF and QuickTime metadata, Live Photo pairing |
| `internal/convert` | image and video conversion, worker pool, cache |
| `internal/placement` | anchors, interpolation and snapping |
| `internal/timeutil` | offset inference, timezones, day bucketing |
| `internal/model` | the `trip.json` types |
| `internal/serve` | the preview server |
| `web/` | the site's HTML, CSS, JavaScript and vendored libraries, embedded in the binary |
| `testdata/` | a small fixture trip used by the tests |

## Known limitations

- One build makes one trip. There is no index of several trips.
- Notes are plain text; no links or formatting.
- Live Photo videos can be attached but the site does not play them.
- The `map` section of the config file is accepted but not used yet; the site
  always offers OpenStreetMap and OpenTopoMap.
- Videos need `ffprobe` to be included at all. Without `ffmpeg` they are not
  transcoded, iPhone HEVC videos will not play in most browsers, and there are no
  poster frames or thumbnails.
- HDR (HLG) iPhone videos look washed out unless ffmpeg has the `zscale` and
  `tonemap` filters.
- HEIC decoding without native libheif is slow on the first build (minutes for a
  few hundred photos); later builds use the cache.
- The elevation profile is desktop only (hidden below 768 px width), and only
  items taken during a track's time range get a tick on it.
- The site needs JavaScript and a modern browser (ES modules, `inert`, CSS `:has`).
