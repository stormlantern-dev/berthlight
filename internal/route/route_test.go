package route

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSaveLoad(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	tab, err := Load(dir)
	if err != nil || len(tab.Routes) != 0 {
		t.Fatalf("Load of missing dir = %v, %v; want empty table", tab, err)
	}
	tab.Put(Route{Name: "shop", Target: "shop", Port: 8080})
	tab.Put(Route{Name: "gallery", Target: "gallery", Port: 8089})
	tab.Put(Route{Name: "shop", Target: "shop", Port: 9090})
	if err := Save(dir, tab); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Routes) != 2 || got.Routes[0].Name != "gallery" || got.Routes[1].Port != 9090 {
		t.Errorf("round trip = %+v", got.Routes)
	}
	fi, err := os.Stat(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if goruntime.GOOS != "windows" && fi.Mode().Perm() != 0o644 {
		t.Errorf("mode %v, want 0644", fi.Mode().Perm())
	}
	if !got.Remove("shop") || got.Remove("shop") {
		t.Error("Remove should report true once, then false")
	}
	if _, ok := got.Lookup("shop"); ok {
		t.Error("shop still present after Remove")
	}
	if r, ok := got.Lookup("gallery"); !ok || r.Addr() != "gallery:8089" || r.Network() != "bl-gallery" {
		t.Errorf("Lookup(gallery) = %+v, %v", r, ok)
	}
}

func TestParseRejects(t *testing.T) {
	bad := map[string]string{
		"not json":     `{`,
		"bad name":     `{"routes":[{"name":"Shop","target":"shop","port":1}]}`,
		"empty target": `{"routes":[{"name":"shop","target":"","port":1}]}`,
		"bad port":     `{"routes":[{"name":"shop","target":"shop","port":0}]}`,
		"duplicate":    `{"routes":[{"name":"a","target":"a","port":1},{"name":"a","target":"b","port":2}]}`,
		"bad network":  `{"routes":[{"name":"a","target":"a","port":1,"network":"bridge"}]}`,
		"odd network":  `{"routes":[{"name":"a","target":"a","port":1,"network":"bl-my project"}]}`,
		"shared net":   `{"routes":[{"name":"a","target":"a","port":1,"network":"bl-x"},{"name":"b","target":"b","port":1,"network":"bl-x"}]}`,
		"shared net 2": `{"routes":[{"name":"a","target":"a","port":1},{"name":"b","target":"b","port":1,"network":"bl-a"}]}`,
		"shared alias": `{"routes":[{"name":"a","target":"web","port":1},{"name":"b","target":"web","port":1}]}`,
	}
	for name, in := range bad {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("%s: Parse accepted %s", name, in)
		}
	}
}

func TestWatch(t *testing.T) {
	dir := t.TempDir()
	var mu sync.Mutex
	var seen []*Table
	var errs []error
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Watch(ctx, dir, 10*time.Millisecond,
		func(tab *Table) { mu.Lock(); seen = append(seen, tab); mu.Unlock() },
		func(err error) { mu.Lock(); errs = append(errs, err); mu.Unlock() })

	waitFor := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			ok := cond()
			mu.Unlock()
			if ok {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s", what)
	}

	waitFor("initial empty table", func() bool { return len(seen) == 1 && len(seen[0].Routes) == 0 })

	tab := &Table{}
	tab.Put(Route{Name: "shop", Target: "shop", Port: 8080})
	if err := Save(dir, tab); err != nil {
		t.Fatal(err)
	}
	waitFor("table with shop", func() bool {
		_, ok := seen[len(seen)-1].Lookup("shop")
		return ok
	})

	// A broken file keeps the previous table and reports an error.
	n := len(seen)
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor("parse error", func() bool { return len(errs) > 0 })
	mu.Lock()
	if len(seen) != n {
		t.Errorf("a broken file replaced the table")
	}
	mu.Unlock()

	if err := os.Remove(filepath.Join(dir, FileName)); err != nil {
		t.Fatal(err)
	}
	waitFor("empty table after delete", func() bool { return len(seen[len(seen)-1].Routes) == 0 })
}

func TestNetwork(t *testing.T) {
	r := Route{Name: "shop-fix-login", Target: "shop", Port: 8080}
	if r.Network() != "bl-shop-fix-login" {
		t.Errorf("default network %q", r.Network())
	}
	r.NetworkName = "bl-WebShop_fix.login"
	if err := r.Validate(); err != nil || r.Network() != "bl-WebShop_fix.login" {
		t.Errorf("own network: %q %v", r.Network(), err)
	}
	same := r
	same.Port = 3000
	if !r.SameOwner(same) {
		t.Error("a port change is not the same owner")
	}
	other := r
	other.NetworkName = "bl-elsewhere"
	if r.SameOwner(other) {
		t.Error("another network is the same owner")
	}
	tab := &Table{}
	tab.Put(r)
	if err := tab.Conflict(Route{Name: "gallery", Target: "gallery", Port: 1, NetworkName: "bl-WebShop_fix.login"}); err == nil || !strings.Contains(err.Error(), "network") {
		t.Errorf("shared network: %v", err)
	}
	if err := tab.Conflict(Route{Name: "gallery", Target: "Shop", Port: 1}); err == nil || !strings.Contains(err.Error(), "target") {
		t.Errorf("shared target: %v", err)
	}
	if err := tab.Conflict(other); err != nil {
		t.Errorf("a route conflicts with itself: %v", err)
	}
	// Routes without an own network keep the old file format.
	b, _ := json.Marshal(Route{Name: "a", Target: "a", Port: 1})
	if strings.Contains(string(b), "network") {
		t.Errorf("default network written: %s", b)
	}
}
