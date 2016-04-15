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
)

const (
	headerGroup  = "Constant"
	commentGroup = "Comment"
	declGroup    = "Special"

	textIndent = "    "
	textWidth  = 80 - len(textIndent)
)

// position encodes a line and column as a single integer
type position int

func (p position) line() int {
	return int(p / 10000)
}

func (p position) column() int {
	return int(p % 10000)
}

func newPosition(line, column int) position {
	return position(line*10000 + column)
}

// link represents a link in the document.
type link struct {
	// Start and end of link.
	start, end position

	// Path is index of the target file path in doc.strings.
	path int

	// Target specifies the position in the target file.
	address position
}

// bufNamePrefix specifies the file name prefix for documentation pages.
const bufNamePrefix = "godoc://"

// highlight represents a range of text in Doc.Text to be highlighted.
type highlight struct {
	// Start and end of highlight range.
	start, end position

	// Vim highlight group name.
	group string
}

// documentation represents a documentation page.
type doc struct {
	importPath string
	links      []*link
	anchors    map[string]position
	strings    []string
	folds      [][2]position
	highlights []*highlight
	text       []byte
}

// printDoc prints the documentation for the given import path.
func printDoc(ctx *build.Context, path string, cwd string) (*doc, error) {
	importPath := strings.TrimPrefix(path, bufNamePrefix)
	p := docPrinter{
		lineNum:    1,
		lineOffset: -1,
		doc:        &doc{importPath: importPath, anchors: make(map[string]position)},
		index:      make(map[string]int),
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

type hlStackItem struct {
	start position
	group string
}

// docPrinter holds state used to create a documentation page.
type docPrinter struct {
	*pkg
	doc *doc

	buf       bytes.Buffer
	scratch   bytes.Buffer
	index     map[string]int
	hlStack   []hlStackItem
	foldStack []position
	linkStart position

	// Fields used by outputPosition
	lineNum    int
	lineOffset int
	scanOffset int
}

func (p *docPrinter) execute() (*doc, error) {
	printDecls := false

	switch {
	case p.doc.importPath == "":
		// root
	case p.Doc == nil:
		p.pushHighlight(headerGroup)
		p.buf.WriteString("Directory ")
		p.printLink(p.Build.ImportPath, p.Build.Dir, -1)
		p.popHighlight()
		p.buf.WriteString("\n\n")
	case p.Doc.Name == "main":
		p.pushHighlight(headerGroup)
		p.buf.WriteString("Command ")
		p.printLink(path.Base(p.Build.ImportPath), p.Build.Dir, -1)
		p.popHighlight()
		p.buf.WriteString("\n\n")
		p.printText(p.Doc.Doc)
	default:
		p.pushHighlight(declGroup)
		p.buf.WriteString("package ")
		p.printLink(p.Doc.Name, p.Build.Dir, -1)
		p.pushHighlight(commentGroup)
		fmt.Fprintf(&p.buf, " // import \"%s\"\n\n", p.Build.ImportPath)
		p.popHighlight()
		p.popHighlight()
		p.printText(p.Doc.Doc)
		p.printExamples("")
		printDecls = true
	}

	if printDecls {
		if len(p.Doc.Consts) > 0 {
			p.printHeader("Constants")
			p.printValues(p.Doc.Consts)
		}

		if len(p.Doc.Vars) > 0 {
			p.printHeader("Variables")
			p.printValues(p.Doc.Vars)
		}

		if len(p.Doc.Funcs) > 0 {
			p.printHeader("Functions")
			p.printFuncs(p.Doc.Funcs, "")
		}

		if len(p.Doc.Types) > 0 {
			p.printHeader("Types")
			for _, d := range p.Doc.Types {
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

	if p.doc.importPath == "" {
		p.printDirs("Standard Packages", []string{build.Default.GOROOT})
		p.printDirs("Third Party Packages", filepath.SplitList(build.Default.GOPATH))
	} else {
		p.printDirs("Directories", append(filepath.SplitList(build.Default.GOPATH), build.Default.GOROOT))
	}

	p.doc.text = p.buf.Bytes()
	return p.doc, nil
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
		p.buf.WriteString(err.Error())
		return
	}
	buf := bytes.TrimRight(p.scratch.Bytes(), " \t\n")

	var s scanner.Scanner
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(buf))
	base := file.Base()
	s.Init(file, buf, nil, scanner.ScanComments)
	lastOffset := 0
	p.pushHighlight(declGroup)
	defer p.popHighlight()
loop:
	for {
		pos, tok, lit := s.Scan()
		switch tok {
		case token.EOF:
			break loop
		case token.COMMENT:
			offset := int(pos) - base
			p.buf.Write(buf[lastOffset:offset])
			lastOffset = offset + len(lit)
			p.pushHighlight(commentGroup)
			p.buf.WriteString(lit)
			p.popHighlight()
		case token.IDENT:
			if len(v.annotations) == 0 {
				// Oops!
				break loop
			}
			offset := int(pos) - base
			p.buf.Write(buf[lastOffset:offset])
			lastOffset = offset + len(lit)
			a := v.annotations[0]
			v.annotations = v.annotations[1:]
			switch a.kind {
			case startLinkAnnotation:
				p.beginLink()
				p.buf.WriteString(lit)
			case linkAnnotation:
				p.beginLink()
				fallthrough
			case endLinkAnnotation:
				file := ""
				if a.data != "" {
					file = bufNamePrefix + a.data
				}
				p.buf.WriteString(lit)
				p.endLink(file, newPosition(0, p.stringIndex(lit)))
			case packageLinkAnnoation:
				p.printLink(lit, bufNamePrefix+a.data, -1)
			case anchorAnnotation:
				p.addAnchor(lit, a.data)
				pos := p.FSet.Position(a.pos)
				p.printLink(lit,
					filepath.Join(p.Build.Dir, pos.Filename),
					newPosition(pos.Line, pos.Column))
			default:
				p.buf.WriteString(lit)
			}
		}
	}
	p.buf.Write(buf[lastOffset:])
	p.buf.WriteString("\n\n")
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
					p.buf.WriteByte('\n')
					p.pushHighlight(headerGroup)
					p.buf.Write(line)
					p.popHighlight()
					p.buf.WriteByte('\n')
				} else {
					for i := 0; i < blank; i++ {
						p.buf.WriteByte('\n')
					}
					p.buf.Write(line)
					p.buf.WriteByte('\n')
				}
				blank = 0
			}
		}
		p.buf.WriteByte('\n')
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
	p.buf.WriteByte('\n')
	p.buf.WriteString(textIndent)
	for _, fname := range fnames {
		n := utf8.RuneCountInString(fname)
		if col != 0 {
			if col+n+3 > textWidth {
				col = 0
				p.buf.WriteByte('\n')
				p.buf.WriteString(textIndent)
			} else {
				col += 1
				p.buf.WriteByte(' ')
			}
		}
		p.printLink(fname, filepath.Join(p.Build.Dir, fname), -1)
		col += n + 2
	}
	p.buf.WriteString("\n")
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
		p.buf.WriteString(textIndent)
		p.beginLink()
		p.buf.WriteString(imp)
		p.endLink(bufNamePrefix+imp, -1)
		p.buf.WriteByte('\n')
	}
	p.buf.WriteString("\n")
}

func (p *docPrinter) printDirs(header string, roots []string) {
	m := map[string]bool{}
	for _, root := range roots {
		dir := filepath.Join(root, "src", filepath.FromSlash(p.doc.importPath))
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

	if len(m) == 0 && p.doc.importPath == "" {
		return
	}

	var names []string
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)

	p.printHeader(header)
	if p.doc.importPath != "" {
		up := path.Dir(p.doc.importPath)
		if up == "." {
			up = ""
		}
		p.buf.WriteString(textIndent)
		p.printLink(".. (up a directory)", bufNamePrefix+up, -1)
		p.buf.WriteByte('\n')
	}
	for _, name := range names {
		p.buf.WriteString(textIndent)
		p.beginLink()
		p.buf.WriteString(name)
		p.endLink(bufNamePrefix+path.Join(p.doc.importPath, name), -1)
		p.buf.WriteByte('\n')
	}
	p.buf.WriteByte('\n')
}

func (p *docPrinter) printHeader(s string) {
	p.pushHighlight(headerGroup)
	p.buf.WriteString(strings.ToUpper(s))
	p.popHighlight()
	p.buf.WriteString("\n\n")
}

func (p *docPrinter) printLink(s string, file string, address position) {
	p.beginLink()
	p.buf.WriteString(s)
	p.endLink(file, address)
}

func (p *docPrinter) beginLink() {
	p.linkStart = p.outputPosition()
}

func (p *docPrinter) endLink(file string, address position) {
	p.doc.links = append(p.doc.links, &link{start: p.linkStart, end: p.outputPosition(), path: p.stringIndex(file), address: address})
}

func (p *docPrinter) pushHighlight(group string) {
	start := p.outputPosition()
	if len(p.hlStack) > 0 {
		hl := p.hlStack[len(p.hlStack)-1]
		if hl.start != start {
			p.doc.highlights = append(p.doc.highlights, &highlight{start: hl.start, end: start, group: hl.group})
		}
	}
	p.hlStack = append(p.hlStack, hlStackItem{start, group})
}

func (p *docPrinter) popHighlight() {
	hl := p.hlStack[len(p.hlStack)-1]
	p.hlStack = p.hlStack[:len(p.hlStack)-1]
	end := p.outputPosition()
	if hl.start != end {
		p.doc.highlights = append(p.doc.highlights, &highlight{start: hl.start, end: end, group: hl.group})
	}
	if len(p.hlStack) > 0 {
		p.hlStack[len(p.hlStack)-1].start = end
	}
}

func (p *docPrinter) pushFold() {
	p.foldStack = append(p.foldStack, p.outputPosition())
}

func (p *docPrinter) popFold() {
	start := p.foldStack[len(p.foldStack)-1]
	p.foldStack = p.foldStack[:len(p.foldStack)-1]
	p.doc.folds = append(p.doc.folds, [2]position{start, p.outputPosition()})
}

func (p *docPrinter) addAnchor(name, typeName string) {
	if typeName != "" {
		name = typeName + "." + name
	}
	p.doc.anchors[name] = p.outputPosition()
}

func (p *docPrinter) stringIndex(s string) int {
	if i, ok := p.index[s]; ok {
		return i
	}
	i := len(p.index)
	p.index[s] = i
	p.doc.strings = append(p.doc.strings, s)
	return i
}

func (p *docPrinter) outputPosition() position {
	b := p.buf.Bytes()
	for i, c := range b[p.scanOffset:] {
		if c == '\n' {
			p.lineNum += 1
			p.lineOffset = p.scanOffset + i
		}
	}
	p.scanOffset = len(b)
	return newPosition(p.lineNum, len(b)-p.lineOffset)
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

func (v *declVisitor) addAnnoation(kind int, data string, pos token.Pos) {
	v.annotations = append(v.annotations, &annotation{kind: kind, data: data, pos: pos})
}

func (v *declVisitor) ignoreName() {
	v.annotations = append(v.annotations, &annotation{kind: noAnnotation})
}

func (v *declVisitor) Visit(n ast.Node) ast.Visitor {
	switch n := n.(type) {
	case *ast.TypeSpec:
		v.addAnnoation(anchorAnnotation, "", n.Pos())
		name := n.Name.Name
		switch n := n.Type.(type) {
		case *ast.InterfaceType:
			for _, f := range n.Methods.List {
				for _, n := range f.Names {
					v.addAnnoation(anchorAnnotation, name, n.Pos())
				}
				ast.Walk(v, f.Type)
			}
		case *ast.StructType:
			for _, f := range n.Fields.List {
				for _, n := range f.Names {
					v.addAnnoation(anchorAnnotation, name, n.Pos())
				}
				ast.Walk(v, f.Type)
			}
		default:
			ast.Walk(v, n)
		}
	case *ast.FuncDecl:
		if n.Recv == nil {
			v.addAnnoation(anchorAnnotation, "", n.Name.NamePos)
		} else {
			ast.Walk(v, n.Recv)
			if len(n.Recv.List) > 0 {
				typ := n.Recv.List[0].Type
				if se, ok := typ.(*ast.StarExpr); ok {
					typ = se.X
				}
				if id, ok := typ.(*ast.Ident); ok {
					v.addAnnoation(anchorAnnotation, id.Name, n.Name.NamePos)
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
			v.addAnnoation(anchorAnnotation, "", n.Pos())
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
			v.addAnnoation(linkAnnotation, "builtin", 0)
		case n.Obj != nil && ast.IsExported(n.Name):
			v.addAnnoation(linkAnnotation, "", 0)
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
							v.addAnnoation(startLinkAnnotation, path, 0)
							v.addAnnoation(endLinkAnnotation, path, 0)
						} else {
							v.addAnnoation(packageLinkAnnoation, path, 0)
							v.addAnnoation(linkAnnotation, path, 0)
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
