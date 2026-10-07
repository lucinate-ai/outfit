# Commands

One page per command. `spinloop version` prints the version, and `spinloop
help` the usage summary.

| Command | What it does |
| ------- | ------------ |
| [`spinloop status`](status.md) | What every engine you run is doing, one row each — a fleet, or one environment |
| [`spinloop dashboard`](dashboard.md) | The live tiled view of the same, with the keys to drive it |
| [`spinloop metrics`](metrics.md) | What each engine is doing with its hardware, and what it has cost |
| [`spinloop logs`](logs.md) | What each engine has said — a daemon's log file or an environment's log store |
| [`spinloop code`](code.md) | Launch the active harness — a one-word shortcut for `spinloop harness open` |
| [`spinloop harness`](harness.md) | Configure the agent (add, remove, apply, unapply, show, export), launch it (open), and set the default (config) |
| [`spinloop provider`](provider.md) | Work with the provider catalogue (list, init) |
| [`spinloop serve`](serve.md) | Run the inference server for the model a `Spinloop` names |
| [`spinloop daemon`](serve.md#the-control-api-api-and-spinloop-daemon) | Supervise an engine over the [control API](../http-api.md), so anything that can reach the machine can start, stop, and watch it |
| [`spinloop up`](up.md) | Start the engine this directory holds: the fleet, or the `Spinloop`'s server |
| [`spinloop fleet`](fleet.md) | Drive the engines on every machine you run: start, stop, deploy, route |
| [`spinloop gateway`](gateway.md) | Serve the fleet under one OpenAI-compatible endpoint |
| [`spinloop orchestrator`](orchestrator.md) | Work a backlog of items against the fleet, at the fleet's declared pace |
| [`spinloop work`](work.md) | Drive the orchestrator's work list from the shell: add, list, logs, abort, remove — or watch it live on `board` |
| [`spinloop cloud`](cloud.md) | Run the model on a cloud GPU that stops when you do |
| [`spinloop hf`](hf.md) | Write a `Spinloop` for a Hugging Face model, from its page reference |
| [`spinloop alias`](alias.md) | Name a `Spinloop` so the name works anywhere a path does |
| [`spinloop unalias`](unalias.md) | Drop a registered name |
| [`spinloop completion`](completion.md) | Tab completion for your shell |
