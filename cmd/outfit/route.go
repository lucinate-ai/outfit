// Routing a launch through the fleet: turning an Outfit's FLEET into the node
// the agent talks to. It sits beside the remote path in main.go — both answer
// "where does this agent send its requests", one by asking a control plane and
// one by choosing a machine — and it runs before the apply for the same reason
// the remote fetch does: a failed route must leave the harness config alone.

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lucinate-ai/outfit/internal/fleet"
	"github.com/lucinate-ai/outfit/internal/outfit"
)

// routeOptions is what the launch flags say about routing. They are inert
// unless something names a fleet.
type routeOptions struct {
	// fleetPath overrides the Outfit's FLEET.
	fleetPath string
	// node pins the selection to one node.
	node string
	// prefer overrides the fleet file's activity preference.
	prefer string
	// noWake refuses to start an engine on a node that is not running one.
	noWake bool
	// wakeTimeout bounds waiting for a woken node; zero leaves the default.
	wakeTimeout time.Duration
}

// fleetTarget is the fleet a launch routes through: the flag when given,
// otherwise the Outfit's own FLEET. Empty means this launch does not route.
func (o routeOptions) fleetTarget(sel outfit.Selection) string {
	if o.fleetPath != "" {
		return o.fleetPath
	}
	return sel.Fleet
}

// routeThroughFleet chooses the node an agent will talk to, waking one when
// nothing is serving what the Outfit asks for. It returns nil when this launch
// does not route, which is every launch that names no fleet.
//
// An Outfit that pins a BASEURL is not routed: the pinned address is the
// explicit answer that already wins over a REMOTE, and it wins here the same
// way. Saying so matters — silently selecting a node whose address is then
// discarded would be a puzzle rather than a behaviour.
func routeThroughFleet(sel outfit.Selection, outfitPath string, opts routeOptions) (*fleet.Choice, error) {
	target := opts.fleetTarget(sel)
	if target == "" {
		return nil, nil
	}
	if sel.BaseURL != "" {
		fmt.Fprintf(os.Stderr,
			"Not routing through %s: this Outfit pins BASEURL %s.\n", target, sel.BaseURL)
		return nil, nil
	}
	// A FLEET naming a URL is the gateway shape: it has already done the
	// choosing. Parsing accepts it so the eventual gateway needs no new
	// keyword; nothing here can act on it yet.
	if isEndpoint(target) {
		return nil, fmt.Errorf(
			"FLEET %s names an endpoint, and gateway routing is not implemented yet: "+
				"name a fleet file to choose a node from", target)
	}

	cfg, err := fleet.Resolve(resolveFleetPath(target, opts.fleetPath != "", outfitPath))
	if err != nil {
		return nil, err
	}
	prefer, err := cfg.Preference(opts.prefer)
	if err != nil {
		return nil, err
	}
	// The deploy config is what a wake would push, and its model id is also a
	// third name a node may report itself serving — derived up front so
	// selection recognises a node woken from this same Outfit. A preset that
	// will not parse is not fatal here: the failure belongs to the wake, which
	// is where it can be explained.
	dc, dcErr := deployConfigForNode(sel, outfitPath)
	want := fleet.Want{
		Model:   sel.Model,
		Alias:   sel.Alias,
		ModelID: dc.ModelID,
		Node:    opts.node,
		Prefer:  prefer,
	}

	fmt.Fprintf(os.Stderr, "Routing through %s...\n", cfg.Path)
	if opts.wakeTimeout > 0 {
		defer func(prev time.Duration) { fleet.WakeTimeout = prev }(fleet.WakeTimeout)
		fleet.WakeTimeout = opts.wakeTimeout
	}

	ctx := context.Background()
	choice, err := cfg.Select(ctx, want)
	if err == nil {
		announceChoice(choice)
		return choice, nil
	}

	// Nothing is serving it. Waking a node is the difference between a fleet
	// that is useful and a fleet you have to prepare by hand — but it starts a
	// process on someone else's machine, so it is announced and refusable.
	var none *fleet.ErrNoneServing
	if !errors.As(err, &none) {
		return nil, err
	}
	if opts.noWake {
		return nil, fmt.Errorf("%w\nStart one with `outfit fleet start <node>`, or drop --no-wake to have outfit do it", err)
	}
	if dcErr != nil {
		return nil, fmt.Errorf("%w\nand this Outfit cannot be turned into something to start: %v", err, dcErr)
	}
	choice, err = cfg.Wake(ctx, want, dc, none.Results, func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, format, args...)
	})
	if err != nil {
		return nil, err
	}
	announceChoice(choice)
	return choice, nil
}

// announceChoice names the node a launch landed on before the agent starts, so
// an unexpected route says so at the time rather than at the first request.
func announceChoice(c *fleet.Choice) {
	fmt.Fprintf(os.Stderr, "Using %s at %s — %s\n", c.Node.Name, c.BaseURL, c.Reason)
}

// resolveFleetPath resolves a fleet file's path. A relative FLEET is resolved
// against the Outfit that names it, the same rule PRESET and REMOTE follow — an
// Outfit and the fleet beside it travel together, and resolving against the
// working directory would make the same Outfit work from one directory and not
// another. A path given on the command line is the user's own, so it is
// resolved against the working directory as any other argument would be.
func resolveFleetPath(target string, fromFlag bool, outfitPath string) string {
	if fromFlag || outfitPath == "" || filepath.IsAbs(target) {
		return target
	}
	return filepath.Join(filepath.Dir(outfitPath), target)
}

// isEndpoint reports whether a FLEET value names an endpoint rather than a
// file. It mirrors outfit.Selection.FleetIsEndpoint for a value that came from
// a flag rather than an Outfit.
func isEndpoint(target string) bool {
	return strings.Contains(target, "://")
}
