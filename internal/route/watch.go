package route

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Watch polls the routes file in dir and calls apply with every table that
// differs from the last one seen, starting with the current one. A file that
// fails to parse is reported through onErr and the previous table stays in
// use. Polling needs no dependencies and works on every bind mount, including
// those of container VMs on macOS, where file events do not cross.
func Watch(ctx context.Context, dir string, every time.Duration, apply func(*Table), onErr func(error)) {
	path := filepath.Join(dir, FileName)
	var last os.FileInfo
	first := true
	check := func() {
		fi, err := os.Stat(path)
		if errors.Is(err, fs.ErrNotExist) {
			if first || last != nil {
				apply(&Table{})
			}
			last, first = nil, false
			return
		}
		if err != nil {
			onErr(err)
			return
		}
		if !first && last != nil && os.SameFile(fi, last) && fi.ModTime().Equal(last.ModTime()) && fi.Size() == last.Size() {
			return
		}
		t, err := Load(dir)
		if err != nil {
			onErr(err)
			last, first = fi, false
			return
		}
		apply(t)
		last, first = fi, false
	}
	check()
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			check()
		}
	}
}
