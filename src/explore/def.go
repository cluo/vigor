// Copyright 2016 Gary Burd. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package explore

import (
	"fmt"
	"go/ast"
	"go/build"
	godoc "go/doc"
	"path/filepath"
	"strings"
)

func findDef(ctx *build.Context, cwd, importPath, symbol string) (string, int, int, error) {
	pkg, err := loadPackage(ctx, importPath, cwd, loadPackageDoc|loadPackageUnexported)
	if err != nil {
		return "", 0, 0, err
	}
	if pkg.GoDoc == nil || symbol == "" {
		return pkg.Build.Dir, 0, 0, nil
	}
	parts := strings.Split(symbol, ".")
	if len(parts) == 2 {
		for _, d := range pkg.GoDoc.Types {
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
		untangleDoc(pkg.GoDoc)
		for _, d := range [][]*godoc.Value{pkg.GoDoc.Consts, pkg.GoDoc.Vars} {
			for _, d := range d {
				for _, name := range d.Names {
					if name == symbol {
						return declPosition(pkg, d.Decl)
					}
				}
			}
		}
		for _, d := range pkg.GoDoc.Funcs {
			if d.Name == symbol {
				return declPosition(pkg, d.Decl)
			}
		}
		for _, d := range pkg.GoDoc.Types {
			if d.Name == symbol {
				return declPosition(pkg, d.Decl)
			}
		}
	}
	return "", 0, 0, fmt.Errorf("%s not found in %s", symbol, pkg.Build.ImportPath)
}

func declPosition(pkg *pkg, n ast.Node) (string, int, int, error) {
	p := pkg.FSet.Position(n.Pos())
	return filepath.Join(pkg.Build.Dir, p.Filename), p.Line, p.Column, nil
}
