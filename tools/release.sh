#!/usr/bin/env bash
# release.sh — cut a Murmel release and publish it into web/.
#
# Usage:
#   tools/release.sh <version>     release x.y.z: bump, commit, tag, build, publish
#   tools/release.sh --refs-only   only regenerate the web/ descriptor files
#                                  and the latest bundle (e.g. after changing
#                                  WEB_URL in tools/release.conf), then upload
#   tools/release.sh --upload      only upload web/ to the server
#   tools/release.sh --rebuild     rebuild and publish the current, already
#                                  tagged version (e.g. after a packaging fix)
#
# A release ends by uploading web/ to the server configured in
# tools/release.conf (an SFTP location mounted via GVfs).
#
# web/ afterwards holds everything to upload (rsync it as a whole):
#   repo/                              signed OSTree Flatpak repository
#   murmel.flatpakrepo                 "add the repository" file
#   io.github.one337.murmel.flatpakref one-click install file
#   murmel.gpg                         public signing key
#   downloads/murmel-<version>.flatpak single-file bundles
#   index.html                         install instructions
#   screenshots/                       screenshots referenced by the metainfo
#   media/                             software center copies (thumbnails)
#
# Release notes default to the feat/fix commit subjects since the last
# tag; set RELEASE_NOTES (one item per line) to write them by hand.
#
# Requires a clean git tree for a release. The signing key lives outside
# the project (see tools/release.conf).

set -euo pipefail
cd "$(dirname "$0")/.."
source tools/release.conf
# Upload target and other local settings, kept out of git.
if [ -f tools/release.local.conf ]; then
	source tools/release.local.conf
fi

APP_ID=io.github.one337.murmel
MANIFEST=packaging/$APP_ID.json
METAINFO=packaging/$APP_ID.metainfo.xml
WEB=web
REPO=$WEB/repo
FLATHUB=https://dl.flathub.org/repo/flathub.flatpakrepo
GPG_ARGS=(--gpg-sign="$GPG_KEY" --gpg-homedir="$GPG_HOME")

die() { printf 'release: %s\n' "$*" >&2; exit 1; }

[ -d "$GPG_HOME" ] || die "signing key home $GPG_HOME missing"
WEB_URL="${WEB_URL%/}/"

# xml_escape escapes text for XML element content.
xml_escape() { sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g'; }

# release_notes prints <li> items: RELEASE_NOTES if set, otherwise the
# user-facing commits since the last tag.
release_notes() {
	if [ -n "${RELEASE_NOTES:-}" ]; then
		printf '%s\n' "$RELEASE_NOTES" | sed '/^[[:space:]]*$/d' | xml_escape |
			sed -e 's/^/          <li>/' -e 's/$/<\/li>/'
		return
	fi
	local range=HEAD
	if last=$(git describe --tags --abbrev=0 --match 'v*' 2>/dev/null); then
		range="$last..HEAD"
	fi
	git log --no-merges --format='%s' "$range" |
		grep -E '^(feat|fix|perf)(\([^)]*\))?!?: ' |
		sed -E 's/^[a-z]+(\([^)]*\))?!?: //' |
		sed -E 's/^(.)/\U\1/' |
		xml_escape |
		sed -e 's/^/          <li>/' -e 's/$/<\/li>/'
}

bump_metainfo() {
	local version=$1 date notes block
	date=$(date +%F)
	notes=$(release_notes)
	[ -n "$notes" ] || notes="          <li>Maintenance release</li>"
	block=$(printf '    <release version="%s" date="%s">\n      <description>\n        <ul>\n%s\n        </ul>\n      </description>\n    </release>' "$version" "$date" "$notes")
	python3 - "$METAINFO" "$block" <<'PY'
import sys
path, block = sys.argv[1], sys.argv[2]
s = open(path, encoding="utf-8").read()
marker = "<releases>\n"
assert marker in s, "no <releases> in metainfo"
s = s.replace(marker, marker + block + "\n", 1)
open(path, "w", encoding="utf-8").write(s)
PY
}

latest_version() { cat VERSION; }

write_descriptors() {
	local version key_b64
	version=$(latest_version)
	key_b64=$(gpg --homedir "$GPG_HOME" --export "$GPG_KEY" | base64 -w0)
	gpg --homedir "$GPG_HOME" --export --armor "$GPG_KEY" > "$WEB/murmel.gpg"

	cat > "$WEB/murmel.flatpakrepo" <<EOR
[Flatpak Repo]
Title=Murmel
Url=${WEB_URL}repo/
Homepage=${WEB_URL}
Comment=Murmel – a Mumble voice chat client for GNOME
Description=Releases of Murmel, a Mumble voice chat client for GNOME.
DefaultBranch=$BRANCH
GPGKey=$key_b64
EOR

	cat > "$WEB/$APP_ID.flatpakref" <<EOR
[Flatpak Ref]
Name=$APP_ID
Branch=$BRANCH
Title=Murmel
Url=${WEB_URL}repo/
RuntimeRepo=$FLATHUB
SuggestRemoteName=murmel
IsRuntime=false
GPGKey=$key_b64
EOR

	mkdir -p "$WEB/downloads"
	flatpak build-bundle "${GPG_ARGS[@]}" --gpg-keys=<(gpg --homedir "$GPG_HOME" --export "$GPG_KEY") \
		--runtime-repo="$FLATHUB" --repo-url="${WEB_URL}repo/" \
		"$REPO" "$WEB/downloads/murmel-$version.flatpak" "$APP_ID" "$BRANCH"
	# A copy, not a symlink: FTP uploads and some hosts drop symlinks.
	cp -f "$WEB/downloads/murmel-$version.flatpak" "$WEB/downloads/murmel-latest.flatpak"

	sed -e "s|@URL@|$WEB_URL|g" -e "s|@VERSION@|$version|g" -e "s|@APP_ID@|$APP_ID|g" \
		tools/release-index.html > "$WEB/index.html"

	# Screenshots referenced by the metainfo (software centers load them
	# from the website).
	if [ -d assets/screenshots ]; then
		mkdir -p "$WEB/screenshots"
		cp -r assets/screenshots/. "$WEB/screenshots/"
	fi
	echo "release: web/ descriptors written for $version ($WEB_URL)"
}

# upload mirrors web/ to the GVfs-mounted SFTP target, remounting it when
# the connection dropped. GVfs SFTP does not keep modification times, so
# rsync cannot detect changes by time. The repository layout makes that
# unnecessary: objects, deltas and versioned bundles are named by content
# or version and never change (size comparison suffices), while the few
# mutable files are always copied. The order keeps the published
# repository consistent: content first, then refs/summary/descriptors,
# and only then are stale files deleted.
upload() {
	if [ -z "${UPLOAD_HOST:-}" ]; then
		echo "release: no upload target configured; upload web/ to $WEB_URL by hand"
		return
	fi
	local mount="/run/user/$(id -u)/gvfs/sftp:host=$UPLOAD_HOST,port=$UPLOAD_PORT,user=$UPLOAD_USER"
	local dest="$mount$UPLOAD_PATH"
	if [ ! -d "$dest" ]; then
		gio mount "sftp://$UPLOAD_USER@$UPLOAD_HOST:$UPLOAD_PORT/" </dev/null >/dev/null 2>&1 || true
	fi
	[ -d "$dest" ] || die "upload target $dest not reachable; open it once in Files (save the password), then run tools/release.sh --upload"

	local base=(-r --no-perms --no-times --omit-dir-times)
	local mutable=(
		--include='/repo/' --include='/repo/summary*' --include='/repo/summaries/***'
		--include='/repo/refs/***' --include='/repo/config'
		--include='/downloads/' --include='/downloads/murmel-latest.flatpak'
		--include='/*.flatpakrepo' --include='/*.flatpakref' --include='/murmel.gpg' --include='/index.html'
	)
	# 1. Immutable content, compared by size.
	rsync "${base[@]}" --size-only \
		--exclude='/repo/summary*' --exclude='/repo/summaries/' --exclude='/repo/refs/' --exclude='/repo/config' \
		--exclude='/downloads/murmel-latest.flatpak' \
		--exclude='/*.flatpakrepo' --exclude='/*.flatpakref' --exclude='/murmel.gpg' --exclude='/index.html' \
		"$WEB/" "$dest/"
	# 2. Mutable files, always copied.
	rsync "${base[@]}" --ignore-times "${mutable[@]}" --exclude='*' "$WEB/" "$dest/"
	# 3. Remove what web/ no longer has (pruned objects, old deltas).
	rsync "${base[@]}" --delete --existing --ignore-existing "$WEB/" "$dest/"

	local code
	code=$(curl -s -o /dev/null -w '%{http_code}' "${WEB_URL}repo/summary" || true)
	[ "$code" = 200 ] || die "uploaded, but ${WEB_URL}repo/summary answers HTTP $code"
	echo "release: uploaded web/ to $WEB_URL"
}

# The app ID until 0.3.9. Its last build stays in the repository, marked
# end-of-life with a pointer to the new ID (no data migration): flatpak
# update and the software centers show the message. Runs once; later
# releases find the refs marked already.
OLD_APP_ID=io.github.murmel.Murmel
retire_old_app() {
	local ref msg
	msg="Murmel has a new app ID, $APP_ID. Install it from ${WEB_URL} and remove this app; saved passwords and settings have to be set up again."
	for ref in "app/$OLD_APP_ID/x86_64/$BRANCH" "runtime/$OLD_APP_ID.Debug/x86_64/$BRANCH"; do
		ostree --repo="$REPO" rev-parse "$ref" >/dev/null 2>&1 || continue
		if [ -n "$(ostree --repo="$REPO" show --print-metadata-key=ostree.endoflife "$ref" 2>/dev/null)" ]; then
			continue
		fi
		flatpak build-commit-from "${GPG_ARGS[@]}" --end-of-life="$msg" --src-ref="$ref" "$REPO" "$ref"
		echo "release: marked $ref end-of-life"
	done
	rm -f "$WEB/$OLD_APP_ID.flatpakref"
}

# build_and_publish builds the current tree into the signed repository,
# refreshes summary and deltas, writes the descriptors and uploads.
# Screenshots are mirrored to ${WEB_URL}media/: appstream compose runs
# without network inside flatpak-builder and would otherwise drop the
# metainfo's remote screenshot URLs from the repository's AppStream data.
build_and_publish() {
	mkdir -p "$WEB"
	# The Flatpak build does not see .git: tell it the release version, so
	# the About dialog shows "0.3.7" and not "0.3.7-dev".
	echo "$version" >build-version
	trap 'rm -f build-version' EXIT
	flatpak-builder --force-clean --default-branch="$BRANCH" "${GPG_ARGS[@]}" \
		--mirror-screenshots-url="${WEB_URL}media/" --compose-url-policy=full \
		--repo="$REPO" build-release "$MANIFEST"
	# The mirrored screenshots and thumbnails compose produced; the
	# AppStream data points at them with full URLs (url policy "full",
	# since the repository summary drops a relative media base URL).
	local media=build-release/files/share/app-info/media
	if [ -d "$media" ]; then
		mkdir -p "$WEB/media"
		cp -r "$media/." "$WEB/media/"
	fi
	retire_old_app
	flatpak build-update-repo "${GPG_ARGS[@]}" --title="Murmel" \
		--default-branch="$BRANCH" --generate-static-deltas --prune --prune-depth=5 "$REPO"
	write_descriptors
	upload
}

case "${1:-}" in
--rebuild)
	[ -z "$(git status --porcelain)" ] || die "git tree not clean; commit first"
	version=$(latest_version)
	git rev-parse -q --verify "refs/tags/v$version" >/dev/null || die "no tag v$version"
	# Only tooling and docs may have changed since the release; anything
	# that ends up in the app needs a new version.
	git diff --quiet "v$version" HEAD -- . ':!tools' ':!README.md' ||
		die "app files changed since v$version; release a new version instead"
	build_and_publish
	echo "release: v$version rebuilt and published"
	exit 0
	;;
--refs-only)
	[ -d "$REPO" ] || die "no $REPO yet; run a release first"
	write_descriptors
	upload
	exit 0
	;;
--upload)
	[ -d "$REPO" ] || die "no $REPO yet; run a release first"
	upload
	exit 0
	;;
esac

version=${1:-}
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "usage: tools/release.sh <x.y.z> | --refs-only"
[ -z "$(git status --porcelain)" ] || die "git tree not clean; commit first"
git rev-parse -q --verify "refs/tags/v$version" >/dev/null && die "tag v$version exists"

echo "$version" > VERSION
bump_metainfo "$version"
appstreamcli validate --no-net "$METAINFO" >/dev/null 2>&1 ||
	echo "release: warning: appstreamcli validate reports issues" >&2
git add VERSION "$METAINFO"
git commit -q -m "chore(release): v$version"
git tag -a "v$version" -m "Murmel $version"

build_and_publish
echo "release: v$version done"
