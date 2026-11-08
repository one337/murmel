module io.github/murmel

go 1.27

require (
	github.com/diamondburned/gotk4-adwaita/pkg v0.0.0-20261031204147-41c057c79b39
	github.com/diamondburned/gotk4/pkg v0.4.2-0.20261031093702-169e0f82bca8
	github.com/gen2brain/malgo v0.11.27
	github.com/golang/protobuf v1.3.1
	github.com/jfreymuth/oggvorbis v1.0.5
	github.com/leonelquinteros/gotext v1.8.0
	gopkg.in/hraban/opus.v2 v2.0.0-20230925203106-0188a62cb302
	layeh.com/gumble v0.0.0-20221205141517-d1df60a3cc14
)

require (
	github.com/jfreymuth/vorbis v1.0.2 // indirect
	golang.org/x/sync v0.22.0 // indirect
)

replace gopkg.in/hraban/opus.v2 => ./third_party/hraban-opus
