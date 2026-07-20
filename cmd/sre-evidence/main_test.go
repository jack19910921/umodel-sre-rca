package main

import (
	"bytes"
	"testing"
)

func TestCLIRejectsNonFixedCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"sql", "select *"}, &out, &errOut, nil); code != 2 {
		t.Fatalf("code=%d stderr=%s", code, errOut.String())
	}
}
