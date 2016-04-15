// Copyright 2015 Gary Burd. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package explore implements the :Godoc and :Godef commands.
package explore

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"vigor/context"

	"github.com/garyburd/neovim-go/vim"
	"github.com/garyburd/neovim-go/vim/plugin"
)

var docs = struct {
	sync.Mutex
	m map[int]*doc
}{
	m: make(map[int]*doc),
}

func init() {
	plugin.HandleCommand("Godoc", &plugin.CommandOptions{NArgs: "*", Complete: "customlist,QQQDocComplete", Eval: "*"}, onDoc)
	plugin.HandleCommand("Godef", &plugin.CommandOptions{NArgs: "*", Complete: "customlist,QQQDocComplete", Eval: "*"}, onDef)
	plugin.HandleFunction("QQQDocComplete", &plugin.FunctionOptions{Eval: "*"}, onComplete)
	plugin.HandleAutocmd("BufReadCmd", &plugin.AutocmdOptions{Pattern: bufNamePrefix + "**", Eval: "*"}, onBufReadCmd)
	plugin.Handle("explorer.onUpdateHighlight", onUpdateHighlight)
	plugin.Handle("explorer.onBufDelete", onBufDelete)
	plugin.Handle("explorer.onJump", onJump)
}

func expandSpec(v *vim.Vim, spec string) (string, error) {
	if len(spec) == 0 {
		return spec, nil
	}
	if spec[0] != '%' && spec[0] != '#' && spec[0] != '<' {
		return spec, nil
	}
	err := v.Call("expand", &spec, spec)
	return spec, err
}

func onDoc(v *vim.Vim, args []string, eval *struct {
	Env  context.Env
	Cwd  string `eval:"getcwd()"`
	Name string `eval:"expand('%')"`
}) error {

	if len(args) < 1 || len(args) > 2 {
		return errors.New("one or two arguments required")
	}

	spec, err := expandSpec(v, args[0])
	if err != nil {
		return err
	}

	ctx := context.Get(&eval.Env)
	name := bufNamePrefix + resolvePackageSpec(&ctx.Build, eval.Cwd, vim.NewBufferReader(v, 0), spec)

	var cmds []string
	if name != eval.Name {
		cmds = append(cmds, "edit "+name)
	}

	if len(args) >= 2 {
		cmds = append(cmds, fmt.Sprintf("call cursor(get(b:anchors, %q, [0, 0]))", strings.Trim(args[1], ".")))
	}
	if len(cmds) == 0 {
		return nil
	}
	return v.Command(strings.Join(cmds, " | "))
}

func onDef(v *vim.Vim, args []string, eval *struct {
	Env context.Env
	Cwd string `eval:"getcwd()"`
}) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New("one or two arguments required")
	}

	spec, err := expandSpec(v, args[0])
	if err != nil {
		return err
	}

	ctx := context.Get(&eval.Env)
	path := resolvePackageSpec(&ctx.Build, eval.Cwd, vim.NewBufferReader(v, 0), spec)

	var sym string
	if len(args) >= 2 {
		sym = strings.Trim(args[1], ".")
	}

	file, line, col, err := findDef(&ctx.Build, eval.Cwd, path, sym)
	if err != nil {
		return errors.New("definition not found")
	}

	return v.Command(fmt.Sprintf("edit %s | call cursor([%d, %d])", file, line, col))
}

func onComplete(v *vim.Vim, a *vim.CommandCompletionArgs, eval *struct {
	Env context.Env
	Cwd string `eval:"getcwd()"`
}) ([]string, error) {

	ctx := context.Get(&eval.Env)

	f := strings.Fields(a.CmdLine)
	var completions []string
	if len(f) >= 3 || (len(f) == 2 && a.ArgLead == "") {
		spec, err := expandSpec(v, f[1])
		if err != nil {
			return nil, err
		}
		completions = completeSymMethodArg(&ctx.Build, resolvePackageSpec(&ctx.Build, eval.Cwd, vim.NewBufferReader(v, 0), spec), a.ArgLead)
	} else {
		completions = completePackageArg(&ctx.Build, eval.Cwd, vim.NewBufferReader(v, 0), a.ArgLead)
	}
	return completions, nil
}

func onBufReadCmd(v *vim.Vim, eval *struct {
	Env  context.Env
	Cwd  string `eval:"getcwd()"`
	Name string `eval:"expand('%')"`
}) error {

	ctx := context.Get(&eval.Env)

	b, err := v.CurrentBuffer()
	if err != nil {
		return err
	}

	docs.Lock()
	delete(docs.m, int(b))
	docs.Unlock()

	d, err := printDoc(&ctx.Build, eval.Name, eval.Cwd)
	if err != nil {
		p := v.NewPipeline()
		p.SetBufferOption(b, "readonly", false)
		p.SetBufferOption(b, "modifiable", true)
		p.SetBufferLines(b, 0, -1, true, bytes.Split([]byte(err.Error()), []byte{'\n'}))
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

	p := v.NewPipeline()
	p.SetBufferOption(b, "readonly", false)
	p.SetBufferOption(b, "modifiable", true)
	p.SetBufferLines(b, 0, -1, true, bytes.Split(d.text, []byte{'\n'}))
	p.SetBufferOption(b, "buftype", "nofile")
	p.SetBufferOption(b, "bufhidden", "hide")
	p.SetBufferOption(b, "buflisted", false)
	p.SetBufferOption(b, "swapfile", false)
	p.SetBufferOption(b, "modifiable", false)
	p.SetBufferOption(b, "readonly", true)
	p.SetBufferOption(b, "tabstop", 4)
	p.Command("autocmd! * <buffer>")
	p.Command(fmt.Sprintf("autocmd BufDelete <buffer> call rpcnotify(%d, 'explorer.onBufDelete', %d)", channelID, int(b)))
	p.Command(fmt.Sprintf("autocmd CursorMoved <buffer> call rpcrequest(%d, 'explorer.onUpdateHighlight', %d, line('.'), col('.'))", channelID, int(b)))
	p.Command(fmt.Sprintf("autocmd BufWinLeave <buffer> call rpcrequest(%d, 'explorer.onUpdateHighlight', %d, -1, -1)", channelID, int(b)))
	p.ClearBufferHighlight(b, -1, 0, -1)
	for _, h := range d.highlights {
		lstart, cstart := h.start.line(), h.start.column()
		lend, cend := h.end.line(), h.end.column()
		for l := lstart; l < lend; l++ {
			var id int
			p.AddBufferHighlight(b, -1, h.group, l-1, cstart-1, -1, &id)
			cstart = 1
		}
		var id int
		p.AddBufferHighlight(b, -1, h.group, lend-1, cstart-1, cend-1, &id)
	}
	for _, f := range d.folds {
		p.Command(fmt.Sprintf("%d,%dfold", f[0], f[1]))
	}
	anchors := make(map[string][2]int)
	for n, a := range d.anchors {
		anchors[n] = [2]int{a.line(), a.column()}
	}
	p.SetBufferVar(b, "anchors", anchors, nil)
	p.Command(fmt.Sprintf("nnoremap <buffer> <silent> <CR> :<C-U>call rpcrequest(%d, 'explorer.onJump', %d, line('.'), col('.'))<CR>", channelID, int(b)))
	p.Command("nnoremap <buffer> <silent> g? :<C-U>help :Godoc<CR>")
	p.Command(`nnoremap <buffer> <silent> ]] :<C-U>call search('\C\v^[^ \t)}]', 'W')<CR>`)
	p.Command(`nnoremap <buffer> <silent> [[ :<C-U>call search('\C\v^[^ \t)}]', 'Wb')<CR>`)
	if err := p.Wait(); err != nil {
		return err
	}

	docs.Lock()
	docs.m[int(b)] = d
	docs.Unlock()

	return nil
}

func onBufDelete(v *vim.Vim, b int) {
	docs.Lock()
	delete(docs.m, b)
	docs.Unlock()
}

func onJump(v *vim.Vim, b, line, col int) error {
	d, link := findLink(b, line, col)
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
		cmds = append(cmds, fmt.Sprintf("call cursor(get(b:anchors, \"%s\", [0, 0]))", d.strings[c]))
	}
	return v.Command(strings.Join(cmds, "| "))
}

type windowHighlight struct {
	id   int
	link *link
}

var highlights = struct {
	sync.Mutex
	m map[vim.Window]*windowHighlight
}{
	m: make(map[vim.Window]*windowHighlight),
}

func onUpdateHighlight(v *vim.Vim, b, line, col int) error {

	_, newLink := findLink(b, line, col)

	w, err := v.CurrentWindow()
	if err != nil {
		return err
	}

	highlights.Lock()
	defer highlights.Unlock()

	hl := highlights.m[w]
	var oldLink *link
	if hl != nil {
		oldLink = hl.link
	}

	if oldLink == newLink {
		return nil
	}

	if hl != nil {
		delete(highlights.m, w)
		if err := v.Call("matchdelete", nil, hl.id); err != nil {
			return err
		}
	}

	if newLink != nil {
		hl := &windowHighlight{link: newLink}
		highlights.m[w] = hl
		if err := v.Call("matchaddpos", &hl.id, "Underlined", [][3]int{{newLink.start.line(), newLink.start.column(), int(newLink.end - newLink.start)}}); err != nil {
			return err
		}
	}

	return nil
}

func findLink(b, line, col int) (*doc, *link) {
	docs.Lock()
	d := docs.m[b]
	docs.Unlock()
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
