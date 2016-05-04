// Copyright 2015 Gary Burd. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package explore

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/buildutil"
)

func completePackageArg(ctx *build.Context, cwd string, src io.Reader, arg string) (completions []string) {
	switch {
	case arg == ".":
		completions = []string{"./", "../"}
	case arg == "..":
		completions = []string{"../"}
	case strings.HasPrefix(arg, "."):
		// Complete using relative directory.
		bpkg, err := ctx.Import(".", cwd, build.FindOnly)
		if err != nil {
			return nil
		}
		dir, name := path.Split(arg)
		fis, err := buildutil.ReadDir(ctx, buildutil.JoinPath(ctx, bpkg.Dir, dir))
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
		completions = completePackageArgByPath(ctx, cwd, arg)
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

func resolvePackageSpec(ctx *build.Context, cwd string, src io.Reader, spec string) string {
	if strings.HasSuffix(spec, ".go") {
		d := path.Dir(spec)
		if !buildutil.IsAbsPath(ctx, d) {
			d = buildutil.JoinPath(ctx, cwd, d)
		}
		if bpkg, err := ctx.ImportDir(d, build.FindOnly); err == nil {
			return bpkg.ImportPath
		}
	}
	path := spec
	switch {
	case strings.HasPrefix(spec, "."):
		if bpkg, err := ctx.Import(spec, cwd, build.FindOnly); err == nil {
			path = bpkg.ImportPath
		}
	case strings.HasPrefix(spec, "/"):
		path = spec[1:]
	default:
		if p, ok := readImports(cwd, src)[spec]; ok {
			path = p
		}
	}
	return strings.TrimSuffix(path, "/")
}

func completePackageArgByPath(ctx *build.Context, cwd, arg string) []string {
	var completions []string
	dir, name := path.Split(arg[1:])
	for _, root := range ctx.SrcDirs() {
		if sub, ok := hasSubDir(ctx, root, cwd); ok {
			for {
				completions = addCompletions(completions, ctx, buildutil.JoinPath(ctx, root, sub, "vendor"), dir, name)
				i := strings.LastIndex(sub, "/")
				if i < 0 {
					break
				}
				sub = sub[:i]
			}
		}
		completions = addCompletions(completions, ctx, root, dir, name)
	}
	return completions
}

func addCompletions(completions []string, ctx *build.Context, root, dir, name string) []string {
	fis, err := buildutil.ReadDir(ctx, buildutil.JoinPath(ctx, root, dir))
	if err != nil {
		return completions
	}
	for _, fi := range fis {
		if !fi.IsDir() || strings.HasPrefix(fi.Name(), ".") {
			continue
		}
		if strings.HasPrefix(fi.Name(), name) {
			completions = append(completions, path.Join("/", dir, fi.Name())+"/")
		}
	}
	return completions
}

func hasSubDir(ctx *build.Context, root, dir string) (rel string, ok bool) {
	if f := ctx.HasSubdir; f != nil {
		return f(root, dir)
	}
	root = filepath.Clean(root)
	dir = filepath.Clean(dir)
	const sep = string(filepath.Separator)
	if !strings.HasSuffix(root, sep) {
		root += sep
	}
	if !strings.HasPrefix(dir, root) {
		return "", false
	}
	return filepath.ToSlash(dir[len(root):]), true
}

func completeSymMethodArg(ctx *build.Context, importPath, symMethod string) (completions []string) {
	pkg, err := loadPackage(ctx, importPath, "", loadPackageDoc)
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
				name := guessPackageNameFromPath(path)
				if !set[path] {
					paths[name] = path
				}
			}
		}
	}
	return paths
}
