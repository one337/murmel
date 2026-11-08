package richtext

import (
	"encoding/base64"
	"strings"
	"testing"
)

func markup(blocks []Block) []string {
	var out []string
	for _, b := range blocks {
		if b.Image != nil {
			out = append(out, "[img]")
		} else {
			out = append(out, b.Markup)
		}
	}
	return out
}

func TestParse(t *testing.T) {
	png := base64.StdEncoding.EncodeToString([]byte("\x89PNG fake"))
	for _, tt := range []struct {
		name string
		in   string
		want []string
	}{
		{"plain keeps newlines", "one\ntwo & three", nil},
		{"plain", "Hallo Welt", []string{"Hallo Welt"}},
		{"inline", "Hi <b>bold <i>both</i></b> end", []string{"Hi <b>bold <i>both</i></b> end"}},
		{"strong em", "<strong>a</strong><em>b</em>", []string{"<b>a</b><i>b</i>"}},
		{"misnested", "<b>a<i>b</b>c</i>", []string{"<b>a<i>b</i></b>c"}}, // the decoder closes <i> with </b>
		{"br", "a<br>b", []string{"a\nb"}},
		{"paragraphs", "<p>one</p><p>two</p>", []string{"one\ntwo"}},
		{"list", "<ul><li>x</li><li>y</li></ul>", []string{"• x\n• y"}},
		{"whitespace", "<p>  lots   of\n space </p>", []string{"lots of space"}},
		{"escaping", "<b>1 &lt; 2 &amp; &quot;q&quot;</b>", []string{"<b>1 &lt; 2 &amp; &#34;q&#34;</b>"}},
		{"safe link", `<a href="https://x.org/?a=1&b=2">site</a>`, []string{`<a href="https://x.org/?a=1&amp;b=2">site</a>`}},
		{"unsafe link", `<a href="javascript:alert(1)">click</a>`, []string{"click"}},
		{"script dropped", "<script>evil()</script>ok", []string{"ok"}},
		{"unknown tags", `<font color="red"><span style="x">t</span></font>`, []string{"t"}},
		{"remote image", `see <img src="https://tracker/x.png">`, []string{`see <a href="https://tracker/x.png">[image]</a>`}},
		{"data image", "pic:<img src=\"data:image/png;base64," + png + "\">after", []string{"pic:", "[img]", "after"}},
		{"image only", "<img src=\"data:image/png;base64," + png + "\"/>", []string{"[img]"}},
		{"empty", "<p> </p>", nil},
	} {
		got := markup(Parse(tt.in))
		if tt.name == "plain keeps newlines" {
			if len(got) != 1 || got[0] != "one\ntwo &amp; three" {
				t.Errorf("%s: %q", tt.name, got)
			}
			continue
		}
		if len(got) != len(tt.want) {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("%s: block %d = %q, want %q", tt.name, i, got[i], tt.want[i])
			}
		}
	}
}

func TestDataImageDecoded(t *testing.T) {
	data := []byte("\x89PNG\r\n\x1a\nrest")
	blocks := Parse(`<img src="data:image/png;base64,` + base64.StdEncoding.EncodeToString(data) + `">`)
	if len(blocks) != 1 || string(blocks[0].Image) != string(data) {
		t.Fatalf("image not decoded: %+v", blocks)
	}
	for _, b := range Parse(`<img src="data:image/png;base64,!!!notbase64">`) {
		if b.Image != nil {
			t.Fatal("broken data URI decoded as an image")
		}
	}
}

func TestFit(t *testing.T) {
	for _, tt := range []struct{ w, h, mw, mh, ww, wh int }{
		{800, 600, 400, 0, 400, 300},
		{800, 600, 400, 200, 266, 200},
		{100, 50, 400, 300, 100, 50},
	} {
		if w, h := fit(tt.w, tt.h, tt.mw, tt.mh); w != tt.ww || h != tt.wh {
			t.Errorf("fit(%d,%d,%d,%d) = %d,%d, want %d,%d", tt.w, tt.h, tt.mw, tt.mh, w, h, tt.ww, tt.wh)
		}
	}
}

func TestDataURIVariants(t *testing.T) {
	data := []byte("\x89PNG\r\n\x1a\nsome image bytes!")
	std := base64.StdEncoding.EncodeToString(data)
	for name, src := range map[string]string{
		"plain":        "data:image/png;base64," + std,
		"upper case":   "DATA:IMAGE/PNG;BASE64," + std,
		"parameters":   "data:image/png;charset=utf-8;base64," + std,
		"url encoded":  "data:image/png;base64," + strings.ReplaceAll(strings.ReplaceAll(std, "+", "%2B"), "/", "%2F"),
		"wrapped":      "data:image/png;base64," + std[:10] + "\n  " + std[10:],
		"no padding":   "data:image/png;base64," + strings.TrimRight(std, "="),
		"url-safe b64": "data:image/png;base64," + base64.RawURLEncoding.EncodeToString(data),
	} {
		got, ok := decodeDataURI(src)
		if !ok || string(got) != string(data) {
			t.Errorf("%s: not decoded", name)
		}
	}
	if _, ok := decodeDataURI("data:text/html;base64," + std); ok {
		t.Error("non-image data URI accepted")
	}
}
