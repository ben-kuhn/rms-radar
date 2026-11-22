# RMS Radar RPM spec
#
# Build:
#   spectool -g packaging/rpm/rms-radar.spec   # fetch Source0 tarball
#   rpmbuild -ba packaging/rpm/rms-radar.spec
#
# Note: point-to-point VOACAP forecasts are optional. The "voacapl" engine is
# not packaged in Fedora/EPEL proper (see the Recommends comment below), so
# RMS Radar falls back to its built-in real-time heuristic until a COPR or
# local build provides it.

Name:           rms-radar
Version:        1.0.8
Release:        1%{?dist}
Summary:        Winlink RMS gateway map with service-code filtering and propagation ranking

License:        MIT
URL:            https://github.com/ben-kuhn/rms-radar
Source0:        %{url}/archive/refs/tags/v%{version}.tar.gz#/%{name}-%{version}.tar.gz

# Static Go binary with no usable DWARF; disable the debuginfo subpackage to
# avoid an empty debugsourcefiles.list.
%global debug_package %{nil}

BuildRequires:  golang
BuildRequires:  systemd-rpm-macros

# Optional runtime engine for point-to-point HF propagation forecasts.
# voacapl is NOT packaged in Fedora/EPEL; this only resolves if a COPR or
# third-party repo provides it, and is skipped gracefully otherwise.
Recommends:     voacapl

%description
A minimal, self-hostable web service that periodically refreshes the Winlink
RMS gateway list, maps gateways on OpenStreetMap, and lets users filter and
rank stations by service code (e.g. MERCURY, VardeFM), band, and mode using
current propagation conditions.

Point-to-point VOACAP forecasts are optional. To enable them, install the
voacapl engine and its itshfbc data (not packaged in Fedora proper — use a
COPR or build from https://github.com/jawatson/voacapl), or point
rms-radar at a remote VOACAP API with -voacap-url.

%prep
%autosetup

%build
# Static, portable binary (no cgo, no external Go dependencies).
export CGO_ENABLED=0
go build -trimpath -ldflags "-s -w -X main.version=%{version}" -o rms-radar .

%install
install -Dm755 rms-radar %{buildroot}%{_bindir}/rms-radar
install -Dm644 packaging/common/rms-radar.service %{buildroot}%{_unitdir}/rms-radar.service
install -Dm644 contrib/desktop/rms-radar.desktop %{buildroot}%{_datadir}/applications/rms-radar.desktop
install -Dm644 contrib/desktop/rms-radar.svg %{buildroot}%{_datadir}/icons/hicolor/scalable/apps/rms-radar.svg
install -Dm644 contrib/desktop/rms-radar-1024.png %{buildroot}%{_datadir}/icons/hicolor/1024x1024/apps/rms-radar.png

%files
%{_bindir}/rms-radar
%{_unitdir}/rms-radar.service
%{_datadir}/applications/rms-radar.desktop
%{_datadir}/icons/hicolor/scalable/apps/rms-radar.svg
%{_datadir}/icons/hicolor/1024x1024/apps/rms-radar.png
%license LICENSE
%doc README.md

%post
%systemd_post rms-radar.service

%preun
%systemd_preun rms-radar.service

%postun
%systemd_postun rms-radar.service

%changelog
* Sun Nov 22 2026 RMS Radar contributors - 1.0.8-1
- Clarify dial-frequency display in the docs
* Sat Nov 21 2026 RMS Radar contributors - 1.0.7-1
- Show HF dial frequencies and add XARA service code
* Thu Nov 19 2026 RMS Radar contributors - 1.0.6-1
- Display each channel operating hours
* Thu Nov 19 2026 RMS Radar contributors - 1.0.5-1
- Remove EMCOMM from the default service-code set
* Thu Nov 19 2026 RMS Radar contributors - 1.0.4-1
- Label dedicated modems by service code: MODEM73, DATA2G, IONHF
* Thu Nov 19 2026 RMS Radar contributors - 1.0.3-1
- Normalize IONHF as IONHF and drop variable widths from Modem73/Data2G
* Thu Nov 19 2026 RMS Radar contributors - 1.0.2-1
- Tile cache hits bypass the rate limiter; retry failed zoom tiles
* Wed Nov 18 2026 RMS Radar contributors - 1.0.1-1
- Add native desktop RPM and Arch packages
* Wed Nov 18 2026 RMS Radar contributors - 1.0.0-1
- First stable release: Docker image, static site, full service-code set
* Wed Nov 18 2026 RMS Radar contributors - 0.2.10-1
- Fetch the full discussed service-code set (including EMCOMM and IONHF)
* Wed Nov 18 2026 RMS Radar contributors - 0.2.9-1
- Add the app icon, service-code reset, and legacy default migration
* Wed Nov 18 2026 RMS Radar contributors - 0.2.8-1
- Security hardening: loopback-only desktop edits, IPv6 /64 limits, GET rank removal
* Wed Nov 18 2026 RMS Radar contributors - 0.2.7-1
- Replace EMCOMM with IONHF in the default service-code set
* Wed Nov 18 2026 RMS Radar contributors - 0.2.6-1
- Desktop setup links open in the system browser; known community codes by default
* Tue Nov 17 2026 RMS Radar contributors - 0.2.5-1
- Daily gateway refresh plus an on-demand Refresh button
* Tue Nov 17 2026 RMS Radar contributors - 0.2.4-1
- Harden service-code editing; sign VOACAP bundles; atomic caches
* Tue Nov 17 2026 RMS Radar contributors - 0.2.3-1
- Desktop auto-downloads a checksum-verified VOACAP engine
* Tue Nov 17 2026 RMS Radar contributors - 0.2.2-1
- Runtime service codes; audit hardening; desktop VOACAP auto-detection
* Tue Nov 17 2026 RMS Radar contributors - 0.2.1-1
- Linux packages (.deb/.rpm/Arch), consumer docs, MODEM73/DATA2G
* Tue Nov 17 2026 RMS Radar contributors - 0.2.0-1
- Rename to RMS Radar; native desktop build; gateway setup guide
* Mon Nov 16 2026 RMS Radar contributors - 0.1.0-1
- Initial package
