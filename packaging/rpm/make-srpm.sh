#!/usr/bin/env bash
# Builds the source RPM from the current git checkout into the directory
# given as $1: a tarball of HEAD plus the spec from murmel.spec.in with the
# version from VERSION, the release (1 on a release tag, otherwise a
# dated git snapshot after it) and a changelog from the metainfo.
set -euo pipefail
cd "$(dirname "$0")/../.."
outdir=${1:?usage: make-srpm.sh <outdir>}
mkdir -p "$outdir"
outdir=$(cd "$outdir" && pwd)

# Copr's clone may belong to another user inside the build chroot.
git config --global --add safe.directory "$PWD" 2>/dev/null || true

version=$(cat VERSION)
display_version=$(tools/version.sh)
if [ "$(git tag --points-at HEAD --list "v$version")" = "v$version" ]; then
	release=1
else
	release="1.$(git log -1 --format=%cd --date=format:%Y%m%d HEAD).git$(git rev-parse --short=7 HEAD)"
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
git archive --format=tar.gz --prefix="murmel-$version/" -o "$work/murmel-$version.tar.gz" HEAD

python3 - "$version" "$release" "$display_version" packaging/rpm/murmel.spec.in \
	packaging/io.github.murmel.Murmel.metainfo.xml "$work/murmel.spec" <<'PY'
import sys, datetime, xml.etree.ElementTree as ET
version, release, display_version, template, metainfo, out = sys.argv[1:]
packager = "Simon Tschöp <13205365+one337@users.noreply.github.com>"
entries = []
for rel in ET.parse(metainfo).getroot().iter("release"):
    v, d = rel.get("version"), rel.get("date")
    day = datetime.date.fromisoformat(d).strftime("%a %b %d %Y")
    items = ["- " + " ".join("".join(li.itertext()).split()).replace("%", "%%") for li in rel.iter("li")] or ["- New release"]
    entries.append(f"* {day} {packager} - {v}-1\n" + "\n".join(items))
spec = open(template, encoding="utf-8").read()
spec = spec.replace("@VERSION@", version).replace("@RELEASE@", release)\
    .replace("@DISPLAY_VERSION@", display_version)
spec = spec.replace("@CHANGELOG@", "\n\n".join(entries))
open(out, "w", encoding="utf-8").write(spec)
PY

rpmbuild -bs "$work/murmel.spec" \
	--define "_sourcedir $work" --define "_srcrpmdir $outdir" --define "dist %{nil}"
