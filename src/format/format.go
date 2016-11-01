// Copyright 2015 Gary Burd. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package format implements the :Fmt command.
package format

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/garyburd/vigor/src/context"

	"github.com/neovim/go-client/nvim"
	"github.com/neovim/go-client/nvim/plugin"
)

func Register(p *plugin.Plugin) {
	p.HandleCommand(&plugin.CommandOptions{Name: "Fmt", Range: "%", Eval: "*"}, format)
}

var errorPat = regexp.MustCompile(`^([^:]+):(\d+)(?::(\d+))?(.*)`)

func format(v *nvim.Nvim, r [2]int, eval *struct {
	Env   context.Env
	Bufnr int `eval:"bufnr('%')"`
}) error {
	var (
		in    [][]byte
		fname string
	)
	buf := nvim.Buffer(eval.Bufnr)

	b := v.NewBatch()
	b.BufferLines(buf, 0, -1, true, &in)
	b.BufferName(buf, &fname)
	if err := b.Execute(); err != nil {
		return nil
	}

	var stdout, stderr bytes.Buffer
	c := exec.Command("goimports", "-srcdir", filepath.Dir(fname))
	c.Stdin = bytes.NewReader(bytes.Join(in, []byte{'\n'}))
	c.Stdout = &stdout
	c.Stderr = &stderr
	c.Env = context.Get(&eval.Env).Environ
	err := c.Run()
	if err == nil {
		out := bytes.Split(bytes.TrimSuffix(stdout.Bytes(), []byte{'\n'}), []byte{'\n'})
		return minUpdate(v, buf, in, out)
	}
	if _, ok := err.(*exec.ExitError); ok {
		var qfl []*nvim.QuickfixError
		for _, m := range errorPat.FindAllSubmatch(stderr.Bytes(), -1) {
			qfe := nvim.QuickfixError{}
			qfe.LNum, _ = strconv.Atoi(string(m[2]))
			qfe.Col, _ = strconv.Atoi(string(m[3]))
			qfe.Text = string(bytes.TrimSpace(m[4]))
			qfe.Bufnr = eval.Bufnr
			qfl = append(qfl, &qfe)
		}
		if len(qfl) > 0 {
			b := v.NewBatch()
			b.Call("setqflist", nil, qfl)
			b.Command("cc")
			return b.Execute()
		}
	}
	return err
}

func minUpdate(v *nvim.Nvim, b nvim.Buffer, in [][]byte, out [][]byte) error {

	// Find matching head lines.

	n := len(out)
	if len(in) < len(out) {
		n = len(in)
	}
	head := 0
	for ; head < n; head++ {
		if !bytes.Equal(in[head], out[head]) {
			break
		}
	}

	// Nothing to do?

	if head == len(in) && head == len(out) {
		return nil
	}

	// Find matching tail lines.

	n -= head
	tail := 0
	for ; tail < n; tail++ {
		if !bytes.Equal(in[len(in)-tail-1], out[len(out)-tail-1]) {
			break
		}
	}

	// Update the buffer.

	start := head
	end := len(in) - tail
	repl := out[head : len(out)-tail]
	return v.SetBufferLines(b, start, end, true, repl)
}
