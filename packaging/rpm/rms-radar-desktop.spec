# Native RMS Radar desktop RPM.
#
# The desktop binary is built once on the Linux CI runner with CGo/WebKitGTK
# and then packaged here, so the RPM contains the same binary as the portable
# tarball and .deb. The release workflow copies these sources into
# ~/rpmbuild/SOURCES before invoking rpmbuild:
#
#   rms-radar-desktop
#   rms-radar-desktop.desktop
#   rms-radar.svg
#   rms-radar-1024.png
#   LICENSE

Name:           rms-radar-desktop
Version:        1.0.7
Release:        1%{?dist}
Summary:        Native desktop client for the RMS Radar Winlink gateway map

License:        MIT
URL:            https://github.com/ben-kuhn/rms-radar
Source0:        rms-radar-desktop
Source1:        rms-radar-desktop.desktop
Source2:        rms-radar.svg
Source3:        rms-radar-1024.png
Source4:        LICENSE
BuildArch:      x86_64
Requires:       webkit2gtk4.1
Requires:       gtk3
Requires:       ca-certificates

# The binary is already stripped by the Go linker; skip the debuginfo package.
%global debug_package %{nil}

%description
A native WebKitGTK desktop window for mapping and ranking Winlink RMS
gateways by service code, band, and mode. It embeds the same map as the web
UI and can download its optional VOACAP engine on first launch.

%prep
cp -p %{SOURCE4} .

%build

%install
install -Dm755 %{SOURCE0} %{buildroot}%{_bindir}/rms-radar-desktop
install -Dm644 %{SOURCE1} %{buildroot}%{_datadir}/applications/rms-radar-desktop.desktop
install -Dm644 %{SOURCE2} %{buildroot}%{_datadir}/icons/hicolor/scalable/apps/rms-radar-desktop.svg
install -Dm644 %{SOURCE3} %{buildroot}%{_datadir}/icons/hicolor/1024x1024/apps/rms-radar-desktop.png

%files
%{_bindir}/rms-radar-desktop
%{_datadir}/applications/rms-radar-desktop.desktop
%{_datadir}/icons/hicolor/scalable/apps/rms-radar-desktop.svg
%{_datadir}/icons/hicolor/1024x1024/apps/rms-radar-desktop.png
%license LICENSE

%changelog
* Sat Nov 21 2026 RMS Radar contributors - 1.0.7-1
- Rebuild for RMS Radar 1.0.7
* Thu Nov 19 2026 RMS Radar contributors - 1.0.6-1
- Rebuild for RMS Radar 1.0.6
* Thu Nov 19 2026 RMS Radar contributors - 1.0.5-1
- Rebuild for RMS Radar 1.0.5
* Thu Nov 19 2026 RMS Radar contributors - 1.0.4-1
- Rebuild for RMS Radar 1.0.4
* Thu Nov 19 2026 RMS Radar contributors - 1.0.3-1
- Rebuild for RMS Radar 1.0.3
* Thu Nov 19 2026 RMS Radar contributors - 1.0.2-1
- Rebuild for RMS Radar 1.0.2
* Wed Nov 18 2026 RMS Radar contributors - 1.0.1-1
- Add a native desktop RPM
* Wed Nov 18 2026 RMS Radar contributors - 1.0.0-1
- First stable release: Docker image, static site, full service-code set
