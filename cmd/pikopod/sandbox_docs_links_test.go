package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSandboxErrorsHaveDocumentationLinks(t *testing.T) {
	fset := token.NewFileSet()
	packages, err := parser.ParseDir(fset, ".", func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg, ok := packages["main"]
	if !ok {
		t.Fatal("main package not found")
	}
	constants := make(map[string]string)
	for _, source := range pkg.Files {
		for _, decl := range source.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				value := spec.(*ast.ValueSpec)
				for i, expr := range value.Values {
					literal, ok := expr.(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						continue
					}
					text, err := strconv.Unquote(literal.Value)
					if err == nil {
						constants[value.Names[i].Name] = text
					}
				}
			}
		}
	}
	file, ok := pkg.Files["sandbox.go"]
	if !ok {
		t.Fatal("sandbox.go not found")
	}
	checked := 0
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok || pkg.Name != "errfmt" {
			return true
		}
		docsIndex := 3
		switch selector.Sel.Name {
		case "New":
		case "Newf":
			docsIndex = 2
		default:
			return true
		}
		checked++
		position := fset.Position(call.Pos())
		var docs string
		switch argument := call.Args[docsIndex].(type) {
		case *ast.BasicLit:
			value, err := strconv.Unquote(argument.Value)
			if err != nil || argument.Kind != token.STRING {
				t.Errorf("%s: docs argument must be a string", position)
				return true
			}
			docs = value
		case *ast.Ident:
			value, known := constants[argument.Name]
			if !known {
				t.Errorf("%s: docs identifier %q is not a known string constant", position, argument.Name)
				return true
			}
			docs = value
		default:
			t.Errorf("%s: docs argument must be a string literal or known string constant", position)
			return true
		}
		if docs == "" {
			t.Errorf("%s: errfmt.%s has no valid documentation link", position, selector.Sel.Name)
			return true
		}
		path, anchor, hasAnchor := strings.Cut(docs, "#")
		raw, err := os.ReadFile(filepath.Join("../..", path))
		if err != nil {
			t.Errorf("%s: documentation file %q: %v", position, path, err)
			return true
		}
		if hasAnchor {
			found := false
			for _, line := range strings.Split(string(raw), "\n") {
				if !strings.HasPrefix(line, "#") {
					continue
				}
				heading := strings.TrimSpace(strings.TrimLeft(line, "#"))
				if strings.ReplaceAll(strings.ToLower(heading), " ", "-") == anchor {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s: documentation anchor %q does not exist", position, docs)
			}
		}
		return true
	})
	if checked == 0 {
		t.Fatal("no sandbox error constructors found")
	}
}
