package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		args     []string
		code     int
		inStdout string
		inStderr string
	}{
		{[]string{"version"}, 0, "berthlight ", ""},
		{[]string{"help"}, 0, "Usage:", ""},
		{nil, 2, "", "Usage:"},
		{[]string{"nope"}, 2, "", `unknown command "nope"`},
		{[]string{"url", "-C", t.TempDir()}, 1, "", "git rev-parse"},
	}
	for _, tt := range tests {
		var out, errOut bytes.Buffer
		code := run(tt.args, &out, &errOut)
		if code != tt.code {
			t.Errorf("run(%q) = %d, want %d (stderr: %s)", tt.args, code, tt.code, errOut.String())
		}
		if !strings.Contains(out.String(), tt.inStdout) {
			t.Errorf("run(%q) stdout %q, want it to contain %q", tt.args, out.String(), tt.inStdout)
		}
		if !strings.Contains(errOut.String(), tt.inStderr) {
			t.Errorf("run(%q) stderr %q, want it to contain %q", tt.args, errOut.String(), tt.inStderr)
		}
	}
}
