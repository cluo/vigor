// Copyright 2015 Gary Burd. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package context

import (
	"go/build"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Env struct {
	GOROOT string `eval:"$GOROOT"`
	GOPATH string `eval:"$GOPATH"`
	GOOS   string `eval:"$GOOS"`
	GOARCH string `eval:"$GOARCH"`
}

type Context struct {
	// Environ is the current environment in the form "key=value".
	Environ []string

	// Build is a build.Context configured for the current environment. This
	// context ignores modified buffers in Neovim.
	Build build.Context

	env *Env
}

var (
	ctx *Context
	mu  sync.Mutex
)

// Get returns a context for the specified environment.
func Get(env *Env) *Context {
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
