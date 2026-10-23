package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"stormlantern.nl/berthlight/internal/naming"
	"stormlantern.nl/berthlight/internal/route"
	rt "stormlantern.nl/berthlight/internal/runtime"
	"stormlantern.nl/berthlight/internal/status"
	"stormlantern.nl/berthlight/internal/tui"
)

// errProblems makes status exit 1 without another message.
var errProblems = errors.New("problems found")

func cmdStatus(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("status", stderr)
	asJSON := fs.Bool("json", false, "print the status as JSON")
	watch := fs.Bool("watch", false, "keep checking, and say when a route comes up or goes down")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return errUsage
	}
	if *asJSON && *watch {
		fmt.Fprintln(stderr, "berthlight: --json and --watch do not go together")
		return errUsage
	}
	c, err := LoadConfig()
	if err != nil {
		return err
	}
	run, err := rt.Detect()
	if err != nil {
		return err
	}
	chk := status.Checker{Runtime: run, Bin: run.Bin, ProxyName: c.ProxyName, Port: c.Port, Bases: c.Bases}
	check := func(ctx context.Context) (status.Report, error) {
		tab, err := route.Load(c.StateDir)
		if err != nil {
			return status.Report{}, err
		}
		return chk.Check(ctx, tab), nil
	}

	if *watch {
		// Short probes keep the screen current; a dev server that is still
		// compiling shows as starting and comes up on a later tick.
		chk.Timeout = 3 * time.Second
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		ticks := time.NewTicker(time.Second)
		defer ticks.Stop()
		return watchStatus(ctx, ticks.C, check, c, stdout)
	}

	rep, err := check(context.Background())
	if err != nil {
		return err
	}
	if *asJSON {
		err = writeJSON(stdout, statusJSON(c, rep))
	} else {
		writeReport(stdout, tui.NewTerm(stdout), c, rep, 0)
	}
	if err != nil {
		return err
	}
	if !rep.OK() {
		return errProblems
	}
	return nil
}

// routeJSON is one route in `ls --json` and `status --json`. The field
// names are a stable interface for scripts and agents.
type routeJSON struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Target  string `json:"target"`
	Port    int    `json:"port"`
	Network string `json:"network"`
	OK      *bool  `json:"ok,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

func newRouteJSON(c Config, r route.Route) routeJSON {
	return routeJSON{Name: r.Name, URL: urlFor(c, r.Name), Target: r.Target, Port: r.Port, Network: r.Network()}
}

func statusJSON(c Config, rep status.Report) any {
	type proxyJSON struct {
		Name    string `json:"name"`
		Port    int    `json:"port"`
		OK      bool   `json:"ok"`
		Problem string `json:"problem,omitempty"`
	}
	out := struct {
		OK     bool        `json:"ok"`
		Proxy  proxyJSON   `json:"proxy"`
		Routes []routeJSON `json:"routes"`
	}{
		OK:     rep.OK(),
		Proxy:  proxyJSON{Name: c.ProxyName, Port: c.Port, OK: rep.Proxy == "", Problem: rep.Proxy},
		Routes: []routeJSON{},
	}
	for _, rs := range rep.Routes {
		r := newRouteJSON(c, rs.Route)
		ok := rs.OK
		r.OK, r.Detail = &ok, rs.Detail
		out.Routes = append(out.Routes, r)
	}
	return out
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// writeReport writes the status. With a width, each line is cut to fit,
// so that --watch, which redraws by counting lines, never has one wrap.
func writeReport(w io.Writer, term tui.Term, c Config, rep status.Report, width int) {
	if rep.Proxy != "" {
		fmt.Fprintf(w, "proxy: %s\n", fit(rep.Proxy, width-len("proxy: ")))
	} else {
		fmt.Fprintf(w, "proxy: %s listening on 127.0.0.1:%d\n", c.ProxyName, c.Port)
	}
	if len(rep.Routes) == 0 {
		fmt.Fprintln(w, "No routes. Add one with: berthlight add <name> <port>")
		return
	}
	fmt.Fprintln(w)
	rows := [][]tui.Cell{{{Text: "NAME"}, {Text: "URL"}, {Text: "STATUS"}}}
	for _, rs := range rep.Routes {
		state := "ok, " + rs.Detail
		if !rs.OK {
			state = rs.Detail
		}
		rows = append(rows, []tui.Cell{{Text: rs.Route.Name}, {Text: rs.URL, Show: term.Link(rs.URL, rs.URL)}, {Text: state}})
	}
	if width > 0 {
		nameW, urlW := 0, 0
		for _, r := range rows {
			nameW, urlW = max(nameW, len([]rune(r[0].Text))), max(urlW, len([]rune(r[1].Text)))
		}
		for _, r := range rows {
			r[2].Text = fit(r[2].Text, width-nameW-urlW-4)
		}
	}
	tui.Table(w, rows)
}

// watchStatus checks on every tick until ctx ends. On a terminal it keeps
// the report on screen, puts a summary in the tab title, shows a progress
// indicator while something is not working, and sends a desktop
// notification when a route comes up or goes down. Without a terminal it
// prints one line per change, for logs.
func watchStatus(ctx context.Context, ticks <-chan time.Time, check func(context.Context) (status.Report, error), c Config, w io.Writer) error {
	term := tui.NewTerm(w)
	return watchWith(ctx, ticks, check, c, w, term, time.Now)
}

func watchWith(ctx context.Context, ticks <-chan time.Time, check func(context.Context) (status.Report, error), c Config, w io.Writer, term tui.Term, now func() time.Time) error {
	var prev *status.Report
	lines := 0
	if term.TTY() {
		term.PushTitle()
		defer func() {
			term.Progress(tui.ProgressOff, 0)
			term.PopTitle()
		}()
	}
	for {
		rep, err := check(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return err
		}
		if term.TTY() {
			var b strings.Builder
			fmt.Fprintf(&b, "berthlight status, %s (Ctrl-C to stop)\n\n", now().Format("15:04:05"))
			writeReport(&b, term, c, rep, term.Width()-1)
			// Redraw in place, as one synchronized update.
			out := "\x1b[?2026h"
			if lines > 0 {
				out += fmt.Sprintf("\x1b[%dF\x1b[J", lines)
			}
			out += b.String() + "\x1b[?2026l"
			fmt.Fprint(w, out)
			lines = strings.Count(b.String(), "\n")

			term.Title(summary(rep))
			switch {
			case rep.Proxy != "":
				term.Progress(tui.ProgressError, 100)
			case !rep.OK():
				term.Progress(tui.ProgressIndeterminate, 0)
			default:
				term.Progress(tui.ProgressOff, 0)
			}
		}
		for _, ch := range changes(prev, rep, c) {
			switch {
			case !term.TTY():
				fmt.Fprintf(w, "%s %s\n", now().Format("15:04:05"), ch)
			case prev != nil: // notify on changes, not on the first look
				term.Notify("berthlight", ch)
			}
		}
		prev = &rep
		select {
		case <-ctx.Done():
			return nil
		case <-ticks:
		}
	}
}

// fit cuts s to n characters, ending in an ellipsis when cut. n <= 0
// means no limit.
func fit(s string, n int) string {
	r := []rune(s)
	if n <= 0 || len(r) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

// summary is the tab title for a report.
func summary(rep status.Report) string {
	if rep.Proxy != "" {
		return "berthlight · proxy down"
	}
	ok := 0
	for _, rs := range rep.Routes {
		if rs.OK {
			ok++
		}
	}
	return fmt.Sprintf("berthlight · %d/%d ok", ok, len(rep.Routes))
}

// changes describes what changed between two reports, one line each.
// Without a previous report, it describes the whole report.
func changes(prev *status.Report, rep status.Report, c Config) []string {
	host := func(name string) string { return naming.Host(name, c.Bases[0]) }
	var out []string
	if prev == nil {
		if rep.Proxy != "" {
			out = append(out, "proxy down: "+rep.Proxy)
		}
		for _, rs := range rep.Routes {
			out = append(out, routeLine(host(rs.Route.Name), rs))
		}
		return out
	}
	if (prev.Proxy == "") != (rep.Proxy == "") {
		if rep.Proxy == "" {
			out = append(out, "proxy up")
		} else {
			out = append(out, "proxy down: "+rep.Proxy)
		}
	}
	was := map[string]bool{}
	for _, rs := range prev.Routes {
		was[rs.Route.Name] = rs.OK
	}
	for _, rs := range rep.Routes {
		ok, known := was[rs.Route.Name]
		if !known || ok != rs.OK {
			out = append(out, routeLine(host(rs.Route.Name), rs))
		}
	}
	return out
}

func routeLine(host string, rs status.RouteStatus) string {
	if rs.OK {
		return host + " is up"
	}
	return host + " is down: " + rs.Detail
}
