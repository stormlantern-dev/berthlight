package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"stormlantern.nl/berthlight/internal/devcontainer"
	"stormlantern.nl/berthlight/internal/diff"
	"stormlantern.nl/berthlight/internal/naming"
	"stormlantern.nl/berthlight/internal/tui"
)

func cmdDevcontainer(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("devcontainer", stderr)
	file := fs.String("file", "", "devcontainer.json to edit (default: .devcontainer/devcontainer.json or .devcontainer.json)")
	dir := fs.String("C", ".", "project directory")
	dryRun := fs.Bool("dry-run", false, "print the changes as a diff instead of writing them")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return errUsage
	}
	if len(pos) != 2 {
		fmt.Fprintln(stderr, "usage: berthlight devcontainer <project> <port> [--file devcontainer.json] [--dry-run]")
		return errUsage
	}
	project := pos[0]
	port, err := strconv.Atoi(pos[1])
	if err != nil {
		return fmt.Errorf("port %q is not a number", pos[1])
	}
	path := *file
	if path == "" {
		if path, err = findDevcontainer(*dir); err != nil {
			return err
		}
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, err := devcontainer.Edit(src, project, port)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if *dryRun {
		d := diff.Unified("a/"+filepath.ToSlash(path), "b/"+filepath.ToSlash(path), string(src), string(out))
		if d == "" {
			fmt.Fprintf(stderr, "%s is already wired to berthlight\n", path)
		}
		fmt.Fprint(stdout, d)
		return nil
	}
	if string(out) == string(src) {
		fmt.Fprintf(stdout, "%s is already wired to berthlight\n", path)
	} else {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, out, info.Mode().Perm()); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "updated %s\n", path)
	}
	fmt.Fprintf(stdout, "Rebuild the container. The dev server must listen on 0.0.0.0:%d inside it.\n", port)
	c, cerr := LoadConfig()
	if label, lerr := naming.LabelFor(*dir, project); cerr == nil && lerr == nil {
		u := urlFor(c, label)
		fmt.Fprintf(stdout, "This worktree's URL: %s\n", tui.NewTerm(stdout).Link(u, u))
	}
	return nil
}

// findDevcontainer finds the project's devcontainer.json the way the
// devcontainer CLI does: .devcontainer/devcontainer.json, then
// .devcontainer.json, then one .devcontainer/<name>/devcontainer.json.
func findDevcontainer(dir string) (string, error) {
	for _, p := range []string{
		filepath.Join(dir, ".devcontainer", "devcontainer.json"),
		filepath.Join(dir, ".devcontainer.json"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
	}
	found, _ := filepath.Glob(filepath.Join(dir, ".devcontainer", "*", "devcontainer.json"))
	switch len(found) {
	case 0:
		return "", fmt.Errorf("no devcontainer.json in %s; pass --file", dir)
	case 1:
		return found[0], nil
	}
	return "", fmt.Errorf("several devcontainer.json files; pick one with --file:\n  %s", strings.Join(found, "\n  "))
}
