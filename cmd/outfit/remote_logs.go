package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lucinate-ai/outfit/internal/remote"
)

// Seam: a package variable so tests drive the command without AWS.
var logsFetchFn = remote.FetchLogs

// logsFollowInterval is how often a follow asks for more. CloudWatch charges
// per request and the agent ships in batches anyway, so polling faster would
// cost more without showing anything sooner. A variable so tests need not wait
// on it.
var logsFollowInterval = 5 * time.Second

// logsFollowOverlap is how far back of the last event a follow re-asks from.
// The shipping agent's delivery lag means an event can land with a timestamp
// slightly behind one already returned, so each poll deliberately re-reads a
// little and suppresses what it has already printed by event id.
const logsFollowOverlap = 10 * time.Second

// cmdRemoteLogs prints the logs an environment's instances shipped to
// CloudWatch. It reads the durable store rather than the instance, so a boot
// that failed and an instance that has since terminated are both still
// readable — which is when logs are wanted most, and exactly when status and
// metrics have nothing left to report.
func cmdRemoteLogs(args []string) error {
	fs := flag.NewFlagSet("remote logs", flag.ContinueOnError)
	var (
		source   string
		since    time.Duration
		limit    int
		instance string
		follow   bool
		format   string
	)
	fs.StringVar(&source, "source", remote.LogSourceEngine,
		"which log to read: engine (default), boot or all")
	fs.DurationVar(&since, "since", time.Hour, "how far back to look, as a duration (30m, 2h)")
	fs.IntVar(&limit, "limit", 200, "maximum events to print, keeping the most recent")
	fs.StringVar(&instance, "instance", "", "restrict output to one instance id")
	const followUsage = "keep printing new events as they arrive"
	fs.BoolVar(&follow, "follow", false, followUsage)
	fs.BoolVar(&follow, "f", false, followUsage+" (shorthand)")
	fs.StringVar(&format, "format", "text", "output format: text (default) or json")
	if err := fs.Parse(sortFlagsBeforeArgs(fs, args)); err != nil {
		return err
	}
	if err := validateLogsFlags(source, format, since, limit); err != nil {
		return err
	}

	cfg, err := resolveRemoteConfig(outfitArg(fs))
	if err != nil {
		return err
	}

	q := remote.LogQuery{
		Environment: cfg.Environment,
		Source:      source,
		Start:       time.Now().Add(-since),
		Limit:       limit,
		Instance:    instance,
	}
	if follow {
		return followLogs(cfg, q, format)
	}
	return runLogsOnce(context.Background(), cfg, q, format, since, os.Stdout)
}

// validateLogsFlags rejects the flag values that cannot mean anything, in the
// same shape as the other remote subcommands' checks.
func validateLogsFlags(source, format string, since time.Duration, limit int) error {
	switch source {
	case remote.LogSourceEngine, remote.LogSourceBoot, remote.LogSourceAll:
	default:
		return fmt.Errorf("--source must be %s, got %q", strings.Join(remote.LogSources, ", "), source)
	}
	if format != "text" && format != "json" {
		return fmt.Errorf("--format must be \"text\" or \"json\", got %q", format)
	}
	if since <= 0 {
		return fmt.Errorf("--since must be positive, got %s", since)
	}
	if limit <= 0 {
		return fmt.Errorf("--limit must be positive, got %d", limit)
	}
	return nil
}

// runLogsOnce fetches and prints a single window. An empty result is reported
// rather than left as silence, since "nothing was logged" and "the command did
// not work" look identical otherwise.
func runLogsOnce(ctx context.Context, cfg remote.Config, q remote.LogQuery, format string,
	window time.Duration, w io.Writer) error {
	res, err := logsFetchFn(ctx, cfg, q)
	if err != nil {
		return err
	}
	if len(res.Events) == 0 {
		if format == "json" {
			return writeLogsJSON(w, nil)
		}
		fmt.Fprintf(w, "no %s logs for environment %q in the last %s\n",
			q.Source, q.Environment, window)
		return nil
	}
	if format == "json" {
		return writeLogsJSON(w, res.Events)
	}
	if res.Omitted > 0 {
		fmt.Fprintf(w, "... %d earlier events omitted (raise --limit to see more)\n", res.Omitted)
	}
	writeLogsText(w, res.Events, mixedOrigins(res.Events))
	return nil
}

// writeLogsText prints events oldest first, one per line, each with its local
// timestamp. Source and instance are prefixed only when label says the output
// actually mixes them: labelling every line of a single instance's engine log
// would be noise on the common case.
func writeLogsText(w io.Writer, events []remote.LogEvent, label bool) {
	for _, e := range events {
		stamp := e.Timestamp.Local().Format("2006-01-02 15:04:05")
		if label {
			fmt.Fprintf(w, "%s  %s/%s  %s\n", stamp, e.Source, e.Instance, e.Message)
			continue
		}
		fmt.Fprintf(w, "%s  %s\n", stamp, e.Message)
	}
}

// mixedOrigins reports whether the events come from more than one source or
// more than one instance, and so need attributing per line.
func mixedOrigins(events []remote.LogEvent) bool {
	if len(events) == 0 {
		return false
	}
	for _, e := range events[1:] {
		if e.Source != events[0].Source || e.Instance != events[0].Instance {
			return true
		}
	}
	return false
}

// writeLogsJSON emits the events as an array, carrying the same fields the text
// format shows, for scripting. An empty result is an empty array rather than
// null, so a consumer can iterate it unconditionally.
func writeLogsJSON(w io.Writer, events []remote.LogEvent) error {
	if events == nil {
		events = []remote.LogEvent{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(events)
}

// followLogs prints the window, then keeps printing what arrives after it.
// Each poll starts a little behind the newest event already seen, to catch
// events the agent delivers late, and the ids seen in that overlap suppress the
// duplicates the overlap would otherwise print. Interrupting is a clean exit:
// the user asked it to stop, which is not a failure.
func followLogs(cfg remote.Config, q remote.LogQuery, format string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		cancel()
	}()

	return followLogsLoop(ctx, cfg, q, format, os.Stdout)
}

// followLogsLoop is the polling itself, with the interrupt wiring left to its
// caller so it can be driven directly.
func followLogsLoop(ctx context.Context, cfg remote.Config, q remote.LogQuery,
	format string, w io.Writer) error {
	printed := map[string]time.Time{}
	newest := time.Time{}
	// Labelling is decided across the whole session, not per batch: a poll that
	// happened to return one instance's lines must not drop the prefix the
	// previous poll's lines carried. Once a second origin appears the output
	// stays labelled.
	origins := map[string]bool{}
	for first := true; ; first = false {
		res, err := logsFetchFn(ctx, cfg, q)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		// Only the opening window can be capped meaningfully — later polls ask
		// for the sliver since the last event — so it is the only one that
		// reports what the cap dropped.
		if first && res.Omitted > 0 && format != "json" {
			fmt.Fprintf(w, "... %d earlier events omitted (raise --limit to see more)\n", res.Omitted)
		}
		fresh := make([]remote.LogEvent, 0, len(res.Events))
		for _, e := range res.Events {
			if _, seen := printed[e.ID]; seen {
				continue
			}
			printed[e.ID] = e.Timestamp
			origins[e.Source+"/"+e.Instance] = true
			fresh = append(fresh, e)
			if e.Timestamp.After(newest) {
				newest = e.Timestamp
			}
		}
		if len(fresh) > 0 {
			if format == "json" {
				if err := writeLogsJSON(w, fresh); err != nil {
					return err
				}
			} else {
				writeLogsText(w, fresh, len(origins) > 1)
			}
		}
		// Only the overlap window can produce a duplicate, so ids older than
		// it are forgotten and the map cannot grow with the session.
		for id, ts := range printed {
			if ts.Before(newest.Add(-2 * logsFollowOverlap)) {
				delete(printed, id)
			}
		}
		if !newest.IsZero() {
			q.Start = newest.Add(-logsFollowOverlap)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(logsFollowInterval):
		}
	}
}
