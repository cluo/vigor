// Copyright 2016 Gary Burd. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package doc

import (
	"errors"
	"fmt"
	"go/ast"
	"go/doc"
	"log"
	"path/filepath"
	"strings"

	"vigor/util"
)

func findDef(cwd, importPath, symbol string) (string, int, int, error) {
	log.Println("LOAD", importPath, cwd)
	pkg, err := util.LoadPackage(importPath, cwd, util.LoadDoc)
	if err != nil {
		return "", 0, 0, err
	}
	if pkg.Doc == nil {
		return "", 0, 0, errors.New("no Go files in directory")
	}
	if symbol == "" {
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

func declPosition(pkg *util.Package, n ast.Node) (string, int, int, error) {
	p := pkg.FSet.Position(n.Pos())
	return filepath.Join(pkg.Build.Dir, p.Filename), p.Line, p.Column, nil
}
