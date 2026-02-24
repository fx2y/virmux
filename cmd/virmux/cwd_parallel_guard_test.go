package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNoParallelInTestsThatChdir(t *testing.T) {
	t.Parallel()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	targetRoots := []string{
		filepath.Join(repoRoot, "cmd"),
		filepath.Join(repoRoot, "internal"),
	}

	var findings []string
	fset := token.NewFileSet()
	for _, root := range targetRoots {
		err := filepath.WalkDir(root, func(path string, dEntry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if dEntry.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fileNode, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			for _, decl := range fileNode.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil || fn.Name == nil || !strings.HasPrefix(fn.Name.Name, "Test") {
					continue
				}
				hasParallel := false
				hasChdir := false
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					x, ok := sel.X.(*ast.Ident)
					if !ok {
						return true
					}
					if x.Name == "t" && sel.Sel.Name == "Parallel" {
						hasParallel = true
					}
					if x.Name == "os" && sel.Sel.Name == "Chdir" {
						hasChdir = true
					}
					return true
				})
				if hasParallel && hasChdir {
					rel, _ := filepath.Rel(repoRoot, path)
					findings = append(findings, rel+"::"+fn.Name.Name)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	if len(findings) != 0 {
		t.Fatalf("tests must not mix t.Parallel with os.Chdir: %s", strings.Join(findings, ", "))
	}
}
