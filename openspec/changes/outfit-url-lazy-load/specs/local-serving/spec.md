## MODIFIED Requirements

### Requirement: Preset-based serving

With a `PRESET`, the referenced `.ini` SHALL supply the command: a relative
preset reference resolves against the Outfit's own source — a local directory
join when the Outfit was read from disk, URL-relative resolution when the
Outfit was fetched from a URL — so the pair can travel together either way. A
`PRESET` MAY itself be an absolute `http://`/`https://` URL regardless of
where the Outfit lives, in which case it is fetched over HTTP. Fetching a
remote `PRESET` SHALL happen only when a command that consumes it (`outfit
serve`, `outfit remote deploy`) actually builds its launch or deploy
configuration — never merely because the Outfit was read or applied. The
preset's `[*]`/`[global]` section holds shared defaults; each named section is
one model whose keys are server arguments with dashes stripped. The served
section is chosen by the Outfit's `ALIAS`, matched case-insensitively; a
preset with exactly one section is always served; no sections is an error;
several sections with no matching name SHALL fail listing the available
sections. Values the Outfit itself states SHALL override the preset's.

A preset SHALL be read in the flag vocabulary of the engine the `PROVIDER`
names, never in a vocabulary inferred from the file. A preset written for one
engine is therefore not portable to another: read in the wrong vocabulary it
would parse cleanly and produce a command with silently rewritten or dropped
flags.

#### Scenario: Outfit overrides the preset

- **WHEN** the preset section sets `ctx-size = 4096` and the Outfit says
  `CONTEXT 32768`
- **THEN** the command carries a context size of 32768

#### Scenario: Ambiguous preset

- **WHEN** the preset defines several sections and the Outfit's `ALIAS` matches
  none
- **THEN** the command fails listing the section names to choose from

#### Scenario: Another engine's preset keys are left alone

- **WHEN** an oMLX preset contains a key that llama.cpp would treat as a short
  alias
- **THEN** the key is rendered as written, not rewritten to llama.cpp's
  long-form flag

#### Scenario: A remote preset

- **WHEN** the Outfit sets `PRESET https://example.com/preset.ini`
- **THEN** `outfit serve` fetches that URL and serves the section its `ALIAS`
  selects

#### Scenario: A preset relative to a URL-sourced Outfit

- **WHEN** an Outfit fetched from `https://example.com/team/Outfit` sets
  `PRESET ./preset.ini`
- **THEN** `outfit serve` fetches `https://example.com/team/preset.ini`

#### Scenario: A preset is not fetched by commands that do not need it

- **WHEN** `outfit apply` runs against an Outfit whose `PRESET` is a URL
- **THEN** the preset URL is never fetched, matching how a local `PRESET` is
  already ignored by `apply`
