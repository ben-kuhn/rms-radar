/* RMS Radar frontend — vanilla JS + Leaflet. No build step. */
"use strict";

const state = {
  stations: [],
  serviceCodes: [],      // [{code, count}]
  config: {},
  conditions: null,
  ranked: new Map(),     // callsign -> best ranked entry
  rankSeq: 0,            // generation guard for in-flight rank requests
  rankAbort: null,       // AbortController for the current rank request
  markers: new Map(),    // callsign -> {base, ranked} Leaflet markers
  expanded: new Set(),   // callsigns whose full channel list is shown
  filters: { serviceCode: "", mode: "", band: "" },
  source: "winlink", // "winlink" or "radiopost"
  theme: "dark",
  frequencyDisplay: "dial", // "dial" or "center"
};

const $ = (id) => document.getElementById(id);

// Bump the version to show the notice again after its wording changes.
const NOTICE_KEY = "rmsradar-notice-v1";

// Mobile map/list switcher.
const MOBILE_VIEW_KEY = "rmsradar-mobile-view";
const FREQUENCY_KEY = "rmsradar-frequency-display";
const SOURCE_KEY = "rmsradar-source";
const MOBILE_QUERY = window.matchMedia("(max-width: 720px)");

// Desktop (double-click) mode: main opens /?desktop=<token>.
const DESKTOP_TOKEN = new URLSearchParams(location.search).get("desktop");

// Admin token for service-code edits on a self-hosted server. Kept per-tab
// only; never in localStorage or a URL.
const ADMIN_TOKEN_KEY = "rmsradar-admin-token";

const BAND_ORDER = ["160m","80m","60m","40m","30m","20m","17m","15m","12m","10m","6m","2m","1.25m","70cm","33cm","23cm","HF","Other"];

// VHF/UHF starts at 30 MHz (6 m and up). For these line-of-sight bands a 0%
// score means the path is implausible; on HF a 0% can be a temporary/forecast
// condition, so HF stations are never dropped.
const VHF_MIN_HZ = 30e6;
const VHF_BANDS = new Set(["6m", "2m", "1.25m", "70cm", "33cm", "23cm"]);
const isHfChannel = (c) => !(c.frequency_hz >= VHF_MIN_HZ || VHF_BANDS.has(c.band));
const MODE_ORDER = ["VARA","VARA HF","VARA FM","Mercury","VardeFM","MODEM73","DATA2G","IONHF","XARA","Aether","Aether FM","ARDOP","AX.25","Packet","PACTOR","Robust Packet","WINMOR","Other"];

/* ---------- theme ---------- */
function applyTheme(theme) {
  state.theme = theme;
  document.documentElement.dataset.theme = theme;
  try { localStorage.setItem("rmsradar-theme", theme); } catch (_) {}
  $("theme-btn").textContent = theme === "dark" ? "☀" : "◐";
}

/* ---------- frequency display ---------- */
function applyFrequencyDisplay(mode, rerender = true) {
  state.frequencyDisplay = (mode === "center" && state.source !== "radiopost") ? "center" : "dial";
  for (const input of document.querySelectorAll('input[name="frequency-display"]')) {
    input.checked = input.value === state.frequencyDisplay;
  }
  try { localStorage.setItem(FREQUENCY_KEY, state.frequencyDisplay); } catch (_) {}
  if (rerender && map) {
    map.closePopup();
    applyFilters();
  }
}

function syncFrequencyAvailability() {
  const center = $("freq-center");
  const label = $("freq-center-label");
  const available = state.source === "winlink";
  if (center) center.disabled = !available;
  if (label) {
    label.title = available
      ? ""
      : "RadioPost publishes dial frequencies only; no center frequency is provided";
  }
  if (!available && state.frequencyDisplay === "center") {
    applyFrequencyDisplay("dial", false);
  }
}

function channelFrequency(c) {
  if (state.frequencyDisplay === "center" && c.center_frequency) return c.center_frequency;
  return c.frequency || fmtHz(c.frequency_hz);
}

function applySource(source, rerender = true) {
  state.source = source === "radiopost" ? "radiopost" : "winlink";
  document.documentElement.dataset.source = state.source;
  const sel = $("source");
  if (sel) sel.value = state.source;
  state.filters = { serviceCode: "", mode: "", band: "" };
  for (const id of ["service-code", "mode", "band"]) {
    if ($(id)) $(id).value = "";
  }
  state.rankSeq++;
  if (state.rankAbort) {
    state.rankAbort.abort();
    state.rankAbort = null;
  }
  state.ranked.clear();
  try { localStorage.setItem(SOURCE_KEY, state.source); } catch (_) {}
  syncFrequencyAvailability();
  if (rerender) {
    load().catch((err) => {
      $("status").textContent = "failed to load: " + err.message;
    });
  }
}

/* ---------- map ---------- */
let map, markerLayer, rankLayer, baseTiles;

const DEFAULT_TILES = "https://tile.openstreetmap.org/{z}/{x}/{y}.png";
const TILE_ATTRIBUTION = '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors';

function initMap() {
  map = L.map("map", { zoomControl: true, worldCopyJump: true }).setView([20, 0], 2);
  markerLayer = L.layerGroup().addTo(map);
  rankLayer = L.layerGroup().addTo(map);
}

// Keyless OSM tiles; dark mode is a CSS filter on the tile pane (see style.css).
function applyTileLayer() {
  if (!map) return;
  const url = (state.config && state.config.tile_url) || DEFAULT_TILES;
  if (baseTiles) map.removeLayer(baseTiles);
  baseTiles = L.tileLayer(url, { attribution: TILE_ATTRIBUTION, maxZoom: 19 }).addTo(map);
  baseTiles.on("tileerror", (event) => {
    const tile = event.tile;
    if (!tile) return;
    const tries = Number(tile.dataset.tileRetries || "0");
    if (tries >= 2) return;
    const src = tile.getAttribute("src");
    if (!src || src.startsWith("data:")) return;
    tile.dataset.tileRetries = String(tries + 1);
    // A zoom can outrun the tile scheduler or hit a transient upstream/rate
    // limit. Leaflet does not retry failed <img> tiles by itself, so re-request
    // this one after the token bucket has had time to refill.
    setTimeout(() => {
      if (!tile.isConnected) return;
      tile.removeAttribute("src");
      tile.src = src;
    }, 1000 * (tries + 1));
  });
}

/* ---------- helpers ---------- */
async function fetchJSON(url, opts) {
  const res = await fetch(url, opts);
  if (!res.ok) throw new Error(`${url} -> ${res.status}`);
  return res.json();
}

async function fetchWithHeaders(url, opts) {
  const res = await fetch(url, opts);
  if (!res.ok) throw new Error(`${url} -> ${res.status}`);
  return { data: await res.json(), headers: res.headers };
}

function freshnessColor(hours) {
  if (hours == null || hours < 0) return "#8b949e";
  if (hours < 2) return getCSS("--green");
  if (hours < 4) return getCSS("--yellow");
  return getCSS("--red");
}

function getCSS(name) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || "#999";
}

function qualityBadge(q) {
  if (q == null) return "";
  const cls = q >= 70 ? "q-good" : q >= 40 ? "q-fair" : "q-poor";
  return `<span class="q-badge ${cls}">${q}%</span>`;
}

function modeDisplay(station) {
  // Return a readable "mode(s)" string for the station.
  const modes = [...new Set(station.channels.map((c) => c.mode))].slice(0, 3);
  return modes.join(" / ");
}

// True when channel c is the one the per-station best rank entry came from.
function isRankedChannel(c, r) {
  if (!r) return false;
  return c.frequency_hz === r.frequency_hz &&
    c.mode === r.mode &&
    (c.service_code || "PUBLIC") === (r.service_code || "PUBLIC");
}

// Community-modem labels replace the API's protocol text; keep
// the reported mode available as a hover hint.
function modeTitle(c) {
  return c.raw_mode && c.raw_mode !== c.mode ? ` title="Winlink reports ${esc(c.raw_mode)}"` : "";
}

// Winlink's OperatingHours is free-form: most gateways use UTC hour ranges
// such as "00-23" (all day), "06-04" (overnight), or "00-01,14-23" (split
// schedule), but some publish clock times or other schedule text. Normalize the
// common forms without inventing a meaning for values we do not recognize.
function hoursLabel(hours) {
  const raw = String(hours || "").trim();
  if (!raw) return "";
  if (/^(00?-23|00?-24|24|24\/7)$/.test(raw)) return "24h";
  if (/^\d{1,2}:\d{2}\s*-\s*\d{1,2}:\d{2}$/.test(raw)) {
    return raw.replace(/\s*-\s*/, "–") + " UTC";
  }
  if (/^\d{4}\/\d{4}$/.test(raw)) {
    return `${raw.slice(0, 2)}:${raw.slice(2, 4)}–${raw.slice(5, 7)}:${raw.slice(7, 9)} UTC`;
  }
  if (/^\d{1,2}\s*-\s*\d{1,2}(?:\s*,\s*\d{1,2}\s*-\s*\d{1,2})*$/.test(raw)) {
    return raw.replace(/\s*-\s*/g, "–") + " UTC";
  }
  return raw;
}

function hoursHTML(c) {
  const raw = String(c.operating_hours || "").trim();
  const label = hoursLabel(raw);
  if (!label) return "";
  const title = /^\d{1,2}:\d{2}\s*-\s*\d{1,2}:\d{2}$|^\d{4}\/\d{4}$|^\d{1,2}\s*-\s*\d{1,2}/.test(raw)
    ? "Operating hours (UTC)"
    : "Operating hours (as reported by Winlink)";
  return `<span class="ch-hours" title="${title}">${esc(label)}</span>`;
}

function scanHTML(c) {
  return c.scan_s > 0
    ? `<span class="ch-scan" title="Seconds a caller must keep calling while this gateway scans">${c.scan_s}s scan</span>`
    : "";
}

/* ---------- data ---------- */
async function load() {
  const config = await fetchJSON("/api/config");
  state.config = config;
  updateCodesAvailability();
  updateDocsLinks();
  applyTileLayer();

  if (state.source === "radiopost") {
    const [gateways, conditions] = await Promise.all([
      fetchJSON("/api/radiopost/gateways"),
      fetchJSON("/api/conditions").catch(() => null),
    ]);
    state.serviceCodes = [];
    state.stations = gateways || [];
    state.conditions = conditions;
    state.ranked.clear();
    $("status").textContent = `${state.stations.length} RadioPost gateways`;
    populateServiceCodes();
    populateBandMode();
    renderConditions();
    applyFilters();
    return;
  }

  const [serviceCodes, gateways, conditions] = await Promise.all([
    fetchJSON("/api/servicecodes"),
    fetchJSON("/api/gateways"),
    fetchJSON("/api/conditions").catch(() => null),
  ]);
  state.serviceCodes = serviceCodes;
  state.stations = gateways || [];
  state.conditions = conditions;

  $("status").textContent =
    `${state.stations.length} gateways · ` + (conditions ? "conditions ready" : "no conditions");
  populateServiceCodes();
  populateBandMode();
  renderConditions();
  applyFilters();
}

function populateServiceCodes() {
  const sel = $("service-code");
  sel.innerHTML = "";
  const all = document.createElement("option");
  all.value = "";
  all.textContent = "All service codes";
  sel.appendChild(all);

  const counts = new Map(state.serviceCodes.map((s) => [s.code, s.count]));
  // Only offer codes that actually appear in the gateway list, so an
  // opt-in code (e.g. MERCURY) shows up only once nodes use it.
  const codes = new Set(counts.keys());
  for (const code of codes) {
    const opt = document.createElement("option");
    opt.value = code;
    const n = counts.get(code);
    opt.textContent = code + (n != null ? ` (${n})` : "");
    sel.appendChild(opt);
  }
  if (state.filters.serviceCode && !counts.has(state.filters.serviceCode)) {
    state.filters.serviceCode = "";
  }
  sel.value = state.filters.serviceCode;
}

function populateBandMode() {
  const bands = new Set();
  const modes = new Set();
  for (const st of state.stations) {
    for (const ch of st.channels) {
      if (ch.band) bands.add(ch.band);
      if (ch.mode_family) modes.add(ch.mode_family);
    }
  }

  const bandSel = $("band");
  bandSel.innerHTML = "";
  bandSel.appendChild(opt("", "All bands"));
  const orderedBands = BAND_ORDER.filter((b) => bands.has(b)).concat(
    [...bands].filter((b) => !BAND_ORDER.includes(b)).sort());
  for (const b of orderedBands) bandSel.appendChild(opt(b, b));

  const modeSel = $("mode");
  modeSel.innerHTML = "";
  modeSel.appendChild(opt("", "All modes"));
  const orderedModes = MODE_ORDER.filter((m) => modes.has(m)).concat(
    [...modes].filter((m) => !MODE_ORDER.includes(m)).sort());
  for (const m of orderedModes) modeSel.appendChild(opt(m, m));
}

function opt(value, text) {
  const o = document.createElement("option");
  o.value = value;
  o.textContent = text;
  return o;
}

/* ---------- filtering + rendering ---------- */
function filteredStations() {
  const { serviceCode, mode, band } = state.filters;
  const out = [];
  for (const st of state.stations) {
    const channels = st.channels.filter((c) => {
      if (serviceCode && c.service_code !== serviceCode) return false;
      if (mode && c.mode_family !== mode) return false;
      if (band && c.band !== band) return false;
      return true;
    });
    if (channels.length) out.push({ ...st, channels });
  }
  return out;
}

function applyFilters() {
  let stations = filteredStations();
  if (state.ranked.size) {
    // Ranking is active. For VHF/UHF (line-of-sight) hide 0% and unscored
    // stations so the list/map only show plausibly-workable paths. HF stations
    // are kept regardless of score (0% may just be a temporary condition).
    stations = stations.filter((st) => {
      const hfCapable = st.channels.some(isHfChannel);
      const r = state.ranked.get(st.callsign);
      if (!r) return hfCapable;            // unscored VHF/UHF -> hide
      return r.link_quality > 0 || hfCapable; // 0% VHF/UHF -> hide unless HF-capable
    });
  }
  $("count").textContent = `${stations.length}`;
  renderMarkers(stations);
  renderList(stations);
}

function renderMarkers(stations) {
  markerLayer.clearLayers();
  rankLayer.clearLayers();
  state.markers.clear();

  for (const st of stations) {
    if (!st.latitude && !st.longitude) continue;
    const color = freshnessColor(st.hours_since_status);
    const marker = L.circleMarker([st.latitude, st.longitude], {
      radius: 6,
      color: "#00000055",
      weight: 1,
      fillColor: color,
      fillOpacity: 0.9,
    });
    marker.bindPopup(popupHTML(st), { maxWidth: 320 });
    markerLayer.addLayer(marker);
    const entry = { base: marker };

    // Ranked highlight (larger, white-ringed, colored by quality).
    const r = state.ranked.get(st.callsign);
    if (r && $("rank-mode").checked) {
      const rc = r.link_quality >= 70 ? getCSS("--green") : r.link_quality >= 40 ? getCSS("--yellow") : getCSS("--red");
      const rm = L.circleMarker([st.latitude, st.longitude], {
        radius: 11,
        color: "#ffffff",
        weight: 1.5,
        fillColor: rc,
        fillOpacity: 0.85,
      });
      rm.bindPopup(popupHTML(st, r), { maxWidth: 320 });
      rankLayer.addLayer(rm);
      entry.ranked = rm;
    }
    state.markers.set(st.callsign, entry);
  }
}

function popupHTML(st, rank) {
  const channels = st.channels.map((c) =>
    `<div class="ch-row${rank && isRankedChannel(c, rank) ? " ch-best" : ""}"><span class="ch-mode"${modeTitle(c)}>${esc(c.mode)}</span>` +
    `<span class="ch-freq">${esc(channelFrequency(c))}</span>` +
    hoursHTML(c) +
    scanHTML(c) +
    `<span class="ch-sc">${esc(c.service_code || "PUBLIC")}</span></div>`).join("");
  const rankLine = rank
    ? `<div><b>Link quality:</b> ${rank.link_quality}% · <span class="best-band">${esc(rank.band)} ${esc(rank.mode)}</span> · ${rank.distance_km.toFixed(0)} km · ${rank.band_open ? "open" : "closed"}` +
      (rank.muf_mhz ? ` · MUF ${rank.muf_mhz.toFixed(1)} MHz` : "") +
      (rank.note ? ` · ${esc(rank.note)}` : "") + `</div>`
    : "";
  return `<div><b>${esc(st.callsign)}</b></div>` +
    `<div class="station-meta">${esc(st.gridsquare || firstGrid(st))} · ${freshText(st.hours_since_status)}</div>` +
    `<div style="margin-top:4px">${channels}</div>` +
    rankLine +
    (st.comments ? `<div class="station-meta">${esc(st.comments)}</div>` : "");
}

function firstGrid(st) {
  return st.channels[0]?.gridsquare || "";
}

function fmtHz(hz) {
  if (!hz) return "";
  if (hz >= 1e6) return (hz / 1e6).toFixed(4).replace(/0+$/, "").replace(/\.$/, "") + " MHz";
  if (hz >= 1e3) return (hz / 1e3).toFixed(1) + " kHz";
  return hz + " Hz";
}

function freshText(h) {
  if (h == null || h < 0) return "unknown";
  return `${h}h ago`;
}

function renderList(stations) {
  const list = $("list");
  list.innerHTML = "";

  const sorted = [...stations];
  if (state.ranked.size) {
    sorted.sort((a, b) => {
      const qa = state.ranked.get(a.callsign)?.link_quality ?? -1;
      const qb = state.ranked.get(b.callsign)?.link_quality ?? -1;
      if (qa !== qb) return qb - qa;
      return a.callsign.localeCompare(b.callsign);
    });
  } else {
    sorted.sort((a, b) => a.callsign.localeCompare(b.callsign));
  }

  if (!sorted.length) {
    list.innerHTML = `<li class="empty">No gateways match the current filters.</li>`;
    return;
  }

  for (const st of sorted) {
    const li = document.createElement("li");
    const r = state.ranked.get(st.callsign);
    const fc = st.hours_since_status == null || st.hours_since_status < 0 ? "fresh-gray"
      : st.hours_since_status < 2 ? "fresh-green"
      : st.hours_since_status < 4 ? "fresh-yellow" : "fresh-red";
    // Put the best-ranked channel first so it stays visible when the station has
    // more channels than the list shows.
    let chans = st.channels;
    if (r) {
      const bi = chans.findIndex((c) => isRankedChannel(c, r));
      if (bi > 0) chans = [chans[bi], ...chans.slice(0, bi), ...chans.slice(bi + 1)];
    }
    const expanded = state.expanded.has(st.callsign);
    const shown = expanded ? chans : chans.slice(0, 4);
    li.innerHTML =
      `<div style="display:flex;align-items:center">` +
        `<span class="fresh ${fc}"></span>` +
        `<span class="station-call">${esc(st.callsign)}<span class="sq">${esc(firstGrid(st))}</span></span>` +
        (r ? qualityBadge(r.link_quality) : "") +
      `</div>` +
      `<div class="station-meta">` +
        (r ? `<span class="best-band" title="Highest-ranked band and mode">★ ${esc(r.band)} ${esc(r.mode)}</span>` : esc(modeDisplay(st))) +
        ` · ${freshText(st.hours_since_status)}` +
        (r ? ` · ${r.distance_km.toFixed(0)} km` : "") +
        (r && r.note ? ` · ${esc(r.note)}` : "") + `</div>` +
      `<div class="station-channels">${shown.map((c) =>
        `<div class="ch-row${r && isRankedChannel(c, r) ? " ch-best" : ""}"><span class="ch-mode"${modeTitle(c)}>${esc(c.mode)}</span><span class="ch-freq">${esc(channelFrequency(c))}</span>${hoursHTML(c)}${scanHTML(c)}<span class="ch-sc">${esc(c.service_code || "PUBLIC")}</span></div>`).join("")}` +
        (chans.length > 4
          ? `<button type="button" class="more-ch">${expanded ? "Show less" : `+${chans.length - 4} more`}</button>`
          : "") +
      `</div>`;
    const more = li.querySelector(".more-ch");
    if (more) {
      more.addEventListener("click", (e) => {
        e.stopPropagation();
        if (expanded) state.expanded.delete(st.callsign);
        else state.expanded.add(st.callsign);
        applyFilters();
      });
    }
    li.addEventListener("click", () => {
      if (!st.latitude && !st.longitude) return;
      const focus = () => {
        map.setView([st.latitude, st.longitude], Math.max(map.getZoom(), 8));
        const m = state.markers.get(st.callsign);
        const marker = m && (m.ranked || m.base);
        if (marker) marker.openPopup();
      };
      if (MOBILE_QUERY.matches && document.documentElement.dataset.mobileView !== "map") {
        setMobileView("map");
        requestAnimationFrame(() => { map.invalidateSize(); focus(); });
      } else {
        focus();
      }
    });
    list.appendChild(li);
  }
}

function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

/* ---------- ranking ---------- */
async function doRank() {
  const grid = $("grid").value.trim();
  if (!grid) {
    setNote("Enter your 4- or 6-character grid locator to rank.");
    $("grid").focus();
    return;
  }
  const req = {
    grid,
    band: $("band").value,
    mode: $("mode").value,
    service_code: $("service-code").value,
    service: state.source,
  };
  const seq = ++state.rankSeq;
  if (state.rankAbort) state.rankAbort.abort();
  const ac = new AbortController();
  state.rankAbort = ac;
  setNote("Ranking…");
  try {
    const { data: results, headers } = await fetchWithHeaders("/api/rank", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(req),
      signal: ac.signal,
    });
    if (seq !== state.rankSeq) return; // superseded by a filter change/new rank
    state.ranked = new Map();
    for (const r of results) {
      if (!state.ranked.has(r.callsign) || state.ranked.get(r.callsign).link_quality < r.link_quality) {
        state.ranked.set(r.callsign, r);
      }
    }
    let msg = `${results.length} candidate${results.length === 1 ? "" : "s"} ranked for ` +
      `${req.band || "all bands"} ${req.mode || "all modes"} from ${grid}.`;
    if (headers.get("X-Rank-Truncated") === "true") msg += " (capped)";
    const unscored = parseInt(headers.get("X-Rank-Errors") || "0", 10);
    if (unscored > 0) msg += ` · ${unscored} unscored`;
    if (!state.config.voacap) {
      msg += " · point-to-point VOACAP forecasts off (install voacapl or set -voacap-url).";
    }
    setNote(msg);
    applyFilters();
  } catch (err) {
    if (ac.signal.aborted || seq !== state.rankSeq) return;
    setNote("Ranking failed: " + err.message);
  }
}

function clearRank() {
  state.rankSeq++;
  if (state.rankAbort) {
    state.rankAbort.abort();
    state.rankAbort = null;
  }
  state.ranked = new Map();
  setNote("");
  applyFilters();
}

/* ---------- conditions ---------- */
function renderConditions() {
  const c = state.conditions;
  const bar = $("conditions");
  if (!c) { bar.hidden = true; return; }
  bar.hidden = false;
  $("sfi").textContent = c.sfi || "–";
  $("a").textContent = c.a_index || "–";
  $("k").textContent = c.k_index || "–";
  $("cond-updated").textContent = c.updated ? new Date(c.updated).toLocaleString() : "–";
  $("cond-stale").hidden = !c.stale;
}

async function refreshGateways() {
  const btn = $("refresh-btn");
  if (btn) btn.disabled = true;
  setNote(state.source === "radiopost" ? "Refreshing RadioPost directory…" : "Refreshing gateway list…");
  try {
    if (state.source === "radiopost") {
      await fetchJSON("/api/radiopost/refresh", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: "{}",
      });
      setNote("RadioPost directory refreshed.");
      await load();
      return;
    }
    const res = await fetchJSON("/api/refresh", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: "{}",
    });
    if (res.status === "refreshing") {
      setNote("A refresh is already running; reloading shortly.");
      setTimeout(() => load().catch(() => {}), 6000);
    } else {
      setNote("Gateway list refreshed.");
      await load();
    }
  } catch (err) {
    setNote(err.message.includes("429")
      ? "Gateway list was refreshed recently; try again in a few minutes."
      : "Refresh failed: " + err.message);
  } finally {
    if (btn) btn.disabled = false;
  }
}

function setNote(msg) {
  const el = $("cond-note");
  el.textContent = msg || "";
  $("conditions").hidden = !(msg || state.conditions);
}

/* ---------- notice ---------- */
function initNotice() {
  const el = $("notice");
  if (!el) return;
  try {
    if (localStorage.getItem(NOTICE_KEY)) return;
  } catch (_) {}
  el.hidden = false;
  $("notice-close").addEventListener("click", () => {
    el.hidden = true;
    try { localStorage.setItem(NOTICE_KEY, "1"); } catch (_) {}
    // The map grows into the freed space; let Leaflet re-measure it.
    if (map) map.invalidateSize();
  });
}

/* ---------- mobile map/list tabs ---------- */
function setMobileView(view) {
  if (view !== "list") view = "map";
  document.documentElement.dataset.mobileView = view;
  for (const btn of document.querySelectorAll(".viewtab")) {
    btn.setAttribute("aria-selected", String(btn.dataset.view === view));
  }
  try { localStorage.setItem(MOBILE_VIEW_KEY, view); } catch (_) {}
  if (view === "map" && map) requestAnimationFrame(() => map.invalidateSize());
}

function initMobileTabs() {
  let view = "map";
  try {
    if (localStorage.getItem(MOBILE_VIEW_KEY) === "list") view = "list";
  } catch (_) {}
  setMobileView(view);

  const tabs = [...document.querySelectorAll(".viewtab")];
  for (const btn of tabs) {
    btn.addEventListener("click", () => setMobileView(btn.dataset.view));
    btn.addEventListener("keydown", (e) => {
      if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
      e.preventDefault();
      const next = tabs[(tabs.indexOf(btn) + (e.key === "ArrowRight" ? 1 : -1) + tabs.length) % tabs.length];
      next.focus();
      setMobileView(next.dataset.view);
    });
  }
}

/* ---------- desktop session (double-click mode) ---------- */
function initDesktopSession() {
  const quitBtn = $("quit-btn");
  if (!DESKTOP_TOKEN) return;
  if (quitBtn) quitBtn.hidden = false;

  const id = (window.crypto && crypto.randomUUID)
    ? crypto.randomUUID()
    : `${Date.now()}-${Math.random().toString(16).slice(2)}`;
  const url = (op) =>
    `/api/desktop/${op}?token=${encodeURIComponent(DESKTOP_TOKEN)}&id=${encodeURIComponent(id)}`;
  const hello = () => fetch(url("hello"), { method: "POST", keepalive: true }).catch(() => {});
  const beat = () => fetch(url("beat"), { method: "POST", keepalive: true }).catch(() => {});
  const bye = () => { try { navigator.sendBeacon(url("bye")); } catch (_) {} };

  let timer = null;
  const startBeat = () => { if (!timer) timer = setInterval(beat, 5000); };
  const stopBeat = () => { if (timer) { clearInterval(timer); timer = null; } };

  hello();
  startBeat();

  window.addEventListener("pagehide", (e) => {
    if (e.persisted) return; // bfcache: the page may come back
    stopBeat();
    bye();
  });
  window.addEventListener("pageshow", (e) => {
    if (!e.persisted) return;
    hello();
    startBeat();
  });

  if (quitBtn) {
    quitBtn.addEventListener("click", async () => {
      stopBeat();
      try { await fetch(url("quit"), { method: "POST", keepalive: true }); } catch (_) {}
      document.body.innerHTML =
        '<div class="doc"><h1>RMS Radar has stopped</h1><p>You can close this tab.</p></div>';
    });
  }
}

/* ---------- documentation links ---------- */
function updateDocsLinks() {
  if (!state.config.desktop) return;
  for (const page of ["clients", "gateway"]) {
    const link = $(`${page}-link`);
    if (!link) continue;
    const url = `https://rmsradar.ku0hn.radio/${page}.html`;
    link.href = url;
    link.removeAttribute("target");
    link.rel = "noopener noreferrer";
    link.onclick = async (e) => {
      e.preventDefault();
      try {
        await fetchJSON("/api/desktop/open", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ page }),
        });
      } catch (err) {
        setNote("Could not open the setup guide: " + err.message);
      }
    };
  }
}

/* ---------- service-code configuration ---------- */
function adminToken() {
  try { return sessionStorage.getItem(ADMIN_TOKEN_KEY) || ""; } catch (_) { return ""; }
}

function setAdminToken(token) {
  try {
    if (token) sessionStorage.setItem(ADMIN_TOKEN_KEY, token);
    else sessionStorage.removeItem(ADMIN_TOKEN_KEY);
  } catch (_) {}
}

function updateCodesAvailability() {
  const btn = $("codes-btn");
  if (!btn) return;
  btn.hidden = !state.config.service_codes_editable;
  const auth = $("codes-auth");
  const hint = $("codes-auth-hint");
  const required = !!state.config.service_codes_token_required;
  if (auth) auth.hidden = !required;
  if (hint) hint.hidden = !required;
  const input = $("code-admin-token");
  if (required && input && !input.value) input.value = adminToken();
}

async function refreshCodesList() {
  const cfg = await fetchJSON("/api/servicecodes/config");
  const list = $("codes-list");
  list.innerHTML = "";
  for (const code of cfg.codes) {
    const row = document.createElement("div");
    row.className = "codes-row";
    const label = document.createElement("span");
    label.textContent = code;
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "btn";
    btn.textContent = "Remove";
    btn.addEventListener("click", () => changeCodes({ remove: code }));
    row.append(label, btn);
    list.appendChild(row);
  }
}

async function openCodesDialog() {
  $("codes-status").textContent = "";
  updateCodesAvailability();
  await refreshCodesList();
  const dlg = $("codes-dialog");
  if (typeof dlg.showModal === "function") dlg.showModal();
  else dlg.setAttribute("open", "");
  $("code-input").focus();
}

async function changeCodes(body) {
  const status = $("codes-status");
  status.textContent = "Updating and refreshing…";
  try {
    const headers = { "Content-Type": "application/json" };
    const token = adminToken();
    if (token) headers["X-RMS-Radar-Admin"] = token;
    const res = await fetchJSON("/api/servicecodes/config", {
      method: "POST",
      headers,
      body: JSON.stringify(body),
    });
    status.textContent = res.warning || (res.refresh ? "Updated; refresh started." : "Updated.");
    await load();
    await refreshCodesList();
  } catch (err) {
    status.textContent = err.message.includes("403")
      ? "Admin token required or invalid."
      : "Update failed: " + err.message;
  }
}

function initCodesDialog() {
  $("codes-btn").addEventListener("click", () => {
    openCodesDialog().catch((err) => setNote("Could not open service codes: " + err.message));
  });
  $("code-add").addEventListener("click", () => {
    const input = $("code-input");
    const code = input.value.trim();
    if (!code) return;
    input.value = "";
    changeCodes({ add: code });
  });
  $("code-input").addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      $("code-add").click();
    }
  });
  const saveToken = () => {
    const input = $("code-admin-token");
    if (!input) return;
    setAdminToken(input.value.trim());
    $("codes-status").textContent = input.value.trim()
      ? "Admin token saved for this tab."
      : "Admin token cleared.";
  };
  $("code-admin-save").addEventListener("click", saveToken);
  $("code-admin-token").addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      saveToken();
    }
  });
  $("code-defaults").addEventListener("click", () => {
    if (!state.config.default_service_codes) return;
    changeCodes({ codes: state.config.default_service_codes });
  });
}

/* ---------- events ---------- */
function wireEvents() {
  $("source").addEventListener("change", (e) => applySource(e.target.value));
  $("service-code").addEventListener("change", (e) => {
    state.filters.serviceCode = e.target.value;
    clearRank();
  });
  $("mode").addEventListener("change", (e) => { state.filters.mode = e.target.value; clearRank(); });
  $("band").addEventListener("change", (e) => { state.filters.band = e.target.value; clearRank(); });
  $("grid").addEventListener("change", () => { $("grid").value = $("grid").value.trim().toUpperCase(); });
  $("rank-btn").addEventListener("click", doRank);
  $("refresh-btn").addEventListener("click", refreshGateways);
  $("rank-mode").addEventListener("change", applyFilters);
  $("theme-btn").addEventListener("click", () => applyTheme(state.theme === "dark" ? "light" : "dark"));
  for (const input of document.querySelectorAll('input[name="frequency-display"]')) {
    input.addEventListener("change", () => {
      if (input.checked) applyFrequencyDisplay(input.value);
    });
  }
}

/* ---------- boot ---------- */
(async function boot() {
  let theme = "dark";
  try { theme = localStorage.getItem("rmsradar-theme") || "dark"; } catch (_) {}
  applyTheme(theme);
  let frequencyDisplay = "dial";
  try { frequencyDisplay = localStorage.getItem(FREQUENCY_KEY) || "dial"; } catch (_) {}
  applyFrequencyDisplay(frequencyDisplay, false);
  let source = "winlink";
  try { source = localStorage.getItem(SOURCE_KEY) || "winlink"; } catch (_) {}
  applySource(source, false);
  initNotice();
  initMap();
  initMobileTabs();
  initDesktopSession();
  wireEvents();
  initCodesDialog();
  try {
    await load();
  } catch (err) {
    $("status").textContent = "failed to load: " + err.message;
    setNote("Could not load data — is the server running?");
  }
})();
