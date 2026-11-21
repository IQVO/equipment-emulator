package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestDomainHasNoJSONTags enforces CLAUDE.md hard rule 1: the domain carries
// no wire format. Adapters map domain values to their own DTOs.
func TestDomainHasNoJSONTags(t *testing.T) {
	root := filepath.Join("..", "domain")
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if fld, ok := n.(*ast.Field); ok && fld.Tag != nil && strings.Contains(fld.Tag.Value, "json:") {
				t.Errorf("%s: domain struct field carries a JSON tag %s", fset.Position(fld.Pos()), fld.Tag.Value)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
