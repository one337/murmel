package main

import (
	"strings"
	"testing"
	"time"

	"io.github/murmel/internal/protocol/gumble"
)

// The tree lists each channel once, sorted and indented, also when the
// server sends a loop (a channel below its own child).
func TestChannelTreeLoop(t *testing.T) {
	root := &gumble.Channel{ID: 0, Name: "Root", Children: gumble.Channels{}}
	lobby := &gumble.Channel{ID: 1, Name: "Lobby", Children: gumble.Channels{}}
	games := &gumble.Channel{ID: 2, Name: "Games", Children: gumble.Channels{}}
	root.Children[1] = lobby
	root.Children[2] = games
	lobby.Children[0] = root // the loop
	games.Children[1] = lobby
	done := make(chan []string, 1)
	go func() { done <- channelTree(root) }()
	select {
	case got := <-done:
		want := "Root|  Games|    Lobby"
		if strings.Join(got, "|") != want {
			t.Errorf("tree %q, want %q", strings.Join(got, "|"), want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the tree does not finish on a channel loop")
	}
}
