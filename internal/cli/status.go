package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"text/tabwriter"

	"stormlantern.nl/berthlight/internal/route"
	rt "stormlantern.nl/berthlight/internal/runtime"
	"stormlantern.nl/berthlight/internal/status"
)

// errProblems makes status exit 1 without another message.
var errProblems = errors.New("problems found")

func cmdStatus(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("status", stderr)
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	c, err := LoadConfig()
	if err != nil {
		return err
	}
	tab, err := route.Load(c.StateDir)
	if err != nil {
		return err
	}
	run, err := rt.Detect()
	if err != nil {
		return err
	}
	chk := status.Checker{Runtime: run, Bin: run.Bin, ProxyName: c.ProxyName, Port: c.Port, Bases: c.Bases}
	rep := chk.Check(context.Background(), tab)
	writeReport(stdout, c, rep)
	if !rep.OK() {
		return errProblems
	}
	return nil
}

func writeReport(w io.Writer, c Config, rep status.Report) {
	if rep.Proxy != "" {
		fmt.Fprintf(w, "proxy: %s\n", rep.Proxy)
	} else {
		fmt.Fprintf(w, "proxy: %s listening on 127.0.0.1:%d\n", c.ProxyName, c.Port)
	}
	if len(rep.Routes) == 0 {
		fmt.Fprintln(w, "No routes. Add one with: berthlight add <name> <port>")
		return
	}
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tURL\tSTATUS")
	for _, rs := range rep.Routes {
		state := "ok, " + rs.Detail
		if !rs.OK {
			state = rs.Detail
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", rs.Route.Name, rs.URL, state)
	}
	tw.Flush()
}
