// Copyright 2016 Gary Burd. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package explore

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/build"
	godoc "go/doc"
	"go/printer"
	"go/scanner"
	"go/token"
	"io/ioutil"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/garyburd/vigor/src/doc"
)

const (
	headerGroup  = "Constant"
	commentGroup = "Comment"
	declGroup    = "Special"
	textIndent   = "    "
	textWidth    = 80 - len(textIndent)
)

// bufNamePrefix specifies the file name prefix for documentation pages.
const bufNamePrefix = "godoc://"

// printDoc prints the documentation for the given import path.
func printDoc(ctx *build.Context, path string, cwd string) (*doc.Doc, error) {
	importPath := strings.TrimPrefix(path, bufNamePrefix)
	p := docPrinter{
		Doc:        doc.NewDoc(),
		importPath: importPath,
	}
	if importPath != "" {
		pkg, err := loadPackage(ctx, importPath, cwd, loadPackageDoc|loadPackageExamples|loadPackageFixVendor)
		if err != nil {
			return nil, err
		}
		p.pkg = pkg
	}
	return p.execute()
}

// docPrinter holds state used to create a documentation page.
type docPrinter struct {
	*pkg
	*doc.Doc
	importPath string
	scratch    bytes.Buffer
}

func (p *docPrinter) execute() (*doc.Doc, error) {
	printDecls := false

	switch {
	case p.importPath == "":
		// root
	case p.GoDoc == nil:
		p.PushHighlight(headerGroup)
		p.WriteString("Directory ")
		p.WriteLinkAnchor(p.Build.ImportPath, p.Build.Dir, "")
		p.PopHighlight()
		p.WriteString("\n\n")
	case p.GoDoc.Name == "main":
		p.PushHighlight(headerGroup)
		p.WriteString("Command ")
		p.WriteLinkAnchor(path.Base(p.Build.ImportPath), p.Build.Dir, "")
		p.PopHighlight()
		p.WriteString("\n\n")
		p.printText(p.GoDoc.Doc)
	default:
		p.PushHighlight(declGroup)
		p.WriteString("package ")
		p.WriteLinkAnchor(p.GoDoc.Name, p.Build.Dir, "")
		p.PushHighlight(commentGroup)
		fmt.Fprintf(p.Doc, " // import \"%s\"\n\n", p.Build.ImportPath)
		p.PopHighlight()
		p.PopHighlight()
		p.printText(p.GoDoc.Doc)
		p.printExamples("")
		printDecls = true
	}

	if printDecls {
		if len(p.GoDoc.Consts) > 0 {
			p.printHeader("Constants")
			p.printValues(p.GoDoc.Consts)
		}

		if len(p.GoDoc.Vars) > 0 {
			p.printHeader("Variables")
			p.printValues(p.GoDoc.Vars)
		}

		if len(p.GoDoc.Funcs) > 0 {
			p.printHeader("Functions")
			p.printFuncs(p.GoDoc.Funcs, "")
		}

		if len(p.GoDoc.Types) > 0 {
			p.printHeader("Types")
			for _, d := range p.GoDoc.Types {
				p.printDecl(d.Decl)
				p.printText(d.Doc)
				p.printExamples(d.Name)
				p.printValues(d.Consts)
				p.printValues(d.Vars)
				p.printFuncs(d.Funcs, "")
				p.printFuncs(d.Methods, d.Name+"_")
			}
		}

		p.printImports()
	}

	if p.importPath == "" {
		p.printDirs("Standard Packages", []string{build.Default.GOROOT})
		p.printDirs("Third Party Packages", filepath.SplitList(build.Default.GOPATH))
	} else {
		p.printDirs("Directories", append(filepath.SplitList(build.Default.GOPATH), build.Default.GOROOT))
	}

	return p.Doc, nil
}

const (
	noAnnotation = iota
	anchorAnnotation
	packageLinkAnnoation
	linkAnnotation
	startLinkAnnotation
	endLinkAnnotation
)

type annotation struct {
	kind int
	data string
	pos  token.Pos
}

func (p *docPrinter) printDecl(decl ast.Decl) {
	v := &declVisitor{}
	ast.Walk(v, decl)
	p.scratch.Reset()
	err := (&printer.Config{Tabwidth: 4}).Fprint(
		&p.scratch,
		p.FSet,
		&printer.CommentedNode{Node: decl, Comments: v.comments})
	if err != nil {
		p.WriteString(err.Error())
		return
	}
	buf := bytes.TrimRight(p.scratch.Bytes(), " \t\n")

	var s scanner.Scanner
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(buf))
	base := file.Base()
	s.Init(file, buf, nil, scanner.ScanComments)
	lastOffset := 0
	p.PushHighlight(declGroup)
	defer p.PopHighlight()
loop:
	for {
		pos, tok, lit := s.Scan()
		switch tok {
		case token.EOF:
			break loop
		case token.COMMENT:
			offset := int(pos) - base
			p.Write(buf[lastOffset:offset])
			lastOffset = offset + len(lit)
			p.PushHighlight(commentGroup)
			p.WriteString(lit)
			p.PopHighlight()
		case token.IDENT:
			if len(v.annotations) == 0 {
				// Oops!
				break loop
			}
			offset := int(pos) - base
			p.Write(buf[lastOffset:offset])
			lastOffset = offset + len(lit)
			a := v.annotations[0]
			v.annotations = v.annotations[1:]
			switch a.kind {
			case startLinkAnnotation:
				file := ""
				if a.data != "" {
					file = bufNamePrefix + a.data
				}
				p.PushLinkAnchor(file, v.annotations[0].data)
				p.WriteString(lit)
			case endLinkAnnotation:
				p.WriteString(lit)
				p.PopLink()
			case linkAnnotation:
				file := ""
				if a.data != "" {
					file = bufNamePrefix + a.data
				}
				p.WriteLinkAnchor(lit, file, lit)
			case packageLinkAnnoation:
				p.WriteLinkAnchor(lit, bufNamePrefix+a.data, "")
			case anchorAnnotation:
				p.addAnchor(lit, a.data)
				pos := p.FSet.Position(a.pos)
				p.WriteLink(lit,
					filepath.Join(p.Build.Dir, pos.Filename),
					pos.Line, pos.Column)
			default:
				p.WriteString(lit)
			}
		}
	}
	p.Write(buf[lastOffset:])
	p.WriteString("\n\n")
}

func (p *docPrinter) printText(s string) {
	s = strings.TrimRight(s, " \t\n")
	if s != "" {
		p.scratch.Reset()
		godoc.ToText(&p.scratch, s, textIndent, textIndent+"\t", textWidth)
		blank := 0
		for _, line := range bytes.Split(p.scratch.Bytes(), []byte{'\n'}) {
			if len(line) == 0 {
				blank++
			} else {
				const k = len(textIndent) + 1
				if blank == 2 && len(line) > k && line[k] != ' ' {
					p.WriteString("\n")
					p.PushHighlight(headerGroup)
					p.Write(line)
					p.PopHighlight()
					p.WriteString("\n")
				} else {
					for i := 0; i < blank; i++ {
						p.WriteString("\n")
					}
					p.Write(line)
					p.WriteString("\n")
				}
				blank = 0
			}
		}
		p.WriteString("\n")
	}
}

var exampleOutputRx = regexp.MustCompile(`(?i)//[[:space:]]*output:`)

func (p *docPrinter) printExamples(name string) {
	for _, e := range p.Examples {
		if !strings.HasPrefix(e.Name, name) {
			continue
		}
		name := e.Name[len(name):]
		if name != "" {
			if i := strings.LastIndex(name, "_"); i != 0 {
				continue
			}
			name = name[1:]
			if r, _ := utf8.DecodeRuneInString(name); unicode.IsUpper(r) {
				continue
			}
			name = strings.Title(name)
		}

		var node interface{}
		if _, ok := e.Code.(*ast.File); ok {
			node = e.Play
		} else {
			node = &printer.CommentedNode{Node: e.Code, Comments: e.Comments}
		}

		var buf bytes.Buffer
		err := (&printer.Config{Tabwidth: 4}).Fprint(&buf, p.FSet, node)
		if err != nil {
			continue
		}

		// Additional formatting if this is a function body.
		b := buf.Bytes()
		if i := len(b); i >= 2 && b[0] == '{' && b[i-1] == '}' {
			// Remove surrounding braces.
			b = b[1 : i-1]
			// Unindent
			b = bytes.Replace(b, []byte("\n    "), []byte("\n"), -1)
			// Remove output comment
			if j := exampleOutputRx.FindIndex(b); j != nil {
				b = bytes.TrimSpace(b[:j[0]])
			}
		} else {
			// Drop output, as the output comment will appear in the code
			e.Output = ""
		}

		/*
			p.buf.Write(b)
			p.buf.WriteByte('\n')
			if e.Output != "" {
				p.buf.WriteString(e.Output)
				buf.WriteByte('\n')
			}
			p.buf.WriteByte('\n')
		*/
	}
}

func (p *docPrinter) printFiles(sets ...[]string) {
	var fnames []string
	for _, set := range sets {
		fnames = append(fnames, set...)
	}
	if len(fnames) == 0 {
		return
	}

	sort.Strings(fnames)

	col := 0
	p.WriteString("\n")
	p.WriteString(textIndent)
	for _, fname := range fnames {
		n := utf8.RuneCountInString(fname)
		if col != 0 {
			if col+n+3 > textWidth {
				col = 0
				p.WriteString("\n")
				p.WriteString(textIndent)
			} else {
				col += 1
				p.WriteString(" ")
			}
		}
		p.WriteLinkAnchor(fname, filepath.Join(p.Build.Dir, fname), "")
		col += n + 2
	}
	p.WriteString("\n")
}

func (p *docPrinter) printValues(values []*godoc.Value) {
	for _, d := range values {
		p.printDecl(d.Decl)
		p.printText(d.Doc)
	}
}

func (p *docPrinter) printFuncs(funcs []*godoc.Func, examplePrefix string) {
	for _, d := range funcs {
		p.printDecl(d.Decl)
		p.printText(d.Doc)
		p.printExamples(examplePrefix + d.Name)
	}
}

func (p *docPrinter) printImports() {
	if len(p.Build.Imports) == 0 {
		return
	}
	p.printHeader("Imports")
	for _, imp := range p.Build.Imports {
		p.WriteString(textIndent)
		p.WriteLinkAnchor(imp, bufNamePrefix+imp, "")
		p.WriteString("\n")
	}
	p.WriteString("\n")
}

func (p *docPrinter) printDirs(header string, roots []string) {
	m := map[string]bool{}
	for _, root := range roots {
		dir := filepath.Join(root, "src", filepath.FromSlash(p.importPath))
		fis, err := ioutil.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, fi := range fis {
			if !fi.IsDir() || strings.HasPrefix(fi.Name(), ".") {
				continue
			}
			m[fi.Name()] = true
		}
	}

	if len(m) == 0 && p.importPath == "" {
		return
	}

	var names []string
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)

	p.printHeader(header)
	if p.importPath != "" {
		up := path.Dir(p.importPath)
		if up == "." {
			up = ""
		}
		p.WriteString(textIndent)
		p.WriteLinkAnchor(".. (up a directory)", bufNamePrefix+up, "")
		p.WriteString("\n")
	}
	for _, name := range names {
		p.WriteString(textIndent)
		p.WriteLinkAnchor(name, bufNamePrefix+path.Join(p.importPath, name), "")
		p.WriteString("\n")
	}
	p.WriteString("\n")
}

func (p *docPrinter) printHeader(s string) {
	p.PushHighlight(headerGroup)
	p.WriteString(strings.ToUpper(s))
	p.PopHighlight()
	p.WriteString("\n\n")
}

func (p *docPrinter) addAnchor(name, typeName string) {
	if typeName != "" {
		name = typeName + "." + name
	}
	p.Doc.AddAnchor(name)
}

const (
	notPredeclared = iota
	predeclaredType
	predeclaredConstant
	predeclaredFunction
)

// predeclared represents the set of all predeclared identifiers.
var predeclared = map[string]int{
	"bool":       predeclaredType,
	"byte":       predeclaredType,
	"complex128": predeclaredType,
	"complex64":  predeclaredType,
	"error":      predeclaredType,
	"float32":    predeclaredType,
	"float64":    predeclaredType,
	"int16":      predeclaredType,
	"int32":      predeclaredType,
	"int64":      predeclaredType,
	"int8":       predeclaredType,
	"int":        predeclaredType,
	"rune":       predeclaredType,
	"string":     predeclaredType,
	"uint16":     predeclaredType,
	"uint32":     predeclaredType,
	"uint64":     predeclaredType,
	"uint8":      predeclaredType,
	"uint":       predeclaredType,
	"uintptr":    predeclaredType,

	"true":  predeclaredConstant,
	"false": predeclaredConstant,
	"iota":  predeclaredConstant,
	"nil":   predeclaredConstant,

	"append":  predeclaredFunction,
	"cap":     predeclaredFunction,
	"close":   predeclaredFunction,
	"complex": predeclaredFunction,
	"copy":    predeclaredFunction,
	"delete":  predeclaredFunction,
	"imag":    predeclaredFunction,
	"len":     predeclaredFunction,
	"make":    predeclaredFunction,
	"new":     predeclaredFunction,
	"panic":   predeclaredFunction,
	"print":   predeclaredFunction,
	"println": predeclaredFunction,
	"real":    predeclaredFunction,
	"recover": predeclaredFunction,
}

// declVisitor modifies a declaration AST for printing and collects annotations.
type declVisitor struct {
	annotations []*annotation
	comments    []*ast.CommentGroup
}

func (v *declVisitor) addAnnoation(a *annotation) {
	v.annotations = append(v.annotations, a)
}

func (v *declVisitor) ignoreName() {
	v.annotations = append(v.annotations, &annotation{kind: noAnnotation})
}

func (v *declVisitor) Visit(n ast.Node) ast.Visitor {
	switch n := n.(type) {
	case *ast.TypeSpec:
		v.addAnnoation(&annotation{kind: anchorAnnotation, pos: n.Pos()})
		name := n.Name.Name
		switch n := n.Type.(type) {
		case *ast.InterfaceType:
			for _, f := range n.Methods.List {
				for _, n := range f.Names {
					v.addAnnoation(&annotation{kind: anchorAnnotation, data: name, pos: n.Pos()})
				}
				ast.Walk(v, f.Type)
			}
		case *ast.StructType:
			for _, f := range n.Fields.List {
				for _, n := range f.Names {
					v.addAnnoation(&annotation{kind: anchorAnnotation, data: name, pos: n.Pos()})
				}
				ast.Walk(v, f.Type)
			}
		default:
			ast.Walk(v, n)
		}
	case *ast.FuncDecl:
		if n.Recv == nil {
			v.addAnnoation(&annotation{kind: anchorAnnotation, pos: n.Name.NamePos})
		} else {
			ast.Walk(v, n.Recv)
			if len(n.Recv.List) > 0 {
				typ := n.Recv.List[0].Type
				if se, ok := typ.(*ast.StarExpr); ok {
					typ = se.X
				}
				if id, ok := typ.(*ast.Ident); ok {
					v.addAnnoation(&annotation{kind: anchorAnnotation, data: id.Name, pos: n.Name.NamePos})
				}
			}
		}

		ast.Walk(v, n.Type)
	case *ast.Field:
		for _ = range n.Names {
			v.ignoreName()
		}
		ast.Walk(v, n.Type)
	case *ast.ValueSpec:
		for _, n := range n.Names {
			v.addAnnoation(&annotation{kind: anchorAnnotation, pos: n.Pos()})
		}
		if n.Type != nil {
			ast.Walk(v, n.Type)
		}
		for _, x := range n.Values {
			ast.Walk(v, x)
		}
	case *ast.Ident:
		switch {
		case n.Obj == nil && predeclared[n.Name] != notPredeclared:
			v.addAnnoation(&annotation{kind: linkAnnotation, data: "builtin"})
		case n.Obj != nil && ast.IsExported(n.Name):
			v.addAnnoation(&annotation{kind: linkAnnotation})
		default:
			v.ignoreName()
		}
	case *ast.SelectorExpr:
		if x, _ := n.X.(*ast.Ident); x != nil {
			if obj := x.Obj; obj != nil && obj.Kind == ast.Pkg {
				if spec, _ := obj.Decl.(*ast.ImportSpec); spec != nil {
					if path, err := strconv.Unquote(spec.Path.Value); err == nil {
						if path == "C" {
							v.ignoreName()
							v.ignoreName()
						} else if n.Sel.Pos()-x.End() == 1 {
							v.addAnnoation(&annotation{kind: startLinkAnnotation, data: path})
							v.addAnnoation(&annotation{kind: endLinkAnnotation, data: n.Sel.Name})
						} else {
							v.addAnnoation(&annotation{kind: packageLinkAnnoation, data: path})
							v.addAnnoation(&annotation{kind: linkAnnotation, data: path})
						}
						return nil
					}
				}
			}
		}
		ast.Walk(v, n.X)
		v.ignoreName()
	case *ast.BasicLit:
		if n.Kind == token.STRING && len(n.Value) > 128 {
			v.comments = append(v.comments,
				&ast.CommentGroup{List: []*ast.Comment{{
					Slash: n.Pos(),
					Text:  fmt.Sprintf("/* %d byte string literal not displayed */", len(n.Value)),
				}}})
			n.Value = `""`
		} else {
			return v
		}
	case *ast.CompositeLit:
		if len(n.Elts) > 100 {
			if n.Type != nil {
				ast.Walk(v, n.Type)
			}
			v.comments = append(v.comments,
				&ast.CommentGroup{List: []*ast.Comment{{
					Slash: n.Lbrace,
					Text:  fmt.Sprintf("/* %d elements not displayed */", len(n.Elts)),
				}}})
			n.Elts = n.Elts[:0]
		} else {
			return v
		}
	default:
		return v
	}
	return nil
}
