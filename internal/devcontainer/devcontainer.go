// Package devcontainer wires a devcontainer.json to berthlight (PRD 0001,
// R5): the container joins its route's network under an alias, an
// initializeCommand creates the network and route on the host before the
// container starts, and the appPort entries for the dev server's port go.
//
// devcontainer.json is shared by every worktree of a project, but each
// worktree has its own route name and network. devcontainer.json can only
// name the folder (${localWorkspaceFolderBasename}); it cannot derive the
// route name from it. So the network is bl-<folder name>, set in runArgs,
// and the initializeCommand, which runs berthlight on the host, derives the
// route name and records that network with the route.
//
// The container's alias is the network name too. The proxy is on every
// route's network and resolves aliases through all of them, so an alias
// must be unique across routes; a fixed one such as the project name would
// make two worktrees answer for each other.
//
// The file is JSON with comments. Edits work on its syntax tree, so
// comments, trailing commas and indentation outside the edited values stay
// as they are.
package devcontainer

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/tailscale/hujson"
	"stormlantern.nl/berthlight/internal/naming"
)

// Network is the runArgs network: one per workspace folder, so per worktree.
const Network = "bl-${localWorkspaceFolderBasename}"

// CommandKey names berthlight's command when initializeCommand is an object
// of commands, which devcontainer runs in parallel.
const CommandKey = "berthlight"

// InitCommand is the initializeCommand that makes the route on the host.
func InitCommand(project string, port int) []string {
	return []string{
		"berthlight", "add",
		"--project", project,
		"--target", Network,
		"--network", Network,
		"-C", "${localWorkspaceFolder}",
		strconv.Itoa(port),
	}
}

// RunArgs are the arguments that put the container on its route network.
var RunArgs = []string{"--network=" + Network, "--network-alias=" + Network}

// Edit returns src with the berthlight wiring for project and port. Running
// it on its own output changes nothing.
func Edit(src []byte, project string, port int) ([]byte, error) {
	if err := checkProject(project); err != nil {
		return nil, err
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("port %d out of range", port)
	}
	v, err := hujson.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("parsing devcontainer.json: %w", err)
	}
	obj, ok := v.Value.(*hujson.Object)
	if !ok {
		return nil, errors.New("devcontainer.json is not a JSON object")
	}
	e := &editor{obj: obj, nl: "\n"}
	if bytes.Contains(src, []byte("\r\n")) {
		e.nl = "\r\n"
	}
	e.indent = memberIndent(obj, "  ")
	if e.find(e.obj, "dockerComposeFile") >= 0 {
		return nil, errors.New("this devcontainer uses Docker Compose, which berthlight devcontainer does not handle yet; join the service to the network in the compose file instead (berthlight add prints the network and alias)")
	}
	if err := e.runArgs(); err != nil {
		return nil, err
	}
	if err := e.initializeCommand(InitCommand(project, port)); err != nil {
		return nil, err
	}
	if err := e.appPort(port); err != nil {
		return nil, err
	}
	return v.Pack(), nil
}

func checkProject(p string) error {
	if p == "" || naming.Sanitize(p) != p || p[0] < 'a' || p[0] > 'z' || len(p) > naming.MaxLabel {
		return fmt.Errorf("project %q is not a valid name: lowercase letters, digits and single hyphens, starting with a letter", p)
	}
	return nil
}

type editor struct {
	obj    *hujson.Object
	nl     string // line ending
	indent string // indentation of a top-level member
}

func (e *editor) find(obj *hujson.Object, name string) int {
	for i, m := range obj.Members {
		if lit, ok := m.Name.Value.(hujson.Literal); ok && lit.String() == name {
			return i
		}
	}
	return -1
}

// runArgs sets the network and alias arguments, in place where they exist.
func (e *editor) runArgs() error {
	want := RunArgs
	i := e.find(e.obj, "runArgs")
	if i < 0 {
		e.appendMember(e.obj, "runArgs", stringArray(want), e.indent)
		return nil
	}
	arr, ok := e.obj.Members[i].Value.Value.(*hujson.Array)
	if !ok {
		return errors.New("runArgs is not an array")
	}
	set := [2]bool{}
	var drop []int
	for j := 0; j < len(arr.Elements); j++ {
		s, ok := stringValue(arr.Elements[j])
		if !ok {
			continue
		}
		kind, val, joined := classifyArg(s)
		if kind < 0 {
			continue
		}
		if !joined && j+1 < len(arr.Elements) {
			val, _ = stringValue(arr.Elements[j+1])
		}
		if kind == argNetwork && !strings.HasPrefix(val, "bl-") {
			return fmt.Errorf("runArgs puts the container on network %q; berthlight needs it on its own network. Remove that argument and run again", val)
		}
		if joined && !set[kind] {
			arr.Elements[j].Value = hujson.String(want[kind])
			set[kind] = true
			continue
		}
		drop = append(drop, j)
		if !joined && j+1 < len(arr.Elements) {
			j++
			drop = append(drop, j)
		}
	}
	for k := len(drop) - 1; k >= 0; k-- {
		removeItem(arrayList{arr}, drop[k])
	}
	for kind, ok := range set {
		if !ok {
			e.appendElement(arr, hujson.String(want[kind]))
		}
	}
	return nil
}

const (
	argNetwork = 0
	argAlias   = 1
)

// classifyArg tells a network or alias argument from others. joined is
// false for the two-argument form, where the value is the next argument.
func classifyArg(s string) (kind int, val string, joined bool) {
	for _, f := range []struct {
		flag string
		kind int
	}{{"--network-alias", argAlias}, {"--network", argNetwork}, {"--net", argNetwork}} {
		if s == f.flag {
			return f.kind, "", false
		}
		if v, ok := strings.CutPrefix(s, f.flag+"="); ok {
			return f.kind, v, true
		}
	}
	return -1, "", false
}

// initializeCommand adds berthlight's command next to any existing one.
func (e *editor) initializeCommand(cmd []string) error {
	ours := stringArray(cmd)
	i := e.find(e.obj, "initializeCommand")
	if i < 0 {
		e.appendMember(e.obj, "initializeCommand", ours, e.indent)
		return nil
	}
	v := &e.obj.Members[i].Value
	switch x := v.Value.(type) {
	case hujson.Literal:
		if x.Kind() != '"' {
			return errors.New("initializeCommand is not a string, array or object")
		}
		if isOurs(strings.Fields(x.String())) {
			v.Value = ours
			return nil
		}
		v.Value = e.newObject(x, ours)
	case *hujson.Array:
		var words []string
		for _, el := range x.Elements {
			s, _ := stringValue(el)
			words = append(words, s)
		}
		if isOurs(words) {
			v.Value = ours
			return nil
		}
		v.Value = e.newObject(x, ours)
	case *hujson.Object:
		if j := e.find(x, CommandKey); j >= 0 {
			x.Members[j].Value.Value = ours
			return nil
		}
		e.appendMember(x, CommandKey, ours, memberIndent(x, e.indent+e.indent))
	}
	return nil
}

func isOurs(words []string) bool {
	return len(words) >= 2 && words[0] == "berthlight" && words[1] == "add"
}

// newObject turns an existing initializeCommand into the object form, so
// the project's command and berthlight's both run.
func (e *editor) newObject(existing, ours hujson.ValueTrimmed) *hujson.Object {
	in := e.nl + e.indent + e.indent
	member := func(name string, v hujson.ValueTrimmed) hujson.ObjectMember {
		return hujson.ObjectMember{
			Name:  hujson.Value{BeforeExtra: hujson.Extra(in), Value: hujson.String(name)},
			Value: hujson.Value{BeforeExtra: hujson.Extra(" "), Value: v},
		}
	}
	return &hujson.Object{
		Members:    []hujson.ObjectMember{member("init", existing), member(CommandKey, ours)},
		AfterExtra: hujson.Extra(e.nl + e.indent),
	}
}

// appPort removes published ports whose container port is port.
func (e *editor) appPort(port int) error {
	i := e.find(e.obj, "appPort")
	if i < 0 {
		return nil
	}
	switch x := e.obj.Members[i].Value.Value.(type) {
	case hujson.Literal:
		if publishes(x, port) {
			removeItem(objectList{e.obj}, i)
		}
	case *hujson.Array:
		for j := len(x.Elements) - 1; j >= 0; j-- {
			if lit, ok := x.Elements[j].Value.(hujson.Literal); ok && publishes(lit, port) {
				removeItem(arrayList{x}, j)
			}
		}
		if len(x.Elements) == 0 {
			removeItem(objectList{e.obj}, i)
		}
	default:
		return errors.New("appPort is not a number, string or array")
	}
	return nil
}

// publishes reports whether an appPort entry (8080, "8080",
// "127.0.0.1:3000:8080", "8080/tcp") maps the container's port. A range
// ("8080-8089", "127.0.0.1:8080-8089:8080-8089") that holds the port
// counts too: such a range is how projects ran several worktrees side by
// side, which the berthlight route now does, so the whole entry goes.
func publishes(lit hujson.Literal, port int) bool {
	switch lit.Kind() {
	case '0':
		return lit.Int() == int64(port)
	case '"':
		s := lit.String()
		if p, ok := strings.CutSuffix(s, "/tcp"); ok {
			s = p
		} else if strings.Contains(s, "/") {
			return false
		}
		if k := strings.LastIndexByte(s, ':'); k >= 0 {
			s = s[k+1:]
		}
		if lo, hi, ok := strings.Cut(s, "-"); ok {
			l, err1 := strconv.Atoi(lo)
			h, err2 := strconv.Atoi(hi)
			return err1 == nil && err2 == nil && l <= port && port <= h
		}
		n, err := strconv.Atoi(s)
		return err == nil && n == port
	}
	return false
}

func stringValue(v hujson.Value) (string, bool) {
	lit, ok := v.Value.(hujson.Literal)
	if !ok || lit.Kind() != '"' {
		return "", false
	}
	return lit.String(), true
}

// stringArray is a one-line array of strings.
func stringArray(ss []string) *hujson.Array {
	arr := &hujson.Array{}
	for i, s := range ss {
		el := hujson.Value{Value: hujson.String(s)}
		if i > 0 {
			el.BeforeExtra = hujson.Extra(" ")
		}
		arr.Elements = append(arr.Elements, el)
	}
	return arr
}

// memberIndent is the indentation of an object's members, or def.
func memberIndent(obj *hujson.Object, def string) string {
	for _, m := range obj.Members {
		b := string(m.Name.BeforeExtra)
		if k := strings.LastIndexByte(b, '\n'); k >= 0 && strings.TrimSpace(b[k+1:]) == "" {
			return b[k+1:]
		}
	}
	return def
}

// appendMember adds a member at the end of obj, on its own line.
func (e *editor) appendMember(obj *hujson.Object, name string, v hujson.ValueTrimmed, indent string) {
	m := hujson.ObjectMember{
		Name:  hujson.Value{Value: hujson.String(name)},
		Value: hujson.Value{BeforeExtra: hujson.Extra(" "), Value: v},
	}
	before := e.nl + indent
	if n := len(obj.Members); n > 0 {
		if obj.Members[n-1].Value.AfterExtra != nil {
			m.Value.AfterExtra = hujson.Extra{} // keep the trailing comma
		}
		// A comment on the last member's line stays on that line.
		same, rest := splitSameLine(obj.AfterExtra)
		before = same + before
		obj.AfterExtra = hujson.Extra(rest)
	}
	if !strings.Contains(string(obj.AfterExtra), "\n") {
		obj.AfterExtra = hujson.Extra(e.nl + strings.TrimSuffix(indent, e.indent))
	}
	m.Name.BeforeExtra = hujson.Extra(before)
	obj.Members = append(obj.Members, m)
}

// appendElement adds an element at the end of arr, in the array's style:
// on its own line if the elements are, else after a space.
func (e *editor) appendElement(arr *hujson.Array, v hujson.ValueTrimmed) {
	el := hujson.Value{Value: v}
	n := len(arr.Elements)
	if n == 0 {
		arr.Elements = append(arr.Elements, el)
		return
	}
	last := arr.Elements[n-1]
	b := string(last.BeforeExtra)
	if k := strings.LastIndexByte(b, '\n'); k >= 0 {
		same, rest := splitSameLine(arr.AfterExtra)
		el.BeforeExtra = hujson.Extra(same + e.nl + b[k+1:])
		arr.AfterExtra = hujson.Extra(rest)
	} else {
		el.BeforeExtra = hujson.Extra(" ")
	}
	if last.AfterExtra != nil {
		el.AfterExtra = hujson.Extra{}
	}
	arr.Elements = append(arr.Elements, el)
}

// splitSameLine splits extra at its first line break into a comment that
// ends the previous line, and the rest. Without such a comment, same is
// empty and rest is all of extra.
func splitSameLine(extra hujson.Extra) (same, rest string) {
	s := string(extra)
	k := strings.IndexByte(s, '\n')
	if k < 0 {
		return "", s
	}
	if k > 0 && s[k-1] == '\r' {
		k--
	}
	if strings.TrimSpace(s[:k]) == "" {
		return "", s
	}
	return strings.TrimRight(s[:k], " \t"), s[k:]
}

// list is an object's members or an array's elements, for removeItem.
type list interface {
	len() int
	before(i int) *hujson.Extra // extra after the preceding comma or bracket
	after(i int) *hujson.Extra  // extra before the following comma
	closing() *hujson.Extra     // extra before the closing bracket
	remove(i int)
}

type objectList struct{ o *hujson.Object }

func (l objectList) len() int                   { return len(l.o.Members) }
func (l objectList) before(i int) *hujson.Extra { return &l.o.Members[i].Name.BeforeExtra }
func (l objectList) after(i int) *hujson.Extra  { return &l.o.Members[i].Value.AfterExtra }
func (l objectList) closing() *hujson.Extra     { return &l.o.AfterExtra }
func (l objectList) remove(i int)               { l.o.Members = append(l.o.Members[:i], l.o.Members[i+1:]...) }

type arrayList struct{ a *hujson.Array }

func (l arrayList) len() int                   { return len(l.a.Elements) }
func (l arrayList) before(i int) *hujson.Extra { return &l.a.Elements[i].BeforeExtra }
func (l arrayList) after(i int) *hujson.Extra  { return &l.a.Elements[i].AfterExtra }
func (l arrayList) closing() *hujson.Extra     { return &l.a.AfterExtra }
func (l arrayList) remove(i int)               { l.a.Elements = append(l.a.Elements[:i], l.a.Elements[i+1:]...) }

// removeItem removes item i with the comments on its own lines, keeping a
// comment that ends the previous item's line, and the trailing comma style.
func removeItem(l list, i int) {
	own := *l.before(i)
	keep, _ := splitSameLine(own)
	multiline := strings.Contains(string(own), "\n")
	if i < l.len()-1 {
		next := l.before(i + 1)
		if multiline && strings.Contains(string(*next), "\n") {
			_, rest := splitSameLine(*next)
			*next = hujson.Extra(keep + rest)
		} else if !strings.Contains(string(*next), "\n") {
			*next = append(hujson.Extra{}, own...)
		}
		l.remove(i)
		return
	}
	trailing := *l.after(i) != nil
	if i > 0 {
		prev := l.after(i - 1)
		switch {
		case trailing && *prev == nil:
			*prev = hujson.Extra{}
		case !trailing && *prev != nil:
			*l.closing() = append(append(hujson.Extra{}, *prev...), *l.closing()...)
			*prev = nil
		}
	}
	if cl := l.closing(); multiline && strings.Contains(string(*cl), "\n") {
		_, rest := splitSameLine(*cl)
		*cl = hujson.Extra(keep + rest)
	}
	l.remove(i)
}

// Project returns the project name a wired devcontainer.json gives
// berthlight: the --project of its initializeCommand, in the array form
// Edit writes or inside the object form. It reports false for a file
// berthlight has not wired.
func Project(src []byte) (string, bool) {
	v, err := hujson.Parse(src)
	if err != nil {
		return "", false
	}
	obj, ok := v.Value.(*hujson.Object)
	if !ok {
		return "", false
	}
	e := &editor{obj: obj}
	i := e.find(obj, "initializeCommand")
	if i < 0 {
		return "", false
	}
	cmd := obj.Members[i].Value.Value
	if o, ok := cmd.(*hujson.Object); ok {
		j := e.find(o, CommandKey)
		if j < 0 {
			return "", false
		}
		cmd = o.Members[j].Value.Value
	}
	var words []string
	switch x := cmd.(type) {
	case *hujson.Array:
		for _, el := range x.Elements {
			s, _ := stringValue(el)
			words = append(words, s)
		}
	case hujson.Literal:
		if x.Kind() == '"' {
			words = strings.Fields(x.String())
		}
	}
	if !isOurs(words) {
		return "", false
	}
	for k := 2; k+1 < len(words); k++ {
		if words[k] == "--project" {
			return words[k+1], true
		}
	}
	return "", false
}
