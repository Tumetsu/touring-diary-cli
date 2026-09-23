# touring-diary — implementation spec

User documentation (install, usage, flags, file formats) is in [README.md](README.md).

A command-line generator that turns GPX tracks, timestamped notes and photos/videos
from a trip into a self-contained static website: a map with the routes, photos and
notes pinned where they happened, and a timeline for moving through the trip
chronologically.

The tool is generic. The Lapland 2026 data in this repo is the first input and the
acceptance test, not a special case.

## 1. Goals and non-goals

Goals

- One command builds a complete static site from three input folders.
- Every note and media file gets a map position, derived from its own GPS metadata
  when present, otherwise from the GPX tracks by timestamp.
- The site works offline apart from map tiles. No server, no build step for the
  reader, deployable to any static host.
- HEIC input is converted to JPEG. Videos are made browser-playable.
- Re-running the build is fast: media conversion is cached.

Non-goals (for now)

- Editing notes or media in the browser.
- Multi-trip site or trip index page. One build = one trip = one site.
- Route planning, elevation analysis beyond a simple per-day summary.

## 2. Inputs

### 2.1 GPX folder

- `*.gpx`, GPX 1.1. The sample files are Strava exports: one `<trk>`, one
  `<trkseg>`, `<trkpt>` with `lat`, `lon`, `<ele>`, `<time>` in UTC, and Garmin
  extensions (`gpxtpx:hr`, `gpxtpx:cad`, `gpxtpx:atemp`).
- The parser must accept multiple tracks and segments per file, missing `<ele>`,
  and missing extensions. Trackpoints without `<time>` are kept for drawing but
  ignored for placement.
- `<trk><name>` becomes the track title, `<trk><type>` the activity type
  (`cycling`, `hiking`, `walking`, ... any string; unknown types get a default
  style).
- `<wpt>` elements, if present, are imported as notes with the waypoint name and
  description.

### 2.2 Notes folder

- `*.json`, each a JSON array of `{ "timestamp": "<ISO 8601 with offset>", "text": "<string>" }`.
  Several files are concatenated. The sample file has 67 notes with `+03:00` offsets.
- Optional fields the tool understands if present: `lat`, `lon` (manual position,
  overrides placement), `title`.
- Text is displayed as plain text with line breaks preserved. No markdown in v1.

### 2.3 Media folder

- Images: `.heic`, `.heif`, `.jpg`, `.jpeg`, `.png`, `.webp`. Videos: `.mov`, `.mp4`.
  Matching is case-insensitive.
- Timestamp sources, in order: EXIF `DateTimeOriginal` + `OffsetTimeOriginal`;
  QuickTime `com.apple.quicktime.creationdate` (has offset); QuickTime
  `creation_time` (UTC). If none exists the file is reported and skipped unless an
  override (2.4) supplies a time. Filesystem mtime is never used: it reflects the
  copy date.
- Position sources: EXIF GPS IFD (`GPSLatitude/Ref`, `GPSLongitude/Ref`); QuickTime
  `com.apple.quicktime.location.ISO6709`. QuickTime `location.accuracy.horizontal`
  is read when present.
- EXIF `Orientation` is applied when converting so output JPEGs are upright. The
  HEIC decoder (`gen2brain/heic`) already returns upright pixels, so the rotate/flip
  step runs only for formats decoded by the standard library (JPEG, PNG, WebP).
  Applying it twice would rotate HEIC output wrongly.
- Live Photos: an image and a video with the same stem (`IMG_2231.HEIC` +
  `IMG_2231.MP4`) are one item. The image is the item; the video is attached as
  `livePhoto` and is only copied when `--live-photos` is given. It never becomes a
  separate timeline entry.

### 2.4 Optional overrides file

`--overrides overrides.json`, an object keyed by media filename or note timestamp:

```json
{
  "camphoto_758783491.jpg": { "time": "2026-06-27T12:30:00+03:00", "lat": 66.66, "lon": 28.36 },
  "2026-07-05T21:50:00+03:00": { "lat": 61.4981, "lon": 23.7608 }
}
```

Fields: `time`, `lat`, `lon`, `title`, `caption`, `skip: true`. Overrides win over
every automatic source. This is the escape hatch for files without metadata and for
the odd wrong GPS fix.

### 2.5 Trip config (optional)

`--config trip.json` for things that are not in the data:

```json
{
  "title": "Lapland 2026",
  "timezone": "Europe/Helsinki",
  "days": {
    "2026-06-26": { "title": "Train to Kemijärvi", "excludeFromFit": true },
    "2026-06-27": { "title": "Kemijärvi → Salla" }
  },
  "map": { "tiles": "osm" }
}
```

Every CLI flag has a config equivalent; CLI wins. `excludeFromFit: true` leaves a
day out of the map's initial view (`fitBounds` in 5.1), e.g. a travel day far from
the route; the day is otherwise shown as usual. A config date that matches no day
is warned about.

## 3. CLI

```
touring-diary build \
  --gpx gpx/ --notes events_log/ --media media/ \
  --out dist/ \
  [--title "Lapland 2026"] [--tz Europe/Helsinki] \
  [--config trip.json] [--overrides overrides.json] \
  [--max-gap 12h] [--photo-size 1600] [--thumb-size 320] \
  [--live-photos] [--no-video] [--force] [--verbose]
```

- `build` is the only subcommand in v1. `touring-diary serve dist/` (a tiny local
  HTTP server for previewing) is a convenience worth adding in the same milestone
  because `file://` breaks `fetch()` of `trip.json`.
- `--tz`: IANA zone used for display and day boundaries (`Local` is rejected: the
  site must not depend on the build machine). Default: the most common UTC offset
  found in notes, EXIF `OffsetTimeOriginal` and QuickTime `creationdate` (UTC-only
  `creation_time` does not count), as a fixed offset. All internal times are UTC.
- `--max-gap`: how far from the nearest position anchor (in time) an item may still
  be snapped. Default 12 h. Beyond that the item is placed on the timeline only.
- Exit code 0 with warnings printed for skipped files; non-zero only for unreadable
  input folders or a failed output write.
- Output of a run ends with a summary: tracks, notes, media counted by placement
  source, and a list of skipped files with reasons.

## 4. Processing pipeline

```
ingest gpx ─┐
ingest notes ├─► normalise times to UTC ─► build anchors ─► place items ─► group into days
ingest media ┘                                                                  │
                                                                    convert media (cached)
                                                                                │
                                                          write trip.json + copy web assets
```

### 4.1 Time handling

- Every input time becomes an aware UTC `datetime`. Notes and EXIF give an offset;
  GPX gives `Z`. If a source gives a naive time (EXIF without `OffsetTimeOriginal`),
  the trip timezone is assumed and the item is flagged `timeAssumed: true`.
- A "day" is a calendar date in the trip timezone. Day 1 is the first date that has
  any item (in the sample data: 2026-06-26, the travel day before the first track).
  Only dates with items or tracks become days; `index` counts them without gaps.
  When the first and last content are more than 60 days apart the build warns and
  names both ends, since that is usually a stray file or an unset camera clock.

### 4.2 Anchors

An anchor is a known (time, lat, lon) pair. Anchors come from:

1. every timed GPX trackpoint;
2. every media item with its own GPS position, unless its reported horizontal
   accuracy is worse than 500 m;
3. notes and media with manual `lat`/`lon` from the notes file or overrides.

Anchors are sorted by time into one array; placement queries use binary search.

### 4.3 Placement of an item without its own position

Given an item at time `t`, find the nearest anchors before (`a`) and after (`b`).

| Case | Result | `placement.source` |
|---|---|---|
| `a` and `b` belong to the same track segment | linear interpolation between them | `interpolated` |
| `a` and `b` exist, both within `max-gap` of `t`, and are less than 2 km apart | linear interpolation | `interpolated` |
| otherwise, the nearer of `a`/`b` in time is within `max-gap` | that anchor's position | `snapped` |
| no anchor within `max-gap` | no position; timeline only | `none` |

`placement.gapSeconds` records the time distance to the anchor(s) used, so the UI can
show "position approximate (2 h 15 min from nearest GPS fix)". Items with their own
metadata position get `source: "gps"`; manual ones get `manual`.

Why this works on the sample data: a note written in the tent at 23:50 lies between
the end of that day's track and the start of the next day's track. Both are the same
camp, under 2 km apart and within 12 h, so the note is interpolated onto the camp.
The 07:45 morning note lands there the same way. Notes and photos on 4 July in Oulu
are placed from the Oulu photos' own GPS acting as anchors. The 5 July train notes in
Tampere are more than 12 h from any anchor and correctly stay unplaced.

Sanity check for items with their own GPS: if the item falls inside a track's time
span and its GPS position is more than 1 km from the interpolated track position,
the tool logs a warning and keeps the item's own position. Overrides fix real
outliers.

### 4.4 Track processing

- Full-resolution points are used for anchors. For display each track is simplified
  with Ramer–Douglas–Peucker (tolerance 5 m) so the largest sample file (17k points)
  becomes a few thousand.
- Per-track stats: distance (haversine over full points), elapsed time, moving time
  (points with speed > 1 km/h), elevation gain (sum of positive deltas after a
  3-point smoothing), min/max elevation, average heart rate when present.
- Per-day stats are the sum over that day's tracks, plus a list of the track names.

### 4.5 Media conversion

- Images: decode (HEIC via `gen2brain/heic`, others via stdlib), apply orientation, write
  `media/<id>.jpg` at max `--photo-size` on the long edge (quality 85) and
  `media/<id>_thumb.jpg` at `--thumb-size` (quality 80). EXIF is not copied to the
  outputs, so published files carry no GPS. Width/height are stored in `trip.json`
  for layout.
- Videos: if `ffmpeg` is on `PATH`, transcode to H.264/AAC MP4 (`-crf 23
  -maxrate 8M -bufsize 16M`, `-movflags +faststart`, long edge capped at 1920 px,
  first video and first audio stream only, `-map_metadata -1` so no GPS or creation
  tags leak) and extract a poster JPEG at 1 s (`<id>_poster.jpg` at `--photo-size`,
  `<id>_thumb.jpg` at `--thumb-size` for markers and timeline tiles). iPhone MOV files are HEVC and often
  HLG HDR: when ffmpeg has `zscale`/`tonemap`, the output is tone-mapped to BT.709,
  otherwise it is encoded as-is and looks washed out. Without `ffmpeg` the video is
  copied unchanged and a warning is printed. `--no-video` skips videos entirely.
- Media with their own GPS but horizontal accuracy worse than 500 m keep their
  position (`source: "gps"`) but are not used as anchors. `accuracyM` is emitted
  so the UI can flag them.
- The media scan is recursive. Outputs in `<out>/media` that no longer correspond
  to a source file are removed on each build. Outputs of files that still exist but
  were not converted in this run (`--no-video`, no `ffprobe`, a `skip` override, a
  failed conversion) are kept.
- Cache: the build keeps `dist/.cache.json` mapping source path + size + mtime +
  conversion parameters to output filenames. Unchanged files are skipped. `--force`
  rebuilds everything.
- Output ids are `<sha1(source path)[:10]>` so renaming the trip folder does not
  invalidate the cache and filenames never collide.

## 5. Output

```
dist/
  index.html
  app.js             (ES module, no framework, no build step)
  style.css
  vendor/            leaflet.js, leaflet.css, leaflet.markercluster.*, images
  trip.json
  media/<id>.jpg, <id>_thumb.jpg (photos and video posters), <id>.mp4, <id>_poster.jpg
  .cache.json
```

### 5.1 `trip.json`

```jsonc
{
  "title": "Lapland 2026",
  "timezone": "Europe/Helsinki",
  "generatedAt": "2026-09-23T15:00:00Z",
  "bounds": [[65.0, 25.4], [66.8, 29.4]],      // everything on the map
  "fitBounds": [[65.0, 25.4], [66.8, 29.4]],   // initial view: without excludeFromFit days; = bounds if none remain
  "days": [
    {
      "date": "2026-06-27",
      "index": 1,
      "title": "Kemijärvi → Salla",            // config, else joined track names
      // "excludeFromFit": true,               // from config; omitted when false
      "stats": { "distanceKm": 91.7, "elevationGainM": 640, "movingTimeS": 18240, "trackIds": ["t1", "t2"] },
      "itemIds": ["n3f9a0c12de", "mab12cd34ef", "m51e0b77a09", "..."]    // chronological
    }
  ],
  "tracks": [
    {
      "id": "t1", "name": "Kemijärven jatkoyhteys", "type": "cycling",
      "start": "2026-06-27T06:00:34Z", "end": "2026-06-27T18:14:34Z",
      "stats": { "...": "..." },
      "points": [[66.72414, 27.40349, 163.6, 0], "..."]   // lat, lon, ele, seconds since start
    }
  ],
  "items": [
    { "id": "n3f9a0c12de", "kind": "note", "time": "2026-06-27T07:10:00Z", "text": "Lähtö Kemijärven kmarketilta",
      "lat": 66.72, "lon": 27.40, "placement": { "source": "interpolated", "gapSeconds": 0 } },
    { "id": "mab12cd34ef", "kind": "photo", "time": "...", "src": "media/ab12cd34ef.jpg", "thumb": "media/ab12cd34ef_thumb.jpg",
      "width": 4032, "height": 3024, "lat": 66.78, "lon": 28.80, "accuracyM": 5,
      "placement": { "source": "gps" }, "original": "IMG_1932.JPG" },   // optional fields are omitted, not null
    { "id": "m51e0b77a09", "kind": "video", "time": "...", "src": "media/....mp4", "poster": "media/...._poster.jpg",
      "thumb": "media/...._thumb.jpg",   // poster at thumb size; both omitted without ffmpeg
      "durationS": 12.4, "lat": null, "lon": null, "placement": { "source": "none", "gapSeconds": 51000 } }
  ]
}
```

Item ids stay the same between builds, so `#item=` links keep working when items
are added: media ids are `m` + the output id (4.5); note ids are `n` + the first 10
hex chars of `sha1(source file path relative to its folder + "\n" + timestamp as
written + "\n" + text)`. Duplicates get `-2`, `-3`, ... in chronological order. The
frontend treats ids as opaque strings.

Times in `trip.json` are UTC; the frontend formats them in `timezone`. `items` is
chronological overall, and each day's `itemIds` is a slice of it.

## 6. Frontend

Plain HTML/CSS/JS ES modules, Leaflet 1.9 with `Leaflet.markercluster`, vendored
into the output so the site has no CDN dependency. Tiles from
`https://tile.openstreetmap.org/{z}/{x}/{y}.png` with the required attribution, and
an OpenTopoMap layer as an alternative in the layer control (useful for fells).

Layout: map fills the viewport; a timeline panel on the left (desktop) or as a
bottom sheet (narrow screens). A header strip shows the trip title and per-day
chips.

### 6.1 Map

- One polyline per track, coloured by activity type (cycling, hiking/walking, other).
  Clicking a track shows its name and stats.
- Photo markers: circular thumbnail markers, clustered at low zoom. Video markers
  use the poster with a play glyph. Note markers use a distinct icon.
- Items with `placement.source` of `snapped` get a dashed marker outline and a
  tooltip with the approximation text; `none` items do not appear on the map.
- Selecting a day (header chip or timeline) fits the map to that day's tracks and
  items and dims the others. "All" resets.
- Position marker: when an item is selected, a highlighted marker shows where it is
  and the map pans to it without changing zoom if it is already in view.

### 6.2 Timeline

- Day sections in order, each with date, title, stats line. Inside a day, items are
  grouped by hour (`10:00`, `11:00`, ...) and listed chronologically: note text
  (full), photo thumbnails in a row, video posters, and small "ride started / ride
  ended" rows from the tracks.
- Clicking any entry selects it: the map focuses it, and photos/videos open in a
  lightbox. The lightbox has prev/next in chronological order across the whole trip
  and shows the time, day, and approximation note.
- Scrolling the timeline updates the active day chip; selecting a map marker scrolls
  the timeline to that entry. Both directions use the item id.
- Keyboard: `←`/`→` for prev/next item, `Esc` closes the lightbox.
- URL hash `#item=<id>` deep-links to an item; `#day=<n>` to a day.

### 6.3 Later (not v1)

- Time scrubber that moves a "you are here" dot along the tracks and hides items
  after the chosen moment.
- Live Photo playback on press.
- Elevation profile on narrow screens (the bottom sheet occupies that space).

### 6.4 Elevation profile

- A strip about 160 px tall docked under the map on desktop, collapsible with a
  ▲/▼ button; the state is remembered in `localStorage`. Hidden below 768 px.
- Shows the selected day's tracks end to end in chronological order ("All": the
  whole trip) against distance in km, computed in the browser with haversine
  once per track at load. A thin divider and the track name mark each track
  boundary; line and light fill are coloured by activity type as on the map.
  Elevation axis with 3-4 gridlines. Inline SVG sized to the panel, re-rendered
  on resize; drawn with at most the first, min and max point per pixel column.
- Hovering (or dragging on touch) shows a cursor, a readout (elevation, km,
  local time, track name) and a position dot on the map at that track point,
  without panning. Leaving the strip hides them.
- Photos, videos and notes whose time falls within a track's time range are
  ticks under the plot, at the point nearest in time. Hovering a tick shows the
  item; clicking selects it (media open in the lightbox).

## 7. Technology choices

| Decision | Choice | Why |
|---|---|---|
| Language | Go ≥ 1.22, single static binary `touring-diary`, web assets embedded with `embed` | One file to copy anywhere, cross-compiles for Linux/macOS/Windows, no runtime to install. HEIC has a cgo-free path (below). |
| GPX parsing | `github.com/tkrajina/gpxgo` | Multi-track/segment files, extensions, same author as `gpxpy`. |
| HEIC decoding | `github.com/gen2brain/heic` v0.7.x | Embedded WASM decoder (a Rust HEIC crate run through wazero), so no cgo and no system libraries. Loads native libheif through purego automatically when it is installed, about 15x faster. Returns pixels already upright. MIT. Verified in the milestone 0 spike (`spike/heic/`). |
| EXIF | `github.com/evanoberholster/imagemeta` v1.1.x for both HEIC and JPEG | Pure Go. `OriginalDate()` returns the time with `OffsetTimeOriginal` applied; `GPS.Latitude()/Longitude()` give signed decimal degrees (0,0 means absent); `HPositioningError()` for accuracy; `IFD0.Orientation` for the JPEG rotate step. `gen2brain/heic.DecodeExif` lacks the offset field, so it is used only for pixels. |
| Static binary | default build links dynamically (purego needs `dlopen` for libheif); `-tags nodynamic` gives a fully static ~12 MB binary that always uses WASM | Release builds use `nodynamic` for portability. Developers on machines with libheif can use the default build for speed. |
| Image resize / JPEG | `golang.org/x/image/draw` (CatmullRom) + stdlib `image/jpeg` | No dependency beyond `x/image`. Orientation is applied with plain rotate/flip on the decoded image. |
| Video | `ffmpeg`/`ffprobe` as optional system dependencies | Only practical way to read QuickTime metadata and transcode HEVC. Same in any language. |
| CLI | stdlib `flag` with a `build`/`serve` subcommand switch | Few flags; avoids a dependency. |
| Map | Leaflet 1.9 + markercluster, raster OpenStreetMap and OpenTopoMap tiles | Free tiles without API keys. MapLibre would need a vector tile provider, which means a key or self-hosting. |
| Frontend tooling | none | Vendored libs, ES modules. The binary writes static files; there is nothing to compile. |
| Tests | `go test` | Unit tests for placement, time parsing, EXIF/GPS parsing, stats; one end-to-end build against a tiny fixture set in `testdata/`. |

Performance note: WASM HEIC decoding is slower than native, on the order of one to
two seconds per 12-megapixel photo. The first build of this trip's 291 HEIC files
may take several minutes without native libheif. Conversions run in a worker pool
sized to CPU count, and the cache (4.5) makes later builds take seconds.

Python was the alternative (`pillow-heif`, `gpxpy`, `Pillow`). It is equally capable
and has native-speed HEIC decoding, but needs a Python environment on every machine
that runs the tool. If the milestone 0 spike fails, the fallback is Python with the
same spec.

Repository layout:

```
go.mod
cmd/touring-diary/main.go   subcommand dispatch
internal/
  build/       pipeline orchestration, summary, cache
  model/       Track, Item, Day, Placement structs and JSON encoding
  timeutil/    offset inference, tz conversion, day bucketing
  gpx/         gpxgo → Track, stats, simplification
  notes/       JSON notes → Item
  media/       EXIF/QuickTime metadata, Live Photo pairing
  convert/     image/video conversion, worker pool, cache
  placement/   anchors, interpolation, snapping
  serve/       local preview server
web/           index.html, app.js, style.css, vendor/  (embedded via embed.FS)
testdata/
gpx/ events_log/ media/   (local trip data, gitignored)
```

## 8. Milestones

0. **HEIC spike (time-boxed to an hour).** A throwaway `main.go` that decodes three
   sample HEIC files with `gen2brain/heic`, prints orientation, `DateTimeOriginal`,
   offset and GPS, writes a resized JPEG, and reports decode time with and without
   native libheif. Pass criteria: correct time and GPS for all three, upright
   output, under 3 s per file in WASM mode. Failing this, switch to Python.
   Result 2026-09-23: PASS. Worst case 1.5 s WASM decode for a 24 MP HEIC, 91 ms native.
1. **Data core.** GPX + notes ingest, time normalisation, anchors, placement,
   `trip.json` written. Tests for placement with synthetic tracks. Run on the sample
   data and inspect the placement summary. No media yet.
2. **Site v1.** `index.html`/`app.js`/`style.css`, Leaflet map with tracks and note
   markers, timeline with days and notes, selection sync both ways, `serve`
   subcommand.
3. **Photos.** EXIF metadata, HEIC → JPEG, thumbnails, cache, photo markers with
   clustering, lightbox with prev/next.
4. **Videos and Live Photos.** ffprobe metadata, transcode, poster, video markers
   and playback in the lightbox, Live Photo pairing.
5. **Polish.** Per-day stats, OpenTopoMap layer, mobile bottom sheet, deep links,
   keyboard navigation, overrides and config files, README.

Each milestone ends with a build of the sample data that is checked in a browser.

## 9. Open questions

- Should notes support minimal markdown (links, emphasis)? Default: no, plain text.
- Should the site offer a "download original" link? Default: no. Originals are
  1.2 GB for this trip and would carry GPS EXIF.
- Live Photo videos: attach and play, or ignore? Default: ignore in v1
  (`--live-photos` to attach).
- Day titles: derive from track names by default, or leave blank until configured?
  Default: joined track names, e.g. "Kemijärven jatkoyhteys · Taivaantavoittelijan kierros".
