//go:build ignore

// Generates Mumble.pb.go from Mumble's Mumble.proto. Needs protoc.
//
// The schema stays at a fixed Mumble commit, so that generating again does
// not change the messages; the generator version matches the
// google.golang.org/protobuf version in go.mod.
package main

import (
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
)

const (
	protoURL     = "https://raw.githubusercontent.com/mumble-voip/mumble/5a4f5297a37968b941a6ebee12b2778eb998b661/src/Mumble.proto"
	protocGenGo  = "google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12"
	importPath   = "github.com/one337/murmel/internal/protocol/gumble/MumbleProto"
	pluginBinDir = "protoc-gen-go.bin"
)

func main() {
	resp, err := http.Get(protoURL)
	if err != nil {
		log.Fatalf("could not download Mumble.proto: %s\n", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Fatalf("could not download Mumble.proto: %s\n", resp.Status)
	}
	f, err := os.Create("Mumble.proto")
	if err != nil {
		log.Fatalf("could not create Mumble.proto: %s\n", err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		log.Fatalf("could not write Mumble.proto: %s\n", err)
	}
	if err := f.Close(); err != nil {
		log.Fatalf("could not close Mumble.proto: %s\n", err)
	}
	defer os.Remove("Mumble.proto")

	bin, err := filepath.Abs(pluginBinDir)
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(bin)
	install := exec.Command("go", "install", protocGenGo)
	install.Env = append(os.Environ(), "GOBIN="+bin, "GOFLAGS=")
	install.Stderr = os.Stderr
	if err := install.Run(); err != nil {
		log.Fatalf("could not build protoc-gen-go: %s\n", err)
	}

	protoc := exec.Command("protoc",
		"--plugin=protoc-gen-go="+filepath.Join(bin, "protoc-gen-go"),
		"--go_out=.", "--go_opt=paths=source_relative",
		"--go_opt=MMumble.proto="+importPath,
		"Mumble.proto")
	protoc.Stderr = os.Stderr
	if err := protoc.Run(); err != nil {
		log.Fatalf("could not run protoc: %s\n", err)
	}
}
