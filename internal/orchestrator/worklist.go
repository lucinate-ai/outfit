package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// flight is one in-flight item: the item, the node it runs on, its child,
// and the done channel the child's wait closes when the agent ends.
type flight struct {
	item  Item
	node  Node
	child Child
	done  chan struct{}
}

// WorkList is the work the orchestrator is working: the items the items file
// carries, in the file's order, the run's record for each, the children the
// run has launched, and the store the records and the logs live in — under
// one lock, so the run's pass and the work list API's operations are
// functions on the same state and cannot race each other over the files.
type WorkList struct {
	mu sync.Mutex

	itemsPath string
	store     Store
	dispatch  Launcher
	gateway   string
	log       *slog.Logger

	// baseDir is harness.yaml's own baseDir — see dispatchConfig's field of
	// the same name. Remove's own cleanup needs it to resolve an item's
	// directory the same way a launch already does.
	baseDir string

	items    []Item
	records  map[string]ItemState
	inflight map[string]*flight
	results  chan result

	// stopping holds the id of an item the API's Abort has detached and is
	// waiting on: the agent is asked to stop, and stopping it can outlast
	// the request. Detaching drops the item from inflight and records
	// immediately (finish must not overwrite the abort's outcome once the
	// old agent's wait resolves), which would otherwise leave it looking
	// like free backlog to a pass mid-stop. stopping holds the id out of
	// admission until the wait Abort is already doing resolves, so a pass
	// cannot launch a second agent into the same directory while the first
	// is still exiting.
	stopping map[string]bool

	// lastMod and lastSize are the items file's mtime and size the last
	// read saw: a change to either is a re-read.
	lastMod  time.Time
	lastSize int64
}

// NewWorkList reads the work a run works from: the items file's items, the
// record the store keeps, and the file's last-modified facts the re-read
// checks against. The store's lock is the one it took opening.
func NewWorkList(itemsPath string, store Store, dispatch Launcher, gateway string, log *slog.Logger) (*WorkList, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	items, err := LoadItems(itemsPath)
	if err != nil {
		return nil, err
	}
	records, err := store.Load()
	if err != nil {
		return nil, err
	}
	wl := &WorkList{
		itemsPath: itemsPath,
		store:     store,
		dispatch:  dispatch,
		gateway:   gateway,
		log:       log,
		items:     items,
		records:   records,
		inflight:  map[string]*flight{},
		results:   make(chan result),
		stopping:  map[string]bool{},
	}
	if fi, err := os.Stat(itemsPath); err == nil {
		wl.lastMod, wl.lastSize = fi.ModTime(), fi.Size()
	}
	return wl, nil
}

// WithBaseDir sets harness.yaml's own baseDir — see dispatchConfig's
// baseDir field. Unset (empty) means unchanged behavior.
func (w *WorkList) WithBaseDir(dir string) *WorkList {
	w.baseDir = dir
	return w
}

// Close releases the store's hold on the items file.
func (w *WorkList) Close() { w.store.Close() }

// run is the loop: the topology read, the items file's re-read, the
// admission under the fleet's declared limits, the reap of the finished, and
// the save — until the context ends. It is the whole lifecycle the run owes:
// a gateway that stops answering ends it, naming the gateway; the clean
// interrupt stops the agents and re-queues their items.
func (w *WorkList) run(ctx context.Context, cfg Config) error {
	tick := cfg.Tick
	if tick <= 0 {
		tick = tickInterval
	}

	topo, err := cfg.Topologist.Topology(ctx)
	if err != nil {
		if ctx.Err() != nil {
			// The interrupt arrived before the first pass: nothing is in
			// flight, so the clean end is to end.
			return nil
		}
		return fmt.Errorf("the gateway %s did not answer: %v", w.gateway, err)
	}

	for {
		w.reap()

		suppressed := w.consumeAborts()

		topo, err = cfg.Topologist.Topology(ctx)
		if err != nil {
			if ctx.Err() != nil {
				// The read failed because the interrupt cancelled it, not
				// because the gateway went away: take the clean end.
				return w.shutdown()
			}
			w.save()
			return fmt.Errorf("the gateway %s stopped answering: %v", w.gateway, err)
		}

		if err := w.refreshItems(); err != nil {
			w.save()
			return err
		}

		w.pass(topo, suppressed)

		w.save()

		select {
		case <-ctx.Done():
			return w.shutdown()
		case <-time.After(tick):
		}
	}
}

// refreshItems re-reads the items file where its mtime or size has changed:
// a new item enters the backlog, and an item dropped from it keeps its
// recorded end. The read is outside the lock; the view it returns goes in
// under it.
func (w *WorkList) refreshItems() error {
	fi, err := os.Stat(w.itemsPath)
	if err != nil {
		return fmt.Errorf("reading the work items file: %v", err)
	}
	if fi.ModTime().Equal(w.lastMod) && fi.Size() == w.lastSize {
		return nil
	}
	items, err := LoadItems(w.itemsPath)
	if err != nil {
		return err
	}
	w.mu.Lock()
	w.items = items
	w.mu.Unlock()
	w.lastMod, w.lastSize = fi.ModTime(), fi.Size()
	return nil
}

// consumeAborts takes up every marker standing beside the file: the marked
// item's agent stopped the way a clean interrupt stops it — the record and
// the flight gone, the state saved, the stop, the grace, the hard end — and
// the marker removed when the child has ended, the id returned so the pass
// that took it up does not re-admit it. A marker for an item not in flight
// is taken up alone: the ask is consumed, and nothing else changes.
func (w *WorkList) consumeAborts() map[string]bool {
	ids, err := AbortsPending(w.itemsPath)
	if err != nil {
		w.log.Error("reading the abort markers", slog.String("error", err.Error()))
		return nil
	}
	suppressed := map[string]bool{}
	for _, id := range ids {
		w.mu.Lock()
		fl, ok := w.detachLocked(id)
		w.mu.Unlock()
		if ok {
			fl.child.Stop()
			select {
			case <-fl.done:
			case <-time.After(stopGrace):
				fl.child.Kill()
				<-fl.done
			}
			suppressed[id] = true
		}
		os.Remove(filepath.Join(abortsDirFor(w.itemsPath), id))
	}
	return suppressed
}

// pruneLocked drops a record the file no longer carries and that is not in
// flight: the file is the set of items the run works, and a record that
// outlived its item would stand in the state, refusing the id's re-add. The
// caller holds the lock.
func (w *WorkList) pruneLocked() {
	for id := range w.records {
		if _, running := w.inflight[id]; running {
			continue
		}
		if !w.carries(id) {
			delete(w.records, id)
			w.log.Info("record dropped, the file no longer carries the item",
				slog.String("item", id))
		}
	}
}

// pass admits the backlog against the topology: the match, the limits, and
// the launch, the record the item takes as it goes in flight. It holds the
// lock across the launch, so the API's operations — an add, a remove, an
// abort — never interleave a launch mid-way. suppressed is the ids this pass
// took up from an abort marker: the take-out is not undone on the same pass,
// and the item is eligible on the next.
func (w *WorkList) pass(topo Topology, suppressed map[string]bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pruneLocked()
	for _, item := range sortedForAdmission(w.items) {
		if suppressed[item.ID] {
			continue // the pass that took the marker up does not re-admit it
		}
		if st, recorded := w.records[item.ID]; recorded && (st.State == StateDone || st.State == StateFailed) {
			continue // a finished end stands: no retry on its own
		}
		if _, running := w.inflight[item.ID]; running {
			continue
		}
		if w.stopping[item.ID] {
			continue // an abort detached this item, and its old agent has not exited yet
		}
		nodes := Match(item, topo)
		if len(nodes) == 0 {
			continue // waiting is not failing: the fleet may change
		}
		// Rebuilt per item: an item admitted earlier this pass is in
		// flight and counts against the limits for the rest of it.
		inflightItems := make([]Item, 0, len(w.inflight))
		for _, fl := range w.inflight {
			inflightItems = append(inflightItems, fl.item)
		}
		if !Admits(topo, inflightItems, item) {
			continue // the limits hold the line until a slot frees
		}
		node := nodes[0]
		child, err := w.dispatch.Launch(item, node, w.store.LogPath(item.ID))
		if err != nil {
			w.records[item.ID] = ItemState{
				State: StateFailed, Why: err.Error(), Node: node.Name,
				EndedAt: time.Now().UTC().Format(time.RFC3339),
			}
			w.log.Info("item failed to launch",
				slog.String("item", item.ID),
				slog.String("error", err.Error()))
			continue // the rest of the backlog goes on
		}
		fl := &flight{item: item, node: node, child: child, done: make(chan struct{})}
		w.inflight[item.ID] = fl
		w.records[item.ID] = ItemState{
			State: StateRunning, Node: node.Name,
			StartedAt: time.Now().UTC().Format(time.RFC3339),
		}
		w.log.Info("item launched",
			slog.String("item", item.ID),
			slog.String("node", node.Name))
		go func(id string, f *flight) {
			err := f.child.Wait()
			close(f.done)
			w.results <- result{id: id, err: err}
		}(item.ID, fl)
	}
}

// reap takes every finished child the waits have reported: done where the
// agent ended cleanly, failed where it did not, its slot freed.
func (w *WorkList) reap() {
	for {
		select {
		case r := <-w.results:
			w.finish(r)
		default:
			return
		}
	}
}

// finish records one finished child and frees its slot. A result the abort
// has already taken up is skipped: the slot is gone, and with it the
// record.
func (w *WorkList) finish(r result) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fl, ok := w.inflight[r.id]
	if !ok {
		return
	}
	delete(w.inflight, r.id)
	st := ItemState{Node: fl.node.Name, EndedAt: time.Now().UTC().Format(time.RFC3339)}
	if r.err == nil {
		st.State = StateDone
	} else {
		st.State = StateFailed
		st.Why = r.err.Error()
	}
	w.records[r.id] = st
	w.log.Info("item ended",
		slog.String("item", r.id),
		slog.String("node", fl.node.Name),
		slog.String("state", st.State))
}

// save writes the records to the store; a failure is logged, the way the
// loop always has logged one: the run goes on, and the next save retries.
func (w *WorkList) save() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.saveLocked()
}

// saveLocked writes the records, the caller holding the lock.
func (w *WorkList) saveLocked() {
	if err := w.store.Save(w.records); err != nil {
		w.log.Error("saving the state", slog.String("error", err.Error()))
	}
}

// shutdown is the clean interrupt: stop the agents, put their items back in
// the backlog — no work lost, none run twice — and give them the grace
// before the hard end.
func (w *WorkList) shutdown() error {
	w.mu.Lock()
	for _, fl := range w.inflight {
		fl.child.Stop()
	}
	for id := range w.inflight {
		delete(w.records, id)
	}
	w.saveLocked()
	w.mu.Unlock()

	deadline := time.After(stopGrace)
	for w.inflightCount() > 0 {
		select {
		case r := <-w.results:
			w.dropFlight(r.id)
		case <-deadline:
			w.killFlights()
			for w.inflightCount() > 0 {
				r := <-w.results
				w.dropFlight(r.id)
			}
		}
	}
	return nil
}

// inflightCount is how many children the run still waits on.
func (w *WorkList) inflightCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.inflight)
}

// dropFlight frees one finished child's slot, where the abort has not taken
// it up first.
func (w *WorkList) dropFlight(id string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.inflight, id)
}

// killFlights ends every still-running child hard, after the grace a Stop
// has had.
func (w *WorkList) killFlights() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, fl := range w.inflight {
		fl.child.Kill()
	}
}

// List is the work list: every item the items file carries, in the file's
// order, each with its record — backlog where there is none.
func (w *WorkList) List() []ItemView {
	w.mu.Lock()
	defer w.mu.Unlock()
	return Join(w.items, w.records)
}

// Log is one item's kept agent output: the output's bytes as text, and ok
// false where there is none — an item in the backlog, an agent that has not
// written yet — which is an answer, not a fault. An id the file does not
// carry is a miss the API answers 404.
func (w *WorkList) Log(id string) (string, bool, error) {
	w.mu.Lock()
	if !w.carries(id) {
		w.mu.Unlock()
		return "", false, &errMissing{msg: fmt.Sprintf("the items file carries no item with id %q", id)}
	}
	path := w.store.LogPath(id)
	w.mu.Unlock()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("reading the output kept for item %q: %v", id, err)
	}
	return string(data), true, nil
}

// Add puts an item in the items file and the backlog: the file's validation
// on the item's fields, a refusal where the file already carries the id, and
// a refusal where the state records the id ended — done or failed, naming
// the record — and, where it is accepted, the items file re-written with the
// item, still a valid items file.
func (w *WorkList) Add(item Item) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	f := fileItemFromItem(item)
	if f.ID == "" {
		return &errInvalid{msg: "the item has no id"}
	}
	if err := checkItem(f); err != nil {
		return &errInvalid{msg: err.Error()}
	}
	for _, it := range w.items {
		if it.ID == item.ID {
			return &errConflict{msg: fmt.Sprintf("the items file already carries an item with id %q", item.ID)}
		}
	}
	if st, recorded := w.records[item.ID]; recorded && (st.State == StateDone || st.State == StateFailed) {
		return &errConflict{msg: fmt.Sprintf("item %q is recorded %s: an ended item is not added again", item.ID, st.State)}
	}
	kept := make([]fileItem, 0, len(w.items)+1)
	for _, it := range w.items {
		kept = append(kept, fileItemFromItem(it))
	}
	kept = append(kept, f)
	if err := writeItemsFile(w.itemsPath, kept); err != nil {
		return err
	}
	w.items = append(w.items, item)
	w.log.Info("item added", slog.String("item", item.ID))
	return nil
}

// Remove takes an item out of the work list: the items file, its record, and
// its kept output — a running item is refused, naming it and the abort that
// goes first, and an id the file does not carry is refused, naming it.
func (w *WorkList) Remove(id string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, running := w.inflight[id]; running {
		return &errConflict{msg: fmt.Sprintf("item %q is running: abort it first, then remove it", id)}
	}
	kept := make([]Item, 0, len(w.items))
	var removed Item
	found := false
	for _, it := range w.items {
		if it.ID == id {
			found = true
			removed = it
			continue
		}
		kept = append(kept, it)
	}
	if !found {
		return &errMissing{msg: fmt.Sprintf("the items file carries no item with id %q", id)}
	}
	files := make([]fileItem, 0, len(kept))
	for _, it := range kept {
		files = append(files, fileItemFromItem(it))
	}
	if err := writeItemsFile(w.itemsPath, files); err != nil {
		return err
	}
	w.items = kept
	delete(w.records, id)
	w.saveLocked()
	w.log.Info("item removed", slog.String("item", id))
	// The kept output goes with it; an item that wrote none has no file.
	os.Remove(w.store.LogPath(id))
	dir := ResolveItemDir(w.baseDir, removed.Dir)
	// The docker backend's scoped config goes with it too — a config
	// subdirectory of the item's own directory, left behind by the item's
	// workspace subdirectory, which is not the orchestrator's to remove. A
	// bare-backend item, or one the docker backend never launched, has no
	// config directory — RemoveAll of one that is not there is a silent
	// no-op.
	os.RemoveAll(ItemConfigDir(dir))
	// The item's own copy of its log goes with it too, the same as the
	// canonical one beside the items file; an item never launched has none.
	os.Remove(ItemLogFile(dir))
	return nil
}

// Abort stops a running item's agent the way a clean interrupt stops it —
// the polite signal, the grace, then the hard end — and puts the item back in
// the backlog: its record is gone from the state, and the run admits it
// again on a later pass. An item that is not running is refused, naming the
// item and its state.
func (w *WorkList) Abort(id string) error {
	w.mu.Lock()
	fl, ok := w.detachLocked(id)
	if !ok {
		state := ""
		if r, recorded := w.records[id]; recorded {
			state = r.State
		}
		if state == "" && !w.carries(id) {
			w.mu.Unlock()
			return &errMissing{msg: fmt.Sprintf("the items file carries no item with id %q", id)}
		}
		if state == "" {
			state = StateBacklog
		}
		w.mu.Unlock()
		return &errConflict{msg: fmt.Sprintf("item %q is not running: it is %s", id, state)}
	}
	w.stopping[id] = true
	w.mu.Unlock()

	fl.child.Stop()
	select {
	case <-fl.done:
	case <-time.After(stopGrace):
		fl.child.Kill()
		<-fl.done
	}

	w.mu.Lock()
	delete(w.stopping, id)
	w.mu.Unlock()
	return nil
}

// Retry puts a failed item back in the backlog: its record is removed from
// the state, so the run admits it again on a later pass. The items file and
// the item's kept output are left as they are; the output is replaced when
// the new attempt starts. An item that has not failed is refused, naming the
// item and its state, and an id the file does not carry is refused, naming it.
func (w *WorkList) Retry(id string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.carries(id) {
		return &errMissing{msg: fmt.Sprintf("the items file carries no item with id %q", id)}
	}
	state := StateBacklog
	if r, recorded := w.records[id]; recorded {
		state = r.State
	}
	if state != StateFailed {
		return &errConflict{msg: fmt.Sprintf("item %q is not failed: it is %s", id, state)}
	}
	delete(w.records, id)
	w.saveLocked()
	w.log.Info("item retried", slog.String("item", id))
	return nil
}

// detachLocked removes the in-flight flight and its record and saves the
// state, the caller holding the lock: ok false where the id is not in
// flight. The API's abort and the marker's consumption both take the item
// out this way, and the stop of the child goes on after the lock is free.
func (w *WorkList) detachLocked(id string) (*flight, bool) {
	fl, ok := w.inflight[id]
	if !ok {
		return nil, false
	}
	delete(w.inflight, id)
	delete(w.records, id)
	w.saveLocked()
	w.log.Info("item aborted",
		slog.String("item", id),
		slog.String("node", fl.node.Name))
	return fl, true
}

// carries reports whether the items view holds the id, the caller holding
// the lock.
func (w *WorkList) carries(id string) bool {
	for _, it := range w.items {
		if it.ID == id {
			return true
		}
	}
	return false
}

// writeItemsFile writes the items back to the items file, atomically: the
// file stays a valid items file through the write, the way the run re-reads
// it.
func writeItemsFile(path string, items []fileItem) error {
	data, err := yaml.Marshal(items)
	if err != nil {
		return fmt.Errorf("writing the work items file %s: %v", path, err)
	}
	return writeItemsFileData(path, data)
}

// writeItemsFileData writes the file's bytes, atomically: a torn write must
// never leave the run's re-read holding a half file.
func writeItemsFileData(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing the work items file %s: %v", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("writing the work items file %s: %v", path, err)
	}
	return nil
}

// ItemView is one item as the work list shows it: the file's fields joined
// with the run's record — backlog where there is none.
type ItemView struct {
	ID           string   `json:"id"`
	Instructions string   `json:"instructions"`
	Dir          string   `json:"dir"`
	Tags         []string `json:"tags,omitempty"`
	Priority     int      `json:"priority,omitempty"`
	// State is the record's state: backlog where there is no record.
	State string `json:"state"`
	Node  string `json:"node,omitempty"`
	// Why is the failed item's reason, the record's.
	Why string `json:"why,omitempty"`
	// StartedAt and EndedAt are the record's, RFC 3339.
	StartedAt string `json:"startedAt,omitempty"`
	EndedAt   string `json:"endedAt,omitempty"`
}

// The API's refusal errors, the status the API answers each with: a
// validation fault is a bad request, a conflict with the file's or the
// state's current shape is a conflict, an id the file does not carry is a
// miss.

// errInvalid is a fault in what the caller gave.
type errInvalid struct{ msg string }

func (e *errInvalid) Error() string { return e.msg }

// errConflict is a request the work list's current shape refuses.
type errConflict struct{ msg string }

func (e *errConflict) Error() string { return e.msg }

// errMissing is an id the items file does not carry.
type errMissing struct{ msg string }

func (e *errMissing) Error() string { return e.msg }

// apiStatus is the status the API answers a work list error with.
func apiStatus(err error) int {
	var inv *errInvalid
	var conf *errConflict
	var miss *errMissing
	switch {
	case errors.As(err, &inv):
		return statusBadRequest
	case errors.As(err, &conf):
		return statusConflict
	case errors.As(err, &miss):
		return statusNotFound
	default:
		return statusInternalServerError
	}
}
