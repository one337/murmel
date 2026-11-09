package admin

import (
	"encoding/xml"
	"io"
	"reflect"
	"strings"
	"testing"
)

// png1x1 is a minimal PNG file.
var png1x1 = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N\x00\x00\x00\x00IEND\xaeB`\x82")

func TestParseDescription(t *testing.T) {
	in := `<h2>Willkommen   in der Lobby</h2><p>Hier <b>bitte</b> die <i>Regeln</i> und <a href="https://example.org">Infos</a>.</p>` +
		`<ul><li>Kein Spam</li><li><b>Fragen</b> an Admins</li></ul><p><span style="color:red">Rot</span> <u>ist</u> weg<br>zweite Zeile</p>` +
		`<a href="javascript:alert(1)">böse</a>`
	got := parseDescription(in)
	want := []descLine{
		{Kind: lineHeading, Runs: []descRun{{Text: "Willkommen in der Lobby"}}},
		// A collapsed space stays with the text before it.
		{Runs: []descRun{{Text: "Hier "}, {Text: "bitte ", Bold: true}, {Text: "die "}, {Text: "Regeln ", Italic: true},
			{Text: "und "}, {Text: "Infos", Link: "https://example.org"}, {Text: "."}}},
		{Kind: lineItem, Runs: []descRun{{Text: "Kein Spam"}}},
		{Kind: lineItem, Runs: []descRun{{Text: "Fragen ", Bold: true}, {Text: "an Admins"}}},
		{Runs: []descRun{{Text: "Rot ist weg"}}},
		{Runs: []descRun{{Text: "zweite Zeile"}}},
		{Runs: []descRun{{Text: "böse"}}}, // unsafe link dropped, text kept
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parse:\n got %+v\nwant %+v", got, want)
	}
}

func TestDescriptionRoundTripIsXML(t *testing.T) {
	lines := []descLine{
		{Kind: lineImage, Image: png1x1},
		{Kind: lineHeading, Runs: []descRun{{Text: "Titel & <Mehr>"}}},
		{Runs: []descRun{{Text: "fett ", Bold: true}, {Text: "und "}, {Text: "beides", Bold: true, Italic: true, Link: "https://a.b/?x=1&y=2"}}},
		{Kind: lineItem, Runs: []descRun{{Text: "eins"}}},
		{Kind: lineItem, Runs: []descRun{{Text: "zwei"}}},
		{Runs: []descRun{{Text: "Ende"}}},
	}
	out := descriptionHTML(lines)
	if !strings.HasPrefix(out, `<img src="data:image/png;base64,`) {
		t.Errorf("image first: %.60s", out)
	}
	d := xml.NewDecoder(strings.NewReader("<root>" + out + "</root>"))
	for {
		if _, err := d.Token(); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("not well-formed XML: %v\n%s", err, out)
		}
	}
	if back := parseDescription(out); !reflect.DeepEqual(back, lines) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", back, lines)
	}
}

func TestParsePlainText(t *testing.T) {
	got := parseDescription("Zeile eins\nZeile zwei\n")
	if len(got) != 2 || got[1].Runs[0].Text != "Zeile zwei" {
		t.Fatalf("got %+v", got)
	}
}
