// Copyright 2015 Gary Burd. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gofmt implelments the :GoFmt command.
package gofmt

import (
	"bytes"
	"go/format"

	"github.com/garyburd/neovim-go/vim"
)

func init() {
	vim.RegisterPluginSetup(func(v *vim.Vim) error {
		return v.RegisterCommand("GoFmt", nil, gofmt)
	})
}

func gofmt(v *vim.Vim) error {
	b, err := v.CurrentBuffer()
	if err != nil {
		return err
	}

	in, err := v.BufferLineSlice(b, 0, -1, true, true)
	if err != nil {
		return err
	}

	buf, err := format.Source(bytes.Join(in, []byte{'\n'}))
	if err != nil {
		return err
	}

	if len(buf) > 0 && buf[len(buf)-1] == '\n' {
		buf = buf[:len(buf)-1]
	}

	return minUpdate(v, b, in, bytes.Split(buf, []byte{'\n'}))
}

func minUpdate(v *vim.Vim, b vim.Buffer, in [][]byte, out [][]byte) error {

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

	if head == len(in) {
		if len(in) == len(out) {
			return nil
		}
		return v.InsertBufferLines(b, -1, out[head:])
	}

	// Find matching tail lines.

	n -= head
	tail := 0
	for ; tail < n; tail++ {
		if !bytes.Equal(in[len(in)-tail-1], out[len(out)-tail-1]) {
			break
		}
	}

	return v.SetBufferLineSlice(b, head, len(in)-tail, true, false, out[head:len(out)-tail])
}
