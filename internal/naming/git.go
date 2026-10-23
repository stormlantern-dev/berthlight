package naming

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Worktree describes where a directory sits in a git repository.
type Worktree struct {
	// MainDir is the main worktree's directory: the one that holds .git.
	MainDir string
	// Dir is the worktree that contains the directory asked about.
	Dir string
}

// IsMain reports whether Dir is the main worktree.
func (w Worktree) IsMain() bool { return filepath.Clean(w.MainDir) == filepath.Clean(w.Dir) }

// FindWorktree asks git which worktree dir belongs to, and where the main
// worktree is.
func FindWorktree(dir string) (Worktree, error) {
	out, err := git(dir, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir")
	if err != nil {
		return Worktree{}, err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		return Worktree{}, fmt.Errorf("unexpected output from git rev-parse: %q", out)
	}
	top := filepath.Clean(filepath.FromSlash(lines[0]))
	common := filepath.Clean(filepath.FromSlash(lines[1]))
	if filepath.Base(common) != ".git" {
		return Worktree{}, fmt.Errorf("%s: bare repositories are not supported", common)
	}
	return Worktree{MainDir: filepath.Dir(common), Dir: top}, nil
}

// LabelFor returns the label for the worktree containing dir. A non-empty
// project overrides the main worktree's directory name.
func LabelFor(dir, project string) (string, error) {
	wt, err := FindWorktree(dir)
	if err != nil {
		return "", err
	}
	mainName := filepath.Base(wt.MainDir)
	if project == "" {
		project = mainName
	}
	worktree := ""
	if !wt.IsMain() {
		worktree = StripProject(filepath.Base(wt.Dir), mainName)
	}
	return Label(project, worktree)
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}
