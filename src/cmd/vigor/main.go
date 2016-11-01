// Copyright 2015 Gary Burd. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command vigor is a Neovim remote plogin.
package main

import (
	"github.com/garyburd/vigor/src/explore"
	"github.com/garyburd/vigor/src/format"

	"github.com/neovim/go-client/nvim/plugin"
)

func main() {
	plugin.Main(func(p *plugin.Plugin) error {
		explore.Register(p)
		format.Register(p)
		return nil
	})
}
