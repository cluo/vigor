// Copyright 2016 Gary Burd. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package doc

import (
	"fmt"
	"go/ast"
	"go/doc"
	"path/filepath"
	"strings"

	"vigor/context"
)

func findDef(ctx *context.Context, cwd, importPath, symbol string) (string, int, int, error) {
	pkg, err := ctx.LoadPackage(importPath, cwd, context.LoadDoc|context.LoadUnexported)
	if err != nil {
		return "", 0, 0, err
	}
	if pkg.Doc == nil || symbol == "" {
		return pkg.Build.Dir, 0, 0, nil
	}
	parts := strings.Split(symbol, ".")
	if len(parts) == 2 {
		for _, d := range pkg.Doc.Types {
			if d.Name == parts[0] {
				for _, m := range d.Methods {
					if m.Name == parts[1] {
						return declPosition(pkg, m.Decl)
					}
				}
				break
			}
		}
	} else {
		untangleDoc(pkg.Doc)
		for _, d := range [][]*doc.Value{pkg.Doc.Consts, pkg.Doc.Vars} {
			for _, d := range d {
				for _, name := range d.Names {
					if name == symbol {
						return declPosition(pkg, d.Decl)
					}
				}
			}
		}
		for _, d := range pkg.Doc.Funcs {
			if d.Name == symbol {
				return declPosition(pkg, d.Decl)
			}
		}
		for _, d := range pkg.Doc.Types {
			if d.Name == symbol {
				return declPosition(pkg, d.Decl)
			}
		}
	}
	return "", 0, 0, fmt.Errorf("%s not found in %s", symbol, pkg.Build.ImportPath)
}

func declPosition(pkg *context.Package, n ast.Node) (string, int, int, error) {
	p := pkg.FSet.Position(n.Pos())
	return filepath.Join(pkg.Build.Dir, p.Filename), p.Line, p.Column, nil
}
