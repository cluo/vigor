// Copyright 2015 Gary Burd. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package doc

import (
	"os"
	"testing"
)

var docTests = []string{
	protoSlashSlash + "net/http",
	protoSlashSlash + "net/http#Request",
	protoSlashSlash + "net/http#Request.Cookies",
}

func TestDoc(t *testing.T) {
	cwd, _ := os.Getwd()
	for _, tt := range docTests {
		_, _, err := print(tt, cwd)
		if err != nil {
			t.Error(tt, err)
		}
	}
}
