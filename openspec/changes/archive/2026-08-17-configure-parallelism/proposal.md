## Why

`CONTEXT` in an Outfit is meant to be the context window a request actually
gets. For `llamacpp`, that is not what `--ctx-size` means: llama.cpp treats
`--ctx-size` as a KV-cache budget shared across `--parallel` slots and silently
divides it, so an Outfit with `CONTEXT 128k` served with two parallel slots
gives each request only 64k — half of what the file says — with nothing in the
Outfit or the printed command calling that out. Today the only way to run more
than one slot at all is to hand-write `np`/`parallel` in a `PRESET` `.ini`
(`internal/preset/preset.go` already knows the alias, and `remote/preset.ini`
does exactly this), and even then nothing scales `ctx-size` to compensate — the
trap stays.

`outfit` has no concept of parallelism at all: no Outfit keyword, no
`Selection` field, no per-engine translation. Fixing llama.cpp's math requires
one, and picking one requires deciding what it means for `vllm` and `omlx`
too, since `outfit serve` already treats them as peers. That question turns
out to have three different answers per engine, which is exactly the
disambiguation this change exists to settle before writing any code:

- **llama.cpp**: `--parallel` (`-np`) sets the slot count, and `--ctx-size` is
  the *total* budget divided across them — the one engine where context and
  parallelism are coupled.
- **vLLM**: concurrency is governed by `--max-num-seqs` against a shared,
  dynamically-allocated KV-cache pool; `--max-model-len` is already a
  per-request ceiling, untouched by how many requests run at once.
  `--tensor-parallel-size`/`--pipeline-parallel-size` are GPU-topology
  settings (sharding a model across devices), not a slot count, and are
  out of scope here.
- **oMLX**: `--max-concurrent-requests` caps concurrency against its own
  paged KV cache; like vLLM, there is no context flag to rescale (`outfit`
  models no context flag for oMLX today, and that stays true).

## What Changes

- Add a `PARALLEL` Outfit keyword (and matching `Selection.Parallel` field):
  the number of concurrent request slots the Outfit wants from the served
  engine. Optional; unset behaves exactly as today (no `--parallel`-family
  flag emitted, `CONTEXT` maps to the engine's context flag unscaled).
- Give `CONTEXT` one fixed meaning everywhere it is used: the usable context
  per request. `outfit` derives whatever backend-specific total each engine
  actually needs to deliver that, instead of a user having to know each
  engine's own accounting.
- Per-engine translation in the shared params builders
  (`llamacppServeParams`/`vllmServeParams`/`omlxServeParams`
  in `cmd/outfit/serve.go`, reused by `outfit serve`, the daemon's pushed-config
  path, and fleet-node starts):
  - `llamacpp`: emit `--parallel <n>`, and — only when the Outfit's own
    `CONTEXT` is set — `--ctx-size` becomes `context_tokens * n` rather than
    `context_tokens`, so the per-slot result matches what `CONTEXT` promised.
  - `vllm`: emit `--max-num-seqs <n>`; `--max-model-len` is left exactly as
    `CONTEXT` states, unscaled.
  - `omlx`: emit `--max-concurrent-requests <n>`; still no context flag.
- Extend `outfit remote deploy`'s config derivation and the wire-level
  `DeployConfig` (Go and the TypeScript Lambda mirror) with the same field, so
  a cloud-deployed or fleet-woken `llamacpp`/`vllm` instance gets the same
  math as a local `outfit serve` — the whole point of fixing this in one
  shared place.
- Update `docs/outfit-file.md`, the README's Outfit example, and
  `docs/commands/serve.md` to state the per-engine mapping and the
  llama.cpp `ctx-size` scaling explicitly, so the disambiguation this change
  makes in code is also made in the docs a user actually reads.

## Non-Goals

- No change to `CONTEXT`'s meaning for the *harness* (opencode/Pi/lucinate
  config) — `limit.context` stays the plain token count a user asked for, on
  the model itself; only the local/cloud *server launch* math changes.
- No automatic `--cont-batching` toggling for llama.cpp, and no attempt to
  derive `--tensor-parallel-size`/`--pipeline-parallel-size` for vLLM from
  `PARALLEL` — those remain hand-set via `PRESET`, as today.
- `PARALLEL` gets no `outfit add`/`outfit remove` CLI flag. It has no meaning
  for a hosted-harness selection, only for a served engine, so it follows the
  same Outfit-file-only precedent as `PRESET`/`REMOTE`/`FLEET` rather than
  `CONTEXT`/`OUTPUT`.
- When `CONTEXT` is left to a `PRESET`'s own `ctx-size` rather than stated in
  the Outfit, `PARALLEL` does not retroactively rescale it — the preset author
  is assumed to already account for slots in their own value, exactly as
  today. Only an Outfit-stated `CONTEXT` gets the multiply.
- No change to how a cloud instance is provisioned or sized (instance type is
  fixed per environment, not derived from context); `PARALLEL` only changes
  the launch command a chosen instance runs.

## Impact

- Affected specs: `outfit-files` (new keyword), `local-serving` (per-engine
  translation), `inference-runners` (deploy-config plumbing for the cloud
  path).
- Affected code: `internal/outfit/outfit.go`, `cmd/outfit/serve.go`,
  `cmd/outfit/serve_daemon.go`, `cmd/outfit/remote.go`,
  `internal/remote/remote.go`, `remote/lambda/shared/deploy-config.ts`,
  `remote/lambda/runners/daemon-boot.ts`, `docs/outfit-file.md`,
  `docs/commands/serve.md`, `README.md`.
- No breaking change: `PARALLEL` is optional and every existing Outfit (no
  `PARALLEL` line) produces byte-identical commands to today.
