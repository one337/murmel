# Local copy of gopkg.in/hraban/opus.v2

Trimmed copy of upstream `gopkg.in/hraban/opus.v2`
(v2.0.0-20230925203106-0188a62cb302), wired into the main module via a
`replace` directive in the root `go.mod`.

## Why

The upstream package bundles OGG-stream support (`stream.go`,
`stream_errors.go`, `streams_map.go`, `callbacks.c`) whose cgo preamble
requires pkg-config `opusfile` (libopusfile headers) in addition to `opus`.
Murmel needs only the raw packet encoder/decoder (Mumble carries bare Opus
packets, no OGG), and the build hosts have libopus but not libopusfile-devel.
Upstream guards those files with the `nolibopusfile` build tag, but a tag
cannot be enforced module-wide, and without it every `go build ./...` /
`go test ./...` fails.

## What changed

Nothing. The kept files (`encoder.go`, `decoder.go`, `errors.go`, `opus.go`)
are verbatim upstream. Only the four `!nolibopusfile`-tagged opusfile files
(and upstream's own test files / testdata) are excluded from this copy.

## Upgrade

Re-copy from a newer upstream module cache directory and delete
`stream*.go`, `streams_map.go`, `callbacks.c`, `*_test.go`, `testdata/`.
