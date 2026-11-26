module github.com/one337/murmel

go 1.27

require (
	github.com/diamondburned/gotk4-adwaita/pkg v0.0.0-20261031204147-41c057c79b39
	github.com/diamondburned/gotk4/pkg v0.4.2-0.20261031093702-169e0f82bca8
	github.com/gen2brain/malgo v0.11.27
	github.com/jfreymuth/oggvorbis v1.0.5
	github.com/leonelquinteros/gotext v1.8.0
	golang.org/x/image v0.48.0
	golang.org/x/net v0.62.0
	google.golang.org/protobuf v1.36.12
	gopkg.in/hraban/opus.v2 v2.0.0-20230925203106-0188a62cb302
	software.sslmate.com/src/go-pkcs12 v0.7.4
)

require (
	github.com/jfreymuth/vorbis v1.0.2 // indirect
	golang.org/x/crypto v0.59.0 // indirect
	golang.org/x/sync v0.25.0 // indirect
	golang.org/x/text v0.44.0 // indirect
)

replace gopkg.in/hraban/opus.v2 => ./third_party/hraban-opus
