// Copyright 2015 Gary Burd. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package doc

import (
	"os"
	"testing"
)

func TestDoc(t *testing.T) {
	cwd, _ := os.Getwd()
	_, _, err := print("x://net/http", cwd)
	if err != nil {
		t.Fatal(err)
	}
}
