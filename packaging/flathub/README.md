# Flathub manifest template

`io.github.one337.murmel.json` is the starting point for the Flathub
submission pull request. It builds a release tag from the public Git
repository; `packaging/io.github.one337.murmel.json` stays the manifest
for local builds (`tools/testbuild.sh`, `tools/release.sh`), which use the
working tree.

Once Flathub has accepted the app, the manifest lives in the app's own
repository under the Flathub organization and is maintained there; this
template is not kept up to date after that.

Before submitting:

- Set `tag` and `commit` to the release to publish:
  `git rev-list -n1 v<version>` gives the commit of the tag.
- Keep `finish-args` in line with the local manifest.
- Lint both the manifest and the metainfo:

      flatpak run --command=flatpak-builder-lint org.flatpak.Builder manifest packaging/flathub/io.github.one337.murmel.json
      flatpak run --command=flatpak-builder-lint org.flatpak.Builder appstream packaging/io.github.one337.murmel.metainfo.xml

The build has no network: the Go modules come from `vendor/`. It does not
see `.git` either, so the first build command writes `VERSION` (on a release
tag, the released version) to `build-version` for the About dialog. The
screenshots in the metainfo are served from the project website; Flathub
mirrors them when it publishes the build.
