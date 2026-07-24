package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/lucinate-ai/outfit/internal/outfit"
	"github.com/lucinate-ai/outfit/internal/remote"
)

// cmdRemote dispatches the remote subcommands, which control the
// scale-to-zero GPU inference instance defined in the cloud-vm-llm repo:
// start boots it and prints the endpoint exports, stop shuts it down
// immediately (its stop Lambda also runs on a schedule to auto-stop on
// idle), and status reports instance state and endpoint health. Each
// subcommand takes an optional Outfit path; see resolveRemoteConfig for how
// the remote config is found.
func cmdRemote(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: outfit remote <start|stop|status> [path]")
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

// resolveRemoteConfig loads the remote config, preferring an Outfit's REMOTE
// instruction over the per-user file. An explicit [path] argument must name
// an Outfit (or a directory holding one) with a REMOTE instruction. With no
// argument, ./Outfit is consulted when present; the per-user config
// (~/.config/outfit/remote.json) is the fallback, so `outfit remote` still
// works outside any project. A relative REMOTE resolves against the Outfit's
// directory, so an Outfit and its remote config travel together — the same
// rule PRESET uses.
func resolveRemoteConfig(outfitArg string) (remote.Config, error) {
	if outfitArg != "" {
		sel, outfitPath, err := readOutfit("remote", outfitArg)
		if err != nil {
			return remote.Config{}, err
		}
		if sel.Remote == "" {
			return remote.Config{}, fmt.Errorf("%s has no REMOTE instruction", outfitPath)
		}
		return remote.LoadConfigFile(remoteConfigPath(sel.Remote, outfitPath), os.Getenv)
	}
	if defaultOutfitExists() {
		sel, outfitPath, err := readOutfit("remote", "")
		if err != nil {
			return remote.Config{}, err
		}
		if sel.Remote != "" {
			return remote.LoadConfigFile(remoteConfigPath(sel.Remote, outfitPath), os.Getenv)
		}
	}
	return remote.LoadConfig(os.Getenv)
}

// defaultOutfitExists reports whether the working directory holds a file
// named exactly "Outfit". A plain os.Stat would do, except that on
// case-insensitive filesystems (macOS, Windows) it also matches a file named
// "outfit" — such as the binary `make build` drops in this repo's root — so
// the directory listing is checked for the exact name instead.
func defaultOutfitExists() bool {
	entries, err := os.ReadDir(".")
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.Name() == outfit.DefaultFile && !entry.IsDir() {
			return true
		}
	}
	return false
}

func remoteConfigPath(remoteValue, outfitPath string) string {
	if filepath.IsAbs(remoteValue) {
		return remoteValue
	}
	return filepath.Join(filepath.Dir(outfitPath), remoteValue)
}

// outfitArg returns the optional positional Outfit path after the flags.
func outfitArg(fs *flag.FlagSet) string {
	if rest := fs.Args(); len(rest) > 0 {
		return rest[0]
	}
	return ""
}

func cmdRemoteStart(args []string) error {
	fs := flag.NewFlagSet("remote start", flag.ContinueOnError)
	var timeout time.Duration
	fs.DurationVar(&timeout, "timeout", 15*time.Minute, "overall time to wait for the endpoint")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := resolveRemoteConfig(outfitArg(fs))
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
	cfg, err := resolveRemoteConfig(outfitArg(fs))
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
	cfg, err := resolveRemoteConfig(outfitArg(fs))
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
