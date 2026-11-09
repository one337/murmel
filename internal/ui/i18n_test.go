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

// textSetterMethods is the set of GTK/Adwaita method names that set
// user-visible text; their string arguments must be wrapped in tr().
var textSetterMethods = map[string]bool{
	"SetTitle":           true,
	"SetSubtitle":        true,
	"SetDescription":     true,
	"SetBody":            true,
	"SetTooltipText":     true,
	"SetPlaceholderText": true,
	"SetLabel":           true,
	"SetText":            true,
	"NewButtonWithLabel": true,
	"NewLabel":           true,
	"NewWindowTitle":     true,
}

// technicalStringPrefixes are known non-translatable string patterns that
// may appear as literal arguments to the above methods without tr() wrapping.
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
// passed to a GTK text-setter is wrapped in tr(). It AST-scans all non-test
// .go files under internal/ui/.
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
			// Identify calls to textSetterMethods.
			var methodName string
			switch fn := call.Fun.(type) {
			case *ast.SelectorExpr:
				methodName = fn.Sel.Name
			case *ast.Ident:
				methodName = fn.Name
			}
			if !textSetterMethods[methodName] {
				return true
			}
			// Check the first string-literal argument.
			if len(call.Args) == 0 {
				return true
			}
			arg := call.Args[0]
			lit, isLit := arg.(*ast.BasicLit)
			if !isLit || lit.Kind != token.STRING {
				return true // already wrapped or not a literal
			}
			// Unwrap surrounding quotes.
			val := strings.Trim(lit.Value, `"`+"`")
			if isTechnical(val) {
				return true
			}
			pos := fset.Position(call.Pos())
			violations = append(violations,
				rel+":"+pos.String()+": literal "+lit.Value+" passed to "+methodName+" without tr()")
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
