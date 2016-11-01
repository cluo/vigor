// Copyright 2016 Gary Burd. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package doc

import (
	"bytes"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"

	"github.com/neovim/go-client/nvim"
	"github.com/neovim/go-client/nvim/plugin"
)

// Doc holds state used to create a documentation page.
type Doc struct {
	mgr *Manager

	data *data

	folds      []*fold
	highlights []*highlight
	anchors    map[string][2]int

	buf                       bytes.Buffer
	index                     map[string]int
	highlightStack, linkStack []stackElement
	foldStack                 []position

	// Fields used by outputPosition
	lineNum    int
	lineOffset int
	scanOffset int
}

func NewDoc() *Doc {
	return &Doc{
		index:      make(map[string]int),
		anchors:    make(map[string][2]int),
		data:       &data{},
		lineNum:    1,
		lineOffset: -1,
	}
}

func (d *Doc) WriteString(s string) (int, error) { return d.buf.WriteString(s) }

func (d *Doc) Write(p []byte) (int, error) { return d.buf.Write(p) }

func (d *Doc) AddAnchor(name string) {
	address := d.outputPosition()
	d.anchors[name] = [2]int{address.line(), address.column()}
}

func (d *Doc) PushFold() {
	d.foldStack = append(d.foldStack, d.outputPosition())
}

func (d *Doc) PopFold() {
	start := d.foldStack[len(d.foldStack)-1]
	d.foldStack = d.foldStack[:len(d.foldStack)-1]
	end := d.outputPosition()

	lstart := start.line()
	lend := end.line()
	if end.column() == 1 {
		lend--
	}
	if lend > lstart {
		d.folds = append(d.folds, &fold{start: lstart, end: lend})
	}
}

func (d *Doc) PushLinkAnchor(path string, anchor string) {
	log.Println("PUSHA", path, anchor)
	address := newPosition(0, -1)
	if anchor != "" {
		address = newPosition(0, d.stringIndex(anchor))
	}
	d.push(&d.linkStack, &link{path: d.stringIndex(path), address: address})
}

func (d *Doc) PushLink(path string, line, column int) {
	d.push(&d.linkStack, &link{path: d.stringIndex(path), address: newPosition(line, column)})
}

func (d *Doc) PopLink() { d.pop(&d.linkStack) }

func (d *Doc) WriteLink(text string, path string, line, column int) {
	d.PushLink(path, line, column)
	d.WriteString(text)
	d.PopLink()
}

func (d *Doc) WriteLinkAnchor(text string, path, anchor string) {
	d.PushLinkAnchor(path, anchor)
	d.WriteString(text)
	d.PopLink()
}

func (d *Doc) PushHighlight(group string) {
	d.push(&d.highlightStack, &highlight{group: group})
}

func (d *Doc) PopHighlight() { d.pop(&d.highlightStack) }

type stackValue interface {
	appendCopy(d *Doc, start, end position)
}

type stackElement struct {
	start position
	value stackValue
}

func (d *Doc) push(stack *[]stackElement, value stackValue) {
	start := d.outputPosition()
	if n := len(*stack); n > 0 {
		e := (*stack)[n-1]
		if e.start != start {
			e.value.appendCopy(d, e.start, start)
		}
	}
	*stack = append(*stack, stackElement{start, value})
}

func (d *Doc) pop(stack *[]stackElement) {
	e := (*stack)[len(*stack)-1]
	*stack = (*stack)[:len(*stack)-1]
	end := d.outputPosition()
	if e.start != end {
		e.value.appendCopy(d, e.start, end)
	}
	if len(*stack) > 0 {
		(*stack)[len(*stack)-1].start = end
	}
}

func (d *Doc) stringIndex(s string) int {
	if i, ok := d.index[s]; ok {
		return i
	}
	i := len(d.index)
	d.index[s] = i
	d.data.strings = append(d.data.strings, s)
	return i
}

func (d *Doc) outputPosition() position {
	p := d.buf.Bytes()
	for i, c := range p[d.scanOffset:] {
		if c == '\n' {
			d.lineNum += 1
			d.lineOffset = d.scanOffset + i
		}
	}
	d.scanOffset = len(p)
	return newPosition(d.lineNum, len(p)-d.lineOffset)
}

// data holds document data required for user interaction
type data struct {
	strings []string
	links   []*link
}

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

	// Path is index of the target file path in data.strings.
	path int

	// Target specifies the position in the target file.
	address position
}

func (e *link) appendCopy(d *Doc, start, end position) {
	d.data.links = append(d.data.links, &link{start: start, end: end, path: e.path, address: e.address})
}

// highlight represents a range of text to highlight.
type highlight struct {
	// Start and end of highlight range.
	start, end position

	// Vim highlight group name.
	group string
}

func (e *highlight) appendCopy(d *Doc, start, end position) {
	d.highlights = append(d.highlights, &highlight{start: start, end: end, group: e.group})
}

// fold represents a range of text to fold
type fold struct {
	// Start and end lines
	start, end int
}

type windowHighlight struct {
	id   int
	link *link
}

type Manager struct {
	nvim       *nvim.Nvim
	mu         sync.Mutex
	docs       map[int]*data
	highlights map[nvim.Window]*windowHighlight
}

func NewManager(p *plugin.Plugin) *Manager {
	m := &Manager{nvim: p.Nvim, docs: make(map[int]*data), highlights: make(map[nvim.Window]*windowHighlight)}
	p.Handle("doc.onUpdateHighlight", m.onUpdateHighlight)
	p.Handle("doc.onBufDelete", m.onBufDelete)
	p.Handle("doc.onJump", m.onJump)
	return m
}

func (m *Manager) onBufDelete(b int) {
	m.mu.Lock()
	delete(m.docs, b)
	m.mu.Unlock()
}

func (m *Manager) onJump(b, line, col int) error {
	d, link := m.findLink(b, line, col)
	if link == nil {
		return nil
	}
	var cmds []string
	if p := d.strings[link.path]; p != "" {
		cmds = append(cmds, fmt.Sprintf("edit %s", p))
	}

	l, c := link.address.line(), link.address.column()
	if l > 0 {
		cmds = append(cmds, fmt.Sprintf("call cursor(%d, %d)", l, c))
	} else if c >= 0 {
		cmds = append(cmds, fmt.Sprintf("call cursor(get(b:anchors, %q, [0, 0]))", d.strings[c]))
	}
	log.Println("JUMP", l, c, cmds)
	return m.nvim.Command(strings.Join(cmds, "| "))
}

func (m *Manager) onUpdateHighlight(b, line, col int) error {

	_, newLink := m.findLink(b, line, col)

	w, err := m.nvim.CurrentWindow()
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	hl := m.highlights[w]
	var oldLink *link
	if hl != nil {
		oldLink = hl.link
	}

	if oldLink == newLink {
		return nil
	}

	if hl != nil {
		delete(m.highlights, w)
		if err := m.nvim.Call("matchdelete", nil, hl.id); err != nil {
			return err
		}
	}

	if newLink != nil {
		hl := &windowHighlight{link: newLink}
		m.highlights[w] = hl
		if err := m.nvim.Call("matchaddpos", &hl.id, "Underlined", [][3]int{{newLink.start.line(), newLink.start.column(), int(newLink.end - newLink.start)}}); err != nil {
			return err
		}
	}

	return nil
}

func (m *Manager) findLink(b, line, col int) (*data, *link) {
	m.mu.Lock()
	d := m.docs[b]
	m.mu.Unlock()
	if d == nil {
		return nil, nil
	}
	p := newPosition(line, col)
	i := sort.Search(len(d.links), func(i int) bool {
		return d.links[i].end > p
	})
	if i >= len(d.links) {
		return nil, nil
	}
	link := d.links[i]
	if d.links[i].start.line() != line {
		return nil, nil
	}
	return d, link
}

func (m *Manager) Display(d *Doc, buf nvim.Buffer) error {
	b := m.nvim.NewBatch()
	b.SetBufferOption(buf, "readonly", false)
	b.SetBufferOption(buf, "modifiable", true)
	b.SetBufferLines(buf, 0, -1, true, bytes.Split(d.buf.Bytes(), []byte{'\n'}))
	b.SetBufferOption(buf, "buftype", "nofile")
	b.SetBufferOption(buf, "bufhidden", "hide")
	b.SetBufferOption(buf, "buflisted", false)
	b.SetBufferOption(buf, "swapfile", false)
	b.SetBufferOption(buf, "modifiable", false)
	b.SetBufferOption(buf, "readonly", true)
	b.SetBufferOption(buf, "tabstop", 4)
	b.Command("autocmd! * <buffer>")
	b.Command(fmt.Sprintf("autocmd BufDelete <buffer> call rpcnotify(%d, 'doc.onBufDelete', bufnr('%%'))", m.nvim.ChannelID()))
	b.Command(fmt.Sprintf("autocmd CursorMoved <buffer> call rpcrequest(%d, 'doc.onUpdateHighlight', bufnr('%%'), line('.'), col('.'))", m.nvim.ChannelID()))
	b.Command(fmt.Sprintf("autocmd BufWinLeave <buffer> call rpcrequest(%d, 'doc.onUpdateHighlight', bufnr('%%'), -1, -1)", m.nvim.ChannelID()))
	b.ClearBufferHighlight(buf, -1, 0, -1)
	for _, h := range d.highlights {
		lstart, cstart := h.start.line(), h.start.column()
		lend, cend := h.end.line(), h.end.column()
		for l := lstart; l < lend; l++ {
			var id int
			b.AddBufferHighlight(buf, -1, h.group, l-1, cstart-1, -1, &id)
			cstart = 1
		}
		var id int
		b.AddBufferHighlight(buf, -1, h.group, lend-1, cstart-1, cend-1, &id)
	}
	for _, f := range d.folds {
		b.Command(fmt.Sprintf("%d,%dfold", f.start, f.end))
	}
	b.SetBufferVar(buf, "anchors", d.anchors)
	b.Command(fmt.Sprintf("nnoremap <buffer> <silent> <CR> :<C-U>call rpcrequest(%d, 'doc.onJump', %d, line('.'), col('.'))<CR>", m.nvim.ChannelID(), int(buf)))
	if err := b.Execute(); err != nil {
		return err
	}
	for _, l := range d.data.links {
		log.Println("LINK", l)
	}
	for i, s := range d.data.strings {
		log.Println(i, s)
	}
	m.mu.Lock()
	m.docs[int(buf)] = d.data
	m.mu.Unlock()
	return nil
}
