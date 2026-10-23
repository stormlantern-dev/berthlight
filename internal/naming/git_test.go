package naming

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestLabelForWorktrees(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	main := filepath.Join(root, "webshop")
	if err := os.Mkdir(main, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, main, "init", "-q", "-b", "main")
	run(t, main, "commit", "-q", "--allow-empty", "-m", "init")
	run(t, main, "worktree", "add", "-q", "-b", "fix/login", filepath.Join(root, "webshop-fix-login"))
	run(t, main, "worktree", "add", "-q", "-b", "other", filepath.Join(root, "Experiment_2"))

	sub := filepath.Join(main, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name, dir, project, want string
	}{
		{"main worktree", main, "", "webshop"},
		{"subdirectory of main", sub, "", "webshop"},
		{"main with project override", main, "shop", "shop"},
		{"worktree with project prefix", filepath.Join(root, "webshop-fix-login"), "", "webshop-fix-login"},
		{"worktree with override", filepath.Join(root, "webshop-fix-login"), "shop", "shop-fix-login"},
		{"worktree without prefix", filepath.Join(root, "Experiment_2"), "shop", "shop-experiment-2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := LabelFor(tt.dir, tt.project)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("LabelFor(%s, %q) = %q, want %q", tt.dir, tt.project, got, tt.want)
			}
		})
	}

	t.Run("branch switch keeps the name", func(t *testing.T) {
		wt := filepath.Join(root, "webshop-fix-login")
		run(t, wt, "switch", "-q", "-c", "something/else")
		got, err := LabelFor(wt, "shop")
		if err != nil {
			t.Fatal(err)
		}
		if got != "shop-fix-login" {
			t.Errorf("after switching branch: %q, want shop-fix-login", got)
		}
	})

	t.Run("outside a repository", func(t *testing.T) {
		if _, err := LabelFor(t.TempDir(), ""); err == nil {
			t.Error("want an error outside a git repository")
		}
	})
}
