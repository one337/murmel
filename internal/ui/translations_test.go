package ui

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// poEntry is one message of a translation file.
type poEntry struct {
	line      int
	fuzzy     bool
	id        string
	idPlural  string
	str       []string // msgstr, or msgstr[0], msgstr[1], …
	hasPlural bool
}

// readPO reads the messages of a .po file, without the header and the
// obsolete (#~) ones.
func readPO(path string) ([]poEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []poEntry
	var cur *poEntry
	var target *string
	flush := func() {
		if cur != nil && cur.id != "" {
			out = append(out, *cur)
		}
		cur, target = nil, nil
	}
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "":
			flush()
			continue
		case strings.HasPrefix(line, "#~"):
			continue
		case strings.HasPrefix(line, "#,"):
			if cur == nil {
				cur = &poEntry{line: n}
			}
			cur.fuzzy = cur.fuzzy || strings.Contains(line, "fuzzy")
			continue
		case strings.HasPrefix(line, "#"):
			continue
		}
		if cur == nil {
			cur = &poEntry{line: n}
		}
		key, rest, _ := strings.Cut(line, " ")
		if strings.HasPrefix(line, `"`) {
			key, rest = "", line
		}
		s, err := strconv.Unquote(rest)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %v", path, n, err)
		}
		switch {
		case key == "":
			if target == nil {
				return nil, fmt.Errorf("%s:%d: a continuation without a keyword", path, n)
			}
			*target += s
		case key == "msgctxt":
			target = new(string)
			*target = s
		case key == "msgid":
			cur.id = s
			target = &cur.id
		case key == "msgid_plural":
			cur.idPlural, cur.hasPlural = s, true
			target = &cur.idPlural
		case key == "msgstr":
			cur.str = append(cur.str, s)
			target = &cur.str[len(cur.str)-1]
		case strings.HasPrefix(key, "msgstr["):
			cur.str = append(cur.str, s)
			target = &cur.str[len(cur.str)-1]
		default:
			return nil, fmt.Errorf("%s:%d: unknown keyword %q", path, n, key)
		}
	}
	flush()
	return out, sc.Err()
}

// goVerb matches a Go format directive: %%, or flags, an argument index,
// width and precision (also given by arguments) and the verb.
var goVerb = regexp.MustCompile(`%(?:%|[-+# 0]*(?:\[(\d+)\])?(?:\*|\d+)?(?:\.(?:\*|\d+)?)?(?:\[(\d+)\])?([a-zA-Z]))`)

// formatArgs lists the arguments a format string uses, as
// "argument number:verb", sorted; a translation may reorder them with
// explicit indexes ("%[2]s … %[1]s").
func formatArgs(s string) []string {
	var out []string
	next := 1
	for _, m := range goVerb.FindAllStringSubmatch(s, -1) {
		if m[0] == "%%" {
			continue
		}
		arg := next
		for _, idx := range []string{m[1], m[2]} {
			if idx != "" {
				arg, _ = strconv.Atoi(idx)
			}
		}
		out = append(out, fmt.Sprintf("%d:%s", arg, m[3]))
		next = arg + 1
	}
	sort.Strings(out)
	return out
}

// formatMismatches lists the translated messages of a .po file whose
// translation does not use the same format arguments as the original,
// each plural form against the original singular (form 0) or plural.
func formatMismatches(path string) ([]string, error) {
	entries, err := readPO(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.fuzzy { // not compiled into the catalog: shown in English
			continue
		}
		for i, str := range e.str {
			if str == "" {
				continue
			}
			want := e.id
			if e.hasPlural && i > 0 {
				want = e.idPlural
			}
			if a, b := formatArgs(want), formatArgs(str); strings.Join(a, " ") != strings.Join(b, " ") {
				out = append(out, fmt.Sprintf("%s:%d: %q: translation %d uses %v, the original %v",
					filepath.Base(path), e.line, e.id, i, b, a))
			}
		}
	}
	return out, nil
}

// Every translation uses the same format arguments as its original, in
// every plural form: a missing or extra %s shows up as "%!s(MISSING)" or
// a literal "%s" in the interface.
func TestTranslationsKeepTheFormatArguments(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "po", "*.po"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no translations found (%v)", err)
	}
	for _, f := range files {
		bad, err := formatMismatches(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range bad {
			t.Error(b)
		}
	}
}

func TestFormatArgs(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"plain", ""},
		{"100%% sure", ""},
		{"%s joined %s", "1:s 2:s"},
		{"%[2]s is in %[1]s", "1:s 2:s"},
		{"%d member", "1:d"},
		{"%.1f s, %5.2f%%, %q, %v, %x", "1:f 2:f 3:q 4:v 5:x"},
		{"%*d", "1:d"},
	} {
		if got := strings.Join(formatArgs(tt.in), " "); got != tt.want {
			t.Errorf("formatArgs(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// The check finds what a bad merge leaves behind: lines of another
// message's translation, a lost or extra argument, a plural form without
// its number.
func TestFormatMismatchesFindsBrokenTranslations(t *testing.T) {
	po := filepath.Join(t.TempDir(), "xx.po")
	if err := os.WriteFile(po, []byte(`msgid ""
msgstr ""
"Plural-Forms: nplurals=2; plural=(n != 1);\n"

msgid "The SuperUser cannot be changed."
msgstr "Der SuperUser lässt sich nicht ändern."
"Der Server konnte nicht erreicht werden:\n"
"%s"

msgid "%s joined"
msgstr "beigetreten"

msgid "%s moved to %s"
msgstr "%[2]s: %[1]s ist da"

#, fuzzy
msgid "%s left"
msgstr "weg"

msgid "%d member"
msgid_plural "%d members"
msgstr[0] "ein Mitglied"
msgstr[1] "%d Mitglieder"

#~ msgid "%s old"
#~ msgstr "alt"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	bad, err := formatMismatches(po)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"The SuperUser cannot be changed.", "%s joined", "%d member"}
	if len(bad) != len(want) {
		t.Fatalf("found %d: %q; want %d", len(bad), bad, len(want))
	}
	for i, w := range want {
		if !strings.Contains(bad[i], strconv.Quote(w)) {
			t.Errorf("finding %d: %s; want one about %q", i, bad[i], w)
		}
	}
}
