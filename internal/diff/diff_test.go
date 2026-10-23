package diff

import "testing"

func TestUnified(t *testing.T) {
	if got := Unified("a", "b", "x\n", "x\n"); got != "" {
		t.Errorf("equal texts: %q", got)
	}
	a := "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n"
	b := "1\n2\nthree\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13\n"
	want := `--- a
+++ b
@@ -1,6 +1,6 @@
 1
 2
-3
+three
 4
 5
 6
@@ -10,3 +10,4 @@
 10
 11
 12
+13
`
	if got := Unified("a", "b", a, b); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	// From nothing.
	want = "--- a\n+++ b\n@@ -0,0 +1,1 @@\n+x\n"
	if got := Unified("a", "b", "", "x\n"); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
