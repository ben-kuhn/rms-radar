# RMS Radar

**A free, open-source map of Winlink and RadioPost gateways.**

RMS Radar shows Winlink gateways near you on a map, lets you filter by the
service codes you care about, and ranks them by how likely you are to reach
them. It can also load the separate RadioPost directory from the same Service
selector. It runs as a normal desktop app on macOS, Windows, and Linux — no server
or command line required — and can also be self-hosted for a club or network.

> **Unofficial.** RMS Radar is not affiliated with the Winlink Development Team
> or ARSFI. Winlink data comes from Winlink's public API; RadioPost data comes
> from [radiopost.org](https://radiopost.org/). The official Winlink list and map
> are at [winlink.org/RMSChannels](https://winlink.org/RMSChannels).

**Website:** [ben-kuhn.github.io/rms-radar](https://ben-kuhn.github.io/rms-radar/) ·
**Live map:** [rmsradar.ku0hn.radio](https://rmsradar.ku0hn.radio)

## Download

Get the latest version from the
**[RMS Radar releases page](https://github.com/ben-kuhn/rms-radar/releases/latest)**.

### macOS

1. Download `rms-radar-desktop-<version>-macos-arm64-app.zip`.
2. Unzip it and drag **RMS Radar.app** into **Applications**.
3. The app is not signed yet, so the first launch needs **right-click → Open**
   (or run `xattr -dr com.apple.quarantine "RMS Radar.app"` in Terminal).

The desktop app is Apple Silicon (arm64). Intel Mac users can use the
web/server build or the browser-based launcher.

### Windows

1. Download `rms-radar-desktop-<version>-windows-amd64.zip`.
2. Unzip it and run `rms-radar-desktop.exe`.
3. If Windows SmartScreen appears, choose **More info → Run anyway**.

RMS Radar uses Microsoft's WebView2 runtime, which is included with Windows 10
and 11. If it is missing, install the
[WebView2 Evergreen Runtime](https://developer.microsoft.com/microsoft-edge/webview2/).

### Linux

- **Debian / Ubuntu:** download `rms-radar-desktop_<version>_amd64.deb`, then:

  ```bash
  sudo apt install ./rms-radar-desktop_<version>_amd64.deb
  ```

  Launch **RMS Radar** from your applications menu.

- **Fedora / RHEL:** download `rms-radar-desktop-<version>-1.*.x86_64.rpm`, then:

  ```bash
  sudo dnf install ./rms-radar-desktop-<version>-1.*.x86_64.rpm
  ```

- **Arch Linux:** download
  `rms-radar-desktop-<version>-1-x86_64.pkg.tar.zst`, then:

  ```bash
  sudo pacman -U rms-radar-desktop-<version>-1-x86_64.pkg.tar.zst
  ```

- **Any other distribution:** download
  `rms-radar-desktop-<version>-linux-amd64.tar.gz`, extract it, and run
  `./rms-radar-desktop`. It needs WebKitGTK 4.1
  (`libwebkit2gtk-4.1-0` on Debian/Ubuntu, `webkit2gtk-4.1` on Arch,
  `webkit2gtk4.1` on Fedora) and GTK 3.

- **NixOS:** use the
  [`nix-ham-packages`](https://github.com/ben-kuhn/nix-ham-packages) overlay.

Self-hosting the web version? The fastest path is Docker:

```bash
docker run -d --name rms-radar --restart unless-stopped   -p 127.0.0.1:8079:8079 -v rms-radar-data:/data   ghcr.io/ben-kuhn/rms-radar:latest
```

Use a `compose.yaml` and reverse proxy for anything beyond localhost. See the
[Docker section of SELF-HOSTING.md](SELF-HOSTING.md#docker-recommended) or the
[website self-hosting guide](https://ben-kuhn.github.io/rms-radar/self-hosting.html)
for `.deb`, `.rpm`, Arch, NixOS, and Docker details.

## Using RMS Radar

1. **Enter your grid square** (for example `EM38xx`). Not sure? Use a
   [Maidenhead locator tool](https://www.levinecentral.com/ham/grid_square.php).
2. **Pick a service code** — `PUBLIC` is the usual open amateur code.
   `IONHF`, `MERCURY`, `VARDEFM`, `MODEM73`, `DATA2G`, `XARA`, and `AETHER` are common
   community codes; other projects and private groups use their own. You can
   also filter by band and mode.
3. **Press Rank** to score the gateways by predicted link quality. Each row
   shows the band and mode that produced its best score.
4. **Click a gateway** to center it on the map and open its details: frequencies,
   modes, service codes, and the link estimate.
5. On a phone, use the **Map** and **List** tabs to switch views.

The gateway list refreshes once a day automatically. Press **Refresh** for an
immediate update; it is limited to once every five minutes to be gentle on the
Winlink API. The map is dark by default; the ◐ button switches to light mode.
The gateway list and map tiles are cached, so previously viewed areas work
offline.

## What it does

- **Maps the live Winlink gateway list** on OpenStreetMap.
- **Filters by service code** — `PUBLIC`, `IONHF`, `MERCURY`, `VARDEFM`,
  `MODEM73`, `DATA2G`, `XARA`, `AETHER`, and any other community or private codes you configure.
- **Shows the real mode** for every channel. Mercury, VardeFM, IONHF, MODEM73, DATA2G,
  XARA, and AETHER gateways are labelled from their service code, even when Winlink
  advertises them under VARA, ARDOP, or Packet. Variable modem widths are not
  shown for the dedicated project codes.
- **Shows each channel's operating hours** — 24h for always-on channels, or the
  UTC schedule when a mode/frequency is only available part of the day.
- **Uses dial frequencies for HF** — Winlink reports the USB center frequency;
  RMS Radar subtracts 1.5 kHz for display, matching the frequency you tune.
  For example, Winlink's 7.0425 MHz center is shown as **7.041 MHz**. HF
  frequencies retain 100 Hz precision (for example **7.1013 MHz**), so the
  displayed dial frequency is usable directly. VHF/UHF frequencies are unchanged.
- **Switch between dial and center** — dial is the default. Winlink supplies
  center frequencies and RMS Radar derives dial; RadioPost supplies dial
  frequencies and RMS Radar derives the center. The toggle works for both.
- **Two separate services** — the **Service** dropdown switches between Winlink
  and RadioPost. RadioPost hides Winlink service codes and shows its own
  `Aether`, `VARA HF`, and other per-channel modes, plus
  `scan_s` for scanning gateways.
- **Ranks by propagation** — a built-in heuristic works everywhere, and an
  optional VOACAP engine gives point-to-point HF forecasts.
- **Hides implausible paths** — 0% VHF/UHF results are dropped while ranking,
  and very short HF paths get a realistic groundwave/NVIS score.
- **Runs natively** on macOS, Windows, and Linux, or in any browser when
  self-hosted.

### Better HF predictions

RMS Radar always gives a built-in estimate. The desktop app goes further: on
first launch it downloads the matching VOACAP engine automatically (a few MB,
once) and uses it for full point-to-point forecasts — no setup, no package
manager.

If you prefer to manage the engine yourself, or you are offline, install one of
these and RMS Radar will use it instead:

- **macOS:** `brew install la5nta/tap/voacapl`
- **Debian/Ubuntu:** `sudo apt install voacapl voacapl-data`
- **Windows:** install the VOACAP/ITSHFBC package into `C:\itshfbc`
  ([download](https://www.greg-hand.com/versions/itshfbc_180417a.exe),
  [notes](https://www.greg-hand.com/hfwin32.html))
- **Arch:** `voacapl` from the AUR
- **NixOS:** use the `voacapl` package from
  [`nix-ham-packages`](https://github.com/ben-kuhn/nix-ham-packages)

The desktop app checks for these installs before downloading anything.
Self-hosters can also point at a remote API or a custom binary. See
[SELF-HOSTING.md](SELF-HOSTING.md).

## Guides

- **[Client setup guide](https://rmsradar.ku0hn.radio/clients.html)** — how to
  add service codes in Winlink Express, PAT, WoAD, RadioMail, and HTCommander.
- **[Gateway setup guide](https://rmsradar.ku0hn.radio/gateway.html)** — how to
  publish service codes from RMS Trimode and BPQ32/LinBPQ, including the
  one-frequency/mode reporting rule.

Both guides are also served by any self-hosted instance at `/clients.html` and
`/gateway.html`.

## Self-hosting

Want to run RMS Radar for a club, a net, or a whole LAN? See
**[SELF-HOSTING.md](SELF-HOSTING.md)** for server packages, `-d` service mode,
LAN exposure, reverse-proxy notes, the full flag list, the API, VOACAP setup,
and security details.

## Building from source

See **[BUILDING.md](BUILDING.md)** for the Go build, the native desktop build,
the test suite, the release scripts, and the distro packaging.

## License

[MIT](LICENSE). Gateway data is © the Winlink Development Team / Amateur Radio
Safety Foundation; solar data © the respective providers (see in-app
attribution).
