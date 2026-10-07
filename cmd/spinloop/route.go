// Routing a launch through the fleet: choosing the node the agent talks to. It
// sits beside the cloud path in main.go — both answer "where does this agent
// send its requests", one by asking a control plane and one by choosing a
// machine — and it runs before the apply for the same reason the cloud fetch
// does: a failed route must leave the harness config alone.

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spinloop-ai/spinloop/internal/fleet"
	"github.com/spinloop-ai/spinloop/internal/spinloop"
)

// routeOptions is what the launch flags say about routing. They are inert
// unless something names a fleet.
type routeOptions struct {
	// envName names the registered environment a launch is applied against.
	// It conflicts with a fleet — each names where the model is served from.
	envName string
	// fleetPath is the fleet file to route through when given.
	fleetPath string
	// spinloopNamed says the worn Spinloop was named by the user — a
	// positional argument, a --spinloop value, or SPINLOOP_ALIAS — so a
	// fleet.yaml in the working directory is not picked up for it.
	spinloopNamed bool
	// node pins the selection to one node.
	node string
	// prefer overrides the fleet file's activity preference.
	prefer string
	// noWake refuses to start an engine on a node that is not running one.
	noWake bool
	// wakeTimeout bounds waiting for a woken node; zero leaves the default.
	wakeTimeout time.Duration
}

// fleetFile is the fleet file a launch routes through: the flag's path when
// given, otherwise the fleet.yaml in the working directory when the worn
// Spinloop was not named explicitly — a named Spinloop travels to its fleet
// only by flag, and a working directory with no fleet.yaml gives no fleet at
// all. Both sources are working-directory paths.
func (o routeOptions) fleetFile() string {
	if o.fleetPath != "" {
		return o.fleetPath
	}
	if o.spinloopNamed {
		return ""
	}
	if _, err := os.Stat(fleet.DefaultFile); err == nil {
		return fleet.DefaultFile
	}
	return ""
}

// routeThroughFleet chooses the node an agent will talk to, waking one when
// nothing is serving what the Spinloop asks for. It returns nil when this launch
// does not route, which is every launch that names no fleet.
//
// A Spinloop that pins a BASEURL is not routed: the pinned address is the
// explicit answer that already wins over an environment, and it wins here
// the same way. Saying so matters — silently selecting a node whose address
// is then discarded would be a puzzle rather than a behaviour.
func routeThroughFleet(sel spinloop.Selection, spinloopPath string, opts routeOptions) (*fleet.Choice, error) {
	target := opts.fleetFile()
	if target == "" {
		return nil, nil
	}
	if sel.BaseURL != "" {
		fmt.Fprintf(os.Stderr,
			"Not routing through %s: this Spinloop pins BASEURL %s.\n", target, sel.BaseURL)
		return nil, nil
	}
	cfg, err := fleet.Resolve(target)
	if err != nil {
		return nil, err
	}
	// A fleet file that names a gateway routes at the gateway rather than a
	// node: it has already done the choosing, so there is no node to contact
	// and nothing to wake. The token is not resolved here: the launch resolves
	// it through the same chain, under the variable the section names.
	if gw, ok := cfg.GatewaySection(); ok {
		choice := &fleet.Choice{
			Gateway:         true,
			BaseURL:         fleet.EndpointBaseURL(gw.URL),
			GatewayTokenEnv: gw.TokenEnv,
			Label:           gw.Label(),
			Reason:          "the fleet file names a gateway",
		}
		announceChoice(choice)
		return choice, nil
	}
	prefer, err := cfg.Preference(opts.prefer)
	if err != nil {
		return nil, err
	}
	// The deploy config is what a wake would push, and its model id is also a
	// third name a node may report itself serving — derived up front so
	// selection recognises a node woken from this same Spinloop. A preset that
	// will not parse is not fatal here: the failure belongs to the wake, which
	// is where it can be explained.
	dc, dcErr := deployConfigForNode(sel, spinloopPath)
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
		return nil, fmt.Errorf("%w\nStart one with `spinloop fleet start <node>`, or drop --no-wake to have spinloop do it", err)
	}
	if !cfg.AnyNodeWakes() {
		// No node in the fleet may be woken — the fleet-wide setting, since
		// no node's own overrides it. The refusal still names the node that
		// would have been woken, the way a --no-wake refusal does: the
		// setting decides whether to wake, not what would be woken.
		if wake, ok := cfg.WouldWake(none.Results, fleet.ConstantConfig(dc, dcErr)); ok {
			return nil, fmt.Errorf(
				"%w\nwake is off in %s: start %s with `spinloop fleet start %s`",
				err, cfg.Path, wake.Name, wake.Name)
		}
		return nil, fmt.Errorf("%w\nwake is off in %s", err, cfg.Path)
	}
	if dcErr != nil {
		return nil, fmt.Errorf("%w\nand this Spinloop cannot be turned into something to start: %v", err, dcErr)
	}
	choice, err = cfg.Wake(ctx, want, fleet.ConstantConfig(dc, dcErr), none.Results, func(format string, args ...any) {
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
	if c.Gateway {
		fmt.Fprintf(os.Stderr, "Routing at %s — %s\n", c.BaseURL, c.Reason)
		return
	}
	fmt.Fprintf(os.Stderr, "Using %s at %s — %s\n", c.Node.Name, c.BaseURL, c.Reason)
}
