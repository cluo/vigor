// Copyright 2015 Gary Burd. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package explore implements the :Godoc and :Godef commands.
package explore

import (
	"errors"
	"fmt"
	"strings"

	"github.com/garyburd/vigor/src/context"
	"github.com/garyburd/vigor/src/doc"
	"github.com/neovim/go-client/nvim"
	"github.com/neovim/go-client/nvim/plugin"
)

func Register(p *plugin.Plugin) {
	e := &explorer{docm: doc.NewManager(p), nvim: p.Nvim}
	p.HandleCommand(&plugin.CommandOptions{Name: "Godoc", NArgs: "*", Complete: "customlist,QQQDocComplete", Eval: "*"}, e.onDoc)
	p.HandleCommand(&plugin.CommandOptions{Name: "Godef", NArgs: "*", Complete: "customlist,QQQDocComplete", Eval: "*"}, e.onDef)
	p.HandleFunction(&plugin.FunctionOptions{Name: "QQQDocComplete", Eval: "*"}, e.onComplete)
	p.HandleAutocmd(&plugin.AutocmdOptions{Event: "BufReadCmd", Pattern: bufNamePrefix + "**", Eval: "*"}, e.onBufReadCmd)
}

type explorer struct {
	nvim *nvim.Nvim
	docm *doc.Manager
}

func (e *explorer) expandSpec(spec string) (string, error) {
	if len(spec) == 0 {
		return spec, nil
	}
	if spec[0] != '%' && spec[0] != '#' && spec[0] != '<' {
		return spec, nil
	}
	err := e.nvim.Call("expand", &spec, spec)
	return spec, err
}

func (e *explorer) onDoc(args []string, eval *struct {
	Env   context.Env
	Cwd   string `eval:"getcwd()"`
	Name  string `eval:"expand('%')"`
	Bufnr int    `eval:"bufnr('%')"`
}) error {

	if len(args) < 1 || len(args) > 2 {
		return errors.New("one or two arguments required")
	}

	spec, err := e.expandSpec(args[0])
	if err != nil {
		return err
	}

	ctx := context.Get(&eval.Env)
	name := bufNamePrefix + resolvePackageSpec(&ctx.Build, eval.Cwd, nvim.NewBufferReader(e.nvim, nvim.Buffer(eval.Bufnr)), spec)

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
	return e.nvim.Command(strings.Join(cmds, " | "))
}

func (e *explorer) onDef(args []string, eval *struct {
	Env   context.Env
	Cwd   string `eval:"getcwd()"`
	Bufnr int    `eval:"bufnr('%')"`
}) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New("one or two arguments required")
	}

	spec, err := e.expandSpec(args[0])
	if err != nil {
		return err
	}

	ctx := context.Get(&eval.Env)
	path := resolvePackageSpec(&ctx.Build, eval.Cwd, nvim.NewBufferReader(e.nvim, nvim.Buffer(eval.Bufnr)), spec)

	var sym string
	if len(args) >= 2 {
		sym = strings.Trim(args[1], ".")
	}

	file, line, col, err := findDef(&ctx.Build, eval.Cwd, path, sym)
	if err != nil {
		return errors.New("definition not found")
	}

	return e.nvim.Command(fmt.Sprintf("edit %s | call cursor(%d, %d)", file, line, col))
}

func (e *explorer) onComplete(a *nvim.CommandCompletionArgs, eval *struct {
	Env   context.Env
	Cwd   string `eval:"getcwd()"`
	Bufnr int    `eval:"bufnr('%')"`
}) ([]string, error) {

	ctx := context.Get(&eval.Env)

	f := strings.Fields(a.CmdLine)
	var completions []string
	if len(f) >= 3 || (len(f) == 2 && a.ArgLead == "") {
		spec, err := e.expandSpec(f[1])
		if err != nil {
			return nil, err
		}
		completions = completeSymMethodArg(&ctx.Build, resolvePackageSpec(&ctx.Build, eval.Cwd, nvim.NewBufferReader(e.nvim, nvim.Buffer(eval.Bufnr)), spec), a.ArgLead)
	} else {
		completions = completePackageArg(&ctx.Build, eval.Cwd, nvim.NewBufferReader(e.nvim, nvim.Buffer(eval.Bufnr)), a.ArgLead)
	}
	return completions, nil
}

func (e *explorer) onBufReadCmd(eval *struct {
	Env   context.Env
	Cwd   string `eval:"getcwd()"`
	Name  string `eval:"expand('%')"`
	Bufnr int    `eval:"bufnr('%')"`
}) error {

	ctx := context.Get(&eval.Env)
	d, err := printDoc(&ctx.Build, eval.Name, eval.Cwd)
	if err != nil {
		d := doc.NewDoc()
		d.WriteString(err.Error())
	}
	return e.docm.Display(d, nvim.Buffer(eval.Bufnr))
	/*
		p.Command("nnoremap <buffer> <silent> g? :<C-U>help :Godoc<CR>")
		p.Command(`nnoremap <buffer> <silent> ]] :<C-U>call search('\C\v^[^ \t)}]', 'W')<CR>`)
		p.Command(`nnoremap <buffer> <silent> [[ :<C-U>call search('\C\v^[^ \t)}]', 'Wb')<CR>`)
	*/
}
