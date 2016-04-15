// Copyright 2015 Gary Burd. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package explore

import (
	"go/ast"
	"go/build"
	godoc "go/doc"
	"go/parser"
	"go/token"
	"io/ioutil"
	"regexp"
	"sort"
	"strconv"
)

// pkg represents a Go package.
type pkg struct {
	FSet     *token.FileSet
	Build    *build.Package
	AST      *ast.Package
	Doc      *godoc.Package
	Examples []*godoc.Example
	Errors   []error
}

// Flags for loadPackage.
const (
	loadPackageDoc = 1 << iota
	loadPackageExamples
	loadPackageUnexported
	loadPackageFixVendor
)

// loadPackage returns details about the Go package named by the import
// path, interpreting local import paths relative to the srcDir directory.
func loadPackage(ctx *build.Context, importPath string, srcDir string, flags int) (*pkg, error) {
	bpkg, err := ctx.Import(importPath, srcDir, build.ImportComment)
	if _, ok := err.(*build.NoGoError); ok {
		return &pkg{Build: bpkg}, nil
	}
	if err != nil {
		return nil, err
	}

	pkg := &pkg{
		FSet:  token.NewFileSet(),
		Build: bpkg,
	}

	files := make(map[string]*ast.File)
	for _, name := range append(pkg.Build.GoFiles, pkg.Build.CgoFiles...) {
		file, err := pkg.parseFile(ctx, name)
		if err != nil {
			pkg.Errors = append(pkg.Errors, err)
			continue
		}
		files[name] = file
	}

	vendor := make(map[string]string)
	pkg.AST, _ = ast.NewPackage(pkg.FSet, files, importer(ctx, bpkg.Dir, vendor), nil)

	if flags&loadPackageFixVendor != 0 {
		for _, f := range pkg.AST.Files {
			for _, i := range f.Imports {
				if lit := i.Path; lit != nil {
					if s, err := strconv.Unquote(lit.Value); err != nil {
						if p, ok := vendor[s]; ok {
							lit.Value = strconv.Quote(p)
						}
					}
				}
			}
		}
	}

	if flags&loadPackageDoc != 0 {
		mode := godoc.Mode(0)
		if pkg.Build.ImportPath == "builtin" || flags&loadPackageUnexported != 0 {
			mode |= godoc.AllDecls
		}
		pkg.Doc = godoc.New(pkg.AST, pkg.Build.ImportPath, mode)
		if pkg.Build.ImportPath == "builtin" {
			for _, t := range pkg.Doc.Types {
				pkg.Doc.Funcs = append(pkg.Doc.Funcs, t.Funcs...)
				t.Funcs = nil
			}
			sort.Sort(byFuncName(pkg.Doc.Funcs))
		}
	}

	if flags&loadPackageExamples != 0 {
		for _, name := range append(pkg.Build.TestGoFiles, pkg.Build.XTestGoFiles...) {
			file, err := pkg.parseFile(ctx, name)
			if err != nil {
				pkg.Errors = append(pkg.Errors, err)
				continue
			}
			pkg.Examples = append(pkg.Examples, godoc.Examples(file)...)
		}
	}

	return pkg, nil
}

type byFuncName []*godoc.Func

func (s byFuncName) Len() int           { return len(s) }
func (s byFuncName) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s byFuncName) Less(i, j int) bool { return s[i].Name < s[j].Name }

func (pkg *pkg) parseFile(ctx *build.Context, name string) (*ast.File, error) {
	f, err := ctx.OpenFile(ctx.JoinPath(pkg.Build.Dir, name))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	p, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, err
	}
	// overwrite //line comments
	for _, m := range linePat.FindAllIndex(p, -1) {
		for i := m[0] + 2; i < m[1]; i++ {
			p[i] = ' '
		}
	}
	return parser.ParseFile(pkg.FSet, name, p, parser.ParseComments)
}

var linePat = regexp.MustCompile(`(?m)^//line .*$`)

func importer(ctx *build.Context, srcDir string, vendor map[string]string) ast.Importer {
	return func(imports map[string]*ast.Object, importPath string) (*ast.Object, error) {
		pkg := imports[importPath]
		if pkg != nil {
			return pkg, nil
		}

		var name string
		bpkg, err := ctx.Import(importPath, srcDir, 0)
		if err != nil {
			name = guessPackageNameFromPath(importPath)
		} else {
			name = bpkg.Name
			vendor[importPath] = bpkg.ImportPath
		}

		pkg = ast.NewObj(ast.Pkg, name)
		pkg.Data = ast.NewScope(nil)
		imports[importPath] = pkg
		return pkg, nil
	}
}

var packageNamePats = []*regexp.Regexp{
	// Last element with .suffix removed.
	regexp.MustCompile(`/([^-./]+)[-.](?:git|svn|hg|bzr|v\d+)$`),

	// Last element with "go" prefix or suffix removed.
	regexp.MustCompile(`/([^-./]+)[-.]go$`),
	regexp.MustCompile(`/go[-.]([^-./]+)$`),

	// It's also common for the last element of the path to contain an
	// extra "go" prefix, but not always. TODO: examine unresolved ids to
	// detect when trimming the "go" prefix is appropriate.

	// Last component of path.
	regexp.MustCompile(`([^/]+)$`),
}

// GuessPackageNameFromPath guesses the package name from the package path.
func guessPackageNameFromPath(path string) string {
	// Guess the package name without importing it.
	for _, pat := range packageNamePats {
		m := pat.FindStringSubmatch(path)
		if m != nil {
			return m[1]
		}
	}
	return ""
}

func untangleDoc(pkg *godoc.Package) {
	for _, t := range pkg.Types {
		pkg.Consts = append(pkg.Consts, t.Consts...)
		t.Consts = nil
		pkg.Vars = append(pkg.Vars, t.Vars...)
		t.Vars = nil
		pkg.Funcs = append(pkg.Funcs, t.Funcs...)
		t.Funcs = nil
	}
}
