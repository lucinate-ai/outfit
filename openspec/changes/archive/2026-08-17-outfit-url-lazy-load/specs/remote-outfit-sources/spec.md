## Purpose

Define the shared mechanism for resolving and fetching an Outfit-family
reference — the Outfit file itself, a `PRESET`, or a path-form `REMOTE` — that
may name either a local path or an `http(s)` URL: how a relative reference is
joined against the reference that named it, the bounds placed on a network
fetch, and the guarantee that a reference is only ever fetched at the point a
command actually consumes it, never as a side effect of resolving or parsing
one.

## ADDED Requirements

### Requirement: A path or a URL, distinguished by prefix

An Outfit-family reference SHALL be treated as a URL when it begins with
`http://` or `https://`, and as a local path otherwise. No other syntax
(bare host names, `//`-relative references, other schemes) SHALL be
recognized as a URL.

#### Scenario: A URL reference

- **WHEN** a reference begins with `https://`
- **THEN** it is fetched over HTTP rather than read from local disk

#### Scenario: A local reference

- **WHEN** a reference does not begin with `http://` or `https://`
- **THEN** it is treated as a local filesystem path, exactly as before this
  capability existed

### Requirement: Relative-reference resolution

A relative reference SHALL resolve against the reference that named it,
matching the kind of that base reference: a relative reference under a local
base SHALL resolve as a filesystem path joined against the base's own
directory; a relative reference under a URL base SHALL resolve the way a
relative link resolves against a base document (the base's own last path
segment is dropped, exactly as `net/url`'s reference resolution defines).
A reference that is already absolute — an absolute local path, or a URL —
SHALL be used unchanged regardless of what named it, so a local Outfit MAY
name a `PRESET` or `REMOTE` that is itself a URL, and a URL-sourced Outfit MAY
name one that is an absolute local path.

#### Scenario: Relative reference under a local base

- **WHEN** a local Outfit at `/home/user/proj/Outfit` names `PRESET
  ./preset.ini`
- **THEN** the preset resolves to `/home/user/proj/preset.ini`

#### Scenario: Relative reference under a URL base

- **WHEN** an Outfit fetched from `https://example.com/team/Outfit` names
  `PRESET ./preset.ini`
- **THEN** the preset resolves to `https://example.com/team/preset.ini`

#### Scenario: Absolute URL reference regardless of base

- **WHEN** a local Outfit names `PRESET https://example.com/preset.ini`
- **THEN** the preset resolves to that URL unchanged, and is fetched over HTTP

#### Scenario: Absolute local reference under a URL base

- **WHEN** an Outfit fetched from a URL names `PRESET /opt/shared/preset.ini`
- **THEN** the preset resolves to that local path unchanged, and is read from
  local disk

### Requirement: Fetching is bounded

A network fetch of an Outfit-family reference SHALL apply a fixed request
timeout and a fixed cap on the response body size, failing with a clear error
rather than hanging or buffering an unbounded response. A non-2xx HTTP status
SHALL fail naming the URL and the status received.

#### Scenario: An unreachable host times out

- **WHEN** a referenced URL's host does not respond within the fixed timeout
- **THEN** the fetch fails with a timeout error naming the URL

#### Scenario: A non-2xx response

- **WHEN** a referenced URL responds with a 404 or other non-2xx status
- **THEN** the fetch fails naming the URL and the status received

#### Scenario: An oversized response

- **WHEN** a referenced URL's response body exceeds the fixed size cap
- **THEN** the fetch fails rather than reading the full body into memory

### Requirement: Fetching happens only at the point of use

Resolving or parsing an Outfit SHALL NOT, by itself, fetch anything a `PRESET`
or path-form `REMOTE` reference names. Each SHALL be fetched only when a
command that actually consumes it does so, at the same point in the command's
existing flow that a local-path reference would be read from disk.

#### Scenario: Reading an Outfit does not fetch its PRESET

- **WHEN** an Outfit naming a `PRESET` (local or remote) is parsed for any
  purpose, including `outfit apply`, which never consumes `PRESET`
- **THEN** the `PRESET` reference is not fetched

#### Scenario: A command fetches only the reference it needs

- **WHEN** `outfit serve` runs against an Outfit whose `PRESET` is a URL and
  whose `REMOTE` is also a URL
- **THEN** only the `PRESET` is fetched; the `REMOTE` reference is left
  untouched
