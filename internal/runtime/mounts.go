package runtime

import (
	"encoding/json"
	"fmt"
	"strings"
)

// BindMount is a host directory a running container sees at Destination.
type BindMount struct {
	Container   string
	Source      string
	Destination string
	Networks    []string
}

// BindMounts lists the bind mounts of every running container, so a path
// an editor or agent sees inside a devcontainer can be traced back to the
// host.
func (r Runtime) BindMounts() ([]BindMount, error) {
	ids, err := r.Run("ps", "-q")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(ids) == "" {
		return nil, nil
	}
	args := append([]string{"container", "inspect", "--format", "{{.Name}}\t{{json .Mounts}}\t{{json .NetworkSettings.Networks}}"}, strings.Fields(ids)...)
	out, err := r.Run(args...)
	if err != nil {
		return nil, err
	}
	return parseBindMounts(out)
}

func parseBindMounts(out string) ([]BindMount, error) {
	var all []BindMount
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		var mounts []struct {
			Type, Source, Destination string
		}
		if err := json.Unmarshal([]byte(parts[1]), &mounts); err != nil {
			return nil, fmt.Errorf("parsing mounts of %s: %w", parts[0], err)
		}
		var nets map[string]json.RawMessage
		json.Unmarshal([]byte(parts[2]), &nets)
		var names []string
		for n := range nets {
			names = append(names, n)
		}
		for _, m := range mounts {
			if m.Type == "bind" {
				all = append(all, BindMount{Container: strings.TrimPrefix(parts[0], "/"), Source: m.Source, Destination: m.Destination, Networks: names})
			}
		}
	}
	return all, nil
}
