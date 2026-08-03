# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).


## [1.13.0] - 2026-08-03
### Added
- feat(remote): rename stats to metrics, add format and watch flags
- feat: add remote env command and inject env vars in harness (#34)
- feat: make remote start heartbeat state-aware and add -t timeout alias (#36)
- feat: support npm or pnpm for the remote bootstrap step (#37)
- feat: unify local-environment precedence and extend ENV to harness (#35)

### Changed
- chore: sync local-environment specs and archive the remote-respect-local-env change
- docs: include remote stats in the local-environment spec
- docs: note the remote commands accept an alias in the alias-registry spec

### Fixed
- fix(remote): convert nvidia-smi MiB to bytes in GPU stats parser
- fix(remote): read API key from file for llamacpp metrics scrape
- fix: surface expired AWS credentials in remote commands (#33)

## [1.12.0] - 2026-08-03
### Added
- feat: add outfit remote stats command
- feat: label a remote harness provider distinctly from the local engine
- feat: name the harness provider after the remote environment
- feat: respect the Outfit's local environment in remote commands

### Changed
- chore: sync remote environment spec and archive the harness-provider-label change
- chore: sync remote environment spec and archive the harness-provider-name change

## [1.11.0] - 2026-08-02
### Added
- feat: add qwen3.6 27B example
- feat: take the remote endpoint's base URL from remote.json
- feat: two-layer remote provisioning with per-env endpoints

## [1.10.0] - 2026-08-01
### Added
- feat: add GCP Vertex AI providers
- feat: add live per-provider model discovery
- feat: add oMLX as a provider and a serve engine (#24)
- feat: retire model families from the provider catalogue

### Changed
- chore: archive add-omlx-provider change
- chore: archive live-model-discovery and fix-pi-option-resolution changes
- chore: mark add-omlx-provider validation task complete
- docs: list supported providers early in the README

### Fixed
- fix: share option resolution between the opencode and Pi builders (#26)

## [1.9.0] - 2026-07-29
### Added
- feat: add a vllm provider to the catalogue
- feat: add outfit remote deploy
- feat: add remote command to control the cloud GPU instance
- feat: bring the cloud GPU deployment into the repo as remote/
- feat: find the remote config via an Outfit REMOTE instruction
- feat: report progress while the remote endpoint starts
- feat: resolve API keys without writing secrets to disk

### Changed
- build(deps): bump actions/setup-go in the github-actions group
- chore: initialise OpenSpec and seed capability specs
- ci: run the remote deployment's checks in their own workflow
- docs: document outfit remote for users, and spec the change
- docs: restructure docs directory as a user manual
- docs: specify the deployment and archive the change

### Fixed
- fix: wire an API key through the llamacpp provider

## [1.8.0] - 2026-07-28
### Added
- feat: name an Outfit with alias/unalias and complete it with TAB (#18)

## [1.7.0] - 2026-07-27
### Added
- feat(cli): apply an Outfit before launching from `outfit harness` (#17)

## [1.6.0] - 2026-07-14
### Added
- feat(cli): accept a directory as an Outfit path

### Changed
- docs: harness demo line, declarative wording, backticked Outfit
- docs: rewrite README to lead with why, not how (#14)

## [1.5.0] - 2026-06-27
### Added
- feat(catalog): add Pi provider config and BuildPiProvider
- feat(cli): add show command to report a harness's configured providers
- feat(cli): launch the harness from `outfit harness`
- feat(cli): route commands through the active harness
- feat: add internal/harness abstraction with opencode and pi adapters
- feat: add internal/pi package for Pi models.json IO
- feat: add unapply command to revert an Outfit

### Changed
- docs: document multi-harness support
- docs: update AGENTS.md for the harness abstraction

## [1.4.0] - 2026-06-25
### Added
- feat: add serve command to run llama-server from a preset
- feat: let Outfit values override the preset under serve
- feat: make MODEL provider-native and add ALIAS; derive serve without a preset

## [1.3.0] - 2026-06-24
### Added
- feat: set limit.output when a model context is configured (#10)

## [1.2.0] - 2026-06-24
### Added
- feat: add init-providers command to scaffold the catalogue
- feat: use -u as the short flag for --base-url

### Changed
- build: configure since

## [1.1.0] - 2026-06-24
### Added
- feat: add --version flag

### Changed
- ci: add dependabot config with grouped updates
- ci: run goreleaser in dry-run mode on non-release builds
- refactor: organise code into cmd/ and internal/ packages

## [1.0.1] - 2026-06-24
### Changed
- ci: upgrade checkout to v7 and setup-go to v6

### Fixed
- fix: publish Homebrew formula into Formula/ directory

## [1.0.0] - 2026-06-23
### Added
- feat: add --context flag to set model context window (#2)
- feat: add declarative Outfit files for provider config
- feat: allow API base URL override via flag or env var (#1)

### Changed
- docs: add Homebrew installation instructions

### Other
- refactor!: rename tool, binary and module to outfit

## [0.2.0] - 2026-06-22
### Added
- feat: allow the provider catalogue to be overridden at runtime

### Changed
- build: add Makefile with build, test, and coverage targets
- ci: publish binary to Homebrew tap on release
- docs: add llama.cpp Qwen3.6 guide and link from README
- docs: add llama.cpp guide for Gemma-4 with MTP
- docs: adds changelog

## [0.1.0] - 2026-06-21
### Added
- feat: add opencode OpenRouter DeepSeek V4 config script
- feat: generalise oc-config into a multi-provider opencode configurator

### Changed
- ci: add test/build and tag-driven release workflows
- docs: add README and AGENTS.md
- refactor: rewrite opencode config tool in Go with JSONC merge

### Other
- Initial commit
