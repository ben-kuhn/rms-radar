# Self-hosting RMS Radar

Run RMS Radar on a home server, a club LAN, or a network-wide host. The
`rms-radar` binary serves the web UI and JSON API; the desktop app is a
separate binary for local use.

> RMS Radar is unofficial and not affiliated with the Winlink Development Team
> or ARSFI. Gateway data comes from Winlink's public API. The official list is
> at [winlink.org/RMSChannels](https://winlink.org/RMSChannels).

## Docker (recommended)

The image contains the web server and the VOACAP engine (`voacapl` + ITSHFBC
data on Debian trixie). It runs as an unprivileged user with a healthcheck and
can run with a read-only root filesystem.

```yaml
name: rms-radar

services:
  rms-radar:
    image: ghcr.io/ben-kuhn/rms-radar:latest
    container_name: rms-radar
    restart: unless-stopped
    ports:
      - "127.0.0.1:8079:8079"
    volumes:
      - rms-radar-data:/data
    environment:
      TZ: UTC
      RMSRADAR_SERVICE_CODES: "PUBLIC,MERCURY,VARDEFM,MODEM73,DATA2G,IONHF,XARA,AETHER"
    read_only: true
    tmpfs:
      - /tmp:size=64m
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL

volumes:
  rms-radar-data:
```

```bash
docker compose up -d
curl -fsS http://127.0.0.1:8079/api/health
```

The repository's [`compose.yaml`](compose.yaml) is the same setup. The default
publish is loopback-only; put Caddy/nginx/Traefik or a Cloudflare Tunnel in
front for remote access. Set `RMSRADAR_ADMIN_TOKEN` to enable service-code
editing from the UI (or use the API header), and set any other flag as an
environment variable using the `RMSRADAR_` prefix.

Update with `docker compose pull && docker compose up -d`. The `/data` volume
holds the gateway cache, tiles, conditions, and the saved service-code
selection; back it up if you care about offline startup.

**Image tags:** `latest`, `1.0`, `1.0.12`, and `edge` (main). Images are
multi-arch for linux/amd64 and linux/arm64. The container is also built and
smoke-tested in CI.

## Quick start (Linux server)

**Debian / Ubuntu**

```bash
sudo apt install ./rms-radar_<version>_amd64.deb
sudo systemctl enable --now rms-radar
```

**Fedora / RHEL**

```bash
sudo dnf install ./rms-radar-<version>-1.*.x86_64.rpm
sudo systemctl enable --now rms-radar
```

**Arch Linux**

```bash
sudo pacman -U rms-radar-<version>-1-x86_64.pkg.tar.zst
sudo systemctl enable --now rms-radar
```

or build the included `packaging/arch/PKGBUILD`.

**NixOS**

```nix
services.rms-radar = {
  enable = true;
  listenAddress = "0.0.0.0";
  port = 8079;
  serviceCodes = [ "PUBLIC" "MERCURY" "VARDEFM" "MODEM73" "DATA2G" "IONHF" "XARA" "AETHER" ];
};
```

See [`nix-ham-packages`](https://github.com/ben-kuhn/nix-ham-packages) for the
overlay and all module options (`cacheDir`, `refreshInterval`, `openFirewall`,
`voacapUrl`, `voacapl`, …).

**Manual binary**

```bash
./rms-radar -d -listen 0.0.0.0:8079 -cache-dir /var/lib/rms-radar
```

The distro packages install `/usr/bin/rms-radar`, a systemd unit with
`DynamicUser=true` and `StateDirectory=rms-radar`, and a desktop entry. The
unit listens on `127.0.0.1:8079` by default; change `ExecStart` or use the
module option to serve the LAN.

## Network-wide / LAN

- Bind to `0.0.0.0:8079` (or a specific LAN address) to serve other machines.
- Put it behind a reverse proxy (Caddy, nginx, Traefik) with TLS if it is
  reachable beyond your LAN. There is **no authentication**.
- Open the firewall for the port, or use the NixOS module's `openFirewall`.
- Make sure the cache directory is writable. The packaged unit uses
  `/var/lib/rms-radar`; the manual default is `./data`.
- Map tiles are proxied and cached to disk (`<cache-dir>/tiles`), so repeated
  views are cheap. New areas need connectivity.
- The gateway list and propagation conditions are cached; an embedded snapshot
  is the final fallback. Previously viewed map areas render offline.

## Desktop mode vs service mode

| | Browser mode (default) | `-d` / `--daemon` |
|---|---|---|
| Opens the browser | yes | no |
| Exits when the UI closes | yes (after the last tab, ~15 s grace) | never |
| Quit button in the UI | yes | no |
| Intended for | double-click / local use | systemd, NixOS, containers |

Browser mode generates a random local token and opens
`/?desktop=<token>`. The page checks in every few seconds; closing the tab
sends a beacon, and the process exits after a short grace so reloads and
multiple tabs are safe. The **Quit** button exits immediately. The desktop
endpoints are token-guarded and disabled in `-d` service mode.

## Configuration

| Flag | Env | Default | Description |
|------|-----|---------|-------------|
| `-listen` | `RMSRADAR_LISTEN` | `127.0.0.1:8079` | HTTP listen address (use `0.0.0.0:8079` to serve the LAN) |
| `-service-codes` | `RMSRADAR_SERVICE_CODES` | `PUBLIC,MERCURY,VARDEFM,MODEM73,DATA2G,IONHF,XARA,AETHER` | Comma-separated service codes to fetch (no wildcard) |
| `-refresh` | `RMSRADAR_REFRESH` | `24h` | Winlink gateway list refresh interval; the UI **Refresh** button forces an update (five-minute cooldown) |
| `-radiopost-refresh` | `RMSRADAR_RADIOPOST_REFRESH` | `10m` | RadioPost directory/status poll interval (minimum 1m; default 10m) |
| `-cache-dir` | `RMSRADAR_CACHE_DIR` | `./data` | Cache directory |
| `-key` | `RMSRADAR_KEY` | Pat's community key | Winlink CMS access key |
| `-admin-token` | `RMSRADAR_ADMIN_TOKEN` | *(empty)* | Enables service-code changes and requires this value in the `X-RMS-Radar-Admin` header |
| `-history-hours` | `RMSRADAR_HISTORY_HOURS` | `48` | Channel history (1–48) |
| `-voacap-url` | `RMSRADAR_VOACAP_URL` | *(empty)* | Optional remote VOACAP API base URL (enables forecasts) |
| `-voacap-executable` | `RMSRADAR_VOACAP_EXECUTABLE` | *(empty)* | Path to a local `voacapl`/`voacapw` binary (enables forecasts) |
| `-voacap-data-dir` | `RMSRADAR_VOACAP_DATA_DIR` | *(auto)* | ITSHFBC data directory for the local VOACAP binary |
| `-voacap-download-url` | `RMSRADAR_VOACAP_DOWNLOAD_URL` | *(empty)* | Download a VOACAP distribution (binary + data) from this URL |
| `-voacap-download-dir` | `RMSRADAR_VOACAP_DOWNLOAD_DIR` | *user cache* | Extract directory for the downloaded VOACAP distribution |
| `-voacap-download-sha256` | `RMSRADAR_VOACAP_DOWNLOAD_SHA256` | *(empty)* | Optional SHA-256 of the distribution archive |
| `-voacap-auto` | `RMSRADAR_VOACAP_AUTO` | `false` (desktop: `true`) | Download the published RMS Radar VOACAP bundle when no engine is installed |
| `-web-dir` | `RMSRADAR_WEB_DIR` | *(embedded)* | Serve web assets from a directory instead |
| `-tile-url` | `RMSRADAR_TILE_URL` | `https://tile.openstreetmap.org/{z}/{x}/{y}.png` | Upstream tile template; proxied and disk-cached by the server |
| `-tile-cache-max` | `RMSRADAR_TILE_CACHE_MAX_MB` | `512` | Tile cache quota in MiB; oldest tiles are evicted when exceeded |
| `-vhf-max-range` | `RMSRADAR_VHF_MAX_RANGE` | `400` | Max plausible VHF/UHF path (km) for ranking — distance-based, not continent-based; set large to disable |
| `-d`, `-daemon` | `RMSRADAR_DAEMON` | `false` | Service mode: do not open a browser and never auto-exit |
| `-open-browser` | `RMSRADAR_OPEN_BROWSER` | `true` | Open the web UI in the default browser on start (browser desktop mode) |

The native `rms-radar-desktop` binary accepts the same data/propagation flags
plus `-debug` for webview developer tools; it has no `-d`/`-open-browser`
because closing its window always quits.

### Service codes

The service-code dropdown is built from the codes present in the fetched data,
and the mode dropdown shows the modes those channels advertise. Community-modem
codes are labelled with the service name: `MERCURY` → **Mercury**, `VARDEFM` →
**VardeFM**, `MODEM73` → **MODEM73**, `DATA2G` → **DATA2G**, `IONHF` →
**IONHF**, `XARA` → **XARA**, and `AETHER` → **Winlink/Aether**, even though
the API advertises them under VARA, Packet, or ARDOP.

Dedicated project modems show only the service code. Their reported bandwidth is
omitted because it varies by channel and configuration: `DATA2G` uses 500 or
2400 Hz, `MODEM73` uses 600 or 2400 Hz, and `IONHF` is ARDOP-based. `PUBLIC`
and private group codes keep the reported mode. The API's raw mode is
preserved in `raw_mode` and shown as a hover hint. HF frequencies are displayed
as radio dial frequencies: Winlink reports the 3 kHz USB center, so RMS Radar
subtracts 1.5 kHz (VHF/UHF frequencies are unchanged).

The Winlink API has no wildcard for service codes, so `-service-codes` lists the
codes to request. The default includes the documented public codes and the
known community codes:

| Code | Notes |
|------|-------|
| `PUBLIC` | maritime and open amateur gateways |
| `MERCURY` | Mercury HF gateways (community) |
| `VARDEFM` | VardeFM gateways (community) |
| `MODEM73` | MODEM73 gateways (community; variable packet speed) |
| `DATA2G` | DATA2G gateways (community; variable VARA bandwidth) |
| `IONHF` | IONHF gateways (community; ARDOP-based) |
| `XARA` | XARA gateways (community) |
| `AETHER` | Aether gateways (community; labelled Winlink/Aether) |
| *other* | any community or private group code |

The list is **runtime-configurable** — in the native and browser desktop apps,
open **Service codes…** to add or remove a code, or set `-service-codes` / the
module option. New community codes do not require a new release; a code with no
gateways simply contributes nothing, and the filter dropdown is built from the
data.

On a server/daemon, the editing endpoint is **disabled by default**. To enable
it, set an admin token and enter the same token in the dialog (or send it from a
script):

```bash
./rms-radar -d -admin-token "$ADMIN_TOKEN"

curl -X POST http://127.0.0.1:8079/api/servicecodes/config \
  -H 'Content-Type: application/json' \
  -H "X-RMS-Radar-Admin: $ADMIN_TOKEN" \
  -d '{"add":"DATA3G"}'
```

Requests must be JSON and same-origin; cross-site `text/plain` forms are
rejected. Codes are limited to `A-Z`, `0-9`, and `_` (1–16 characters, at most
32 codes). The cache file is named from a hash of the code list, never from the
raw text.

Winlink does not publish private group codes; if your group uses one, add it in
the desktop **Service codes…** dialog or with `-service-codes
PUBLIC,MYGROUP` (or `RMSRADAR_SERVICE_CODES`). Codes with no gateways simply
contribute nothing. There is no API call that enumerates all private codes.

Runtime changes made in the desktop dialog are preserved across restarts while
the configured seed list is unchanged. Changing `-service-codes` or the module
option intentionally re-seeds the list, so an operator configuration change
takes effect.

The app serves a per-client setup guide at `/clients.html` (Winlink Express,
PAT, WoAD, RadioMail, HTCommander) and a gateway guide at `/gateway.html`
(RMS Trimode and BPQ32/LinBPQ, including the one-frequency/mode report caveat).

## Propagation

RMS Radar estimates link success two ways, preferring the more accurate one and
falling back gracefully:

1. **Heuristic (always available)** — real-time ionosonde `foF2`/MUF from
   [prop.kc2g.com](https://prop.kc2g.com) plus solar indices (SFI/A/K) from
   [hamqsl.com](https://www.hamqsl.com), combined with great-circle distance and
   the gateway's advertised radio range. The last-known conditions are cached to
   disk, so ranking still works offline (marked as stale).
2. **VOACAP (optional, point-to-point forecast)** — a port of Pat's MIT-licensed
   VOACAP encoder/parser and Link Quality Index (LQI) scoring. Two engines are
   supported: a **remote VOACAP API** (`-voacap-url`) or a **local
   `voacapl`/`voacapw` binary with ITSHFBC data** (`-voacap-executable` /
   `-voacap-data-dir`). The LQI combines path reliability, SNR, takeoff angle,
   MUFday, propagation mode (hops/layer), and signal power into a 0–100 score.
   The smoothed sunspot number (SSN) input comes from the current hamqsl daily
   sunspot count (Pat uses a SILSO smoothed-SSN forecast instead). The ranker
   prefers VOACAP when configured and automatically falls back to the heuristic
   when it is unavailable or errors.

Close-in HF paths (a few to a few dozen km) are groundwave/NVIS, not long-haul
skywave, so skywave statistics such as MUFday under-report them. Both predictors
apply a band-aware groundwave/NVIS floor for these distances: low bands
(80/60/40 m) score in the high 90s, while high bands (20 m and up) stay low
because their groundwave is weak and they are in the skip zone.

The rank endpoint (`POST /api/rank`) accepts `{grid, band, mode, service_code}`
and returns stations sorted by predicted link quality, with distance, bearing,
band-open status, and MUF. When ranking across multiple bands or modes, each
station row shows the band and mode that produced its best score, and that
channel is highlighted.

## Installing VOACAP (optional)

Point-to-point forecasts use an external VOACAP engine. RMS Radar is MIT; the
engine it shells out to is a separate program with its own permissive terms: the
core VOACAP prediction code is U.S. Government public domain (NTIA/ITS), the
`voacapl` Linux port (J.A. Watson) is CC0, and its `itshfbc` coefficient data is
freely redistributable (Debian ships it as `voacapl-data`). Because it is exec'd
rather than linked, there is no combined-work license question.

RMS Radar (server and desktop) auto-detects `voacapl` on `PATH` and the
standard data directories (`/usr/share/voacapl/itshfbc`,
`/usr/local/share/voacapl/itshfbc`, the Homebrew prefixes, `~/itshfbc`, and
`C:\itshfbc` on Windows). Install it with your package manager, or point at a
custom engine explicitly:

- **Debian/Ubuntu:** `sudo apt install voacapl voacapl-data`
- **Arch:** install `voacapl` from the AUR
- **Fedora/NixOS:** build from [jawatson/voacapl](https://github.com/jawatson/voacapl)
  or use the `nix-ham-packages` `voacapl` package

```bash
./rms-radar -voacap-executable "$(command -v voacapl)"
```

`-voacap-data-dir` is only needed if the `itshfbc` data lives somewhere
non-standard; on Linux it auto-detects a read-only data directory (voacapl runs
with `--run-dir`).

### Automatic VOACAP setup (desktop)

The `rms-radar-desktop` app is designed for zero-configuration use. If no engine
is installed, it downloads the bundle for its platform from the latest RMS Radar
release, verifies the published SHA-256 sidecar, extracts it into
`-voacap-download-dir` (by default the user cache, e.g.
`~/.cache/rms-radar/voacap`), and starts with forecasts enabled. This happens
once, on first launch; subsequent launches reuse the verified install. The whole
archive is a few MB.

The checksum is mandatory: if it cannot be fetched or verified, the download is
discarded and the app continues with the built-in estimator. Set
`-voacap-auto=false` (or `RMSRADAR_VOACAP_AUTO=false`) to opt out, for example
on a metered or offline connection.

Self-hosted web/server builds default to auto-download **off**. Enable it with
`-voacap-auto` if the host can reach GitHub and you want the same behavior:

```bash
./rms-radar -voacap-auto
```

To use your own distribution instead of the published bundle:

```bash
./rms-radar -voacap-download-url https://example.com/voacap-macos.tar.gz \
  -voacap-download-sha256 <sha256>
```

Point `-voacap-download-url` at a `.zip`, `.tar.gz`, `.tgz`, or `.tar` containing
the `voacapl`/`voacapw` binary and its `itshfbc` data. The archive is extracted
to `-voacap-download-dir`, the engine and data directory are located
automatically, and the resulting paths are used. Supply `-voacap-download-sha256`
to verify the archive before extracting.

Downloading and executing a third-party binary is a trust decision. The default
bundle is built from the public-domain NTIA/ITS VOACAP engine, the CC0
`voacapl` port, and freely redistributable coefficient data; the release
workflow publishes its checksum next to it.

## API

| Endpoint | Description |
|----------|-------------|
| `GET /api/health` | Service status, data source, station count |
| `GET /api/gateways` | Normalized stations + channels (cached) |
| `GET /api/servicecodes` | Distinct service codes present in the data |
| `GET /api/conditions` | Current solar + MUF summary |
| `POST /api/rank` | Ranked stations for a grid/band/mode |
| `GET /api/config` | Service codes, version, VOACAP status, tile URL, editing flag |
| `POST /api/refresh` | Force a gateway refresh (same-origin JSON; five-minute cooldown) |
| `POST /api/servicecodes/config` | Change the fetched codes (desktop same-origin, or `X-RMS-Radar-Admin` on a server) |
| `GET /api/radiopost/gateways` | Normalized RadioPost stations from the separate RadioPost source |
| `POST /api/radiopost/refresh` | Force a RadioPost directory/status refresh (same-origin JSON; status poll floor 1m) |

**Frequency convention:** the `frequency_hz` and `frequency` fields returned by
`/api/gateways` and `/api/rank` are **radio dial frequencies** and are the
default UI display. Winlink reports the USB center frequency for HF channels, so
RMS Radar subtracts 1.5 kHz for those fields. The original center is also
available as `center_frequency_hz` / `center_frequency`; the UI's **Dial /
Center** toggle switches between them. VHF/UHF frequencies have no offset. For
example, Winlink's 7.0425 MHz center is returned as dial **7.041 MHz** and
center **7.0425 MHz**. HF values retain 100 Hz precision (for example
**7.1013 MHz**).

## RadioPost source

RadioPost is a separate gateway network, not a Winlink service code. The web UI
has a **Source** selector: **Winlink** is the default, and **RadioPost** loads
the RadioPost directory instead. RadioPost does not use Winlink service codes,
so the service-code filter and editor are hidden for that source.

The server reads:

- `https://radiopost.org/data/directory.json` for the signed gateway lists.
- Each post office's status JWS at `<api>/gateways/status`, with the default
  source at `https://po.radiopost.org/rp/v1/gateways/status`.

The directory is cached for up to six hours. Status is polled once per
`-radiopost-refresh` interval (default **10m**), with a hard minimum of **1m** as
required by RadioPost. Gateways marked `test: true` and channels using `tcp` or
unknown modem names are skipped. Known modems are labelled
`RadioPost/Aether`, `RadioPost/Aether FM`, `RadioPost/ARDOP`,
`RadioPost/AX.25`, `RadioPost/Mercury`, `RadioPost/VARA HF`, and
`RadioPost/VARA FM`. `scan_s` is shown on each channel when a gateway scans
multiple frequencies.

## Offline behavior

1. The gateway list is cached to `-cache-dir`. It refreshes once a day and can
   be refreshed immediately with the **Refresh** button (rate-limited to one
   request per five minutes). If the network is unavailable on a later start,
   the cache is served.
2. If there is no cache, an embedded snapshot (bundled in the binary) is used.
3. Propagation conditions are cached too; ranking degrades to a
   distance/band-based estimate when no ionospheric data is available.

Map tiles are proxied and cached to disk by the server (`<cache-dir>/tiles`), so
previously viewed areas render offline; new areas require connectivity. Tile
requests have a per-client token-bucket limit (10/s, burst 20) in addition to the
eight-request upstream concurrency cap; put heavier traffic behind a reverse
proxy with its own limit.

## Security & privacy

RMS Radar is designed as a single-user, self-hosted tool and has **no user
authentication**. Treat it as a LAN/loopback service:

- **Binds to `127.0.0.1:8079` by default.** Pass `-listen 0.0.0.0:8079` (and put
  it behind a reverse proxy or VPN) only if you intend to expose it.
- **No user accounts, no cookies.** The data API is read-only; `/api/rank` only
  computes predictions, capped at 500 candidates and time-limited. Service-code
  editing is disabled in daemon mode unless `-admin-token` is set, requires the
  token header and a same-origin JSON request, and is rate-limited. The desktop
  lifecycle endpoints (`/api/desktop/*`) require the random token from the
  launch URL and are disabled in `-d` service mode.
- **Security headers** (CSP, `X-Content-Type-Options: nosniff`,
  `X-Frame-Options`, `Referrer-Policy`) are set on every response, and all
  third-party gateway data is HTML-escaped before rendering.
- **Outbound only:** the service fetches the gateway list from `api.winlink.org`
  and propagation data from `prop.hamqsl.com` / `prop.kc2g.com`, over verified
  TLS. It makes no inbound connections.
- **VOACAP auto-download executes a third-party binary.** Desktop builds
  download the project's published bundle, pinned to the app version, and
  verify both its SHA-256 and a minisign signature (public key embedded in the
  app) before extracting; web/server builds default to off and only download
  when `-voacap-auto` or `-voacap-download-url` is set. The release workflow
  pins the upstream VOACAP source and Windows archive hashes. Downloads use
  HTTPS and are size-capped, and extraction rejects path-traversal entries.
  Prefer your OS package manager if you want to audit the engine separately; a
  failed verification leaves the previous install (or the built-in estimator)
  in place.
- **Privacy:** gateway data (including the sysop `Comments` field, which may
  contain contact details) is the same data Winlink publishes publicly. The
  embedded fallback snapshot is a frozen copy of that public feed.

## Packaging

- **systemd (packaged):** `packaging/common/rms-radar.service` — `DynamicUser`,
  `StateDirectory=rms-radar`, `/usr/bin/rms-radar`.
- **systemd (manual):** `contrib/systemd/rms-radar.service` — static
  `rms-radar` user, `/usr/local/bin`, `/var/lib/rms-radar`.
- **Arch:** `packaging/arch/PKGBUILD` (server) and
  `packaging/arch/PKGBUILD.desktop` (native desktop).
- **RPM:** `packaging/rpm/rms-radar.spec` (server) and
  `packaging/rpm/rms-radar-desktop.spec` (native desktop).
- **DEB:** `scripts/build-deb.sh` (server and desktop; used by the release
  workflow).
- **Nix / NixOS:** the [`nix-ham-packages`](https://github.com/ben-kuhn/nix-ham-packages)
  overlay (`pkgs.rms-radar` + `services.rms-radar` module).

Release assets include `rms-radar-web-*` for the server plus `.deb`, `.rpm`,
and Arch packages for both the server and the native desktop client. The Arch
recipes (`PKGBUILD` and `PKGBUILD.desktop`) are published alongside the
packages so they can be inspected or rebuilt without cloning the repository.

## Building from source

See [BUILDING.md](BUILDING.md).
