#!/usr/bin/env bash
# install.sh — build Murmel and install it with its desktop integration.
#
# Usage:
#   tools/install.sh [--prefix DIR] [--destdir DIR] [--no-build]
#   tools/install.sh --uninstall [--prefix DIR]
#
#   --prefix DIR   installation prefix (default: /usr/local); ~/.local
#                  installs for the current user only, without root
#   --destdir DIR  stage the files under DIR (for packages); the installed
#                  paths still refer to the prefix
#   --no-build     install an already built ./murmel
#   --uninstall    remove what an earlier installation to the prefix added
#
# Installs the binary, the settings schema, the desktop file, the
# metainfo, the icons, the D-Bus service (Murmel is D-Bus activated), the
# translations, and the licenses of Murmel and of the components built
# into it (tools/licenses.txt). Without --destdir it also compiles the schemas and
# refreshes the icon and desktop caches. The Flatpak manifest and the RPM
# spec use this script too.
set -euo pipefail
cd "$(dirname "$0")/.."

APP_ID=io.github.murmel.Murmel
prefix=/usr/local
destdir=
build=1
uninstall=
while [ $# -gt 0 ]; do
	case "$1" in
	--prefix) prefix=${2:?--prefix needs a directory}; shift 2 ;;
	--prefix=*) prefix=${1#*=}; shift ;;
	--destdir) destdir=${2:?--destdir needs a directory}; shift 2 ;;
	--destdir=*) destdir=${1#*=}; shift ;;
	--no-build) build=; shift ;;
	--uninstall) uninstall=1; shift ;;
	-h | --help) sed -n '2,20p' "$0"; exit 0 ;;
	*) echo "install.sh: unknown option $1" >&2; exit 2 ;;
	esac
done
prefix=${prefix%/}
root=$destdir$prefix
share=$root/share
sizes="48 64 128 256"
# Licenses go to share/licenses/<app id> in a Flatpak (Flathub's layout),
# to share/licenses/murmel elsewhere (as rpm's %license does).
licenses=$share/licenses/${FLATPAK_ID:-murmel}

files=(
	"bin/murmel"
	"share/applications/$APP_ID.desktop"
	"share/metainfo/$APP_ID.metainfo.xml"
	"share/glib-2.0/schemas/$APP_ID.gschema.xml"
	"share/dbus-1/services/$APP_ID.service"
	"share/icons/hicolor/scalable/apps/$APP_ID.svg"
)
for s in $sizes; do
	files+=("share/icons/hicolor/${s}x${s}/apps/$APP_ID.png")
done
langs=$(grep -v '^\s*#' po/LINGUAS | tr -s ' \t' '\n' | sed '/^$/d' || true)
for l in $langs; do
	files+=("share/locale/$l/LC_MESSAGES/murmel.mo")
done

# refresh updates the caches the desktop reads; only for a live install.
refresh() {
	[ -z "$destdir" ] || return 0
	if command -v glib-compile-schemas >/dev/null; then
		glib-compile-schemas "$share/glib-2.0/schemas"
	fi
	# Inside a Flatpak build the caches are flatpak's business.
	[ -z "${FLATPAK_ID:-}" ] || return 0
	if command -v gtk-update-icon-cache >/dev/null && [ -f "$share/icons/hicolor/index.theme" ]; then
		gtk-update-icon-cache -q -t "$share/icons/hicolor" || true
	fi
	if command -v update-desktop-database >/dev/null; then
		update-desktop-database -q "$share/applications" || true
	fi
}

if [ -n "$uninstall" ]; then
	for f in "${files[@]}"; do
		rm -f "$root/$f"
	done
	rm -rf "$licenses"
	refresh
	echo "install.sh: removed Murmel from $prefix"
	exit 0
fi

if [ -n "$build" ]; then
	version=$(tools/version.sh)
	echo "install.sh: building murmel $version"
	go build -mod=vendor -trimpath \
		-ldflags "-X io.github/murmel/internal/ui.Version=$version" \
		-o murmel ./cmd/murmel
fi
[ -x murmel ] || { echo "install.sh: ./murmel not built" >&2; exit 1; }

install -Dm755 murmel "$root/bin/murmel"
install -Dm644 data/$APP_ID.desktop "$share/applications/$APP_ID.desktop"
install -Dm644 packaging/$APP_ID.metainfo.xml "$share/metainfo/$APP_ID.metainfo.xml"
install -Dm644 data/$APP_ID.gschema.xml "$share/glib-2.0/schemas/$APP_ID.gschema.xml"
# The desktop file is D-Bus activatable: the session bus starts the
# binary named in the service file.
install -d "$share/dbus-1/services"
sed "s|^Exec=.*/murmel |Exec=$prefix/bin/murmel |" data/$APP_ID.service \
	>"$share/dbus-1/services/$APP_ID.service"
install -Dm644 data/icons/hicolor/scalable/apps/$APP_ID.svg \
	"$share/icons/hicolor/scalable/apps/$APP_ID.svg"
for s in $sizes; do
	install -Dm644 "data/icons/hicolor/${s}x${s}/apps/$APP_ID.png" \
		"$share/icons/hicolor/${s}x${s}/apps/$APP_ID.png"
done
tools/install-translations.sh "$share/locale"
install -Dm644 LICENSE "$licenses/LICENSE"
while read -r name _ paths; do
	case "$name" in "" | \#*) continue ;; esac
	for f in $paths; do
		install -Dm644 "$f" "$licenses/$name/$(basename "$f")"
	done
done <tools/licenses.txt
refresh
echo "install.sh: installed Murmel to $root"
