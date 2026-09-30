package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

func TestScenarioAndPRDirectErrorsHaveDocumentationLinks(t *testing.T) {
	for _, source := range []string{"scenario.go", "pr.go"} {
		t.Run(source, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, source, nil, 0)
			if err != nil {
				t.Fatal(err)
			}

			checked := 0
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "New" {
					return true
				}
				pkg, ok := selector.X.(*ast.Ident)
				if !ok || pkg.Name != "errfmt" || len(call.Args) != 4 {
					return true
				}

				checked++
				position := fset.Position(call.Pos())
				literal, ok := call.Args[3].(*ast.BasicLit)
				if !ok {
					t.Errorf("%s: errfmt.New docs argument must be a string literal", position)
					return true
				}
				docs, err := strconv.Unquote(literal.Value)
				if err != nil {
					t.Errorf("%s: invalid docs string: %v", position, err)
					return true
				}
				if docs == "" {
					t.Errorf("%s: errfmt.New has no documentation link", position)
				}
				return true
			})

			if checked == 0 {
				t.Fatal("no errfmt.New calls found")
			}
		})
	}
}
