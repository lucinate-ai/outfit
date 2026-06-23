# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).


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
