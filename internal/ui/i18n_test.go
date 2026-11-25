package ui_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// textArgs maps the GTK/Adwaita functions and methods that show text to the
// positions of their text arguments; a string literal there must be wrapped
// in i18n.Tr(). Proper names (SetApplicationName, SetDeveloperName,
// SetCopyright) stay out on purpose, as do arguments that are IDs or action
// names (AddResponse's id, NewMenuItem's action).
var textArgs = map[string][]int{
	"SetTitle":                {0},
	"SetSubtitle":             {0},
	"SetDescription":          {0},
	"SetBody":                 {0},
	"SetComments":             {0},
	"SetTooltipText":          {0},
	"SetPlaceholderText":      {0},
	"SetLabel":                {0},
	"SetText":                 {0},
	"SetHeading":              {0},
	"SetButtonLabel":          {0},
	"SetName":                 {0}, // file filter names; widget names are lowercase IDs
	"NewButtonWithLabel":      {0},
	"NewCheckButtonWithLabel": {0},
	"NewLabel":                {0},
	"NewWindowTitle":          {0},
	"NewToast":                {0},
	"NewShortcutsSection":     {0},
	"NewShortcutsItem":        {0},
	"NewMenuItem":             {0},
	"Append":                  {0}, // menu labels; Box.Append takes a widget
	"NewAlertDialog":          {0, 1},
	"AddResponse":             {1},
}

// technicalAllowlist holds known non-translatable string patterns that may
// appear as literal text arguments without i18n.Tr() wrapping.
var technicalAllowlist = []string{
	"-symbolic", // icon names
	"app.",      // action names
	"win.",
	"suggested-action", // CSS classes
	"destructive-action",
	"pill",
	"flat",
	"dim-label",
	"osd",
	"empty", "session", "wide", "narrow", // stack child names
	"mumble://",
	"GDK_SCALE", // env var names
	"",          // empty string is never translatable
}

// TestNoHardcodedStrings verifies that every user-visible string literal
// passed as a text argument (textArgs) is wrapped in i18n.Tr(). It AST-scans
// all non-test .go files under internal/ui/.
func TestNoHardcodedStrings(t *testing.T) {
	root := filepath.Join("..", "..")
	uiRoot := filepath.Join(root, "internal", "ui")

	var violations []string

	fset := token.NewFileSet()
	err := filepath.WalkDir(uiRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") ||
			strings.HasSuffix(path, "i18n.go") {
			return nil
		}
		f, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			t.Logf("parse error in %s: %v (skipping)", path, parseErr)
			return nil
		}
		rel, _ := filepath.Rel(root, path)

		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			// Identify calls with text arguments.
			var methodName string
			switch fn := call.Fun.(type) {
			case *ast.SelectorExpr:
				methodName = fn.Sel.Name
			case *ast.Ident:
				methodName = fn.Name
			}
			for _, i := range textArgs[methodName] {
				if i >= len(call.Args) {
					continue
				}
				lit, isLit := call.Args[i].(*ast.BasicLit)
				if !isLit || lit.Kind != token.STRING {
					continue // already wrapped or not a literal
				}
				// Unwrap surrounding quotes.
				val := strings.Trim(lit.Value, `"`+"`")
				if isTechnical(val) {
					continue
				}
				pos := fset.Position(call.Pos())
				violations = append(violations,
					rel+":"+pos.String()+": literal "+lit.Value+" passed to "+methodName+" without i18n.Tr()")
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk error: %v", err)
	}
	for _, v := range violations {
		t.Error(v)
	}
}

// isTechnical returns true for string values that are known to be
// non-translatable: icon names, CSS classes, action names, etc.
func isTechnical(s string) bool {
	if s == "" {
		return true
	}
	for _, prefix := range technicalAllowlist {
		if prefix != "" && strings.Contains(s, prefix) {
			return true
		}
	}
	// Stack child names and similar short single-word technical strings
	// (no spaces, all lowercase) are not user-visible.
	if !strings.Contains(s, " ") && s == strings.ToLower(s) {
		return true
	}
	return false
}
