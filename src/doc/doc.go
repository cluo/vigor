// Copyright 2015 Gary Burd. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package doc

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/build"
	"go/doc"
	"go/format"
	goprinter "go/printer"
	"go/scanner"
	"go/token"
	"io/ioutil"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"util"

	"github.com/garyburd/neovim-go/vim"
	"github.com/garyburd/neovim-go/vim/plugin"
)

const (
	textIndent      = "    "
	textWidth       = 80 - len(textIndent)
	protoSlashSlash = "x://"
)

// printer holds state used to create a documentation page.
type printer struct {
	*util.Package

	out     bytes.Buffer
	scratch bytes.Buffer

	links []*link

	// Fields used by outputPosition
	lineNum    int
	lineOffset int
	scanOffset int
}

// link represents a link in the documentation text.
type link struct {
	start, end int
	path       string
	frag       string
}

// bufferData is local data stored for a Neovim buffer.
type bufferData struct {
	importPath string
	links      []*link
	curLink    *link
	matchID    int
}

var state = struct {
	sync.Mutex
	m map[int]*bufferData
}{
	m: make(map[int]*bufferData),
}

func init() {
	plugin.Handle("doc.onCursorMoved", onCursorMoved)
	plugin.Handle("doc.onBufWinLeave", onBufWinLeave)
	plugin.Handle("doc.onBufDelete", onBufDelete)
	plugin.Handle("doc.onJump", onJump)
	plugin.Handle("doc.onUp", onUp)
	plugin.HandleAutocmd("BufReadCmd", &plugin.AutocmdOptions{Pattern: protoSlashSlash + "**", Eval: "expand('%')"}, onBufReadCmd)
}

func onBufReadCmd(v *vim.Vim, u string) error {
	var (
		cwd string
		b   vim.Buffer
	)
	p := v.NewPipeline()
	p.Call("getcwd", &cwd)
	p.CurrentBuffer(&b)
	if err := p.Wait(); err != nil {
		return err
	}

	state.Lock()
	delete(state.m, int(b))
	state.Unlock()

	lines, bd, err := print(u, cwd)
	if err != nil {
		p.SetBufferOption(b, "readonly", false)
		p.SetBufferOption(b, "modifiable", true)
		p.SetBufferLineSlice(b, 0, -1, true, true, bytes.Split([]byte(err.Error()), []byte{'\n'}))
		p.SetBufferOption(b, "buftype", "nofile")
		p.SetBufferOption(b, "bufhidden", "delete")
		p.SetBufferOption(b, "buflisted", false)
		p.SetBufferOption(b, "swapfile", false)
		p.SetBufferOption(b, "modifiable", false)
		return p.Wait()
	}

	channelID, err := v.ChannelID()
	if err != nil {
		return err
	}

	p.SetBufferOption(b, "readonly", false)
	p.SetBufferOption(b, "modifiable", true)
	p.SetBufferLineSlice(b, 0, -1, true, true, lines)
	p.SetBufferOption(b, "buftype", "nofile")
	p.SetBufferOption(b, "bufhidden", "hide")
	p.SetBufferOption(b, "swapfile", false)
	p.SetBufferOption(b, "modifiable", false)
	p.SetBufferOption(b, "readonly", true)
	p.SetBufferVar(b, "curlink", -1, nil)
	p.Command("autocmd! * <buffer>")
	p.Command(fmt.Sprintf("autocmd CursorMoved <buffer> call rpcrequest(%d, 'doc.onCursorMoved', %d, line('.'), col('.'))", channelID, int(b)))
	p.Command(fmt.Sprintf("autocmd BufWinLeave <buffer> call rpcrequest(%d, 'doc.onBufWinLeave', %d)", channelID, int(b)))
	p.Command(fmt.Sprintf("autocmd BufDelete <buffer> call rpcnotify(%d, 'doc.onBufDelete', %d)", channelID, int(b)))
	p.Command(`syntax region godocDecl start='^\(package\|const\|var\|func\|type\) ' end='^$' contains=godocComment,godocParen,godocBrace`)
	p.Command(`syntax region godocParen start='(' end=')' contained contains=godocComment,godocParen,godocBrace`)
	p.Command(`syntax region godocBrace start='{' end='}' contained contains=godocComment,godocParen,godocBrace`)
	p.Command(`syntax region godocComment start='/\*' end='\*/'  contained`)
	p.Command(`syntax region godocComment start='//' end='$' contained`)
	p.Command(`syntax match godocHead '\n\n\n[^\t ].*$'`)
	p.Command(`highlight link godocComment Comment`)
	p.Command(`highlight link godocHead Statement`)
	p.Command(fmt.Sprintf("nnoremap <buffer> <silent> <c-]> :execute rpcrequest(%d, 'doc.onJump', %d, line('.'), col('.'))<CR>", channelID, int(b)))
	p.Command(fmt.Sprintf("nnoremap <buffer> <silent> - :execute rpcrequest(%d, 'doc.onUp', expand('%%'))<CR>", channelID))
	if err := p.Wait(); err != nil {
		return err
	}

	state.Lock()
	state.m[int(b)] = bd
	state.Unlock()

	return nil
}

func onCursorMoved(v *vim.Vim, b, line, col int) error {
	state.Lock()
	defer state.Unlock()

	bd := state.m[b]
	if bd == nil {
		return nil
	}

	link := findLink(bd.links, line, col)
	if link == bd.curLink {
		return nil
	}
	bd.curLink = link

	p := v.NewPipeline()
	if bd.matchID != 0 {
		p.Call("matchdelete", nil, bd.matchID)
		bd.matchID = 0
	}
	if link != nil {
		line, column := lineColumn(link.start)
		len := link.end - link.start
		p.Call("matchaddpos", &bd.matchID, "Underlined", [][]int{[]int{line, column, len}})
	}
	return p.Wait()
}

func onBufWinLeave(v *vim.Vim, b int) error {
	state.Lock()
	defer state.Unlock()

	bd := state.m[b]
	if bd == nil {
		return nil
	}

	if bd.matchID == 0 {
		return nil
	}

	err := v.Call("matchdelete", nil, bd.matchID)
	bd.matchID = 0
	bd.curLink = nil
	return err
}

func onBufDelete(v *vim.Vim, b int) {
	state.Lock()
	delete(state.m, b)
	state.Unlock()
}

func onJump(v *vim.Vim, b, line, col int) (string, error) {
	state.Lock()
	defer state.Unlock()

	bd := state.m[b]
	if bd == nil {
		return "", nil
	}

	link := findLink(bd.links, line, col)
	if link == nil {
		return "", nil
	}

	cmd := "edit " + protoSlashSlash + link.path
	if link.frag != "" {
		cmd += "\\#" + link.frag
	}
	return cmd, nil
}

func onUp(v *vim.Vim, u string) (string, error) {
	importPath, symbol, method := parseURI(u)
	cmd := "edit " + protoSlashSlash
	switch {
	case method != "":
		cmd += importPath + "\\#" + symbol
	case symbol != "":
		cmd += importPath
	default:
		importPath = filepath.Dir(importPath)
		if importPath == "." {
			importPath = ""
		}
		cmd += importPath
	}
	return cmd, nil
}

func position(line, column int) int {
	return line*10000 + column
}

func lineColumn(pos int) (int, int) {
	return pos / 10000, pos % 10000
}

func findLink(links []*link, line, col int) *link {
	p := position(line, col)
	for _, link := range links {
		if p >= link.start {
			if p < link.end {
				return link
			}
		} else if p > link.start {
			break
		}
	}
	return nil
}

// print prints the documentation for the given uri.
func print(uri string, cwd string) ([][]byte, *bufferData, error) {
	defer util.WithGoBuildForPath(cwd)()
	importPath, symbol, method := parseURI(uri)
	p := printer{
		lineNum:    1,
		lineOffset: -1,
	}
	if importPath == "" {
		p.directoryPage()
	} else {
		pkg, err := util.LoadPackage(importPath, cwd, util.LoadDoc)
		if err != nil {
			return nil, nil, err
		}
		p.Package = pkg
		untangleDoc(p.Doc)
		switch {
		case p.Doc == nil:
			p.directoryPage()
		case method != "":
			p.methodPage(symbol, method)
		case symbol != "":
			p.symbolPage(symbol)
		case p.Doc.Name == "main":
			p.commandPage()
		default:
			p.packagePage()
		}
	}
	return bytes.Split(p.out.Bytes(), []byte{'\n'}),
		&bufferData{links: p.links, importPath: importPath}, nil
}

func parseURI(s string) (importPath, symbol, method string) {
	s = strings.TrimPrefix(filepath.ToSlash(s), protoSlashSlash)
	i := strings.Index(s, "#")
	if i < 0 {
		return s, "", ""
	}
	importPath = s[:i]
	s = s[i+1:]
	i = strings.Index(s, ".")
	if i < 0 {
		return importPath, s, ""
	}
	return importPath, s[:i], s[i+1:]
}

func (p *printer) directoryPage() {
	p.dirs()
}

func (p *printer) commandPage() {
	p.printf("Command ")
	p.printf("\n\n")
	p.doc(p.Doc.Doc)
	p.dirs()
}

func (p *printer) packagePage() {
	importPath := p.Build.ImportPath
	if p.Build.ImportComment != "" {
		importPath = p.Build.ImportComment
	}
	p.printf("package %s // import \"%s\"\n\n", p.Doc.Name, importPath)
	p.doc(p.Doc.Doc)
	p.examples("")
	for _, d := range p.Doc.Consts {
		p.valueLine(d)
	}
	for _, d := range p.Doc.Vars {
		p.valueLine(d)
	}
	for _, d := range p.Doc.Funcs {
		p.funcLine(d)
	}
	for _, d := range p.Doc.Types {
		p.typeLine(d)
	}
	p.dirs()
}

func (p *printer) symbolPage(symbol string) {
	for _, d := range [][]*doc.Value{p.Doc.Consts, p.Doc.Vars} {
		for _, d := range d {
			for _, name := range d.Names {
				if name == symbol {
					p.valuePage(d)
					return
				}
			}
		}
	}
	for _, d := range p.Doc.Funcs {
		if d.Name == symbol {
			p.funcPage(d, "")
			return
		}
	}
	for _, d := range p.Doc.Types {
		if d.Name == symbol {
			p.typePage(d)
			return
		}
	}
}

func (p *printer) methodPage(symbol, method string) {
	for _, d := range p.Doc.Types {
		if d.Name == symbol {
			for _, m := range d.Methods {
				if m.Name == method {
					p.funcPage(m, d.Name+"_")
					return
				}
			}
			return
		}
	}
}

func (p *printer) valuePage(d *doc.Value) {
	p.decl(d.Decl)
	p.doc(d.Doc)
}

func (p *printer) funcPage(d *doc.Func, examplePrefix string) {
	p.decl(d.Decl)
	p.doc(d.Doc)
	p.examples(examplePrefix + d.Name)
}

func (p *printer) typePage(d *doc.Type) {
	p.decl(d.Decl)
	p.doc(d.Doc)
	p.examples(d.Name)
	for _, m := range d.Methods {
		p.funcLine(m)
	}
}

func (p *printer) funcLine(d *doc.Func) {
	decl := *d.Decl
	decl.Doc = nil
	decl.Body = nil
	startPos := p.outputPosition()
	p.out.Write(p.nodeLine(&decl))
	n := d.Name
	if d.Recv != "" {
		n = strings.TrimPrefix(d.Recv, "*") + "." + n
	}
	p.addLink(startPos, p.Build.ImportPath, n)
	p.out.WriteByte('\n')
}

func (p *printer) typeLine(d *doc.Type) {
	startPos := p.outputPosition()
	spec := d.Decl.Specs[0].(*ast.TypeSpec) // Must succeed.
	switch spec.Type.(type) {
	case *ast.InterfaceType:
		p.printf("type %s interface { ... }", d.Name)
	case *ast.StructType:
		p.printf("type %s struct { ... }", d.Name)
	default:
		p.printf("type %s %s", d.Name, p.nodeLine(spec.Type))
	}
	p.addLink(startPos, "", d.Name)
	p.out.WriteByte('\n')
}

func (p *printer) valueLine(d *doc.Value) {
	startPos := p.outputPosition()
	spec := d.Decl.Specs[0].(*ast.ValueSpec)
	typ := ""
	if spec.Type != nil {
		typ = fmt.Sprintf(" %s", p.nodeLine(spec.Type))
	}
	val := ""
	if len(spec.Values) > 0 {
		val = fmt.Sprintf(" = %s", p.nodeLine(spec.Values[0]))
	}
	dotDotDot := ""
	if len(d.Decl.Specs) > 1 {
		dotDotDot = " ..."
	}
	p.printf("%s %s%s%s%s", d.Decl.Tok, spec.Names[0], typ, val, dotDotDot)
	p.addLink(startPos, "", d.Names[0])
	p.out.WriteByte('\n')
}

func (p *printer) doc(s string) {
	s = strings.TrimRight(s, " \t\n")
	if s != "" {
		doc.ToText(&p.out, s, "", textIndent, textWidth)
		b := p.out.Bytes()
		if b[len(b)-1] != '\n' {
			p.out.WriteByte('\n')
		}
		p.out.WriteByte('\n')
	}
}

func (p *printer) printf(f string, args ...interface{}) {
	fmt.Fprintf(&p.out, f, args...)
}

var delims = map[byte]byte{
	'{': '}',
	'[': ']',
	'(': ')',
}

func (p *printer) nodeLine(node ast.Node) []byte {
	p.scratch.Reset()
	format.Node(&p.scratch, p.FSet, node)
	b := p.scratch.Bytes()
	if i := bytes.Index(b, []byte{'\n'}); i > 0 {
		if d, ok := delims[b[i-1]]; ok {
			b = append(b[:i], ' ', '.', '.', '.', ' ', d)
		}
	}
	return b
}

var exampleOutputRx = regexp.MustCompile(`(?i)//[[:space:]]*output:`)

func (p *printer) examples(name string) {
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
			node = &goprinter.CommentedNode{Node: e.Code, Comments: e.Comments}
		}

		var buf bytes.Buffer
		err := (&goprinter.Config{Tabwidth: 4}).Fprint(&buf, p.FSet, node)
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

		// Hide examples for now. I tried displaying comments inline and folded
		// and found them both distracting. Consider including link from doc
		// to examples at the end of the page.
		/*
			p.out.Write(b)
			p.out.WriteByte('\n')
			if e.Output != "" {
				p.string(e.Output)
				buf.WriteByte('\n')
			}
			p.out.WriteByte('\n')
		*/
	}
}

type dirSlice []string

func (p dirSlice) Len() int      { return len(p) }
func (p dirSlice) Swap(i, j int) { p[i], p[j] = p[j], p[i] }
func (p dirSlice) Less(i, j int) bool {
	istd := !strings.Contains(p[i], ".")
	jstd := !strings.Contains(p[j], ".")
	if istd && !jstd {
		return true
	}
	if !istd && jstd {
		return false
	}
	return p[i] < p[j]
}

func (p *printer) dirs() {
	importPath := ""
	if p.Package != nil {
		importPath = p.Package.Build.ImportPath
	}
	m := make(map[string]bool)
	for _, dir := range build.Default.SrcDirs() {
		dir = filepath.Join(dir, filepath.FromSlash(importPath))
		fis, err := ioutil.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, fi := range fis {
			if !fi.IsDir() || strings.HasPrefix(fi.Name(), ".") || fi.Name() == "testdata" {
				continue
			}
			m[fi.Name()] = true
		}
	}

	var names []string
	for name := range m {
		names = append(names, name)
	}
	sort.Sort(dirSlice(names))

	for _, name := range names {
		startPos := p.outputPosition()
		p.printf("%s%c", name, filepath.Separator)
		p.addLink(startPos, path.Join(importPath, name), "")
		p.out.WriteByte('\n')
	}
}

func (p *printer) addLink(startPos int, path, frag string) {
	if path == "" {
		path = p.Build.ImportPath
	}
	p.links = append(p.links, &link{startPos, p.outputPosition(), path, frag})
}

func (p *printer) outputPosition() int {
	b := p.out.Bytes()
	for i, c := range b[p.scanOffset:] {
		if c == '\n' {
			p.lineNum += 1
			p.lineOffset = p.scanOffset + i
		}
	}
	p.scanOffset = len(b)
	return position(p.lineNum, len(b)-p.lineOffset)
}

var adjustSuffixes = [][]byte{[]byte{'*'}, []byte{'[', ']'}, []byte{'*'}, []byte{'&'}}

func (p *printer) adjustedOutputPosition() int {
	b := p.out.Bytes()
	for _, s := range adjustSuffixes {
		b = bytes.TrimSuffix(b, s)
	}
	return p.outputPosition() - p.out.Len() + len(b)
}

const (
	noAnnotation = iota
	packageLinkAnnoation
	linkAnnotation
	startLinkAnnotation
	endLinkAnnotation
)

type annotation struct {
	kind       int
	importPath string
}

// decl formats and prints a decleration.
func (p *printer) decl(decl ast.Decl) {
	v := &declVisitor{}
	ast.Walk(v, decl)
	var w bytes.Buffer
	err := (&goprinter.Config{Tabwidth: 4}).Fprint(
		&w,
		p.FSet,
		&goprinter.CommentedNode{Node: decl, Comments: v.comments})
	if err != nil {
		p.out.WriteString(err.Error())
		return
	}
	buf := bytes.TrimRight(w.Bytes(), " \t\n")

	var s scanner.Scanner
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(buf))
	base := file.Base()
	s.Init(file, buf, nil, scanner.ScanComments)
	lastOffset := 0
	var startPos int
loop:
	for {
		pos, tok, lit := s.Scan()
		switch tok {
		case token.EOF:
			break loop
		case token.IDENT:
			if len(v.annotations) == 0 {
				// Oops!
				break loop
			}
			offset := int(pos) - base
			p.out.Write(buf[lastOffset:offset])
			lastOffset = offset + len(lit)
			a := v.annotations[0]
			v.annotations = v.annotations[1:]
			switch a.kind {
			case startLinkAnnotation:
				startPos = p.adjustedOutputPosition()
				p.out.WriteString(lit)
			case linkAnnotation:
				startPos = p.adjustedOutputPosition()
				fallthrough
			case endLinkAnnotation:
				p.out.WriteString(lit)
				p.addLink(startPos, a.importPath, lit)
			case packageLinkAnnoation:
				p.out.WriteString(lit)
				p.addLink(startPos, a.importPath, "")
			default:
				p.out.WriteString(lit)
			}
		}
	}
	p.out.Write(buf[lastOffset:])
	p.out.WriteString("\n\n")
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

func (v *declVisitor) addAnnoation(kind int, importPath string) {
	v.annotations = append(v.annotations, &annotation{kind: kind, importPath: importPath})
}

func (v *declVisitor) ignoreName() {
	v.annotations = append(v.annotations, &annotation{kind: noAnnotation})
}

func (v *declVisitor) Visit(n ast.Node) ast.Visitor {
	switch n := n.(type) {
	case *ast.TypeSpec:
		v.ignoreName()
		ast.Walk(v, n.Type)
	case *ast.FuncDecl:
		if n.Recv != nil {
			ast.Walk(v, n.Recv)
		}
		v.ignoreName()
		ast.Walk(v, n.Type)
	case *ast.Field:
		for _ = range n.Names {
			v.ignoreName()
		}
		ast.Walk(v, n.Type)
	case *ast.ValueSpec:
		for range n.Names {
			v.ignoreName()
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
			v.addAnnoation(linkAnnotation, "builtin")
		case n.Obj != nil && ast.IsExported(n.Name):
			v.addAnnoation(linkAnnotation, "")
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
							v.addAnnoation(startLinkAnnotation, path)
							v.addAnnoation(endLinkAnnotation, path)
						} else {
							v.addAnnoation(packageLinkAnnoation, path)
							v.addAnnoation(linkAnnotation, path)
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

func untangleDoc(d *doc.Package) {
	if d == nil {
		return
	}
	for _, t := range d.Types {
		d.Consts = append(d.Consts, t.Consts...)
		t.Consts = nil
		d.Vars = append(d.Vars, t.Vars...)
		t.Vars = nil
		d.Funcs = append(d.Funcs, t.Funcs...)
		t.Funcs = nil
	}
}
