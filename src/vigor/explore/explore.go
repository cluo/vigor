// Copyright 2015 Gary Burd. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package explore implements the :Godoc and :Godef commands.
package doc

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"vigor/util"

	"github.com/garyburd/neovim-go/vim"
	"github.com/garyburd/neovim-go/vim/plugin"
	"github.com/garyburd/neovim-go/vim/vimutil"
)

var state = struct {
	sync.Mutex
	m map[int]*bufferData
}{
	m: make(map[int]*bufferData),
}

func init() {
	plugin.HandleCommand("Godoc", &plugin.CommandOptions{NArgs: "*", Complete: "customlist,QQQDocComplete", Eval: "[getcwd(), 0]"}, onDoc)
	plugin.HandleCommand("Pgodoc", &plugin.CommandOptions{NArgs: "*", Complete: "customlist,QQQDocComplete", Eval: "[getcwd(), 1]"}, onDoc)
	plugin.HandleCommand("Godef", &plugin.CommandOptions{NArgs: "*", Complete: "customlist,QQQDocComplete", Eval: "getcwd()"}, onDef)
	plugin.HandleFunction("QQQDocComplete", &plugin.FunctionOptions{Eval: "getcwd()"}, onComplete)
	plugin.HandleAutocmd("BufReadCmd", &plugin.AutocmdOptions{Pattern: protoSlashSlash + "**", Eval: "[expand('%'), getcwd()]"}, onBufReadCmd)
	plugin.Handle("doc.onBufDelete", onBufDelete)
	plugin.Handle("doc.onBufWinEnter", onBufWinEnter)
	plugin.Handle("doc.onJump", onJump)
	plugin.Handle("doc.onUp", onUp)
}

type onDocEval struct {
	Cwd     string `msgpack:",array"`
	Preview bool
}

func onDoc(v *vim.Vim, args []string, eval *onDocEval) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New("one or two arguments required")
	}

	cleanup := util.WithGoBuildForPath(eval.Cwd)
	path := resolvePackageSpec(eval.Cwd, vimutil.CurrentBufferReader(v), args[0])
	cleanup()

	var sym string
	if len(args) >= 2 {
		sym = strings.Trim(args[1], ".")
	}

	editCommand := "edit"
	if eval.Preview {
		editCommand = "pedit"
	}
	/*
	   This commented out code opens the documentation in a window that's already
	   showing the documentationn or in a new tab.

	   b, err := v.CurrentBuffer()
	   if err != nil {
	       return err
	   }

	   var ft string
	   if err := v.BufferOption(b, "filetype", &ft); err != nil {
	       return err
	   }
	   if ft != "godoc" {
	       editCommand = "tabnew"
	       windows, err := v.Windows()
	       if err != nil {
	           return err
	       }
	       buffers := make([]vim.Buffer, len(windows))
	       p := v.NewPipeline()
	       for i := range buffers {
	           p.WindowBuffer(windows[i], &buffers[i])
	       }
	       if err := p.Wait(); err != nil {
	           return err
	       }
	       fts := make([]string, len(buffers))
	       for i := range fts {
	           p.BufferOption(buffers[i], "filetype", &fts[i])
	       }
	       if err := p.Wait(); err != nil {
	           return err
	       }
	       for i := range fts {
	           if ft == "godoc" {
	               if err := v.SetCurrentWindow(windows[i]); err != nil {
	                   return err
	               }
	               editCommand = "edit"
	               break
	           }
	       }
	   }
	*/

	sharp := ""
	if sym != "" {
		sharp = "\\#"
	}
	return v.Command(fmt.Sprintf("%s %s%s%s%s", editCommand, protoSlashSlash, path, sharp, sym))
}

func onDef(v *vim.Vim, args []string, cwd string) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New("one or two arguments required")
	}

	defer util.WithGoBuildForPath(cwd)()
	path := resolvePackageSpec(cwd, vimutil.CurrentBufferReader(v), args[0])

	var sym string
	if len(args) >= 2 {
		sym = strings.Trim(args[1], ".")
	}

	file, line, col, err := findDef(cwd, path, sym)

	if err != nil {
		return errors.New("definition not found")
	}

	p := v.NewPipeline()
	p.Command(fmt.Sprintf("edit %s", file))
	if line != 0 {
		p.Command(fmt.Sprintf("%d", line))
	}
	if col != 0 {
		p.Command(fmt.Sprintf("normal! %d|", col))
	}
	return p.Wait()
}

func onComplete(v *vim.Vim, a *vimutil.CommandCompletionArgs, cwd string) ([]string, error) {
	defer util.WithGoBuildForPath(cwd)()
	f := strings.Fields(a.CmdLine)
	var completions []string
	if len(f) >= 3 || (len(f) == 2 && a.ArgLead == "") {
		completions = completeSymMethod(resolvePackageSpec(cwd, vimutil.CurrentBufferReader(v), f[1]), a.ArgLead)
	} else {
		completions = completePackage(cwd, vimutil.CurrentBufferReader(v), a.ArgLead)
	}
	return completions, nil
}

type bufReadEval struct {
	Name string `msgpack:",array"`
	Cwd  string
}

func onBufReadCmd(v *vim.Vim, eval *bufReadEval) error {
	var (
		b vim.Buffer
		w vim.Window
	)
	p := v.NewPipeline()
	p.CurrentBuffer(&b)
	p.CurrentWindow(&w)
	if err := p.Wait(); err != nil {
		return err
	}

	state.Lock()
	delete(state.m, int(b))
	state.Unlock()

	defer util.WithGoBuildForPath(eval.Cwd)()

	lines, bd, err := print(eval.Name, eval.Cwd)
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
	p.SetBufferOption(b, "buflisted", false)
	p.SetBufferOption(b, "swapfile", false)
	p.SetBufferOption(b, "modifiable", false)
	p.SetBufferOption(b, "readonly", true)
	p.SetBufferOption(b, "tabstop", 4)
	p.SetWindowOption(w, "conceallevel", 3)
	p.SetWindowOption(w, "concealcursor", "nv")
	p.Command("autocmd! * <buffer>")
	p.Command(fmt.Sprintf("autocmd BufDelete <buffer> call rpcnotify(%d, 'doc.onBufDelete', %d)", channelID, int(b)))
	p.Command(fmt.Sprintf("autocmd BufWinEnter <buffer> call rpcrequest(%d, 'doc.onBufWinEnter', %d)", channelID, int(b)))
	p.Command("autocmd BufWinLeave <buffer> call clearmatches()")
	p.Command(`syntax region godocCode start='\%^.' end='^[^ \t)}]'me=e-1 contains=godocComment`)
	p.Command(`syntax region godocCode matchgroup=helpIgnore start=' >$' start='^>$' end='^[^ \t]'me=e-1 end='^<' concealends contains=godocComment`)
	p.Command(`syntax region godocComment start='/\*' end='\*/'  contained`)
	p.Command(`syntax region godocComment start='//' end='$' contained`)
	p.Command(`syntax match godocHead '^.*\ze\~$' nextgroup=godocIgnore`)
	p.Command(`syntax match godocIgnore '.' conceal contained`)
	p.Command(`highlight link godocComment Comment`)
	p.Command(`highlight link godocHead Statement`)
	p.Command(`highlight link godocCode Statement`)
	p.Command(`highlight link godocIgnore Ignore`)
	for _, f := range bd.folds {
		p.Command(fmt.Sprintf("%d,%dfold", f[0], f[1]))
	}
	p.Command(fmt.Sprintf("nnoremap <buffer> <silent> <c-]> :execute rpcrequest(%d, 'doc.onJump', %d, line('.'), col('.'))<CR>", channelID, int(b)))
	p.Command(fmt.Sprintf("nnoremap <buffer> <silent> - :execute rpcrequest(%d, 'doc.onUp', expand('%%'))<CR>", channelID))
	p.Command("nnoremap <buffer> <silent> g? :help :Godef")
	if bd.file != "" {
		c := `nnoremap <buffer> <silent> o :if &previewwindow \| wincmd p \| endif \| edit ` + bd.file
		if bd.line != 0 {
			c += fmt.Sprintf(`\| %d`, bd.line)
		}
		c += "<CR>"
		p.Command(c)
	}
	if err := p.Wait(); err != nil {
		return err
	}

	state.Lock()
	state.m[int(b)] = bd
	state.Unlock()

	return nil
}

func onBufDelete(v *vim.Vim, b int) {
	state.Lock()
	delete(state.m, b)
	state.Unlock()
}

func onBufWinEnter(v *vim.Vim, b int) error {
	state.Lock()
	defer state.Unlock()
	bd := state.m[b]
	if bd == nil {
		return nil
	}
	p := v.NewPipeline()
	p.Call("clearmatches", nil)
	for _, l := range bd.links {
		line, column := lineColumn(l.start)
		p.Call("matchaddpos", nil, "Identifier", []interface{}{[]int{line, column, l.end - l.start}})
	}
	return p.Wait()
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
