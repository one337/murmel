package secretstest

import (
	"bufio"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const secretsPkg = "/internal/ui/secrets"

// pkg is one directory of the module, as far as this check needs it.
type pkg struct {
	imports     []string // of the non-test files
	testImports []string
	isolates    bool // a test file calls Isolate
	hasTests    bool
}

// Every test program that links libsecret (the secrets package itself,
// anything importing it, directly or not, and anything whose tests do)
// calls Isolate.
func TestEveryTestProgramWithLibsecretIsolates(t *testing.T) {
	root, module := moduleRoot(t)
	files, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	pkgs := map[string]*pkg{}
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			if rel == "vendor" || rel == "target" || strings.HasPrefix(d.Name(), ".") && rel != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, err := files.ReadFile(rel)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, path, src, parser.ImportsOnly)
		if err != nil {
			return err
		}
		ip := module
		if dir := filepath.Dir(rel); dir != "." {
			ip += "/" + filepath.ToSlash(dir)
		}
		p := pkgs[ip]
		if p == nil {
			p = &pkg{}
			pkgs[ip] = p
		}
		var imps []string
		for _, s := range f.Imports {
			v, _ := strconv.Unquote(s.Path.Value)
			imps = append(imps, v)
		}
		if strings.HasSuffix(path, "_test.go") {
			p.hasTests = true
			p.testImports = append(p.testImports, imps...)
			if strings.Contains(string(src), "secretstest.Isolate()") {
				p.isolates = true
			}
		} else {
			p.imports = append(p.imports, imps...)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	memo := map[string]bool{}
	var links func(ip string) bool
	links = func(ip string) bool {
		if ip == module+secretsPkg {
			return true
		}
		if v, ok := memo[ip]; ok {
			return v
		}
		memo[ip] = false
		p := pkgs[ip]
		if p == nil {
			return false
		}
		for _, imp := range p.imports {
			if links(imp) {
				memo[ip] = true
				return true
			}
		}
		return false
	}
	checked := 0
	for ip, p := range pkgs {
		if !p.hasTests || ip == module+secretsPkg+"/secretstest" {
			continue
		}
		need := links(ip)
		for _, imp := range p.testImports {
			need = need || links(imp)
		}
		if !need {
			continue
		}
		checked++
		if !p.isolates {
			t.Errorf("%s: its tests link libsecret, but no TestMain calls secretstest.Isolate()", ip)
		}
	}
	if checked == 0 {
		t.Error("found no test program that links libsecret: the check looks in the wrong place")
	}
}

// moduleRoot returns the directory of go.mod and the module path.
func moduleRoot(t *testing.T) (string, string) {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		f, err := os.Open(filepath.Join(dir, "go.mod"))
		if err == nil {
			defer f.Close()
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				if m, ok := strings.CutPrefix(sc.Text(), "module "); ok {
					return dir, strings.TrimSpace(m)
				}
			}
			t.Fatal("go.mod names no module")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found")
		}
		dir = parent
	}
}
