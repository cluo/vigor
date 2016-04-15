// Copyright 2015 Gary Burd. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package explore

import (
	"os"
	"testing"

	"vigor/context"
)

var docTests = []string{
	bufNamePrefix + "net/http",
}

func TestDoc(t *testing.T) {
	ctx := context.Get(&context.Env{})
	cwd, _ := os.Getwd()
	for _, tt := range docTests {
		_, err := printDoc(&ctx.Build, tt, cwd)
		if err != nil {
			t.Error(tt, err)
		}
	}
}
