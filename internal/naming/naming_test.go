package naming

import (
	"errors"
	"strings"
	"testing"
)

func TestLabel(t *testing.T) {
	long := strings.Repeat("a", 70)
	tests := []struct {
		name, project, worktree, want string
	}{
		{"main worktree", "shop", "", "shop"},
		{"other worktree", "shop", "fix-login", "shop-fix-login"},
		{"uppercase", "Shop", "Fix-Login", "shop-fix-login"},
		{"slashes from a branch-like name", "shop", "feature/login", "shop-feature-login"},
		{"underscores", "my_shop", "fix_login", "my-shop-fix-login"},
		{"runs of invalid characters", "shop", "fix -- login!!", "shop-fix-login"},
		{"leading and trailing junk", "-shop-", "_fix_", "shop-fix"},
		{"non-ASCII", "café", "zoë", "caf-zo"},
		{"double hyphen collapses", "ab--cd", "", "ab-cd"},
		{"worktree may start with a digit", "shop", "2fa", "shop-2fa"},
		{"exactly 63", strings.Repeat("a", 63), "", strings.Repeat("a", 63)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Label(tt.project, tt.worktree)
			if err != nil {
				t.Fatalf("Label(%q, %q): %v", tt.project, tt.worktree, err)
			}
			if got != tt.want {
				t.Errorf("Label(%q, %q) = %q, want %q", tt.project, tt.worktree, got, tt.want)
			}
			checkValid(t, got)
		})
	}

	t.Run("long labels are cut and hashed", func(t *testing.T) {
		a, err := Label(long, "one")
		if err != nil {
			t.Fatal(err)
		}
		b, err := Label(long, "two")
		if err != nil {
			t.Fatal(err)
		}
		if a == b {
			t.Errorf("two long names collide: %q", a)
		}
		for _, l := range []string{a, b} {
			if len(l) != MaxLabel {
				t.Errorf("len(%q) = %d, want %d", l, len(l), MaxLabel)
			}
			checkValid(t, l)
		}
	})

	t.Run("cut does not leave a double hyphen", func(t *testing.T) {
		// Put a hyphen exactly where the cut falls.
		p := strings.Repeat("a", MaxLabel-hashLen-2) + "-" + strings.Repeat("b", 20)
		got, err := Label(p, "")
		if err != nil {
			t.Fatal(err)
		}
		checkValid(t, got)
	})
}

func TestLabelErrors(t *testing.T) {
	tests := []struct{ name, project, worktree string }{
		{"empty project", "", ""},
		{"project of junk only", "__", ""},
		{"project starting with a digit", "2shop", ""},
		{"worktree of junk only", "shop", "///"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := Label(tt.project, tt.worktree); err == nil {
				t.Errorf("Label(%q, %q) = %q, want an error", tt.project, tt.worktree, got)
			}
		})
	}
	if _, err := Label("", ""); !errors.Is(err, ErrEmpty) {
		t.Errorf("empty project: error %v, want ErrEmpty", err)
	}
}

func TestStripProject(t *testing.T) {
	tests := []struct{ dir, project, want string }{
		{"webshop-fix-login", "webshop", "fix-login"},
		{"webshop.fix", "webshop", "fix"},
		{"webshop_fix", "webshop", "fix"},
		{"fix-login", "webshop", "fix-login"},
		{"webshop-", "webshop", "webshop-"},
		{"webshopper", "webshop", "webshopper"},
	}
	for _, tt := range tests {
		if got := StripProject(tt.dir, tt.project); got != tt.want {
			t.Errorf("StripProject(%q, %q) = %q, want %q", tt.dir, tt.project, got, tt.want)
		}
	}
}

func TestHost(t *testing.T) {
	tests := []struct{ label, base, want string }{
		{"shop", "", "shop.localhost"},
		{"shop", "dev.example.com", "shop.dev.example.com"},
		{"shop", ".Dev.Example.com.", "shop.dev.example.com"},
	}
	for _, tt := range tests {
		if got := Host(tt.label, tt.base); got != tt.want {
			t.Errorf("Host(%q, %q) = %q, want %q", tt.label, tt.base, got, tt.want)
		}
	}
}

// checkValid asserts the label rules from PRD 0001, R4.
func checkValid(t *testing.T, l string) {
	t.Helper()
	if len(l) < 1 || len(l) > MaxLabel {
		t.Errorf("%q: length %d outside 1..%d", l, len(l), MaxLabel)
	}
	for _, r := range l {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' {
			t.Errorf("%q: invalid character %q", l, r)
		}
	}
	if l[0] < 'a' || l[0] > 'z' {
		t.Errorf("%q: does not start with a letter", l)
	}
	if strings.HasSuffix(l, "-") {
		t.Errorf("%q: ends with a hyphen", l)
	}
	if len(l) >= 4 && l[2:4] == "--" {
		t.Errorf("%q: -- in position 3-4", l)
	}
	if strings.Contains(l, "--") {
		t.Errorf("%q: contains --", l)
	}
}
