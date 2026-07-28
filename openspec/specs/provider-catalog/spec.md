# Provider Catalog Specification

## Purpose

Define the catalogue of model providers and model families that `outfit` can
configure: where the catalogue comes from, what it declares, how users inspect
it (`outfit list`), and how they replace it with their own
(`outfit init-providers`, `--providers`, `OUTFIT_PROVIDERS`).

## Requirements

### Requirement: Embedded provider catalogue

The system SHALL ship with a built-in catalogue of providers and model
families, defined in a YAML file (`providers.yaml`) embedded into the binary at
build time. Each provider entry declares a description and MAY declare a
display name, an npm package, an API key environment variable (with optional
required flag and expected key prefix), static options (such as `baseURL`),
options resolved from environment variables, a `pi` block marking the provider
as usable by the Pi harness, and named model families. Each family declares a
description, a default model, and a set of models.

#### Scenario: Catalogue loads without external files

- **WHEN** any command that needs the catalogue runs with no `--providers` flag
  and no `OUTFIT_PROVIDERS` environment variable
- **THEN** the embedded catalogue is used, with no file read from disk

#### Scenario: Family default model is always a member of the family

- **WHEN** the catalogue defines a model family
- **THEN** that family's `defaultModel` is one of the family's `models` keys

### Requirement: Catalogue listing

`outfit list` SHALL print every provider in the catalogue in stable
(alphabetical) order, showing for each: its id and description, its API key
environment variable (marked `(required)` when the key is mandatory), the
harnesses that support it (`opencode`, plus `pi` when the provider has a `pi`
block), and each model family with its description and default model.

#### Scenario: Listing the built-in catalogue

- **WHEN** the user runs `outfit list`
- **THEN** every embedded provider is printed with its families, key
  requirements, and supported harnesses

### Requirement: Runtime catalogue override

The system SHALL let the user substitute their own catalogue file at runtime,
resolved with the precedence: `--providers` flag, then the `OUTFIT_PROVIDERS`
environment variable, then the embedded catalogue.

#### Scenario: Flag wins over environment variable

- **WHEN** both `--providers ./mine.yaml` and `OUTFIT_PROVIDERS=./other.yaml`
  are set
- **THEN** the catalogue is loaded from `./mine.yaml`

#### Scenario: Unreadable override is an error

- **WHEN** the resolved catalogue path cannot be read or parsed as YAML
- **THEN** the command fails with an error naming the file

### Requirement: Catalogue scaffolding

`outfit init-providers [path]` SHALL write a copy of the embedded catalogue to
`./providers.yaml` (or the given path) as a starting point for customisation,
and SHALL refuse to overwrite an existing file unless `--force`/`-F` is given.
On success it SHALL print how to point `outfit` at the written file.

#### Scenario: Refuses to clobber an existing file

- **WHEN** the user runs `outfit init-providers` and `./providers.yaml` already
  exists
- **THEN** the command fails, telling the user to pass a different path or
  `--force`

#### Scenario: Writing the catalogue out

- **WHEN** the user runs `outfit init-providers custom.yaml` and no such file
  exists
- **THEN** the embedded catalogue is written to `custom.yaml` byte-for-byte
