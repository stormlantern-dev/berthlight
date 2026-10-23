// Package naming turns a project and a git worktree into a route name,
// following the naming convention in PRD 0001 (R4).
//
// A full name is <label>.<base>. The label is one DNS label: <project> for a
// project's main worktree, <project>-<worktree> for any other. One label
// keeps every name under a single wildcard certificate.
package naming

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// DefaultBase is the base domain when the user configures none. Browsers and
// systemd-resolved send *.localhost to loopback without any DNS change.
const DefaultBase = "localhost"

// MaxLabel is the longest DNS label (RFC 1035).
const MaxLabel = 63

// hashLen is the number of hex characters appended to a label that was cut.
const hashLen = 4

// ErrEmpty is returned when nothing valid is left after sanitising.
var ErrEmpty = errors.New("name has no letters or digits")

// Sanitize lowercases s and turns every run of characters other than ASCII
// letters and digits into one hyphen, without hyphens at either end. It does
// not enforce the length limit; Label does.
func Sanitize(s string) string {
	var b strings.Builder
	hyphen := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if hyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			hyphen = false
			b.WriteRune(r)
			continue
		}
		hyphen = true
	}
	return b.String()
}

// Label returns the DNS label for a project and worktree. Pass an empty
// worktree for the project's main worktree.
//
// The project must start with a letter after sanitising. A label longer than
// MaxLabel is cut and given a hyphen and a short hash of the full label, so
// two long names that share a prefix stay distinct.
func Label(project, worktree string) (string, error) {
	p := Sanitize(project)
	if p == "" {
		return "", fmt.Errorf("project %q: %w", project, ErrEmpty)
	}
	if p[0] < 'a' || p[0] > 'z' {
		return "", fmt.Errorf("project %q must start with a letter", project)
	}
	label := p
	if worktree != "" {
		w := Sanitize(worktree)
		if w == "" {
			return "", fmt.Errorf("worktree %q: %w", worktree, ErrEmpty)
		}
		label = p + "-" + w
	}
	return shorten(label), nil
}

func shorten(label string) string {
	if len(label) <= MaxLabel {
		return label
	}
	sum := sha256.Sum256([]byte(label))
	cut := strings.TrimRight(label[:MaxLabel-hashLen-1], "-")
	return cut + "-" + hex.EncodeToString(sum[:])[:hashLen]
}

// StripProject removes the project's directory name from the front of a
// worktree's directory name: "webshop-fix-login" in project directory
// "webshop" gives "fix-login". A directory without that prefix is returned
// unchanged.
func StripProject(worktreeDir, projectDir string) string {
	for _, sep := range []string{"-", ".", "_"} {
		if rest, ok := strings.CutPrefix(worktreeDir, projectDir+sep); ok && rest != "" {
			return rest
		}
	}
	return worktreeDir
}

// Host returns the full host name <label>.<base>. An empty base means
// DefaultBase.
func Host(label, base string) string {
	base = strings.Trim(strings.ToLower(base), ".")
	if base == "" {
		base = DefaultBase
	}
	return label + "." + base
}
