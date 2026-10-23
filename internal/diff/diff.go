// Package diff prints a unified diff of two small texts, for
// `berthlight devcontainer --dry-run`.
package diff

import (
	"fmt"
	"strings"
)

// context is the number of unchanged lines shown around a change.
const context = 3

type op struct {
	kind   byte // ' ', '-' or '+'
	line   string
	ai, bi int // line index in a and b where the op applies
}

// Unified returns a unified diff from a to b, or "" when they are equal.
// It compares whole lines with a longest-common-subsequence table, which
// is fine for files of a few hundred lines.
func Unified(aName, bName, a, b string) string {
	if a == b {
		return ""
	}
	al, bl := lines(a), lines(b)
	n, m := len(al), len(bl)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if al[i] == bl[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var ops []op
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && al[i] == bl[j]:
			ops = append(ops, op{' ', al[i], i, j})
			i++
			j++
		case j >= m || (i < n && lcs[i+1][j] >= lcs[i][j+1]):
			ops = append(ops, op{'-', al[i], i, j})
			i++
		default:
			ops = append(ops, op{'+', bl[j], i, j})
			j++
		}
	}

	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", aName, bName)
	for k := 0; k < len(ops); {
		if ops[k].kind == ' ' {
			k++
			continue
		}
		start := max(k-context, 0)
		end := k
		// Extend the hunk while the next change is close enough.
		for end < len(ops) {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			next := end
			for next < len(ops) && ops[next].kind == ' ' {
				next++
			}
			if next == len(ops) || next-end > 2*context {
				end = min(end+context, len(ops))
				break
			}
			end = next
		}
		writeHunk(&out, ops[start:end])
		k = end
	}
	return out.String()
}

func writeHunk(out *strings.Builder, ops []op) {
	var na, nb int
	for _, o := range ops {
		if o.kind != '+' {
			na++
		}
		if o.kind != '-' {
			nb++
		}
	}
	as, bs := ops[0].ai+1, ops[0].bi+1
	if na == 0 {
		as--
	}
	if nb == 0 {
		bs--
	}
	fmt.Fprintf(out, "@@ -%d,%d +%d,%d @@\n", as, na, bs, nb)
	for _, o := range ops {
		out.WriteByte(o.kind)
		out.WriteString(o.line)
		out.WriteByte('\n')
	}
}

func lines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
