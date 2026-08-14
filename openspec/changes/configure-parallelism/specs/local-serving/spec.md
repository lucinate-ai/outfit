## ADDED Requirements

### Requirement: Parallelism

`CONTEXT` SHALL always mean the context window a single request gets, whatever
engine serves it. An Outfit's `PARALLEL` SHALL set the number of concurrent
request slots, translated per engine so that `CONTEXT`'s meaning holds:

- For `llamacpp`, `PARALLEL n` SHALL be rendered as `--parallel n`. Because
  llama.cpp treats `--ctx-size` as a total budget divided across its parallel
  slots, when the Outfit **also** states `CONTEXT`, the rendered `--ctx-size`
  SHALL be `context_tokens * n` rather than `context_tokens`, so each slot
  still gets the context the Outfit asked for. `CONTEXT` set with no
  `PARALLEL` SHALL render `--ctx-size` as `context_tokens`, unscaled, exactly
  as before this capability existed.
- For `vllm`, `PARALLEL n` SHALL be rendered as `--max-num-seqs n`.
  `--max-model-len` (from `CONTEXT`) SHALL NOT be scaled by `PARALLEL`: vLLM's
  concurrency is bounded independently of a single request's context length.
- For `omlx`, `PARALLEL n` SHALL be rendered as `--max-concurrent-requests n`.
  oMLX has no context flag to scale either way.

An Outfit stating no `PARALLEL` SHALL produce a command identical to one from
before this capability existed, for all three engines. A `PARALLEL` value
SHALL be validated as a positive integer at the point it is used (`serve`, a
daemon-pushed config, or `remote deploy`); a value that is not SHALL fail
naming the invalid value rather than being passed to the engine.

When `CONTEXT` is supplied by a `PRESET` section's own `ctx-size` rather than
by the Outfit, `PARALLEL` SHALL NOT rescale it — only an Outfit-stated
`CONTEXT` participates in the `llamacpp` multiply. A `PARALLEL` value SHALL
still override a preset's own `np`/`parallel` (llama.cpp), `max-num-seqs`
(vLLM), or `max-concurrent-requests` (oMLX) value by the same
override-by-canonical-name rule `CONTEXT` already uses against a preset's
`ctx-size`.

#### Scenario: llama.cpp context is scaled by parallel slots

- **WHEN** an Outfit states `PROVIDER llamacpp`, `CONTEXT 128k`, and
  `PARALLEL 2`
- **THEN** the printed command includes `--ctx-size 256000` and `--parallel 2`

#### Scenario: llama.cpp parallel with no context set

- **WHEN** an Outfit states `PROVIDER llamacpp` and `PARALLEL 4` with no
  `CONTEXT`
- **THEN** the printed command includes `--parallel 4` and no `--ctx-size`
  flag is added

#### Scenario: llama.cpp context with no parallel set is unscaled

- **WHEN** an Outfit states `PROVIDER llamacpp` and `CONTEXT 128k` with no
  `PARALLEL`
- **THEN** the printed command includes `--ctx-size 128000`, exactly as it
  would have before `PARALLEL` existed

#### Scenario: A preset's own context is not rescaled

- **WHEN** an Outfit states `PROVIDER llamacpp`, `PARALLEL 2`, and a `PRESET`
  whose section sets `ctx-size` but the Outfit itself states no `CONTEXT`
- **THEN** the printed command includes `--parallel 2` and the preset's
  `ctx-size` value passes through unscaled

#### Scenario: vLLM concurrency does not touch context

- **WHEN** an Outfit states `PROVIDER vllm`, `CONTEXT 128k`, and `PARALLEL 4`
- **THEN** the printed command includes `--max-model-len 128000` and
  `--max-num-seqs 4`, with neither value derived from the other

#### Scenario: oMLX concurrency

- **WHEN** an Outfit states `PROVIDER omlx` and `PARALLEL 8`
- **THEN** the printed command includes `--max-concurrent-requests 8` and no
  context flag, exactly as with no `PARALLEL` stated

#### Scenario: No PARALLEL means no change

- **WHEN** an Outfit states no `PARALLEL`, for any of the three engines
- **THEN** the printed command is identical to what it would have been before
  this capability existed

#### Scenario: Invalid parallel count

- **WHEN** an Outfit states `PARALLEL 0`, a negative number, or a non-numeric
  value, for any of the three engines
- **THEN** the command fails naming the invalid value rather than passing it
  to the engine

#### Scenario: Outfit PARALLEL overrides a preset's own value

- **WHEN** an Outfit states `PROVIDER llamacpp` and `PARALLEL 2`, and its
  `PRESET` section separately sets `np = 4`
- **THEN** the printed command includes `--parallel 2`, not `--parallel 4`
