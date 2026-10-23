// Package cast reads and writes terminal recordings in the asciicast v2
// format (https://docs.asciinema.org/manual/asciicast/v2/): a JSON header
// line, then one JSON array per output event, [time, "o", data].
package cast

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// Header is the first line of a recording.
type Header struct {
	Version int               `json:"version"`
	Width   int               `json:"width"`
	Height  int               `json:"height"`
	Title   string            `json:"title,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// Event is output written to the terminal at Time seconds.
type Event struct {
	Time float64
	Data string
}

// Write writes a recording.
func Write(w io.Writer, h Header, events []Event) error {
	h.Version = 2
	enc := json.NewEncoder(w)
	if err := enc.Encode(h); err != nil {
		return err
	}
	for _, e := range events {
		if err := enc.Encode([]any{e.Time, "o", e.Data}); err != nil {
			return err
		}
	}
	return nil
}

// Read reads a recording; it keeps output events only.
func Read(r io.Reader) (Header, []Event, error) {
	var h Header
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	if !sc.Scan() {
		return h, nil, fmt.Errorf("empty recording")
	}
	if err := json.Unmarshal(sc.Bytes(), &h); err != nil {
		return h, nil, fmt.Errorf("header: %w", err)
	}
	var events []Event
	for sc.Scan() {
		var raw []json.RawMessage
		if err := json.Unmarshal(sc.Bytes(), &raw); err != nil || len(raw) != 3 {
			return h, nil, fmt.Errorf("event %q: %v", sc.Text(), err)
		}
		var e Event
		var kind string
		if err := json.Unmarshal(raw[0], &e.Time); err != nil {
			return h, nil, err
		}
		json.Unmarshal(raw[1], &kind)
		if kind != "o" {
			continue
		}
		if err := json.Unmarshal(raw[2], &e.Data); err != nil {
			return h, nil, err
		}
		events = append(events, e)
	}
	return h, events, sc.Err()
}
