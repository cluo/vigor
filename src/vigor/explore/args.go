// Copyright 2015 Gary Burd. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package doc

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io"
	"io/ioutil"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"vigor/util"
)

func completePackage(cwd string, src io.Reader, arg string) (completions []string) {
	switch {
	case arg == ".":
		completions = []string{"./", "../"}
	case arg == "..":
		completions = []string{"../"}
	case strings.HasPrefix(arg, "."):
		// Complete using relative directory.
		bpkg, err := build.Import(".", cwd, build.FindOnly)
		if err != nil {
			return nil
		}
		dir, name := path.Split(arg)
		fis, err := ioutil.ReadDir(filepath.Join(bpkg.Dir, filepath.FromSlash(dir)))
		if err != nil {
			return nil
		}
		for _, fi := range fis {
			if !fi.IsDir() || strings.HasPrefix(fi.Name(), ".") {
				continue
			}
			if strings.HasPrefix(fi.Name(), name) {
				completions = append(completions, path.Join(dir, fi.Name())+"/")
			}
		}
	case strings.HasPrefix(arg, "/"):
		// Complete using full import path.
		completions = completePackageByPath(arg)
	default:
		// Complete with package names imported in current file.
		for n := range readImports(cwd, src) {
			if strings.HasPrefix(n, arg) {
				completions = append(completions, n)
			}
		}
	}
	if len(completions) == 0 {
		completions = []string{arg}
	}
	sort.Strings(completions)
	return completions
}

func resolvePackageSpec(cwd string, src io.Reader, spec string) string {
	if strings.HasSuffix(spec, ".go") {
		d := filepath.Dir(spec)
		if !filepath.IsAbs(d) {
			d = filepath.Join(cwd, d)
		}
		if bpkg, err := build.ImportDir(d, build.FindOnly); err == nil {
			return bpkg.ImportPath
		}
	}
	path := strings.TrimRight(spec, "/")
	switch {
	case strings.HasPrefix(spec, "."):
		if bpkg, err := build.Import(spec, cwd, build.FindOnly); err == nil {
			path = bpkg.ImportPath
		}
	case strings.HasPrefix(spec, "/"):
		path = path[1:]
	default:
		if p, ok := readImports(cwd, src)[spec]; ok {
			path = p
		}
	}
	return path
}

func completePackageByPath(arg string) []string {
	var completions []string
	dir, name := path.Split(arg[1:])
	for _, srcDir := range build.Default.SrcDirs() {
		fis, err := ioutil.ReadDir(filepath.Join(srcDir, filepath.FromSlash(dir)))
		if err != nil {
			continue
		}
		for _, fi := range fis {
			if !fi.IsDir() || strings.HasPrefix(fi.Name(), ".") {
				continue
			}
			if strings.HasPrefix(fi.Name(), name) {
				completions = append(completions, path.Join("/", dir, fi.Name())+"/")
			}
		}
	}
	return completions
}

func completeSymMethod(importPath, symMethod string) (completions []string) {
	pkg, err := util.LoadPackage(importPath, "", util.LoadDoc)
	if err != nil {
		return []string{symMethod}
	}

	symMethod = strings.ToLower(symMethod)
	sym := symMethod
	method := ""
	if i := strings.Index(symMethod, "."); i >= 0 {
		sym = symMethod[:i]
		method = symMethod[i+1:]
	}

	if method != "" {
		for _, d := range pkg.Doc.Types {
			if strings.ToLower(d.Name) == sym {
				for _, m := range d.Methods {
					if strings.HasPrefix(strings.ToLower(m.Name), method) {
						completions = append(completions, d.Name+"."+m.Name)
					}
				}
			}
		}
	} else {
		untangleDoc(pkg.Doc)
		add := func(n string) {
			if strings.HasPrefix(strings.ToLower(n), sym) {
				completions = append(completions, n)
			}
		}
		for _, d := range append(pkg.Doc.Consts, pkg.Doc.Vars...) {
			for _, n := range d.Names {
				add(n)
			}
		}
		for _, d := range pkg.Doc.Funcs {
			add(d.Name)
		}
		for _, d := range pkg.Doc.Types {
			add(d.Name + ".")
		}
	}

	sort.Strings(completions)
	return completions
}

// readImports returns the imports from the Go source file src. Errors are
// silently ignored.
func readImports(cwd string, src io.Reader) map[string]string {
	paths := map[string]string{}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, parser.ImportsOnly)
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	for _, decl := range file.Decls {
		d, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, dspec := range d.Specs {
			spec, ok := dspec.(*ast.ImportSpec)
			if !ok || spec.Path == nil {
				continue
			}
			quoted := spec.Path.Value
			path, err := strconv.Unquote(quoted)
			if err != nil || path == "C" {
				continue
			}
			if spec.Name != nil {
				if spec.Name.Name != "_" {
					paths[spec.Name.Name] = path
					set[spec.Name.Name] = true
				}
			} else {
				name := util.GuessPackageNameFromPath(path)
				if !set[path] {
					paths[name] = path
				}
			}
		}
	}
	return paths
}
