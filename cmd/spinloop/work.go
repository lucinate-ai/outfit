// The `work` command family: the orchestrator's work list — the backlog the
// orchestrator works — worked from the shell as a client of the work list API
// the orchestrator serves: add an item, read the work, read an item's kept
// output, stop a running item, remove an item. The commands name the API's
// address and present its token; the run's view of the items is the source
// of truth, and a refusal reads the way the API states it.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"

	"github.com/spinloop-ai/spinloop/internal/orchestrator"
)

// workRequestBound bounds one call to the work list API. The API's abort
// blocks for the run's stop grace, so the bound outlives a whole stop, and a
// run that never answers cannot hang the command forever.
var workRequestBound = 30 * time.Second

func cmdWork(args []string) error { return execCmd(workCmd(), args) }

// workCmd builds the `work` group.
func workCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "work",
		Short: "work the orchestrator's work list",
		Long: `works the orchestrator's work list — the backlog it works — from
the shell, as a client of the work list API the orchestrator serves: add an
item, read the work, read an item's kept output, stop a running item,
re-queue a failed item, remove an item.

Each subcommand takes --url, the API's base address, and presents the API's
token — from --api-token, else --api-token-file, else the SPINLOOP_API_TOKEN
environment — as a bearer on every call it makes. The run's view of the items
is the source of truth: the commands call the API and report its answer, and
a refusal reads the way the API states it. A command that names no --url
fails before it calls the API, naming the flag.`,
	}
	c.AddCommand(
		workAddCmd(),
		workListCmd(),
		workAbortCmd(),
		workRetryCmd(),
		workRemoveCmd(),
		workLogsCmd(),
		workBoardCmd(),
	)
	return c
}

// workAPIFlags adds the flags that name the work list API — its address and
// its token — to a subcommand's flag set, the way the fleet subcommands each
// carry --fleet: the same flag on every subcommand, not a persistent flag
// the group owns.
func workAPIFlags(fs *pflag.FlagSet, base, apiToken, apiTokenFile *string) {
	fs.StringVar(base, "url", "", "the work list API's base address")
	fs.StringVar(apiToken, "api-token", "", "the work list API's bearer token")
	fs.StringVar(apiTokenFile, "api-token-file", "", "the file the work list API's bearer token stands in")
}

// workTarget resolves a work command's --url and token flags into the API it
// calls: the address, required and named where absent, and the token,
// resolved the way the daemon resolves its own.
func workTarget(use, base, apiToken, apiTokenFile string) (string, string, error) {
	if base == "" {
		return "", "", fmt.Errorf("%s needs the work list API's address: --url", use)
	}
	token, err := daemonToken(apiToken, apiTokenFile)
	if err != nil {
		return "", "", err
	}
	return base, token, nil
}

// workRequest makes one call to the work list API and checks its answer: the
// token as a bearer where there is one, the body as JSON where there is one,
// and a non-2xx an error carrying the API's message, so a refusal reads the
// way the API states it.
func workRequest(base, token, method, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	ctx, cancel := context.WithTimeout(context.Background(), workRequestBound)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(base, "/")+path, reader)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling the work list API at %s: %w", base, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading the answer from the work list API at %s: %w", base, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, workAPIError(base, resp.StatusCode, data)
	}
	return data, nil
}

// workAPIErr is a non-2xx answer from the work list API: Error() reads the
// way the API states the refusal, and status is kept so a caller that
// cares — a follow noticing its item was removed — can tell a "not found"
// apart from any other refusal without parsing the message.
type workAPIErr struct {
	status int
	msg    string
}

func (e *workAPIErr) Error() string { return e.msg }

// workAPIError is a non-2xx answer from the work list API, as the error the
// command reports: the API's message where it states one — the refusal reads
// the way the API states it — the status and the address where it does not.
func workAPIError(base string, status int, data []byte) error {
	var out struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	msg := fmt.Sprintf("the work list API at %s answered %d", base, status)
	if err := json.Unmarshal(data, &out); err == nil && out.Error.Message != "" {
		msg = out.Error.Message
	}
	return &workAPIErr{status: status, msg: msg}
}

// workAddBody is one item as the work list API takes it: the fields the items
// file takes, the API's JSON form.
type workAddBody struct {
	ID           string   `json:"id"`
	Instructions string   `json:"instructions"`
	Dir          string   `json:"dir"`
	Tags         []string `json:"tags"`
	Priority     int      `json:"priority"`
}

// workAddCmd builds `work add`.
func workAddCmd() *cobra.Command {
	var base, apiToken, apiTokenFile string
	var id, instructions, dir string
	var tags []string
	var priority int
	c := &cobra.Command{
		Use:   "add",
		Short: "add an item to the orchestrator's work list",
		Long: `adds an item to the orchestrator's work list: the item's fields
come from the flags, sent to the work list API the orchestrator serves, and
the API applies the items file's validation on them — the instructions and
the working directory present, the tags well-formed key=value pairs, no key
named twice — and its own refusals.

An id the file already carries is refused, naming it, and an id the state
beside the file has recorded done or failed is refused too, naming the
record — an ended item is not worked again under its own id. The API answers
once the item is in the file, and the command reports its answer: a refusal
reads the way the API states it.`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(_ *cobra.Command, _ []string) error {
			b, token, err := workTarget("work add", base, apiToken, apiTokenFile)
			if err != nil {
				return err
			}
			body := workAddBody{
				ID: id, Instructions: instructions, Dir: dir,
				Tags: tags, Priority: priority,
			}
			if _, err := workRequest(b, token, http.MethodPost, "/v1/items", body); err != nil {
				return err
			}
			fmt.Printf("item %q added\n", id)
			return nil
		},
	}
	fs := c.Flags()
	workAPIFlags(fs, &base, &apiToken, &apiTokenFile)
	fs.StringVar(&id, "id", "", "the item's id")
	fs.StringVar(&instructions, "instructions", "", "the instructions the item's agent is given")
	fs.StringVar(&dir, "dir", "", "the directory the agent works in")
	fs.StringArrayVar(&tags, "tag", nil, "a tag the item carries, key=value; repeatable")
	fs.IntVar(&priority, "priority", 0, "the item's priority, higher first")
	return c
}

// workListCmd builds `work list`.
func workListCmd() *cobra.Command {
	var base, apiToken, apiTokenFile string
	c := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "report the orchestrator's work list",
		Long: `reports every item in the orchestrator's work list with its record,
in the file's order: the item's id, its state — backlog, running, done or
failed — the node a running item runs on, and when it started and ended, one
plain line per item, a dash where a value is absent.

The list reads the work list API the orchestrator serves: the run's view of
the items, the source of truth, an item with no record backlog. The state
colour is drawn only where there is a terminal to draw it on.`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			b, token, err := workTarget("work list", base, apiToken, apiTokenFile)
			if err != nil {
				return err
			}
			data, err := workRequest(b, token, http.MethodGet, "/v1/items", nil)
			if err != nil {
				return err
			}
			var out struct {
				Data []orchestrator.ItemView `json:"data"`
			}
			if err := json.Unmarshal(data, &out); err != nil {
				return fmt.Errorf("reading the work list from the work list API at %s: %w", b, err)
			}
			tty := term.IsTerminal(int(os.Stdout.Fd()))
			w := cmd.OutOrStdout()
			if tty {
				fmt.Fprint(w, workListTable(out.Data))
			} else {
				for _, v := range out.Data {
					fmt.Fprintln(w, workListLine(v, false))
				}
			}
			return nil
		},
	}
	fs := c.Flags()
	workAPIFlags(fs, &base, &apiToken, &apiTokenFile)
	return c
}

// workListLine is one item's line: the columns a program can split, tab
// separated for a program to consume off a terminal.
func workListLine(v orchestrator.ItemView, tty bool) string {
	node, started, ended := workListDashed(v)
	return v.ID + "\t" + workListColouredState(v.State, tty) + "\t" + node + "\t" + started + "\t" + ended
}

// workListDashed is an item's node, start and end, each "-" where absent.
func workListDashed(v orchestrator.ItemView) (node, started, ended string) {
	node, started, ended = v.Node, v.StartedAt, v.EndedAt
	if node == "" {
		node = "-"
	}
	if started == "" {
		started = "-"
	}
	if ended == "" {
		ended = "-"
	}
	return node, started, ended
}

// workListColouredState is a state in its colour where tty, plain otherwise.
func workListColouredState(state string, tty bool) string {
	if !tty {
		return state
	}
	switch state {
	case orchestrator.StateDone:
		return ansiGreen + state + ansiReset
	case orchestrator.StateFailed:
		return ansiRed + state + ansiReset
	case orchestrator.StateRunning:
		return ansiYellow + state + ansiReset
	default:
		return state
	}
}

// workListHeadings names the table's columns, in column order.
var workListHeadings = [5]string{"ID", "STATE", "NODE", "STARTED", "ENDED"}

// workListTable is the work list as a table for a terminal: a heading row,
// then each column as wide as its widest value — the heading included — so a
// long id does not throw the rest of the row out of line the way a fixed
// terminal tab stop would.
func workListTable(items []orchestrator.ItemView) string {
	idW, stateW, nodeW, startedW := len(workListHeadings[0]), len(workListHeadings[1]), len(workListHeadings[2]), len(workListHeadings[3])
	rows := make([][5]string, len(items))
	for i, v := range items {
		node, started, ended := workListDashed(v)
		rows[i] = [5]string{v.ID, v.State, node, started, ended}
		idW = max(idW, len(v.ID))
		stateW = max(stateW, len(v.State))
		nodeW = max(nodeW, len(node))
		startedW = max(startedW, len(started))
	}
	var b strings.Builder
	h := workListHeadings
	b.WriteString(ansiGrey)
	b.WriteString(h[0])
	b.WriteString(strings.Repeat(" ", idW-len(h[0])+2))
	b.WriteString(h[1])
	b.WriteString(strings.Repeat(" ", stateW-len(h[1])+2))
	b.WriteString(h[2])
	b.WriteString(strings.Repeat(" ", nodeW-len(h[2])+2))
	b.WriteString(h[3])
	b.WriteString(strings.Repeat(" ", startedW-len(h[3])+2))
	b.WriteString(h[4])
	b.WriteString(ansiReset)
	b.WriteByte('\n')
	for _, r := range rows {
		id, state, node, started, ended := r[0], r[1], r[2], r[3], r[4]
		b.WriteString(id)
		b.WriteString(strings.Repeat(" ", idW-len(id)+2))
		b.WriteString(workListColouredState(state, true))
		b.WriteString(strings.Repeat(" ", stateW-len(state)+2))
		b.WriteString(node)
		b.WriteString(strings.Repeat(" ", nodeW-len(node)+2))
		b.WriteString(started)
		b.WriteString(strings.Repeat(" ", startedW-len(started)+2))
		b.WriteString(ended)
		b.WriteByte('\n')
	}
	return b.String()
}

// workAbortCmd builds `work abort`.
func workAbortCmd() *cobra.Command {
	var base, apiToken, apiTokenFile string
	c := &cobra.Command{
		Use:   "abort <id>",
		Short: "stop a running item and put it back in the backlog",
		Long: `stops a running item, through the work list API the orchestrator
serves: the item's agent stopped the way a clean interrupt stops it, and its
record removed — the item back in the backlog, and the run's next pass
admits it again.

Only a running item can be aborted: an item the run records backlog, done or
failed is refused, naming the item and its state, and an id the file does not
carry is refused, naming it. The API answers once the item is stopped, and
the command reports its answer: a refusal reads the way the API states it.`,
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(_ *cobra.Command, args []string) error {
			id := args[0]
			b, token, err := workTarget("work abort", base, apiToken, apiTokenFile)
			if err != nil {
				return err
			}
			if _, err := workRequest(b, token, http.MethodPost, "/v1/items/"+url.PathEscape(id)+"/abort", nil); err != nil {
				return err
			}
			fmt.Printf("item %q stopped: it is back in the backlog\n", id)
			return nil
		},
	}
	fs := c.Flags()
	workAPIFlags(fs, &base, &apiToken, &apiTokenFile)
	c.ValidArgsFunction = itemIDSlot
	return c
}

// workRetryCmd builds `work retry`.
func workRetryCmd() *cobra.Command {
	var base, apiToken, apiTokenFile string
	c := &cobra.Command{
		Use:   "retry <id>",
		Short: "put a failed item back in the backlog",
		Long: `puts a failed item back in the backlog, through the work list API
the orchestrator serves: the item's record removed, and the run's next pass
admits it again. The item's fields are unchanged, and its kept output stays
until the new attempt writes over it.

Only a failed item can be retried: an item the run records backlog, running
or done is refused, naming the item and its state, and an id the file does
not carry is refused, naming it. The API answers once the item is back in
the backlog, and the command reports its answer: a refusal reads the way the
API states it.`,
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(_ *cobra.Command, args []string) error {
			id := args[0]
			b, token, err := workTarget("work retry", base, apiToken, apiTokenFile)
			if err != nil {
				return err
			}
			if _, err := workRequest(b, token, http.MethodPost, "/v1/items/"+url.PathEscape(id)+"/retry", nil); err != nil {
				return err
			}
			fmt.Printf("item %q is back in the backlog\n", id)
			return nil
		},
	}
	fs := c.Flags()
	workAPIFlags(fs, &base, &apiToken, &apiTokenFile)
	c.ValidArgsFunction = itemIDSlot
	return c
}

// workRemoveCmd builds `work remove`.
func workRemoveCmd() *cobra.Command {
	var base, apiToken, apiTokenFile string
	c := &cobra.Command{
		Use:   "remove <id>",
		Short: "remove an item from the orchestrator's work list",
		Long: `removes an item, through the work list API the orchestrator
serves: the item out of the work list — the items file, its record, and its
kept output — the file a valid items file after the removal.

A running item cannot be removed: the refusal names it and the abort that
goes first, and an id the file does not carry is refused, naming it. The API
answers once the item is out, and the command reports its answer: a refusal
reads the way the API states it.`,
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(_ *cobra.Command, args []string) error {
			id := args[0]
			b, token, err := workTarget("work remove", base, apiToken, apiTokenFile)
			if err != nil {
				return err
			}
			if _, err := workRequest(b, token, http.MethodDelete, "/v1/items/"+url.PathEscape(id), nil); err != nil {
				return err
			}
			fmt.Printf("item %q removed\n", id)
			return nil
		},
	}
	fs := c.Flags()
	workAPIFlags(fs, &base, &apiToken, &apiTokenFile)
	c.ValidArgsFunction = itemIDSlot
	return c
}
