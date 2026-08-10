## Purpose

Define how outfit reads Hugging Face: the reference forms it accepts, the repo
metadata it fetches, the optional token it sends, the already-downloaded copies
it finds in the local caches, and the bounded, explained failures that keep a
slow or unreachable Hub from hanging a command.

## ADDED Requirements

### Requirement: Model reference forms

A Hugging Face model reference SHALL be accepted in every form a user is likely
to paste: a bare `org/model`, a `hf.co/` or `huggingface.co/` prefixed form, a
full `https://huggingface.co/org/model` URL including a trailing `/tree/<rev>`
or `/blob/<rev>/<file>` path, and any of those with llama.cpp's `:QUANT`
suffix or an `@<revision>` suffix. Parsing SHALL yield the repo id, the
revision (defaulting to `main`), and the quantisation or file when one was
named. A reference naming no organisation, or carrying both a `@revision` and a
`/tree/<rev>` that disagree, SHALL fail naming what was wrong with it.

#### Scenario: A pasted model page URL

- **WHEN** `https://huggingface.co/unsloth/Qwen3.6-35B-A3B-GGUF/tree/main` is
  parsed
- **THEN** it yields repo `unsloth/Qwen3.6-35B-A3B-GGUF` at revision `main`

#### Scenario: A quantisation suffix

- **WHEN** `unsloth/Qwen3.6-35B-A3B-GGUF:UD-Q4_K_XL` is parsed
- **THEN** it yields that repo with the quantisation `UD-Q4_K_XL`

#### Scenario: A reference that names no repo

- **WHEN** `qwen3.6` is parsed
- **THEN** it fails saying a reference is `org/model`, and shows an example

### Requirement: Reading a repo from the Hub

The system SHALL read a repo's published metadata over the Hub's API: the list
of files it holds, its tags and library, and its `config.json` where one
exists. The endpoint SHALL be overridable by the conventional `HF_ENDPOINT`
environment variable so a mirror or a private deployment can be used. Every
request SHALL carry a bounded timeout, so an unreachable or slow Hub cannot
hang a command.

Metadata SHALL be read without downloading any weights: a repo's file list and
`config.json` are enough to describe it, and reading a multi-gigabyte file to
learn what it is SHALL never happen.

#### Scenario: A repo's files and config are read

- **WHEN** metadata is read for a repo that publishes `config.json`
- **THEN** the file list and the config's contents are available, and no
  weights file was fetched

#### Scenario: A mirror endpoint is honoured

- **WHEN** `HF_ENDPOINT` names another host
- **THEN** metadata requests go to that host rather than `huggingface.co`

#### Scenario: An unreachable Hub does not hang

- **WHEN** the endpoint does not respond within the timeout
- **THEN** the read is abandoned and reported as a failure to reach the Hub

### Requirement: Optional Hugging Face token

A token SHALL be resolved, when one exists, from `HF_TOKEN`, then
`HUGGING_FACE_HUB_TOKEN`, then the token file the Hugging Face CLI writes under
its home directory. A resolved token SHALL be sent as the request's bearer
credential; with no token, requests SHALL be made unauthenticated and public
repos SHALL work exactly as before. A token SHALL NOT be written to any file
outfit produces, printed, or included in narration.

Where a repo is unreadable because it is private or gated, the failure SHALL
say which — and, when no token was resolved, SHALL say that setting `HF_TOKEN`
or logging in with the Hugging Face CLI is what makes it readable — rather than
reporting a bare HTTP status.

#### Scenario: No token is needed for a public repo

- **WHEN** metadata is read for a public repo with no token set anywhere
- **THEN** the request is made unauthenticated and succeeds

#### Scenario: A stored token is used

- **WHEN** no token variable is set but the Hugging Face CLI's token file holds
  one
- **THEN** that token is sent as the request's bearer credential

#### Scenario: A gated repo without a token

- **WHEN** a gated repo is read and no token was resolved
- **THEN** the failure says the repo is gated and how to authenticate

#### Scenario: The token stays out of everything written

- **WHEN** a command that resolved a token produces its output
- **THEN** neither the output, the written file, nor the narration contains it

### Requirement: Finding an already-downloaded copy

Before anything is fetched, the local caches SHALL be consulted for the
reference: the Hugging Face cache (`HF_HUB_CACHE`, else `HF_HOME`'s hub
directory, else the conventional `~/.cache/huggingface/hub`) in the layout the
Hugging Face libraries write, and llama.cpp's own model cache (`LLAMA_CACHE`,
else its conventional location) which is where `llama-server` puts what it
downloads itself. A file found SHALL be reported with its path on disk.

A cache entry SHALL count only when the file it names is genuinely present and
readable — a snapshot entry whose stored content is missing, and a
partly-downloaded file, SHALL be treated as not cached, so an interrupted
download is never mistaken for a model that is ready.

Where the cache holds the repo's metadata too, it SHALL be read from there, so
a model that is already downloaded resolves without a network request at all.

#### Scenario: A downloaded model is found

- **WHEN** the reference names a repo whose file is already in the Hugging Face
  cache
- **THEN** the file's path on disk is reported

#### Scenario: llama.cpp's own cache counts

- **WHEN** the model was downloaded by `llama-server` rather than by a Hugging
  Face tool
- **THEN** it is found in llama.cpp's cache and its path reported

#### Scenario: An interrupted download is not a cached model

- **WHEN** the cache holds only a partial file for the reference
- **THEN** the reference is reported as not cached

#### Scenario: A cached model needs no network

- **WHEN** the reference is fully present in the cache, including its metadata
- **THEN** it resolves with no request to the Hub

### Requirement: Failures name what went wrong

Reading Hugging Face SHALL fail with a message naming the cause and the repo:
a repo that does not exist, a repo that exists but is gated or private, a
network or timeout failure, and a response that cannot be understood SHALL each
be distinguishable. No failure SHALL surface as a bare status code or a raw
transport error.

#### Scenario: A repo that does not exist

- **WHEN** metadata is read for a repo the Hub does not have
- **THEN** the failure names the repo and says it was not found

#### Scenario: Offline

- **WHEN** metadata is read with no network available and nothing cached
- **THEN** the failure says the Hub could not be reached, naming the endpoint
