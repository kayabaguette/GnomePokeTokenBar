%global debug_package %{nil}
%global uuid poketokenbar@local

Name:           gnome-shell-extension-poketokenbar
Version:        %{?pkg_version}%{!?pkg_version:0.1.1}
Release:        1%{?dist}
Summary:        Top-bar Claude Code token usage tracker with a Pokemon companion
License:        MIT
URL:            https://github.com/chattymin/PokeTokenBar
Source0:        %{name}-%{version}.tar.gz

BuildRequires:  golang
BuildRequires:  glib2
Requires:       gnome-shell >= 51

%description
GNOME Shell extension showing Claude Code token usage in the top bar, with a
Pokemon companion, backed by a bundled Go helper process. This is a GNOME
Shell port of the upstream macOS app PokeTokenBar
(https://github.com/chattymin/PokeTokenBar).

%prep
%autosetup -n %{name}-%{version}

%build
cd helper
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -B gobuildid -X main.version=%{version}" -o ../ptb-helper ./cmd/ptb-helper
cd ..
glib-compile-schemas extension/schemas

%install
install -d %{buildroot}%{_datadir}/gnome-shell/extensions/%{uuid}
cp -a extension/. %{buildroot}%{_datadir}/gnome-shell/extensions/%{uuid}/
install -Dpm0755 ptb-helper %{buildroot}%{_libexecdir}/poketokenbar/ptb-helper
install -d %{buildroot}%{_datadir}/gnome-shell/extensions/%{uuid}/bin
ln -sr %{buildroot}%{_libexecdir}/poketokenbar/ptb-helper %{buildroot}%{_datadir}/gnome-shell/extensions/%{uuid}/bin/ptb-helper

%files
%license LICENSE
%doc README.md
%{_datadir}/gnome-shell/extensions/%{uuid}/
%{_libexecdir}/poketokenbar/

%changelog
* Wed Nov 18 2026 PokeTokenBar-GNOME maintainers - 0.1.1-1
- Wider popover; the overlay scrollbar no longer covers the content

* Wed Nov 18 2026 PokeTokenBar-GNOME maintainers - 0.1.0-1
- First usable build: Claude Code usage, official limits, companion, top bar and popover

* Wed Nov 18 2026 PokeTokenBar-GNOME maintainers - 0.0.1-1
- Initial package
