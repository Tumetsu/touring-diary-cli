// touring-diary frontend: map + timeline for one trip (SPEC.md section 6).
// Plain ES module; Leaflet and Leaflet.markercluster are loaded as globals
// from vendor/ by index.html.

const L = window.L;

const state = {
  trip: null,
  itemsById: new Map(),
  itemIndex: new Map(), // id -> index in trip.items (chronological)
  dayOfItem: new Map(), // id -> day index (1-based)
  tracksById: new Map(),
  dayByIndex: new Map(),
  media: [], // photos and videos, chronological
  mediaIndex: new Map(), // id -> index in media
  lightboxId: null, // item shown in the lightbox, null when closed
  selectedId: null,
  selectedDay: null, // null = all days
  fmt: null,
};

const els = {
  title: document.getElementById('trip-title'),
  chips: document.getElementById('day-chips'),
  timeline: document.getElementById('timeline'),
  scroll: document.getElementById('timeline-scroll'),
  handle: document.getElementById('sheet-handle'),
  map: document.getElementById('map'),
  summary: document.getElementById('trip-summary'),
  layout: document.querySelector('.layout'),
  topbar: document.querySelector('.topbar'),
  lb: document.getElementById('lightbox'),
  lbStage: document.getElementById('lb-stage'),
  lbClose: document.getElementById('lb-close'),
  lbPrev: document.getElementById('lb-prev'),
  lbNext: document.getElementById('lb-next'),
  lbWhen: document.getElementById('lb-when'),
  lbCaption: document.getElementById('lb-caption'),
  lbApprox: document.getElementById('lb-approx'),
  lbCount: document.getElementById('lb-count'),
  lbFull: document.getElementById('lb-full'),
  lbMini: document.getElementById('lb-mini'),
  profile: document.getElementById('profile'),
  profileBody: document.getElementById('profile-body'),
  profileSvg: document.getElementById('profile-svg'),
  profileSummary: document.getElementById('profile-summary'),
  profileLegend: document.getElementById('profile-legend'),
  profileToggle: document.getElementById('profile-toggle'),
  profileReadout: document.getElementById('profile-readout'),
  profileEmpty: document.getElementById('profile-empty'),
};

const narrowQuery = window.matchMedia('(max-width: 767px)');

// ---------------------------------------------------------------- time

const OFFSET_RE = /^([+-])(\d{2}):?(\d{2})$/;

// makeFormatters returns date/time formatting functions for the trip zone.
// The zone is an IANA name or a fixed offset like "+03:00". Recent browsers
// accept offsets in Intl.DateTimeFormat directly; older ones throw, in
// which case we shift the instant by the offset and format in UTC.
function makeFormatters(timezone) {
  const locale = 'en-GB';
  const opts = {
    time: { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' },
    hour: { hour: '2-digit', hourCycle: 'h23' },
    date: { weekday: 'short', day: 'numeric', month: 'short' },
    longDate: { weekday: 'long', day: 'numeric', month: 'long', year: 'numeric' },
  };
  let tz = timezone || 'UTC';
  let shiftMs = 0;
  try {
    new Intl.DateTimeFormat(locale, { timeZone: tz });
  } catch {
    const m = OFFSET_RE.exec(tz);
    if (m) {
      shiftMs = (m[1] === '-' ? -1 : 1) * (Number(m[2]) * 60 + Number(m[3])) * 60000;
    } else {
      console.warn(`unknown timezone ${tz}; showing UTC`);
    }
    tz = 'UTC';
  }
  const make = (o) => {
    const f = new Intl.DateTimeFormat(locale, { ...o, timeZone: tz });
    return (d) => f.format(new Date(new Date(d).getTime() + shiftMs));
  };
  // Day dates ("2026-06-27") are calendar dates, not instants: format at noon UTC.
  const dateOnly = (o) => {
    const f = new Intl.DateTimeFormat(locale, { ...o, timeZone: 'UTC' });
    return (s) => f.format(new Date(`${s}T12:00:00Z`));
  };
  return {
    time: make(opts.time),
    hour: make(opts.hour),
    dayShort: dateOnly(opts.date),
    dayLong: dateOnly(opts.longDate),
    mode: shiftMs ? 'offset-fallback' : 'intl',
  };
}

function formatDuration(seconds) {
  const total = Math.round((seconds || 0) / 60);
  const h = Math.floor(total / 60);
  const m = total % 60;
  if (h === 0) return `${m} min`;
  return `${h} h ${String(m).padStart(2, '0')} min`;
}

// formatGap renders the approximation distance in the spec's short form,
// e.g. "2h 15min".
function formatGap(seconds) {
  if (seconds < 60) return 'under 1 min';
  const total = Math.round(seconds / 60);
  const h = Math.floor(total / 60);
  const m = total % 60;
  if (h === 0) return `${m}min`;
  return m ? `${h}h ${m}min` : `${h}h`;
}

const formatKm = (km) => `${km.toFixed(1)} km`;

// formatClock renders a video length as m:ss.
function formatClock(seconds) {
  const s = Math.max(1, Math.round(seconds || 0));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
}

const plural = (n, word) => `${n} ${word}${n === 1 ? '' : 's'}`;

// Positions reported with worse accuracy than this are shown as approximate.
const POOR_ACCURACY_M = 500;

// roundMetres keeps two significant figures: 5607.6 -> 5600.
function roundMetres(m) {
  if (m < 100) return Math.round(m);
  const p = 10 ** (Math.floor(Math.log10(m)) - 1);
  return Math.round(m / p) * p;
}

function approxText(item) {
  const p = item.placement || {};
  if (p.source === 'snapped') {
    return `Position approximate (${formatGap(p.gapSeconds ?? 0)} from nearest GPS fix)`;
  }
  if (item.accuracyM > POOR_ACCURACY_M) {
    return `GPS accuracy about ${roundMetres(item.accuracyM).toLocaleString('en-GB')} m`;
  }
  return '';
}

const isMedia = (item) => item.kind === 'photo' || item.kind === 'video';
const onMap = (item) => item.lat != null && item.lon != null && item.placement?.source !== 'none';

// ---------------------------------------------------------------- DOM helpers

function h(tag, attrs = {}, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v == null || v === false) continue;
    if (k === 'class') el.className = v;
    else if (k === 'dataset') Object.assign(el.dataset, v);
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v === true ? '' : v);
  }
  for (const c of children.flat()) {
    if (c == null || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}

// ---------------------------------------------------------------- tracks

function trackCategory(type) {
  const t = (type || '').toLowerCase();
  if (t === 'cycling' || t === 'biking' || t === 'ride') return 'cycling';
  if (t === 'hiking' || t === 'walking' || t === 'running') return 'hiking';
  return 'other';
}

function cssVar(name) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

function trackColor(type) {
  return cssVar(`--track-${trackCategory(type)}`) || '#666';
}

function trackVerb(type) {
  switch (trackCategory(type)) {
    case 'cycling': return 'Ride';
    case 'hiking': return (type || '').toLowerCase() === 'walking' ? 'Walk' : 'Hike';
    default: return 'Activity';
  }
}

// trackSegments splits the flat point list at segmentStarts.
function trackSegments(track) {
  const pts = track.points.map((p) => [p[0], p[1]]);
  const starts = [0, ...(track.segmentStarts || []), pts.length];
  const segs = [];
  for (let i = 0; i < starts.length - 1; i++) {
    const s = pts.slice(starts[i], starts[i + 1]);
    if (s.length > 1) segs.push(s);
  }
  return segs;
}

function trackPopup(track) {
  const s = track.stats || {};
  const row = (k, v) => [h('dt', {}, k), h('dd', {}, v)];
  return h('div', { class: 'track-popup' },
    h('h3', {}, track.name || 'Untitled track'),
    h('p', { class: 'type' }, track.type || 'activity'),
    h('dl', {},
      row('Distance', formatKm(s.distanceKm || 0)),
      row('Elevation gain', `${Math.round(s.elevationGainM || 0)} m`),
      row('Moving time', formatDuration(s.movingTimeS || 0)),
      track.start ? row('Time', `${state.fmt.time(track.start)}–${state.fmt.time(track.end)}`) : null,
    ),
  );
}

// ---------------------------------------------------------------- map

let map;
const trackLayers = new Map(); // track id -> { line, casing }
const markers = new Map(); // item id -> L.Marker
let selectionRing = null;
let layers; // per-kind marker containers

const NOTE_SVG = '<svg viewBox="0 0 16 16" aria-hidden="true"><path fill="currentColor" d="M3 1.5h7l3 3v10H3zM4.5 3v10h7V5.2L9.3 3zM6 7h4v1.2H6zm0 2.4h4v1.2H6z"/></svg>';

const PLAY_SVG = '<svg viewBox="0 0 24 24" aria-hidden="true"><path fill="currentColor" d="M8 5.5v13l10.5-6.5z"/></svg>';

const MEDIA_ICON = 40;

function mediaMarker(item, image, extra = '') {
  const approx = approxText(item) ? ' is-approx' : '';
  const bg = image ? ` style="background-image:url('${encodeURI(image)}')"` : '';
  return L.marker([item.lat, item.lon], {
    icon: L.divIcon({
      className: '',
      html: `<div class="media-marker${approx}"${bg}>${extra}</div>`,
      iconSize: [MEDIA_ICON, MEDIA_ICON],
      iconAnchor: [MEDIA_ICON / 2, MEDIA_ICON / 2],
    }),
    keyboard: false,
    riseOnHover: true,
    itemId: item.id,
  });
}

// Marker factory per item kind. Photos and videos go into the clustered
// layer (layerForKind).
const markerFactories = {
  photo(item) {
    return mediaMarker(item, item.thumb || item.src);
  },
  video(item) {
    return mediaMarker(item, item.thumb || item.poster, `<span class="play-glyph">${PLAY_SVG}</span>`);
  },
  note(item) {
    const approx = approxText(item) ? ' is-snapped' : '';
    return L.marker([item.lat, item.lon], {
      icon: L.divIcon({
        className: '',
        html: `<div class="note-marker${approx}">${NOTE_SVG}</div>`,
        iconSize: [26, 26],
        iconAnchor: [13, 13],
      }),
      keyboard: false,
      riseOnHover: true,
    });
  },
  default(item) {
    return L.marker([item.lat, item.lon], {
      icon: L.divIcon({ className: '', html: '<div class="plain-marker"></div>', iconSize: [14, 14], iconAnchor: [7, 7] }),
      keyboard: false,
    });
  },
};

function layerForKind(kind) {
  return kind === 'photo' || kind === 'video' ? layers.clustered : layers.plain;
}

function itemTooltip(item) {
  const text = isMedia(item)
    ? item.title || item.caption || (item.kind === 'video' ? `Video · ${formatClock(item.durationS)}` : '')
    : item.title || item.text || item.original || '';
  const short = text.length > 90 ? `${text.slice(0, 88).trimEnd()}…` : text;
  const approx = approxText(item);
  return `<span class="tip-time">${state.fmt.time(item.time)}</span> ${escapeHTML(short)}` +
    (approx ? `<span class="tip-approx">${escapeHTML(approx)}</span>` : '');
}

function escapeHTML(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

function initMap(trip) {
  map = L.map(els.map, { zoomControl: true, preferCanvas: false });

  const osm = L.tileLayer('https://tile.openstreetmap.org/{z}/{x}/{y}.png', {
    maxZoom: 19,
    attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors',
  });
  const topo = L.tileLayer('https://{s}.tile.opentopomap.org/{z}/{x}/{y}.png', {
    maxZoom: 17,
    subdomains: 'abc',
    attribution: 'Map data: &copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors, ' +
      '<a href="http://viewfinderpanoramas.org">SRTM</a> | Map style: &copy; <a href="https://opentopomap.org">OpenTopoMap</a> ' +
      '(<a href="https://creativecommons.org/licenses/by-sa/3.0/">CC-BY-SA</a>)',
  });
  osm.addTo(map);
  L.control.layers({ OpenStreetMap: osm, OpenTopoMap: topo }, null, { position: 'topright' }).addTo(map);
  L.control.scale({ imperial: false, position: 'bottomright' }).addTo(map);

  layers = {
    tracks: L.layerGroup().addTo(map),
    plain: L.layerGroup().addTo(map),
    clustered: L.markerClusterGroup({
      showCoverageOnHover: false,
      maxClusterRadius: 48,
      spiderfyDistanceMultiplier: 1.6,
      chunkedLoading: true,
      iconCreateFunction: clusterIcon,
    }).addTo(map),
  };

  for (const track of trip.tracks) {
    const segs = trackSegments(track);
    if (!segs.length) continue;
    const casing = L.polyline(segs, { color: cssVar('--track-casing') || '#fff', weight: 7, opacity: 0.85, interactive: false });
    const line = L.polyline(segs, { color: trackColor(track.type), weight: 4, opacity: 0.95, lineJoin: 'round' });
    line.bindPopup(() => trackPopup(track), { maxWidth: 280 });
    line.on('mouseover', () => line.setStyle({ weight: 6 }));
    line.on('mouseout', () => line.setStyle({ weight: 4 }));
    casing.addTo(layers.tracks);
    line.addTo(layers.tracks);
    trackLayers.set(track.id, { line, casing });
  }

  const clustered = [];
  for (const item of trip.items) {
    if (!onMap(item)) continue;
    const factory = markerFactories[item.kind] || markerFactories.default;
    const m = factory(item);
    const media = isMedia(item);
    m.bindTooltip(() => itemTooltip(item), { className: 'item-tip', direction: 'top', offset: [0, media ? -22 : -14] });
    m.on('click', () => {
      selectItem(item.id, { source: 'map' });
      if (media) openLightbox(item.id);
    });
    // Clustered markers get a fresh element each time they are unclustered.
    m.on('add', () => {
      if (state.selectedId === item.id) setMarkerSelected(item.id, true);
      setMarkerDimmed(m, isDimmed(item.id));
    });
    if (layerForKind(item.kind) === layers.clustered) clustered.push(m);
    else m.addTo(layers.plain);
    markers.set(item.id, m);
  }
  layers.clustered.addLayers(clustered); // bulk add is much faster than one by one

  fitTrip();
}

// clusterIcon shows the count over the thumbnail of the earliest child.
// Clusters with nothing from the selected day are dimmed.
function clusterIcon(cluster) {
  const children = cluster.getAllChildMarkers();
  let first = null;
  let inDay = !state.selectedDay;
  for (const m of children) {
    const id = m.options.itemId;
    if (!first || state.itemIndex.get(id) < state.itemIndex.get(first)) first = id;
    if (!inDay && state.dayOfItem.get(id) === state.selectedDay) inDay = true;
  }
  const item = state.itemsById.get(first);
  const img = item && (item.thumb || item.poster);
  const n = children.length;
  const size = n < 10 ? 46 : n < 50 ? 52 : 58;
  const bg = img ? ` style="background-image:url('${encodeURI(img)}')"` : '';
  return L.divIcon({
    className: '',
    html: `<div class="media-cluster${inDay ? '' : ' is-dimmed'}"${bg}><span>${n}</span></div>`,
    iconSize: [size, size],
    iconAnchor: [size / 2, size / 2],
  });
}

// fitTrip shows the initial view: fitBounds leaves out the days marked
// excludeFromFit in the config (bounds for older trip.json files).
function fitTrip() {
  const b = state.trip.fitBounds || state.trip.bounds;
  if (b) map.fitBounds(b, fitOptions());
  else map.setView([62, 25], 5);
}

// Visible map area excludes the bottom sheet on narrow screens.
function sheetHeight() {
  return narrowQuery.matches ? els.timeline.getBoundingClientRect().height : 0;
}

function fitOptions() {
  const pad = 32;
  return { paddingTopLeft: [pad, pad], paddingBottomRight: [pad, pad + sheetHeight()], maxZoom: 14 };
}

// focusLatLng pans so that latlng is visible, without changing zoom, and
// only if it is outside the visible area.
function focusLatLng(latlng) {
  const size = map.getSize();
  const visibleH = size.y - sheetHeight();
  const p = map.latLngToContainerPoint(latlng);
  const margin = 40;
  const inside = p.x >= margin && p.x <= size.x - margin && p.y >= margin && p.y <= visibleH - margin;
  if (inside) return;
  const target = L.point(size.x / 2, visibleH / 2);
  map.panBy(p.subtract(target), { animate: true });
}

function showSelectionRing(item) {
  if (selectionRing) selectionRing.remove();
  selectionRing = null;
  if (!item || !onMap(item)) return;
  const size = isMedia(item) ? 56 : 44;
  selectionRing = L.marker([item.lat, item.lon], {
    icon: L.divIcon({ className: '', html: '<div class="selection-ring"></div>', iconSize: [size, size], iconAnchor: [size / 2, size / 2] }),
    interactive: false,
    keyboard: false,
    zIndexOffset: -100,
  }).addTo(map);
}

function setMarkerSelected(id, on) {
  const m = markers.get(id);
  if (!m) return;
  const el = m.getElement()?.firstElementChild;
  if (el) el.classList.toggle('is-selected', on);
  m.setZIndexOffset(on ? 1000 : 0);
}

const isDimmed = (id) => state.selectedDay != null && state.dayOfItem.get(id) !== state.selectedDay;

// setMarkerDimmed fades a marker of another day and makes it ignore the
// pointer (CSS), so its tooltip and click do not fire. Clustered markers get
// a fresh element each time they are shown, hence the 'add' hook in initMap.
function setMarkerDimmed(m, dimmed) {
  m.setOpacity(dimmed ? 0.35 : 1);
  m.getElement()?.classList.toggle('is-dimmed', dimmed);
}

function applyDayDimming() {
  const day = state.selectedDay;
  const dayTracks = day ? new Set(state.dayByIndex.get(day).stats.trackIds) : null;
  for (const [id, { line, casing }] of trackLayers) {
    const on = !dayTracks || dayTracks.has(id);
    line.setStyle({ opacity: on ? 0.95 : 0.25 });
    casing.setStyle({ opacity: on ? 0.85 : 0.2 });
    line.getElement()?.classList.toggle('is-dimmed', !on);
    if (on) line.bringToFront();
  }
  for (const [id, m] of markers) setMarkerDimmed(m, isDimmed(id));
  layers.clustered.refreshClusters();
}

// ---------------------------------------------------------------- timeline

// renderItem returns the timeline row for one item. New kinds (photo,
// video) get their own case; unknown kinds fall back to a plain row.
function renderItem(item) {
  switch (item.kind) {
    case 'note':
      return renderNote(item);
    case 'photo':
    case 'video':
      return renderTile(item);
    default:
      return renderPlain(item);
  }
}

function entryShell(item, extraClass, body) {
  const approx = approxText(item);
  const meta = [];
  if (approx) meta.push(h('span', { class: 'badge badge-approx', title: approx }, 'Approximate position'));
  if (item.placement?.source === 'none') meta.push(h('span', { class: 'badge badge-none' }, 'Not on map'));
  if (item.timeAssumed) meta.push(h('span', { class: 'badge', title: 'The source had no UTC offset; trip timezone assumed' }, 'Time assumed'));
  return h('button', {
    type: 'button',
    class: `entry ${extraClass}`,
    id: `entry-${item.id}`,
    dataset: { id: item.id },
    onclick: () => selectItem(item.id, { source: 'timeline' }),
  },
  entryTime(item.time),
  h('span', { class: 'entry-body' },
    body,
    meta.length ? h('span', { class: 'entry-meta' }, meta) : null,
  ));
}

// entryTime is the time column of a timeline row. A time exactly on the hour
// repeats the hour label above it, so it is left blank (screen readers
// still get it).
function entryTime(time) {
  const t = state.fmt.time(time);
  return t.endsWith(':00')
    ? h('span', { class: 'entry-time is-on-hour' }, h('span', { class: 'sr-only' }, t))
    : h('span', { class: 'entry-time' }, t);
}

function renderNote(item) {
  return entryShell(item, 'entry-note', [
    item.title ? h('span', { class: 'entry-title'}, item.title) : null,
    item.text ? h('span', { class: 'entry-text'}, item.text) : null,
  ]);
}

// renderTile is one square thumbnail in a media grid row.
function renderTile(item) {
  const video = item.kind === 'video';
  const img = item.thumb || item.poster;
  const approx = approxText(item);
  const notes = [approx, onMap(item) ? '' : 'Not on map'].filter(Boolean);
  const label = `${video ? 'Video' : 'Photo'} at ${state.fmt.time(item.time)}` +
    (item.title || item.caption ? `: ${item.title || item.caption}` : '');
  return h('button', {
    type: 'button',
    class: `tile${video ? ' tile-video' : ''}`,
    id: `entry-${item.id}`,
    dataset: { id: item.id },
    title: [label, ...notes].join('\n'),
    'aria-label': label,
    onclick: () => {
      selectItem(item.id, { source: 'timeline' });
      openLightbox(item.id);
    },
  },
  // loading must be set before src, or the detached img fetches eagerly.
  img ? h('img', { loading: 'lazy', decoding: 'async', alt: '', src: img }) : h('span', { class: 'tile-missing' }, video ? 'Video' : 'Photo'),
  video ? h('span', { class: 'tile-play' }, h('span', { class: 'play-glyph' })) : null,
  video && item.durationS ? h('span', { class: 'tile-duration' }, formatClock(item.durationS)) : null,
  notes.length ? h('span', { class: 'tile-flag', 'aria-hidden': 'true' }, approx ? '≈' : '∅') : null,
  h('span', { class: 'tile-time' }, state.fmt.time(item.time)));
}

function renderPlain(item) {
  const label = item.title || item.text || item.original || item.kind;
  return entryShell(item, `entry-${item.kind}`, h('span', { class: 'entry-text'}, label));
}

function renderTrackEvent(ev) {
  const t = ev.track;
  const verb = trackVerb(t.type);
  const text = ev.kind === 'start'
    ? `${verb} started · ${t.name}`
    : [`${verb} ended`, formatKm(t.stats.distanceKm), `↑ ${Math.round(t.stats.elevationGainM)} m`,
      t.stats.movingTimeS ? formatDuration(t.stats.movingTimeS) : null].filter(Boolean).join(' · ');
  return h('button', {
    type: 'button',
    class: 'entry entry-track',
    dataset: { track: t.id },
    onclick: () => focusTrack(t.id),
  },
  entryTime(ev.time),
  h('span', { class: 'entry-body' },
    h('span', { class: 'entry-text' }, h('span', { class: `track-swatch ${trackCategory(t.type)}` }), text)));
}

function mediaCounts(ids) {
  let photos = 0;
  let videos = 0;
  for (const id of ids) {
    const k = state.itemsById.get(id)?.kind;
    if (k === 'photo') photos++;
    else if (k === 'video') videos++;
  }
  return { photos, videos };
}

function mediaCountText({ photos, videos }) {
  return [photos ? plural(photos, 'photo') : '', videos ? plural(videos, 'video') : ''].filter(Boolean).join(' · ');
}

function dayStatsLine(day) {
  const s = day.stats;
  const media = mediaCountText(mediaCounts(day.itemIds));
  const mediaEl = media ? h('span', { class: 'media-count' }, media) : null;
  if (!s.trackIds.length) return h('p', { class: 'day-stats' }, h('span', { class: 'none' }, 'No tracks recorded'), mediaEl);
  return h('p', { class: 'day-stats' },
    h('span', {}, formatKm(s.distanceKm)),
    h('span', {}, `↑ ${Math.round(s.elevationGainM)} m`),
    h('span', {}, formatDuration(s.movingTimeS)),
    mediaEl,
  );
}

function renderTimeline(trip) {
  const frag = document.createDocumentFragment();
  for (const day of trip.days) {
    const events = day.itemIds.map((id) => {
      const item = state.itemsById.get(id);
      return { time: item.time, node: () => renderItem(item), media: isMedia(item) };
    });
    for (const tid of day.stats.trackIds) {
      const track = state.tracksById.get(tid);
      if (!track?.start) continue;
      events.push({ time: track.start, node: () => renderTrackEvent({ kind: 'start', track, time: track.start }), order: -1 });
      events.push({ time: track.end, node: () => renderTrackEvent({ kind: 'end', track, time: track.end }), order: 1 });
    }
    events.sort((a, b) => (Date.parse(a.time) - Date.parse(b.time)) || ((a.order || 0) - (b.order || 0)));

    const section = h('section', { class: 'day', id: `day-${day.index}`, dataset: { day: day.index } },
      h('header', { class: 'day-head', onclick: () => selectDay(day.index, { source: 'timeline' }) },
        h('p', { class: 'day-kicker' }, `Day ${day.index} · ${state.fmt.dayLong(day.date)}`),
        day.title ? h('h2', { class: 'day-title' }, day.title) : null,
        dayStatsLine(day),
      ));

    // Consecutive photos and videos within an hour share one grid row.
    let hourKey = null;
    let hourEl = null;
    let gridEl = null;
    for (const ev of events) {
      const hk = state.fmt.hour(ev.time);
      if (hk !== hourKey) {
        hourKey = hk;
        hourEl = h('div', { class: 'hour' }, h('p', { class: 'hour-label' }, `${hk}:00`));
        section.append(hourEl);
        gridEl = null;
      }
      if (ev.media) {
        if (!gridEl) {
          gridEl = h('div', { class: 'media-grid' });
          hourEl.append(gridEl);
        }
        gridEl.append(ev.node());
      } else {
        gridEl = null;
        hourEl.append(ev.node());
      }
    }
    if (!events.length) section.append(h('p', { class: 'empty-day' }, 'Nothing recorded.'));
    frag.append(section);
  }
  els.scroll.replaceChildren(frag);
}

const CAMERA_SVG = '<svg viewBox="0 0 16 16" aria-hidden="true"><path fill="currentColor" d="M5.6 2.5h4.8l1 1.6H14a1 1 0 0 1 1 1V13a1 1 0 0 1-1 1H2a1 1 0 0 1-1-1V5.1a1 1 0 0 1 1-1h2.6zM8 5.8a3 3 0 1 0 0 6 3 3 0 0 0 0-6zm0 1.4a1.6 1.6 0 1 1 0 3.2 1.6 1.6 0 0 1 0-3.2z"/></svg>';

function cameraGlyph() {
  const s = h('span', { class: 'chip-cam' });
  s.innerHTML = CAMERA_SVG;
  return s;
}

function renderChips(trip) {
  const chips = [h('button', {
    type: 'button', class: 'chip chip-all', dataset: { day: 'all' },
    onclick: () => selectDay(null, { source: 'chip' }),
  }, h('span', { class: 'chip-label' }, 'All'))];
  for (const day of trip.days) {
    const counts = mediaCounts(day.itemIds);
    const media = mediaCountText(counts);
    chips.push(h('button', {
      type: 'button', class: 'chip', dataset: { day: day.index },
      title: [day.title, day.stats.distanceKm ? formatKm(day.stats.distanceKm) : '', media].filter(Boolean).join('\n'),
      onclick: () => selectDay(day.index, { source: 'chip' }),
    },
    h('span', { class: 'chip-label' }, `Day ${day.index}`,
      counts.photos ? h('span', { class: 'chip-count', 'aria-label': plural(counts.photos, 'photo') }, cameraGlyph(), counts.photos) : null),
    h('span', { class: 'chip-date' }, state.fmt.dayShort(day.date))));
  }
  els.chips.replaceChildren(...chips);
}

function updateChips() {
  for (const c of els.chips.children) {
    const d = c.dataset.day === 'all' ? null : Number(c.dataset.day);
    c.classList.toggle('is-selected', d === state.selectedDay);
    c.setAttribute('aria-pressed', String(d === state.selectedDay));
  }
}

function setCurrentChip(dayIndex) {
  for (const c of els.chips.children) {
    const on = Number(c.dataset.day) === dayIndex;
    if (on && !c.classList.contains('is-current')) {
      c.scrollIntoView({ block: 'nearest', inline: 'nearest' });
    }
    c.classList.toggle('is-current', on);
  }
}

// Scroll spy: the day whose section spans the top of the timeline viewport.
function initScrollSpy() {
  let raf = 0;
  const update = () => {
    raf = 0;
    const top = els.scroll.getBoundingClientRect().top + 8;
    let current = null;
    for (const sec of els.scroll.querySelectorAll('.day')) {
      if (sec.getBoundingClientRect().top <= top) current = Number(sec.dataset.day);
      else break;
    }
    if (current == null && state.trip.days.length) current = state.trip.days[0].index;
    setCurrentChip(current);
  };
  els.scroll.addEventListener('scroll', () => { if (!raf) raf = requestAnimationFrame(update); }, { passive: true });
  update();
}

// ---------------------------------------------------------------- selection

// lastHash is the hash the page itself last wrote; a hashchange to it is
// ours (e.g. restored by a history traversal) and is not re-applied.
let lastHash = location.hash;

// setHash records the selection in the URL without adding history entries
// (replaceState does not fire hashchange).
function setHash(value) {
  const hash = value ? `#${value}` : '';
  lastHash = hash;
  if (location.hash !== hash) history.replaceState(history.state, '', location.pathname + location.search + hash);
}

function scrollToEl(el, source) {
  if (!el) return;
  const block = source === 'timeline' ? 'nearest' : 'center';
  // The lightbox hides the timeline, so no point animating behind it.
  const instant = source === 'hash' || source === 'lightbox';
  el.scrollIntoView({ block, behavior: instant ? 'auto' : 'smooth' });
}

function selectItem(id, { source = 'timeline' } = {}) {
  const item = state.itemsById.get(id);
  if (!item) return;
  if (state.selectedId && state.selectedId !== id) {
    document.getElementById(`entry-${state.selectedId}`)?.classList.remove('is-selected');
    setMarkerSelected(state.selectedId, false);
  }
  state.selectedId = id;

  const day = state.dayOfItem.get(id);
  if (state.selectedDay != null && state.selectedDay !== day) {
    state.selectedDay = day;
    updateChips();
    applyDayDimming();
    updateProfile();
  }

  const entry = document.getElementById(`entry-${id}`);
  entry?.classList.add('is-selected');
  if (source !== 'timeline') scrollToEl(entry, source);
  else entry?.scrollIntoView({ block: 'nearest' });

  map.closePopup();
  setMarkerSelected(id, true);
  showSelectionRing(item);
  markProfileSelection();
  if (onMap(item)) {
    const ll = L.latLng(item.lat, item.lon);
    if (source === 'hash') {
      map.setView(ll, Math.max(map.getZoom(), 12), { animate: false });
      map.panBy([0, sheetHeight() / 2], { animate: false });
    }
    if (source !== 'map' && !revealClustered(id)) {
      if (source !== 'hash') focusLatLng(ll);
    }
  }
  setHash(`item=${id}`);
}

// Photos taken metres apart stay clustered up to the maximum zoom; zooming
// that far loses all context, so reveal stops here and fans the cluster out.
const REVEAL_ZOOM = 16;

// revealClustered zooms in just far enough for a clustered marker to be
// shown on its own (at most REVEAL_ZOOM, then spiderfying the cluster) and
// highlights it. Returns false when the marker is already shown or not
// clustered, so the caller pans as usual.
function revealClustered(id) {
  const m = markers.get(id);
  if (!m || !layers.clustered.hasLayer(m)) return false;
  // getVisibleParent is the marker itself when shown, its cluster when
  // clustered, null when off-screen.
  if (layers.clustered.getVisibleParent(m) === m) return false;
  const show = () => {
    if (state.selectedId !== id) return;
    // The cluster layer re-clusters after a zoom with its own animation.
    if (layers.clustered._inZoomAnimation) {
      layers.clustered.once('animationend', show);
      return;
    }
    const parent = layers.clustered.getVisibleParent(m);
    if (parent && parent !== m) parent.spiderfy(); // the 'add' hook highlights
    else setMarkerSelected(id, true);
  };
  // __parent._zoom is the deepest zoom at which the marker is clustered.
  const deepest = m.__parent?._zoom ?? map.getZoom();
  const zoom = Math.max(map.getZoom(), Math.min(deepest + 1, REVEAL_ZOOM));
  const ll = m.getLatLng();
  if (zoom === map.getZoom() && layers.clustered.getVisibleParent(m)) {
    show();
    return true;
  }
  // Centre it in the part of the map not covered by the bottom sheet.
  const center = map.unproject(map.project(ll, zoom).add([0, sheetHeight() / 2]), zoom);
  map.once('moveend', show);
  map.setView(center, zoom);
  return true;
}

function clearItemSelection() {
  if (!state.selectedId) return;
  document.getElementById(`entry-${state.selectedId}`)?.classList.remove('is-selected');
  setMarkerSelected(state.selectedId, false);
  showSelectionRing(null);
  state.selectedId = null;
  markProfileSelection();
}

function dayBounds(day) {
  let b = null;
  const add = (lat, lon) => {
    if (b) b.extend([lat, lon]);
    else b = L.latLngBounds([lat, lon], [lat, lon]);
  };
  for (const tid of day.stats.trackIds) {
    for (const p of state.tracksById.get(tid)?.points || []) add(p[0], p[1]);
  }
  for (const id of day.itemIds) {
    const it = state.itemsById.get(id);
    if (it.lat != null && it.placement?.source !== 'none') add(it.lat, it.lon);
  }
  return b;
}

function selectDay(index, { source = 'chip' } = {}) {
  state.selectedDay = index;
  updateChips();
  applyDayDimming();
  updateProfile();
  map.closePopup();
  if (index == null) {
    fitTrip();
    if (source !== 'hash') setHash(state.selectedId ? `item=${state.selectedId}` : '');
    return;
  }
  if (state.selectedId && state.dayOfItem.get(state.selectedId) !== index) clearItemSelection();
  const day = state.dayByIndex.get(index);
  const b = dayBounds(day);
  if (b) map.fitBounds(b, fitOptions());
  else fitTrip(); // nothing on the map that day
  if (source !== 'timeline') {
    const sec = document.getElementById(`day-${index}`);
    if (sec) {
      const top = sec.getBoundingClientRect().top - els.scroll.getBoundingClientRect().top + els.scroll.scrollTop;
      els.scroll.scrollTo({ top, behavior: source === 'hash' ? 'auto' : 'smooth' });
    }
  }
  setHash(`day=${index}`);
}

function focusTrack(id) {
  const t = trackLayers.get(id);
  if (!t) return;
  map.fitBounds(t.line.getBounds(), fitOptions());
  const pts = t.line.getLatLngs().flat();
  t.line.openPopup(pts[Math.floor(pts.length / 2)]);
}

function step(delta) {
  if (state.lightboxId) {
    lightboxStep(delta);
    return;
  }
  const items = state.trip.items;
  if (!items.length) return;
  let i = state.selectedId ? state.itemIndex.get(state.selectedId) + delta : (delta > 0 ? 0 : items.length - 1);
  i = Math.max(0, Math.min(items.length - 1, i));
  selectItem(items[i].id, { source: 'key' });
}

function applyHash() {
  const params = new URLSearchParams(location.hash.slice(1));
  const item = params.get('item');
  const day = params.get('day');
  if (item && state.itemsById.has(item)) {
    selectItem(item, { source: 'hash' });
    if (isMedia(state.itemsById.get(item))) openLightbox(item);
    else closeLightbox();
  } else if (day && state.dayByIndex.has(Number(day))) {
    closeLightbox();
    selectDay(Number(day), { source: 'hash' });
  }
}

// ---------------------------------------------------------------- lightbox

let lbReturnFocus = null;
// lbHistory tracks the history entry pushed while the lightbox is open, so
// the browser back button (or gesture) closes it: 'open' while it is
// there, 'closing' while our own history.back() is in flight.
let lbHistory = null;
let miniMap = null;
let miniDot = null;
const preloaded = new Set();

// fitBox returns the largest size with the item's aspect ratio that fits the
// stage. Sizing from width/height before the file loads avoids layout jumps.
function fitBox(item) {
  const r = els.lbStage.getBoundingClientRect();
  const w = item.width || 4;
  const hgt = item.height || 3;
  const scale = Math.min(r.width / w, r.height / hgt);
  return { width: Math.floor(w * scale), height: Math.floor(hgt * scale) };
}

function sizeLightboxMedia() {
  const el = els.lbStage.firstElementChild;
  const item = state.itemsById.get(state.lightboxId);
  if (!el || !item) return;
  const { width, height } = fitBox(item);
  el.style.width = `${width}px`;
  el.style.height = `${height}px`;
}

function renderLightboxMedia(item) {
  const old = els.lbStage.querySelector('video');
  if (old) { old.pause(); old.removeAttribute('src'); old.load(); }
  let el;
  if (item.kind === 'video') {
    el = h('video', {
      class: 'lb-media', src: item.src, poster: item.poster, controls: true, playsinline: true,
      preload: 'metadata', width: item.width, height: item.height,
    });
  } else {
    el = h('img', {
      class: 'lb-media', src: item.src, alt: item.title || item.caption || '',
      width: item.width, height: item.height, decoding: 'async', draggable: 'false',
    });
    // The already-loaded thumbnail stands in until the full image arrives.
    if (item.thumb) el.style.backgroundImage = `url('${encodeURI(item.thumb)}')`;
  }
  els.lbStage.replaceChildren(el);
  sizeLightboxMedia();
}

function renderLightboxCaption(item) {
  const day = state.dayByIndex.get(state.dayOfItem.get(item.id));
  const when = [state.fmt.time(item.time)];
  if (day) when.push(`Day ${day.index} · ${state.fmt.dayLong(day.date)}`);
  if (item.kind === 'video' && item.durationS) when.push(formatClock(item.durationS));
  els.lbWhen.textContent = when.join(' · ');
  const caption = [item.title, item.caption].filter(Boolean).join(' — ');
  els.lbCaption.textContent = caption;
  els.lbCaption.hidden = !caption;
  const approx = onMap(item) ? approxText(item) : 'Not on map: no position for this item';
  els.lbApprox.textContent = approx;
  els.lbApprox.hidden = !approx;
  const i = state.mediaIndex.get(item.id);
  els.lbCount.textContent = `${i + 1} / ${state.media.length}`;
  els.lbFull.href = item.src;
  els.lbPrev.disabled = i === 0;
  els.lbNext.disabled = i === state.media.length - 1;
  els.lb.setAttribute('aria-label', item.kind === 'video' ? 'Video viewer' : 'Photo viewer');
}

function preload(item) {
  if (!item || item.kind !== 'photo' || preloaded.has(item.id)) return;
  preloaded.add(item.id);
  const img = new Image();
  img.decoding = 'async';
  img.src = item.src;
}

// updateMiniMap shows where the current item is, in a small inset map.
// Hidden on narrow screens (CSS), so it is only created when visible.
function updateMiniMap(item) {
  if (!els.lbMini.offsetParent) return;
  if (!miniMap) {
    miniMap = L.map(els.lbMini, {
      zoomControl: false, attributionControl: true, dragging: false, scrollWheelZoom: false,
      doubleClickZoom: false, boxZoom: false, keyboard: false, touchZoom: false, fadeAnimation: false,
    });
    miniMap.attributionControl.setPrefix(false);
    L.tileLayer('https://tile.openstreetmap.org/{z}/{x}/{y}.png', {
      maxZoom: 19, attribution: '&copy; OpenStreetMap',
    }).addTo(miniMap);
    for (const [, { line }] of trackLayers) {
      L.polyline(line.getLatLngs(), { color: line.options.color, weight: 3, opacity: 0.9, interactive: false }).addTo(miniMap);
    }
    miniDot = L.circleMarker([0, 0], { radius: 7, color: '#fff', weight: 2, fillColor: cssVar('--accent'), fillOpacity: 1, interactive: false }).addTo(miniMap);
  }
  miniMap.invalidateSize();
  const has = onMap(item);
  els.lbMini.classList.toggle('is-empty', !has);
  if (!has) return;
  const ll = [item.lat, item.lon];
  miniDot.setLatLng(ll);
  miniDot.setStyle({ dashArray: approxText(item) ? '3 3' : null });
  miniMap.setView(ll, 11, { animate: false });
}

function showInLightbox(id) {
  const item = state.itemsById.get(id);
  if (!item) return;
  state.lightboxId = id;
  renderLightboxMedia(item);
  renderLightboxCaption(item);
  updateMiniMap(item);
  preload(state.media[state.mediaIndex.get(id) + 1]);
}

function openLightbox(id) {
  if (!state.mediaIndex.has(id)) return;
  const wasOpen = !!state.lightboxId;
  if (!wasOpen) {
    lbReturnFocus = document.activeElement;
    els.lb.hidden = false;
    els.layout.inert = true;
    els.topbar.inert = true;
    if (lbHistory !== 'open') {
      history.pushState({ lightbox: true }, '', location.href);
      lbHistory = 'open';
    }
  }
  showInLightbox(id);
  if (!wasOpen) els.lbClose.focus({ preventScroll: true });
}

function closeLightbox({ fromHistory = false } = {}) {
  if (!state.lightboxId) return;
  const id = state.lightboxId;
  const v = els.lbStage.querySelector('video');
  if (v) v.pause();
  els.lbStage.replaceChildren();
  state.lightboxId = null;
  els.lb.hidden = true;
  els.layout.inert = false;
  els.topbar.inert = false;
  // Return focus to the thumbnail of the item last shown, else where we were.
  const target = document.getElementById(`entry-${id}`) || lbReturnFocus;
  target?.focus?.({ preventScroll: true });
  lbReturnFocus = null;
  if (lbHistory === 'open' && !fromHistory) {
    lbHistory = 'closing';
    history.back(); // drop our entry; onPopState keeps the current hash
  }
}

// onPopState closes the lightbox when its history entry is popped. The
// entry below it may carry an older #item hash (prev/next changed it), so
// the current one is written back; hashchange then ignores it (lastHash).
function onPopState() {
  if (!lbHistory) return;
  if (lbHistory === 'open') closeLightbox({ fromHistory: true });
  lbHistory = null;
  if (location.hash !== lastHash) {
    history.replaceState(history.state, '', location.pathname + location.search + lastHash);
  }
}

// lightboxStep moves through photos and videos only, across day boundaries.
function lightboxStep(delta) {
  const i = state.mediaIndex.get(state.lightboxId) + delta;
  if (i < 0 || i >= state.media.length) return;
  const id = state.media[i].id;
  selectItem(id, { source: 'lightbox' });
  showInLightbox(id);
}

function initLightbox() {
  els.lbClose.addEventListener('click', closeLightbox);
  els.lbPrev.addEventListener('click', () => lightboxStep(-1));
  els.lbNext.addEventListener('click', () => lightboxStep(1));
  // A click on the backdrop (not on the photo or video) closes.
  els.lbStage.addEventListener('click', (e) => {
    if (e.target === els.lbStage && !swipe.moved) closeLightbox();
  });
  window.addEventListener('resize', () => {
    if (!state.lightboxId) return;
    sizeLightboxMedia();
    const item = state.itemsById.get(state.lightboxId);
    if (item) updateMiniMap(item);
  });

  // Swipe: a mostly horizontal drag of 50 px or more on the image.
  const swipe = { id: null, x: 0, y: 0, moved: false };
  els.lbStage.addEventListener('pointerdown', (e) => {
    if (e.target.closest('video') || (e.pointerType === 'mouse' && e.button !== 0)) return;
    Object.assign(swipe, { id: e.pointerId, x: e.clientX, y: e.clientY, moved: false });
  });
  els.lbStage.addEventListener('pointerup', (e) => {
    if (swipe.id !== e.pointerId) return;
    swipe.id = null;
    const dx = e.clientX - swipe.x;
    const dy = e.clientY - swipe.y;
    if (Math.abs(dx) >= 50 && Math.abs(dx) > 1.5 * Math.abs(dy)) {
      swipe.moved = true;
      lightboxStep(dx < 0 ? 1 : -1);
    }
  });
  els.lbStage.addEventListener('pointercancel', () => { swipe.id = null; });
}

// ---------------------------------------------------------------- elevation profile

// The profile strip under the map (SPEC.md 6.4): the selected day's tracks,
// or the whole trip's, end to end along a distance axis. Hidden by CSS on
// narrow screens; everything here then sees a zero-size panel and draws
// nothing, so enabling it there later is a CSS change.

const PROFILE_KEY = 'touringDiary.profileCollapsed';
// Plot margins: room for track names above, elevation labels left, and the
// item tick strip plus distance labels below.
const PM = { top: 18, right: 14, bottom: 30, left: 48 };
const TICK_Y = 3; // item ticks start this far below the plot
const TICK_LEN = 8;
const TICK_HIT = 8; // px either side of a tick that still counts as on it

const profile = {
  cum: new Map(), // track id -> Float64Array of cumulative km per point
  view: null, // { segs, totalKm, lo, hi, items } for the current day
  geom: null, // scales of the last render
  ticks: [], // { entry, x, el } sorted by x
  hoverTick: null,
  marker: null,
  hoverEls: null,
  timer: 0,
};

const EARTH_KM = 6371.0088;
const RAD = Math.PI / 180;

function haversineKm(a, b) {
  const dLat = (b[0] - a[0]) * RAD;
  const dLon = (b[1] - a[1]) * RAD;
  const s = Math.sin(dLat / 2) ** 2 + Math.cos(a[0] * RAD) * Math.cos(b[0] * RAD) * Math.sin(dLon / 2) ** 2;
  return 2 * EARTH_KM * Math.asin(Math.min(1, Math.sqrt(s)));
}

// cumulativeKm returns the distance from the track start at each point. The
// jump between recorded segments is not counted.
function cumulativeKm(track) {
  const pts = track.points;
  const out = new Float64Array(pts.length);
  const breaks = new Set(track.segmentStarts || []);
  for (let i = 1; i < pts.length; i++) {
    out[i] = out[i - 1] + (breaks.has(i) ? 0 : haversineKm(pts[i - 1], pts[i]));
  }
  return out;
}

// lowerBound returns the first index in the sorted array-like whose value
// (via get) is >= v.
function lowerBound(n, get, v) {
  let lo = 0;
  let hi = n;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (get(mid) < v) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}

// nearestIndex returns the index whose value is closest to v.
function nearestIndex(n, get, v) {
  const i = lowerBound(n, get, v);
  if (i <= 0) return 0;
  if (i >= n) return n - 1;
  return v - get(i - 1) <= get(i) - v ? i - 1 : i;
}

// buildProfileView lays out the tracks of the selected day (or the whole
// trip) end to end in chronological order and places the items on them.
function buildProfileView() {
  const day = state.selectedDay ? state.dayByIndex.get(state.selectedDay) : null;
  const ids = day ? day.stats.trackIds : state.trip.tracks.map((t) => t.id);
  const tracks = ids.map((id) => state.tracksById.get(id))
    .filter((t) => t && t.start && t.points?.length > 1)
    .sort((a, b) => Date.parse(a.start) - Date.parse(b.start));
  const segs = [];
  let offset = 0;
  let lo = Infinity;
  let hi = -Infinity;
  for (const track of tracks) {
    const cum = profile.cum.get(track.id);
    const km = cum[cum.length - 1];
    for (const p of track.points) {
      if (p[2] == null) continue;
      if (p[2] < lo) lo = p[2];
      if (p[2] > hi) hi = p[2];
    }
    segs.push({ track, cum, offset, km, t0: Date.parse(track.start), t1: Date.parse(track.end), cat: trackCategory(track.type) });
    offset += km;
  }

  // Items within a track's time range sit at the point nearest in time. When
  // ranges overlap (a hike during a paused ride), the closer point wins.
  const items = [];
  const itemIds = day ? day.itemIds : state.trip.items.map((it) => it.id);
  for (const id of itemIds) {
    const item = state.itemsById.get(id);
    const t = Date.parse(item.time);
    let best = null;
    for (const seg of segs) {
      if (t < seg.t0 || t > seg.t1) continue;
      const pts = seg.track.points;
      const s = (t - seg.t0) / 1000;
      const idx = nearestIndex(pts.length, (i) => pts[i][3], s);
      const dt = Math.abs(pts[idx][3] - s);
      if (!best || dt < best.dt) best = { item, seg, idx, dt, km: seg.offset + seg.cum[idx] };
    }
    if (best) items.push(best);
  }
  items.sort((a, b) => a.km - b.km);
  return { day, segs, totalKm: offset, lo, hi, items };
}

// niceScale rounds [lo, hi] out to steps of 1, 2, 2.5 or 5 x 10^n giving
// about `count` intervals.
function niceScale(lo, hi, count) {
  const span = Math.max(hi - lo, 1e-9);
  const raw = span / count;
  const mag = 10 ** Math.floor(Math.log10(raw));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * mag).find((v) => v >= raw * 0.999);
  const start = Math.floor(lo / step) * step;
  const end = Math.ceil(hi / step) * step;
  const ticks = [];
  for (let v = start; v <= end + step / 2; v += step) ticks.push(Math.round(v * 1000) / 1000);
  return { lo: start, hi: end, step, ticks };
}

function elevationScale(lo, hi) {
  // Flat days keep at least 40 m of range, so noise does not look like hills.
  if (hi - lo < 40) {
    const mid = (hi + lo) / 2;
    lo = mid - 20;
    hi = mid + 20;
  }
  // The smallest nice step that needs at most four gridlines.
  const mag = 10 ** Math.floor(Math.log10((hi - lo) / 3));
  let s = null;
  for (const m of [1, 2, 2.5, 5, 10, 20, 25, 50]) {
    s = niceScale(lo, hi, (hi - lo) / (m * mag));
    if (s.ticks.length <= 4) break;
  }
  // Gridlines sit on multiples of the step, but the domain is rounded to a
  // fifth of it, so a −46 m dip does not push the floor down to −250 m.
  const pad = s.step / 5;
  const domLo = Math.floor(lo / pad) * pad;
  const domHi = Math.ceil(hi / pad) * pad;
  const ticks = s.ticks.filter((v) => v >= domLo - 1e-9 && v <= domHi + 1e-9);
  return { lo: domLo, hi: domHi, step: s.step, ticks };
}

const r1 = (v) => Math.round(v * 10) / 10;

// segmentPaths draws one track, keeping at most the first, min and max
// point of each 1 px column (about 2 points per pixel) so peaks survive.
function segmentPaths(seg, xOf, yOf, bottom) {
  const pts = seg.track.points;
  const out = [];
  const push = (i) => { if (out[out.length - 1] !== i) out.push(i); };
  let col = null;
  let iMin = -1;
  let iMax = -1;
  let lastEle = pts[0][2] ?? 0;
  const ele = new Float64Array(pts.length);
  for (let i = 0; i < pts.length; i++) {
    lastEle = pts[i][2] ?? lastEle;
    ele[i] = lastEle;
  }
  const flush = () => {
    if (iMin < 0) return;
    if (iMin <= iMax) { push(iMin); push(iMax); } else { push(iMax); push(iMin); }
  };
  push(0);
  for (let i = 1; i < pts.length - 1; i++) {
    const c = Math.floor(xOf(seg.offset + seg.cum[i]));
    if (c !== col) {
      flush();
      col = c;
      iMin = iMax = i;
    } else {
      if (ele[i] < ele[iMin]) iMin = i;
      if (ele[i] > ele[iMax]) iMax = i;
    }
  }
  flush();
  push(pts.length - 1);
  let line = '';
  for (let k = 0; k < out.length; k++) {
    const i = out[k];
    line += `${k ? 'L' : 'M'}${r1(xOf(seg.offset + seg.cum[i]))},${r1(yOf(ele[i]))}`;
  }
  const x0 = r1(xOf(seg.offset));
  const x1 = r1(xOf(seg.offset + seg.km));
  return { line, area: `${line}L${x1},${bottom}L${x0},${bottom}Z`, points: out.length, ele };
}

function fitLabel(text, px) {
  const max = Math.floor(px / 6); // ~6 px per character at 10.5 px
  if (max < 4) return '';
  return text.length > max ? `${text.slice(0, max - 1).trimEnd()}…` : text;
}

function renderProfileHead(view) {
  const parts = [];
  if (view.day) {
    const s = view.day.stats;
    parts.push(`Day ${view.day.index}`);
    if (s.trackIds.length) parts.push(formatKm(s.distanceKm), `↑ ${Math.round(s.elevationGainM).toLocaleString('en-GB')} m`);
  } else {
    const km = state.trip.days.reduce((a, d) => a + (d.stats.distanceKm || 0), 0);
    const gain = state.trip.days.reduce((a, d) => a + (d.stats.elevationGainM || 0), 0);
    parts.push('Whole trip', `${Math.round(km).toLocaleString('en-GB')} km`, `↑ ${Math.round(gain).toLocaleString('en-GB')} m`);
  }
  if (view.segs.length) parts.push(`${Math.round(view.lo)}–${Math.round(view.hi)} m`);
  els.profileSummary.textContent = parts.join(' · ');
  // Legend only when both kinds are on screen; one kind is named by the
  // track labels and the readout.
  const cats = [...new Set(view.segs.map((s) => s.cat))];
  const names = { cycling: 'Cycling', hiking: 'Hiking / walking', other: 'Other' };
  els.profileLegend.replaceChildren(...(cats.length > 1
    ? ['cycling', 'hiking', 'other'].filter((c) => cats.includes(c)).map((c) => h('span', {}, h('i', { class: c }), names[c]))
    : []));
}

function renderProfile() {
  const view = profile.view;
  if (!view) return;
  hideProfileHover();
  renderProfileHead(view);
  const W = els.profileBody.clientWidth;
  const H = els.profileBody.clientHeight;
  const svg = els.profileSvg;
  const hasData = view.segs.length && Number.isFinite(view.lo);
  els.profileEmpty.hidden = !!hasData;
  els.profileEmpty.textContent = view.segs.length ? 'No elevation data' : 'No tracks recorded';
  profile.ticks = [];
  profile.geom = null;
  if (!hasData || W < 120 || H < 60) {
    svg.replaceChildren();
    return;
  }
  const t0 = performance.now();
  const pw = W - PM.left - PM.right;
  const ph = H - PM.top - PM.bottom;
  const bottom = PM.top + ph;
  const ys = elevationScale(view.lo, view.hi);
  const xOf = (km) => PM.left + (km / view.totalKm) * pw;
  const yOf = (e) => PM.top + ((ys.hi - e) / (ys.hi - ys.lo)) * ph;
  const parts = [];

  const baseY = Math.round(yOf(ys.lo)) + 0.5;
  parts.push(`<line class="p-base" x1="${PM.left}" x2="${W - PM.right}" y1="${baseY}" y2="${baseY}"/>`);
  for (const v of ys.ticks) {
    const y = Math.round(yOf(v)) + 0.5;
    if (y !== baseY) parts.push(`<line class="p-grid" x1="${PM.left}" x2="${W - PM.right}" y1="${y}" y2="${y}"/>`);
    parts.push(`<text x="${PM.left - 6}" y="${y + 3.5}" text-anchor="end">${v.toLocaleString('en-GB')} m</text>`);
  }
  const xs = niceScale(0, view.totalKm, Math.max(2, Math.floor(pw / 90)));
  for (const v of xs.ticks) {
    if (v > view.totalKm + 1e-6) break;
    const x = xOf(v);
    if (x > W - PM.right - 24 && v !== 0) continue; // leave room for the last label
    parts.push(`<text x="${r1(x)}" y="${H - 5}" text-anchor="${v === 0 ? 'start' : 'middle'}">${v.toLocaleString('en-GB')} km</text>`);
  }

  let drawn = 0;
  for (let i = 0; i < view.segs.length; i++) {
    const seg = view.segs[i];
    const p = segmentPaths(seg, xOf, yOf, bottom);
    seg.ele = p.ele;
    drawn += p.points;
    const color = `var(--track-${seg.cat})`;
    parts.push(`<path class="p-area" d="${p.area}" fill="var(--track-${seg.cat}-wash)"/>`,
      `<path class="p-line" d="${p.line}" stroke="${color}"/>`);
  }
  // Track boundaries and names, drawn over the fills.
  for (let i = 0; i < view.segs.length; i++) {
    const seg = view.segs[i];
    const x = Math.round(xOf(seg.offset)) + 0.5;
    const next = i + 1 < view.segs.length ? xOf(view.segs[i + 1].offset) : W - PM.right;
    if (i > 0) parts.push(`<line class="p-divider" x1="${x}" x2="${x}" y1="${PM.top - 14}" y2="${bottom}"/>`);
    const label = fitLabel(seg.track.name || 'Untitled track', next - x - 8);
    if (label) parts.push(`<text class="p-track-label" x="${x + (i > 0 ? 4 : 0)}" y="${PM.top - 6}">${escapeHTML(label)}</text>`);
  }
  // Item ticks under the plot.
  const tickTop = bottom + TICK_Y;
  view.items.forEach((entry, i) => {
    const x = r1(xOf(entry.km));
    const cls = isMedia(entry.item) ? 'media' : 'note';
    parts.push(`<line class="p-tick ${cls}" data-i="${i}" x1="${x}" x2="${x}" y1="${tickTop}" y2="${tickTop + TICK_LEN}"/>`);
  });
  parts.push('<g class="p-hover" visibility="hidden">' +
    `<line class="p-cursor" x1="0" x2="0" y1="${PM.top}" y2="${bottom}"/>` +
    '<circle class="p-dot" r="4.5" cx="0" cy="0"/></g>');

  svg.setAttribute('viewBox', `0 0 ${W} ${H}`);
  svg.innerHTML = parts.join('');
  const tickEls = svg.querySelectorAll('.p-tick');
  profile.ticks = view.items.map((entry, i) => ({ entry, x: xOf(entry.km), el: tickEls[i] }));
  const g = svg.querySelector('.p-hover');
  profile.hoverEls = { g, cursor: g.firstElementChild, dot: g.lastElementChild };
  profile.geom = { W, H, pw, ph, bottom, tickTop, xOf, yOf };
  markProfileSelection();
  profile.lastRender = { ms: performance.now() - t0, points: drawn };
}

// updateProfile rebuilds the profile for the selected day.
function updateProfile() {
  if (!state.trip) return;
  profile.view = buildProfileView();
  renderProfile();
}

function markProfileSelection() {
  for (const t of profile.ticks) t.el.classList.toggle('is-selected', t.entry.item.id === state.selectedId);
}

function hideProfileHover() {
  profile.hoverEls?.g.setAttribute('visibility', 'hidden');
  els.profileReadout.hidden = true;
  els.profileSvg.classList.remove('is-over-tick');
  setHoverTick(null);
  profile.marker?.remove();
}

function setHoverTick(t) {
  if (profile.hoverTick === t) return;
  profile.hoverTick?.el.classList.remove('is-hover');
  profile.hoverTick = t;
  t?.el.classList.add('is-hover');
}

// pointAt returns the track point nearest to plot x.
function pointAt(x) {
  const { segs, totalKm } = profile.view;
  const { xOf, pw } = profile.geom;
  const km = Math.max(0, Math.min(totalKm, ((x - PM.left) / pw) * totalKm));
  let si = lowerBound(segs.length, (i) => segs[i].offset, km + 1e-9) - 1;
  si = Math.max(0, si);
  const seg = segs[si];
  const idx = nearestIndex(seg.cum.length, (i) => seg.cum[i], km - seg.offset);
  return { seg, idx, x: xOf(seg.offset + seg.cum[idx]) };
}

function nearestTick(x) {
  const ticks = profile.ticks;
  if (!ticks.length) return null;
  const i = nearestIndex(ticks.length, (k) => ticks[k].x, x);
  return Math.abs(ticks[i].x - x) <= TICK_HIT ? ticks[i] : null;
}

function readoutName(cls, text) {
  return h('span', { class: 'r-name' }, h('i', { class: cls }), text);
}

function showProfileHover(e) {
  const geom = profile.geom;
  if (!geom) return;
  const rect = els.profileSvg.getBoundingClientRect();
  const x = e.clientX - rect.left;
  const y = e.clientY - rect.top;
  const tick = y >= geom.bottom - 2 ? nearestTick(x) : null;
  setHoverTick(tick);
  els.profileSvg.classList.toggle('is-over-tick', !!tick);

  let seg;
  let idx;
  let cx;
  if (tick) {
    ({ seg, idx } = tick.entry);
    cx = tick.x;
  } else {
    ({ seg, idx, x: cx } = pointAt(x));
  }
  const p = seg.track.points[idx];
  const ele = seg.ele ? seg.ele[idx] : p[2];
  const { g, cursor, dot } = profile.hoverEls;
  const cxr = Math.round(cx) + 0.5;
  cursor.setAttribute('x1', cxr);
  cursor.setAttribute('x2', cxr);
  dot.setAttribute('cx', r1(cx));
  dot.setAttribute('cy', r1(geom.yOf(ele)));
  dot.setAttribute('fill', `var(--track-${seg.cat})`);
  g.setAttribute('visibility', 'visible');

  const ms = seg.t0 + p[3] * 1000;
  const when = state.selectedDay ? state.fmt.time(ms) : `Day ${state.dayOfItem.get(tick?.entry.item.id) ?? dayOfTrack(seg.track.id)} · ${state.fmt.time(ms)}`;
  const km = seg.offset + seg.cum[idx];
  let content;
  if (tick) {
    const it = tick.entry.item;
    const kind = { photo: 'Photo', video: 'Video', note: 'Note' }[it.kind] || it.kind;
    const text = it.title || it.caption || it.text || '';
    content = [
      h('strong', {}, state.fmt.time(it.time)), ' ', h('span', { class: 'r-sub' }, `· ${km.toFixed(1)} km · ${Math.round(ele)} m`),
      readoutName(isMedia(it) ? 'media' : 'note', kind),
      text ? h('span', { class: 'r-text' }, text.length > 80 ? `${text.slice(0, 78).trimEnd()}…` : text) : null,
    ];
  } else {
    content = [
      h('strong', {}, `${Math.round(ele).toLocaleString('en-GB')} m`), ' ',
      h('span', { class: 'r-sub' }, `· ${km.toFixed(1)} km · ${when}`),
      readoutName(seg.cat, seg.track.name || 'Untitled track'),
    ];
  }
  const ro = els.profileReadout;
  ro.replaceChildren(...content.filter(Boolean));
  ro.hidden = false;
  const w = ro.offsetWidth;
  const left = cx + 12 + w <= geom.W - 4 ? cx + 12 : Math.max(4, cx - 12 - w);
  ro.style.left = `${Math.round(left)}px`;

  // Position marker on the main map; never pans.
  const ll = [p[0], p[1]];
  if (!profile.marker) {
    map.createPane('profileHover');
    map.getPane('profileHover').style.zIndex = 640; // above markers, below popups
    map.getPane('profileHover').style.pointerEvents = 'none';
    profile.marker = L.circleMarker(ll, { pane: 'profileHover', radius: 7, color: '#fff', weight: 3, fillOpacity: 1, interactive: false });
  }
  profile.marker.setLatLng(ll);
  profile.marker.setStyle({ fillColor: trackColor(seg.track.type) });
  if (!map.hasLayer(profile.marker)) profile.marker.addTo(map);
}

function dayOfTrack(id) {
  for (const d of state.trip.days) if (d.stats.trackIds.includes(id)) return d.index;
  return '?';
}

function restoreProfileCollapsed() {
  let collapsed = false;
  try { collapsed = localStorage.getItem(PROFILE_KEY) === '1'; } catch { /* storage blocked */ }
  setProfileCollapsed(collapsed);
}

function setProfileCollapsed(collapsed) {
  els.profile.classList.toggle('is-collapsed', collapsed);
  els.profileToggle.textContent = collapsed ? '▲' : '▼';
  els.profileToggle.setAttribute('aria-expanded', String(!collapsed));
  els.profileToggle.setAttribute('aria-label', collapsed ? 'Show elevation profile' : 'Hide elevation profile');
}

function initProfile(trip) {
  for (const t of trip.tracks) if (t.points?.length) profile.cum.set(t.id, cumulativeKm(t));

  els.profileToggle.addEventListener('click', () => {
    const collapsed = !els.profile.classList.contains('is-collapsed');
    setProfileCollapsed(collapsed);
    try { localStorage.setItem(PROFILE_KEY, collapsed ? '1' : '0'); } catch { /* storage blocked */ }
    hideProfileHover();
    map.invalidateSize();
  });

  const svg = els.profileSvg;
  svg.addEventListener('pointermove', showProfileHover);
  svg.addEventListener('pointerdown', showProfileHover);
  svg.addEventListener('pointerleave', hideProfileHover);
  svg.addEventListener('click', (e) => {
    showProfileHover(e);
    const t = profile.hoverTick;
    if (!t) return;
    const id = t.entry.item.id;
    selectItem(id, { source: 'profile' });
    if (isMedia(t.entry.item)) openLightbox(id);
  });

  // Re-render at the new width; the map also resizes with the strip.
  let lastW = 0;
  let lastH = 0;
  new ResizeObserver(() => {
    const w = els.profileBody.clientWidth;
    const hgt = els.profileBody.clientHeight;
    if (w === lastW && hgt === lastH) return;
    lastW = w;
    lastH = hgt;
    clearTimeout(profile.timer);
    profile.timer = setTimeout(renderProfile, 100);
  }).observe(els.profileBody);

  updateProfile();
}

// ---------------------------------------------------------------- init

function index(trip) {
  trip.items.forEach((it, i) => {
    state.itemsById.set(it.id, it);
    state.itemIndex.set(it.id, i);
    if (isMedia(it)) {
      state.mediaIndex.set(it.id, state.media.length);
      state.media.push(it);
    }
  });
  for (const t of trip.tracks) state.tracksById.set(t.id, t);
  for (const d of trip.days) {
    state.dayByIndex.set(d.index, d);
    for (const id of d.itemIds) state.dayOfItem.set(id, d.index);
  }
}

function renderSummary(trip) {
  const km = trip.days.reduce((sum, d) => sum + (d.stats.distanceKm || 0), 0);
  const parts = [plural(trip.days.length, 'day')];
  if (km) parts.push(`${Math.round(km).toLocaleString('en-GB')} km`);
  const media = mediaCountText(mediaCounts(trip.items.map((it) => it.id)));
  if (media) parts.push(media);
  els.summary.textContent = parts.join(' · ');
}

function initSheet() {
  els.handle.addEventListener('click', () => {
    const expanded = els.timeline.classList.toggle('is-expanded');
    els.handle.setAttribute('aria-expanded', String(expanded));
    els.handle.setAttribute('aria-label', expanded ? 'Collapse timeline' : 'Expand timeline');
  });
  els.timeline.addEventListener('transitionend', () => map.invalidateSize());
  narrowQuery.addEventListener('change', () => map.invalidateSize());
}

function initKeys() {
  document.addEventListener('keydown', (e) => {
    if (e.altKey || e.ctrlKey || e.metaKey || e.shiftKey) return;
    if (e.target.closest?.('input, textarea, select, [contenteditable]')) return;
    if (e.key === 'Escape' && state.lightboxId) { e.preventDefault(); closeLightbox(); }
    else if (e.key === 'ArrowRight') { e.preventDefault(); step(1); }
    else if (e.key === 'ArrowLeft') { e.preventDefault(); step(-1); }
  });
}

async function main() {
  let trip;
  try {
    const res = await fetch('trip.json', { cache: 'no-cache' });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    trip = await res.json();
  } catch (err) {
    els.scroll.replaceChildren(h('p', { class: 'error' }, `Could not load trip.json (${err.message}). ` +
      'Open the site through a web server, e.g. "touring-diary serve".'));
    throw err;
  }
  state.trip = trip;
  state.fmt = makeFormatters(trip.timezone);
  document.title = trip.title || 'Touring diary';
  els.title.textContent = trip.title || 'Touring diary';
  index(trip);
  renderSummary(trip);

  restoreProfileCollapsed(); // before the map, so its first fit has the final size
  initMap(trip);
  renderChips(trip);
  renderTimeline(trip);
  updateChips();
  initScrollSpy();
  initSheet();
  initKeys();
  initLightbox();
  initProfile(trip);
  applyHash();
  window.addEventListener('hashchange', () => { if (location.hash !== lastHash) applyHash(); });
  window.addEventListener('popstate', onPopState);
  // Handle for debugging from the console.
  window.touringDiary = { map, state, selectItem, selectDay, openLightbox, closeLightbox, profile };
}

main();
