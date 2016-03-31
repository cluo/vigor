// Copyright 2015 Gary Burd. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package context

import (
	"errors"
	"go/ast"
	"go/build"
	"go/doc"
	"go/parser"
	"go/token"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/garyburd/neovim-go/vim"
)

type Env struct {
	GOROOT string `eval:"$GOROOT"`
	GOPATH string `eval:"$GOPATH"`
	GOOS   string `eval:"$GOOS"`
	GOARCH string `eval:"$GOARCH"`
}

type Context struct {
	Environ []string
	Build   build.Context

	env *Env
	mu  sync.Mutex
}

var (
	ctx *Context
	mu  sync.Mutex
)

func Get(env *Env, v *vim.Vim) *Context {
	mu.Lock()
	defer mu.Unlock()
	if ctx != nil && *ctx.env == *env {
		return ctx
	}
	m := make(map[string]string)
	for _, e := range os.Environ() {
		if i := strings.Index(e, "="); i > 0 {
			m[e[:i]] = e
		}
	}
	ctx = &Context{env: env, Build: build.Default}
	if env.GOROOT != "" {
		ctx.Build.GOROOT = env.GOROOT
		m["GOROOT"] = "GOROOT=" + env.GOROOT
	}
	if env.GOPATH != "" {
		ctx.Build.GOPATH = env.GOPATH
		m["GOPATH"] = "GOPATH=" + env.GOPATH
	}
	if env.GOOS != "" {
		ctx.Build.GOOS = env.GOOS
		m["GOOS"] = "GOOS=" + env.GOOS
	}
	if env.GOARCH != "" {
		ctx.Build.GOARCH = env.GOARCH
		m["GOARCH"] = "GOARCH=" + env.GOARCH
	}
	for _, e := range m {
		ctx.Environ = append(ctx.Environ, e)
	}
	ctx.Build.OpenFile = func(s string) (io.ReadCloser, error) { return os.Open(s) }
	ctx.Build.ReadDir = ioutil.ReadDir
	ctx.Build.JoinPath = filepath.Join
	return ctx
}

// Package represents a Go package.
type Package struct {
	FSet     *token.FileSet
	Build    *build.Package
	AST      *ast.Package
	Doc      *doc.Package
	Examples []*doc.Example
	Errors   []error
}

// Flags for LoadPackage.
const (
	LoadDoc = 1 << iota
	LoadExamples
	LoadUnexported
)

// LoadPackage returns details about the Go package named by the import
// path, interpreting local import paths relative to the srcDir directory.
func (ctx *Context) LoadPackage(importPath string, srcDir string, flags int) (*Package, error) {
	bpkg, err := ctx.Build.Import(importPath, srcDir, build.ImportComment)
	if _, ok := err.(*build.NoGoError); ok {
		return &Package{Build: bpkg}, nil
	}
	if err != nil {
		return nil, err
	}

	pkg := &Package{
		FSet:  token.NewFileSet(),
		Build: bpkg,
	}

	files := make(map[string]*ast.File)
	for _, name := range append(pkg.Build.GoFiles, pkg.Build.CgoFiles...) {
		file, err := pkg.parseFile(&ctx.Build, name)
		if err != nil {
			pkg.Errors = append(pkg.Errors, err)
			continue
		}
		files[name] = file
	}

	pkg.AST, _ = ast.NewPackage(pkg.FSet, files, simpleImporter, nil)

	if flags&LoadDoc != 0 {
		mode := doc.Mode(0)
		if pkg.Build.ImportPath == "builtin" || flags&LoadUnexported != 0 {
			mode |= doc.AllDecls
		}
		pkg.Doc = doc.New(pkg.AST, pkg.Build.ImportPath, mode)
		if pkg.Build.ImportPath == "builtin" {
			for _, t := range pkg.Doc.Types {
				pkg.Doc.Funcs = append(pkg.Doc.Funcs, t.Funcs...)
				t.Funcs = nil
			}
			sort.Sort(byFuncName(pkg.Doc.Funcs))
		}
	}

	if flags&LoadExamples != 0 {
		for _, name := range append(pkg.Build.TestGoFiles, pkg.Build.XTestGoFiles...) {
			file, err := pkg.parseFile(&ctx.Build, name)
			if err != nil {
				pkg.Errors = append(pkg.Errors, err)
				continue
			}
			pkg.Examples = append(pkg.Examples, doc.Examples(file)...)
		}
	}

	return pkg, nil
}

type byFuncName []*doc.Func

func (s byFuncName) Len() int           { return len(s) }
func (s byFuncName) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s byFuncName) Less(i, j int) bool { return s[i].Name < s[j].Name }

func (pkg *Package) parseFile(ctx *build.Context, name string) (*ast.File, error) {
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

func simpleImporter(imports map[string]*ast.Object, path string) (*ast.Object, error) {
	pkg := imports[path]
	if pkg != nil {
		return pkg, nil
	}

	n := GuessPackageNameFromPath(path)
	if n == "" {
		return nil, errors.New("package not found")
	}

	pkg = ast.NewObj(ast.Pkg, n)
	pkg.Data = ast.NewScope(nil)
	imports[path] = pkg
	return pkg, nil
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
func GuessPackageNameFromPath(path string) string {
	// Guess the package name without importing it.
	for _, pat := range packageNamePats {
		m := pat.FindStringSubmatch(path)
		if m != nil {
			return m[1]
		}
	}
	return ""
}
