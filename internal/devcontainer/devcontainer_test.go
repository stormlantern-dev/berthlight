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
			got, err := Edit(src, "shop", 8080)
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
			again, err := Edit(got, "shop", 8080)
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
		{"compose", `{"dockerComposeFile": "compose.yml", "service": "app"}`, "Docker Compose"},
		{"other network", `{"runArgs": ["--network=host"]}`, `network "host"`},
		{"other network, two args", `{"runArgs": ["--net", "shared"]}`, `network "shared"`},
		{"runArgs not array", `{"runArgs": "--init"}`, "runArgs is not an array"},
		{"not an object", `[]`, "not a JSON object"},
		{"broken", `{"image": }`, "parsing devcontainer.json"},
		{"bad init", `{"initializeCommand": 3}`, "initializeCommand"},
	}
	for _, tt := range tests {
		_, err := Edit([]byte(tt.src), "shop", 8080)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: error %v, want it to mention %q", tt.name, err, tt.want)
		}
	}
	if _, err := Edit([]byte(`{}`), "Shop!", 8080); err == nil {
		t.Error("invalid project accepted")
	}
	if _, err := Edit([]byte(`{}`), "shop", 0); err == nil {
		t.Error("port 0 accepted")
	}
}

func TestEditValues(t *testing.T) {
	out, err := Edit([]byte(`{"image": "x"}`), "gallery", 8089)
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
