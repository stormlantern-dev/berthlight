package runtime

import "testing"

func TestSameImage(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		same bool
	}{
		{"ghcr.io/stormlantern-dev/berthlight:0.8.2", "ghcr.io/stormlantern-dev/berthlight:0.8.2", true},
		{"ghcr.io/stormlantern-dev/berthlight:0.8.1", "ghcr.io/stormlantern-dev/berthlight:0.8.2", false},
		{"ghcr.io/stormlantern-dev/berthlight:edge", "ghcr.io/stormlantern-dev/berthlight:latest", false},
		{"localhost/berthlight:test", "berthlight:test", true},
		{"docker.io/library/nginx:1", "nginx:1", true},
		{"nginx", "nginx:latest", true},
		{"localhost:5000/berthlight", "localhost:5000/berthlight:latest", true},
		{"localhost:5000/berthlight:1", "localhost:5000/berthlight:2", false},
		{"ghcr.io/x/y@sha256:abc", "ghcr.io/x/y@sha256:abc", true},
	} {
		if got := SameImage(tc.a, tc.b); got != tc.same {
			t.Errorf("SameImage(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.same)
		}
	}
}
