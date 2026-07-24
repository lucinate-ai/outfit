package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/lucinate-ai/outfit/internal/remote"
)

// cmdRemote dispatches the remote subcommands, which control the
// scale-to-zero GPU inference instance defined in the cloud-vm-llm repo:
// start boots it and prints the endpoint exports, stop shuts it down
// immediately (its stop Lambda also runs on a schedule to auto-stop on
// idle), and status reports instance state and endpoint health.
func cmdRemote(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: outfit remote <start|stop|status>")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "start":
		return cmdRemoteStart(rest)
	case "stop":
		return cmdRemoteStop(rest)
	case "status":
		return cmdRemoteStatus(rest)
	default:
		return fmt.Errorf("unknown remote subcommand %q (expected start, stop or status)", sub)
	}
}

func cmdRemoteStart(args []string) error {
	fs := flag.NewFlagSet("remote start", flag.ContinueOnError)
	var timeout time.Duration
	fs.DurationVar(&timeout, "timeout", 15*time.Minute, "overall time to wait for the endpoint")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := remote.LoadConfig(os.Getenv)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	resp, err := remote.Start(ctx, cfg, func(msg string) { fmt.Println(msg) })
	if err != nil {
		return err
	}

	fmt.Println("ready")
	fmt.Println()
	fmt.Printf("export OPENAI_BASE_URL=%s\n", resp.BaseURL)
	fmt.Printf("export OPENAI_API_KEY=%s\n", resp.APIKey)
	return nil
}

func cmdRemoteStop(args []string) error {
	fs := flag.NewFlagSet("remote stop", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := remote.LoadConfig(os.Getenv)
	if err != nil {
		return err
	}
	resp, err := remote.Stop(context.Background(), cfg)
	if err != nil {
		return err
	}
	fmt.Printf("state: %s\n", resp.State)
	return nil
}

func cmdRemoteStatus(args []string) error {
	fs := flag.NewFlagSet("remote status", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := remote.LoadConfig(os.Getenv)
	if err != nil {
		return err
	}
	resp, err := remote.Status(context.Background(), cfg)
	if err != nil {
		return err
	}
	fmt.Printf("state: %s\n", resp.State)
	if resp.Healthy != nil {
		fmt.Printf("healthy: %t\n", *resp.Healthy)
	}
	if resp.BaseURL != "" {
		fmt.Printf("base_url: %s\n", resp.BaseURL)
	}
	return nil
}
