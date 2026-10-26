package devcontainer

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tailscale/hujson"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// TestGolden edits each testdata/*.in.jsonc for project "shop" on port
// 8080 and compares with the .golden.jsonc next to it. Run with -update to
// rewrite the golden files, then read the diff.
func TestGolden(t *testing.T) {
	ins, err := filepath.Glob("testdata/*.in.jsonc")
	if err != nil || len(ins) == 0 {
		t.Fatalf("no test inputs: %v", err)
	}
	for _, in := range ins {
		name := strings.TrimSuffix(filepath.Base(in), ".in.jsonc")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(in)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Edit(src, "shop", 8080, ".devcontainer")
			if err != nil {
				t.Fatal(err)
			}
			golden := strings.TrimSuffix(in, ".in.jsonc") + ".golden.jsonc"
			if *update {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("got:\n%s\nwant:\n%s", got, want)
			}
			if _, err := hujson.Standardize(got); err != nil {
				t.Errorf("output is not valid JSONC: %v", err)
			}
			again, err := Edit(got, "shop", 8080, ".devcontainer")
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(again, got) {
				t.Errorf("second run changed the file:\n%s", again)
			}
		})
	}
}

func TestEditRefuses(t *testing.T) {
	tests := []struct {
		name, src, want string
	}{
		{"compose without service", `{"dockerComposeFile": "compose.yml"}`, "without a service"},
		{"compose file not a string", `{"dockerComposeFile": 3, "service": "app"}`, "dockerComposeFile is not"},
		{"other network", `{"runArgs": ["--network=host"]}`, `network "host"`},
		{"other network, two args", `{"runArgs": ["--net", "shared"]}`, `network "shared"`},
		{"runArgs not array", `{"runArgs": "--init"}`, "runArgs is not an array"},
		{"not an object", `[]`, "not a JSON object"},
		{"broken", `{"image": }`, "parsing devcontainer.json"},
		{"bad init", `{"initializeCommand": 3}`, "initializeCommand"},
	}
	for _, tt := range tests {
		_, err := Edit([]byte(tt.src), "shop", 8080, ".devcontainer")
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: error %v, want it to mention %q", tt.name, err, tt.want)
		}
	}
	if _, err := Edit([]byte(`{}`), "Shop!", 8080, ".devcontainer"); err == nil {
		t.Error("invalid project accepted")
	}
	if _, err := Edit([]byte(`{}`), "shop", 0, ".devcontainer"); err == nil {
		t.Error("port 0 accepted")
	}
}

func TestEditValues(t *testing.T) {
	out, err := Edit([]byte(`{"image": "x"}`), "gallery", 8089, ".devcontainer")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		RunArgs           []string
		InitializeCommand []string
	}
	std, err := hujson.Standardize(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(std, &got); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.RunArgs, " ") != "--network=bl-${localWorkspaceFolderBasename} --network-alias=bl-${localWorkspaceFolderBasename}" {
		t.Errorf("runArgs %q", got.RunArgs)
	}
	if strings.Join(got.InitializeCommand, " ") != "berthlight add --project gallery --target bl-${localWorkspaceFolderBasename} --network bl-${localWorkspaceFolderBasename} -C ${localWorkspaceFolder} 8089" {
		t.Errorf("initializeCommand %q", got.InitializeCommand)
	}
}

func TestPublishes(t *testing.T) {
	for s, want := range map[string]bool{
		`8080`: true, `8081`: false, `"8080"`: true, `"127.0.0.1:8080:8080"`: true,
		`"8080:3000"`: false, `"3000:8080"`: true, `"8080/tcp"`: true, `"8080/udp"`: false,
		`"8080-8089"`: true, `"127.0.0.1:8080-8089:8080-8089"`: true, `"8070-8079"`: false,
		`"127.0.0.1:9080-9089:8080-8089"`: true, `"8080-8089:9080-9089"`: false, `"8080-x"`: false,
	} {
		v, err := hujson.Parse([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		if got := publishes(v.Value.(hujson.Literal), 8080); got != want {
			t.Errorf("publishes(%s, 8080) = %v, want %v", s, got, want)
		}
	}
}

func TestProject(t *testing.T) {
	for _, src := range []string{`{"image":"x"}`, `{"initializeCommand": {"init": "touch .env"}}`, `not json`} {
		if p, ok := Project([]byte(src)); ok {
			t.Errorf("%s: project %q", src, p)
		}
	}
	// Every golden file is wired for project shop, in its own form.
	goldens, _ := filepath.Glob("testdata/*.golden.jsonc")
	for _, g := range goldens {
		b, _ := os.ReadFile(g)
		if p, ok := Project(b); !ok || p != "shop" {
			t.Errorf("%s: %q %v, want shop", g, p, ok)
		}
	}
}

func TestHas(t *testing.T) {
	src := []byte("{\n  // c\n  \"workspaceMount\": \"source=x,target=/w,type=bind\",\n  \"image\": \"x\"\n}")
	if !Has(src, "workspaceMount") || Has(src, "mounts") || Has([]byte("nope"), "image") {
		t.Error("Has")
	}
}

// TestEditCompose: a Compose devcontainer gets the override in
// dockerComposeFile and an initializeCommand that writes it for its
// service, but no runArgs, which Compose ignores (PRD 0041, C1).
func TestEditCompose(t *testing.T) {
	out, err := Edit([]byte(`{"dockerComposeFile": "compose.yml", "service": "web"}`), "api", 3000, ".devcontainer/web")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		DockerComposeFile []string
		InitializeCommand []string
		RunArgs           []string
	}
	std, _ := hujson.Standardize(out)
	if err := json.Unmarshal(std, &got); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.DockerComposeFile, " ") != "compose.yml berthlight.compose.yml" {
		t.Errorf("dockerComposeFile %q", got.DockerComposeFile)
	}
	if strings.Join(got.InitializeCommand, " ") != "berthlight add --project api --target bl-${localWorkspaceFolderBasename} --network bl-${localWorkspaceFolderBasename} --compose-service web --compose-override .devcontainer/web/berthlight.compose.yml -C ${localWorkspaceFolder} 3000" {
		t.Errorf("initializeCommand %q", got.InitializeCommand)
	}
	if got.RunArgs != nil {
		t.Errorf("runArgs %q", got.RunArgs)
	}
	// Next to a .devcontainer.json in the workspace folder itself.
	out, _ = Edit([]byte(`{"dockerComposeFile": "compose.yml", "service": "web"}`), "api", 3000, ".")
	if !strings.Contains(string(out), `"--compose-override", "berthlight.compose.yml"`) {
		t.Errorf("at the root:\n%s", out)
	}
}

func TestComposeFiles(t *testing.T) {
	for src, want := range map[string]string{
		`{"dockerComposeFile": "compose.yml", "service": "app"}`:                                        "compose.yml",
		`{"dockerComposeFile": ["../compose.yml", "berthlight.compose.yml"], "service": "app"} /* c */`: "../compose.yml",
		`{"dockerComposeFile": ["a.yml", "./berthlight.compose.yml", "b.yml",], "service": "app"}`:      "a.yml b.yml",
	} {
		files, service, err := ComposeFiles([]byte(src))
		if err != nil || strings.Join(files, " ") != want || service != "app" {
			t.Errorf("%s: %q %q %v, want %q", src, files, service, err, want)
		}
	}
	for _, src := range []string{`{"image": "x"}`, `{"dockerComposeFile": "berthlight.compose.yml"}`, `nope`} {
		if files, _, err := ComposeFiles([]byte(src)); err == nil {
			t.Errorf("%s: %q", src, files)
		}
	}
}

// TestGitIgnore: the override is added once, to a new or an existing
// .gitignore, in its line endings.
func TestGitIgnore(t *testing.T) {
	for in, want := range map[string]string{
		"":                        "# Written by berthlight add, for each worktree\nberthlight.compose.yml\n",
		"node_modules":            "node_modules\n# Written by berthlight add, for each worktree\nberthlight.compose.yml\n",
		"a\r\nb\r\n":              "a\r\nb\r\n# Written by berthlight add, for each worktree\r\nberthlight.compose.yml\r\n",
		"/berthlight.compose.yml": "/berthlight.compose.yml",
	} {
		got := GitIgnore([]byte(in))
		if string(got) != want {
			t.Errorf("GitIgnore(%q) = %q, want %q", in, got, want)
		}
		if again := GitIgnore(got); string(again) != string(got) {
			t.Errorf("second GitIgnore(%q) = %q", in, again)
		}
	}
}
