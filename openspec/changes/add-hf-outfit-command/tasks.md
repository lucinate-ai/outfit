## 1. Reference parsing

- [ ] 1.1 Create `internal/hf` (stdlib only, plus `internal/contextsize`) with a
  `Ref` type holding repo owner, name, revision, and an optional quant or file.
- [ ] 1.2 Implement `ParseRef`: bare `org/model`, `hf.co/`/`huggingface.co/`
  prefixes, full `https://huggingface.co/...` URLs with `/tree/<rev>` and
  `/blob/<rev>/<file>`, `:QUANT` suffixes, and `@<revision>` suffixes.
- [ ] 1.3 Reject a reference naming no organisation, and one whose `@revision`
  contradicts a `/tree/<rev>`, with messages naming what was wrong.
- [ ] 1.4 Table-driven tests covering every accepted form and each rejection.

## 2. Hub client

- [ ] 2.1 Resolve the endpoint from `HF_ENDPOINT`, defaulting to
  `https://huggingface.co`; give the client a bounded timeout and no retries,
  following `internal/discovery`'s shape.
- [ ] 2.2 Fetch repo metadata (`/api/models/{repo}/revision/{rev}`), reading
  `siblings[].rfilename`, `tags`, `library_name` and `sha`.
- [ ] 2.3 Fetch `config.json` via `/{repo}/resolve/{rev}/config.json` and read
  `max_position_embeddings`, falling back to
  `text_config.max_position_embeddings`; a missing file or field means "no
  declared window", not an error.
- [ ] 2.4 Resolve an optional token: `HF_TOKEN`, then `HUGGING_FACE_HUB_TOKEN`,
  then `$HF_HOME/token` (else `~/.cache/huggingface/token`); send it as a bearer
  only when one was found.
- [ ] 2.5 Map failures to distinct, named errors: not found, gated/private (with
  the authenticate-or-set-`HF_TOKEN` hint when no token was resolved), rate
  limited, unreachable/timed out, and unparseable — each naming the repo and,
  for transport failures, the endpoint.
- [ ] 2.6 Tests against an `httptest` server with `HF_ENDPOINT` pointed at it:
  metadata parsing, config parsing and its fallbacks, each failure mapping, the
  bearer header sent only when a token resolves, and the token absent from every
  produced value.

## 3. Cache lookup

- [ ] 3.1 Resolve cache roots in one function — `HF_HUB_CACHE`, else
  `$HF_HOME/hub`, else `~/.cache/huggingface/hub`; `LLAMA_CACHE`, else the
  platform cache dir — and pass them into every lookup as arguments rather than
  reading the environment at the point of use.
- [ ] 3.2 Look a ref up in the Hugging Face cache:
  `models--{owner}--{name}/refs/{rev}` for the sha, then
  `snapshots/{sha}/{path}`; count an entry only when the link resolves to a
  readable regular file, so a dangling link or a `.incomplete` blob reads as not
  cached.
- [ ] 3.3 Read the cached `config.json` from the snapshot when present, so a
  fully cached model resolves with no network request.
- [ ] 3.4 Look a chosen quant up in llama.cpp's cache: a case-insensitive scan
  for a `.gguf` whose name carries the repo owner, the repo name and the quant —
  all three, so a near-match never produces a wrong path.
- [ ] 3.5 Tests over temp-dir caches: a hit in each cache, a dangling snapshot
  link, an `.incomplete` blob, a near-miss llama.cpp filename that must not
  match, and the no-network path for a fully cached model.

## 4. Inference rules

- [ ] 4.1 Infer the provider: any `.gguf` → `llamacpp`; else `library_name`/tag
  `mlx` → `omlx`; else any `.safetensors` → `vllm`; else an error naming what the
  repo appears to hold and the providers that can be inferred.
- [ ] 4.2 Parse quant names from the file list: strip `.gguf` and any
  `-000NN-of-000NN` shard suffix, match the quant token
  (`IQ*`/`Q*_*`/`UD-*`/`F16`/`BF16`/`F32`/`MXFP4`), prefer a directory level
  where the repo uses one, and group shards into one choice.
- [ ] 4.3 Select the quant: reference suffix, then `-q`, then the documented
  preference order (`Q4_K_M`, `UD-Q4_K_XL`, `Q4_K_S`, `Q5_K_M`, `Q6_K`, `Q8_0`),
  then the smallest non-full-precision group with the name as tie-break. Match
  case-insensitively; an unmatched name fails listing what the repo offers.
- [ ] 4.4 Derive the alias from the repo name: lower-cased, with a trailing
  `-GGUF`/`-MLX` packaging suffix removed.
- [ ] 4.5 Build the `outfit.Selection`: provider, the `MODEL` per the design's
  engine table (cached path for `llamacpp`, repo reference otherwise), alias,
  context from the declared window, and no `OUTPUT`.
- [ ] 4.6 Return the reasoning alongside the selection (provider and why, quant
  chosen and alternatives, context and its source, cache hit and which cache) as
  data, so the command formats it and tests assert on it.
- [ ] 4.7 Tests for each inference rule, including a repo carrying both GGUF and
  safetensors, a sharded quant, a repo declaring no window, and a repo matching
  no provider.

## 5. The command

- [ ] 5.1 Add `cmd/outfit/hf.go` with `cmdHF`: flags `-p/--provider`,
  `-q/--quant`, `-c/--context`, `-a/--alias`, `-o/--output-file`, `--force`,
  `--no-cache`, `--apply`, `-H/--harness`; no output-tokens flag. Register
  `case "hf":` in `run`'s switch in `main.go` (the command body stays out of
  `main.go` so the dispatch-coverage scan sees only that switch).
- [ ] 5.2 Render the selection with `outfit.Format` to stdout; write the
  reasoning to stderr.
- [ ] 5.3 Implement `-o`: write the rendered Outfit to the path, report where it
  went, and refuse to overwrite an existing file unless `--force` is given.
- [ ] 5.4 Implement `--apply`: route the resolved selection through
  `applySelection`, honouring `--harness`/`-H`.
- [ ] 5.5 Fail with usage when no reference is given.
- [ ] 5.6 Tests in `cmd/outfit/hf_test.go` against a stub Hub and temp caches:
  the printed Outfit round-trips through `outfit.Parse`, `-o` writes and refuses
  to clobber, `--force` overwrites, `--apply` configures the harness, `--no-cache`
  forces the repo form, and stdout stays free of narration.

## 6. Completion, docs and checks

- [ ] 6.1 Add `hf` to the `commands` table in `cmd/outfit/complete.go` with its
  flags, provider-name candidates for `-p`, and a file directive for `-o`; check
  `TestCompletionCoversDispatch` passes.
- [ ] 6.2 Write `docs/commands/hf.md`: the reference forms, each inference and
  how to override it, the quant preference order, cache behaviour and
  `--no-cache`, token setup, and an explicit note that `-o` here is the output
  *file* (unlike `add`/`apply`, where it is max output tokens).
- [ ] 6.3 Link the new page from `docs/README.md`, document `HF_TOKEN`,
  `HUGGING_FACE_HUB_TOKEN`, `HF_HOME`, `HF_HUB_CACHE`, `HF_ENDPOINT` and
  `LLAMA_CACHE` in `docs/env-vars.md`, and add a quickstart line to the root
  `README.md`.
- [ ] 6.4 Add an `internal/hf` entry and a `cmd/outfit/hf.go` note to AGENTS.md's
  Layout, plus a Traps entry for the two caches being separate (a model
  downloaded by `llama-server` is not in the Hugging Face cache, and vice versa).
- [ ] 6.5 Run `gofmt -w ./...`, `go vet ./...` and `go test ./... -cover`,
  keeping total coverage at or above 80%.
