package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"stormlantern.nl/berthlight/internal/route"
	rt "stormlantern.nl/berthlight/internal/runtime"
)

// TestMain lets a test run this test binary as `berthlight mcp`, so the
// stdio server is tested as the separate process an editor starts.
func TestMain(m *testing.M) {
	if os.Getenv("BERTHLIGHT_TEST_RUN_MCP") == "1" {
		os.Exit(Run([]string{"mcp"}, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

// mcpClient connects an in-memory client to a fresh server.
func mcpClient(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := newMCPServer().Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

// structured is a tool result's structured content as plain JSON values.
func structured(t *testing.T, res *mcp.CallToolResult) any {
	t.Helper()
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	json.Unmarshal(b, &v)
	return v
}

func TestMCPToolsAreReadOnly(t *testing.T) {
	cs := mcpClient(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is not marked read-only", tool.Name)
		}
		if tool.OutputSchema == nil || tool.Description == "" {
			t.Errorf("%s lacks an output schema or description", tool.Name)
		}
	}
	if strings.Join(names, " ") != "ls status url" {
		t.Errorf("tools: %v", names)
	}
}

// TestMCPLsMatchesCLI: the ls tool gives what `ls --json` prints.
func TestMCPLsMatchesCLI(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BERTHLIGHT_STATE_DIR", dir)
	tab := &route.Table{}
	tab.Put(route.Route{Name: "shop", Target: "bl-webshop", Port: 8080, NetworkName: "bl-webshop"})
	route.Save(dir, tab)

	var cli bytes.Buffer
	if code := Run([]string{"ls", "--json"}, &cli, &cli); code != 0 {
		t.Fatalf("ls --json: %d %s", code, cli.String())
	}
	var want any
	json.Unmarshal(cli.Bytes(), &want)

	res := callTool(t, mcpClient(t), "ls", nil)
	if res.IsError || !reflect.DeepEqual(structured(t, res), want) {
		t.Errorf("ls tool: %v, want %v", structured(t, res), want)
	}
}

// TestMCPURL: the url tool names the route the wired devcontainer gets,
// from a dir argument or the server's working directory, and says it is not
// registered yet.
func TestMCPURL(t *testing.T) {
	t.Setenv("BERTHLIGHT_STATE_DIR", t.TempDir())
	main := filepath.Join(t.TempDir(), "webshop")
	os.MkdirAll(filepath.Join(main, ".devcontainer"), 0o755)
	os.WriteFile(filepath.Join(main, ".devcontainer", "devcontainer.json"), []byte(`{"image": "x"}`), 0o644)
	if out, err := exec.Command("git", "-C", main, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	var out bytes.Buffer
	if code := Run([]string{"devcontainer", "-C", main, "shop", "8080"}, &out, &out); code != 0 {
		t.Fatalf("devcontainer: %s", out.String())
	}

	check := func(res *mcp.CallToolResult) {
		t.Helper()
		got, _ := structured(t, res).(map[string]any)
		if res.IsError || got["name"] != "shop" || got["url"] != "http://shop.localhost:7480" || got["registered"] != false {
			t.Errorf("url tool: %v", got)
		}
	}
	check(callTool(t, mcpClient(t), "url", map[string]any{"dir": main}))
	t.Chdir(filepath.Join(main, ".devcontainer"))
	check(callTool(t, mcpClient(t), "url", nil))
}

// TestMCPErrorsKeepServing: a bad dir is a tool error with a message,
// and the server answers the next call.
func TestMCPErrorsKeepServing(t *testing.T) {
	t.Setenv("BERTHLIGHT_STATE_DIR", t.TempDir())
	cs := mcpClient(t)
	for _, dir := range []string{filepath.Join(t.TempDir(), "gone"), "/etc/hostname"} {
		res := callTool(t, cs, "url", map[string]any{"dir": dir})
		text := ""
		if len(res.Content) > 0 {
			if tc, ok := res.Content[0].(*mcp.TextContent); ok {
				text = tc.Text
			}
		}
		if !res.IsError || !strings.Contains(text, dir) {
			t.Errorf("url %s: isError %v, %q", dir, res.IsError, text)
		}
	}
	if res := callTool(t, cs, "ls", nil); res.IsError {
		t.Error("server stopped answering after an error")
	}
}

// TestMCPStdio (M1): the server speaks MCP on stdio as its own process,
// prints nothing else on stdout, and exits soon after stdin closes.
func TestMCPStdio(t *testing.T) {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "BERTHLIGHT_TEST_RUN_MCP=1", "BERTHLIGHT_STATE_DIR="+t.TempDir(), "GORACE=atexit_sleep_ms=0")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	for _, msg := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	} {
		io.WriteString(stdin, msg+"\n")
	}
	lines := bufio.NewScanner(stdout)
	lines.Buffer(nil, 1<<20)
	for id := 1; id <= 2; id++ {
		if !lines.Scan() {
			t.Fatalf("no answer to request %d: %v", id, lines.Err())
		}
		var res struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(lines.Bytes(), &res); err != nil || res.ID != id || res.Result == nil {
			t.Fatalf("answer %d is not a JSON-RPC result: %s", id, lines.Text())
		}
		if id == 1 && !strings.Contains(string(res.Result), `"name":"berthlight"`) {
			t.Errorf("initialize: %s", res.Result)
		}
		if id == 2 && strings.Count(string(res.Result), `"readOnlyHint":true`) != 3 {
			t.Errorf("tools/list: %s", res.Result)
		}
	}
	start := time.Now()
	stdin.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("exit: %v", err)
		}
		if d := time.Since(start); d > time.Second {
			t.Errorf("exit took %v after stdin closed", d)
		}
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		t.Fatal("still running 5 s after stdin closed")
	}
}

func TestMCPConfig(t *testing.T) {
	for client, want := range map[string]string{
		"claude": "claude mcp add --scope user berthlight -- /opt/bin/berthlight mcp\n",
		"zed": `// Add to Zed's settings.json (zed: open settings), merging with any
// "context_servers" already there:
{
  "context_servers": {
    "berthlight": {
      "command": "/opt/bin/berthlight",
      "args": [
        "mcp"
      ],
      "env": {}
    }
  }
}
`,
	} {
		var out bytes.Buffer
		if err := writeMCPConfig(&out, client, "/opt/bin/berthlight"); err != nil || out.String() != want {
			t.Errorf("%s: %v\n%s", client, err, out.String())
		}
	}
	if err := writeMCPConfig(&bytes.Buffer{}, "vim", "x"); err == nil {
		t.Error("unknown client accepted")
	}
}

func TestHostPath(t *testing.T) {
	m := func(c, src, dst string, nets ...string) rt.BindMount {
		return rt.BindMount{Container: c, Source: src, Destination: dst, Networks: nets}
	}
	mounts := []rt.BindMount{
		m("shop", "/home/u/webshop", "/workspaces/webshop", "bl-webshop"),
		m("shop", "/home/u/.kube", "/root/.kube"),
		m("old", "/home/u/webshop", "/workspaces/webshop"),
		m("fix", "/home/u/webshop/.worktrees/fix", "/workspaces/.worktrees/fix", "bl-fix"),
		m("fix", "/home/u/webshop/.git", "/workspaces/.git", "bl-fix"),
		m("a", "/home/u/one/app", "/workspaces/app", "bl-app"),
		m("b", "/home/u/two/app", "/workspaces/app", "bl-app-2"),
		m("c", "/home/u/three/site", "/workspaces/site"),
		m("d", "/home/u/four/site", "/workspaces/site", "bl-site"),
		m("root", "/home/u", "/"),
	}
	for dir, want := range map[string]string{
		"/workspaces/webshop":            "/home/u/webshop",
		"/workspaces/webshop/src/":       "/home/u/webshop/src",
		"/workspaces/.worktrees/fix/web": "/home/u/webshop/.worktrees/fix/web",
		"/workspaces/site":               "/home/u/four/site",
	} {
		got, ok, err := hostPath(dir, mounts)
		if err != nil || !ok || got != want {
			t.Errorf("%s: %q %v %v, want %s", dir, got, ok, err, want)
		}
	}
	if _, ok, err := hostPath("/srv/other", mounts); ok || err != nil {
		t.Errorf("unmounted dir: %v %v", ok, err)
	}
	if _, _, err := hostPath("/workspaces/app", mounts); err == nil || !strings.Contains(err.Error(), "/home/u/one/app") {
		t.Errorf("ambiguous dir: %v", err)
	}
	if _, _, err := hostPath("/workspaces/webshopper", mounts); err != nil {
		t.Errorf("prefix that is not a path: %v", err)
	}
}
