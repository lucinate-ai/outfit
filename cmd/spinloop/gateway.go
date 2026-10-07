// spinloop gateway: the fleet's OpenAI-compatible front door. It is the fleet
// client wearing a server — selection, waking, endpoint resolution, and key
// handling all come from internal/fleet, and the HTTP surface lives in
// internal/gateway, so this command resolves the fleet file, resolves its own
// token the way the daemon's is resolved, and serves.

package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spinloop-ai/spinloop/internal/fleet"
	"github.com/spinloop-ai/spinloop/internal/gateway"
	"github.com/spinloop-ai/spinloop/internal/inference"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func gatewayCmd() *cobra.Command {
	var fleetPath, listen, apiToken, apiTokenFile string
	var wakeTimeout time.Duration
	var maxRequestBytes int64
	var loopback bool
	c := &cobra.Command{
		Use:   "gateway",
		Short: "serve the fleet under one OpenAI-compatible endpoint",
		Long: `runs in the foreground, the way spinloop serve does: it holds the
fleet file it serves (a --fleet path, or ./fleet.yaml), answers
/v1/models and completion requests by choosing a node with the fleet's
own selector, and wakes a node when nothing is serving what a request
asks for, holding the request until the engine answers. It needs the
same environment a machine running spinloop fleet start would: the
tokens the fleet file names, set here or in the .env beside it.

A fleet file points an agent at it with a gateway section that names its
address:

    gateway:
      url: http://gateway.internal:4000

The agent then needs only the gateway's token, as OPENAI_API_KEY.`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, args []string) error {
			resolve(c)
			return runGatewayCommand(fleetPath, listen, apiToken, apiTokenFile, wakeTimeout, maxRequestBytes, loopback, c.Flags())
		},
	}
	fs := c.Flags()
	fs.StringVarP(&fleetPath, "fleet", "f", "", fleetFileUsage)
	fs.StringVar(&listen, "listen", gateway.DefaultListen, "the address to listen on")
	fs.BoolVarP(&loopback, "loopback", "l", false, "bind the gateway to loopback on the default port ("+gateway.LoopbackListen+"); needs no token")
	fs.StringVar(&apiTokenFile, "api-token-file", "", "read the gateway's bearer token from this file")
	fs.StringVar(&apiToken, "api-token", "", "the gateway's bearer token")
	fs.DurationVar(&wakeTimeout, "wake-timeout", 0, "how long to wait for a woken engine to answer")
	fs.Int64Var(&maxRequestBytes, "max-request-bytes", gateway.DefaultMaxRequestBytes, "the largest completion request body to accept, in bytes")
	compRegister(c, "fleet", compFiles)
	return c
}

// cmdGateway runs the command through the tree — the seam the suite calls.
func cmdGateway(args []string) error { return execCmd(gatewayCmd(), args) }

// runGatewayCommand is the body of `spinloop gateway`: the server, and the
// signal handling that shuts it down cleanly.
func runGatewayCommand(fleetPath, listen, apiToken, apiTokenFile string, wakeTimeout time.Duration, maxRequestBytes int64, loopback bool, flags *pflag.FlagSet) error {
	// Whether --listen was typed at all, not whether it differs from the
	// default: --listen :4000 --loopback is still a conflict, and a
	// compare-against-default check would let it pass.
	listenExplicit := flags.Changed("listen")
	listen, err := gatewayListenAddr(listen, listenExplicit, loopback)
	if err != nil {
		return err
	}
	// The override lives as long as the server serves, not as long as the
	// setup takes.
	var restore func()
	if wakeTimeout > 0 {
		prev := fleet.WakeTimeout
		fleet.WakeTimeout = wakeTimeout
		restore = func() { fleet.WakeTimeout = prev }
		defer restore()
	}
	if maxRequestBytes < 1 {
		return fmt.Errorf("--max-request-bytes must be at least 1, got %d", maxRequestBytes)
	}
	srv, ln, err := newGatewayServer(fleetPath, listen, apiToken, apiTokenFile, maxRequestBytes)
	if err != nil {
		return err
	}
	defer ln.Close()

	// The handler goes in before a signal can arrive, so a signal at any point
	// from here on shuts the server down rather than killing the process.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go srv.Serve(ln)

	// Foreground until signalled, the way the daemon waits: the signal is the
	// only exit, so the command returns nil when a clean shutdown ends Serve's
	// http.ErrServerClosed with it.
	<-sigCh
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	srv.Shutdown(ctx)
	cancel()
	return nil
}

// gatewayListenAddr resolves the address the gateway listens on: --loopback
// takes the default, and an explicit address and --loopback are two answers
// to one question.
func gatewayListenAddr(listen string, listenExplicit, loopback bool) (string, error) {
	if loopback && listenExplicit {
		return "", fmt.Errorf("--loopback and --listen both given: pass one")
	}
	if loopback {
		return gateway.LoopbackListen, nil
	}
	return listen, nil
}

// newGatewayServer resolves the fleet file and the gateway's token, checks the
// file's token references the way a startup must, opens the listener, and
// prints the address a fleet file's gateway section names. Everything that can
// fail without serving fails here, before a listener exists.
func newGatewayServer(fleetPath, listen, apiToken, apiTokenFile string, maxRequestBytes int64) (*http.Server, net.Listener, error) {
	cfg, err := fleet.Resolve(fleetPath)
	if err != nil {
		return nil, nil, err
	}
	token, err := daemonToken(apiToken, apiTokenFile)
	if err != nil {
		return nil, nil, err
	}
	logger, err := commandLogger("")
	if err != nil {
		return nil, nil, err
	}

	// The gateway's own startup failures are the fleet file's: a token variable
	// it names that is set nowhere fails here, naming the node, rather than
	// surfacing later as a per-request authentication failure.
	for _, entry := range cfg.Nodes {
		if _, err := cfg.Token(entry); err != nil {
			return nil, nil, err
		}
		if entry.Kind == fleet.KindCloud {
			if _, err := cfg.CloudEngineToken(entry); err != nil {
				return nil, nil, err
			}
		} else if _, err := cfg.EngineToken(entry); err != nil {
			return nil, nil, err
		}
	}

	// The gateway is the client that wakes a node, so it resolves what each
	// node runs the way `spinloop fleet start` does: the node's own source,
	// never a config invented for the request.
	cfgFor := func(entry fleet.NodeConfig) (inference.DeployConfig, error) {
		arg, _, err := resolveNodeSpinloop(entry, cfg.Dir)
		if err != nil {
			return inference.DeployConfig{}, err
		}
		sel, path, err := readSpinloop("the Spinloop of node "+entry.Name, arg)
		if err != nil {
			return inference.DeployConfig{}, err
		}
		if err := applySpinloopEnv(sel, path); err != nil {
			return inference.DeployConfig{}, err
		}
		return deployConfigForNode(sel, path)
	}

	h := gateway.New(cfg, token, gateway.Options{ConfigFor: cfgFor, Log: logger, MaxRequestBytes: maxRequestBytes})
	ln, err := gateway.Listen(listen, token)
	if err != nil {
		return nil, nil, err
	}

	fmt.Printf("Gateway for %s is listening on %s\n", cfg.Path, ln.Addr().String())
	fmt.Printf("Name %s in the fleet file's gateway section\n\n", fleetURL(ln.Addr().String()))
	return &http.Server{Handler: h}, ln, nil
}

// fleetURL turns the address the gateway listens on into the URL a fleet file's
// gateway section names: an http URL the agent's machine can reach. The host it
// can know is the one it was told to bind; for a wildcard bind the host is
// whatever this machine is called from the other side, which only the operator
// knows.
func fleetURL(listenAddr string) string {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return listenAddr
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "<this-machine>"
	}
	return "http://" + net.JoinHostPort(host, port)
}
