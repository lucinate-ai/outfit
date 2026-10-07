# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).


## [1.43.0] - 2026-10-07
### Added
- feat(metrics): report GPU utilisation on macOS
- feat(remote): start and stop environments on cron schedules
- feat(remote): warn when the control plane version differs (#257)
- feat(work): retry a failed item from the shell and the board
- feat(work): watch the work list on a live kanban board

### Changed
- build(deps): bump docker/setup-qemu-action in the github-actions group (#241)
- build(deps): bump the go-dependencies group across 1 directory with 11 updates
- build(deps): bump the go-dependencies group across 1 directory with 2 updates (#243)
- build(deps): bump the go-dependencies group across 1 directory with 9 updates
- docs(openspec): archive the scheduled-remote-start-stop change
- docs(openspec): archive the wake-lock change
- docs(openspec): end a start whose instance is stopped under it
- docs(openspec): propose a per-environment lock on remote starts
- docs(openspec): propose scheduled start and stop for remote environments
- docs(openspec): state the purpose of the remote-schedule capability
- docs(remote): document scheduled start and stop
- docs: drop the CNAME file the workflow deploy never reads
- refactor: rename the remote command group to cloud (#261)
- test(daemon): stop reading the stub engine's argv before it is written
- test(remote): cover schedule validation edges and failures after the list is stored

### Fixed
- fix(daemon): drain the engine's pseudo-terminal before closing it
- fix(daemon): present a captured engine's stdout as a pseudo-terminal
- fix(examples): build the docker fleets on Go 1.26 (#244)
- fix(gateway): refuse over-limit request bodies with 413
- fix(remote): give the schedule Lambda the control plane version
- fix(remote): serialise starts of one environment with a lock
- fix(remote): take the start lock on a scheduled start and scope the schedule grant
- fix: report no requests figure where the engine exposes none (#247)

## [1.42.0] - 2026-09-20
### Added
- feat: make metrics and logs top-level verbs

### Changed
- ci: let the first docs deploy enable GitHub Pages itself
- docs: build the site with MkDocs Material and publish to GitHub Pages
- docs: record the one-time Pages enablement the workflow cannot do

## [1.41.0] - 2026-09-19
### Added
- feat(orchestrator): print the work list on startup
- feat(remote): require a named environment, drop the implicit default
- feat: add spinloop work logs (#237)
- feat: make agent dispatch pluggable, add a docker backend
- feat: make status and dashboard top-level verbs
- feat: make the work commands clients of the work list API

### Changed
- docs: describe fleet.yaml (#215)

### Fixed
- fix(gateway): carry a remote node's served name into its wake stats

## [1.40.0] - 2026-09-14
### Added
- feat(fleet): target a registered environment with --env
- feat(gateway): let the gateway wake a deployed remote node
- feat: add code as a shortcut for harness open
- feat: add the work command family
- feat: converge fleet harness into code
- feat: serve the orchestrator's work list over HTTP
- feat: work a backlog of items against the fleet

### Changed
- chore(openspec): archive fleet-env-target and neutral-engine-config
- refactor: group the harness and provider commands (#210)
- refactor: move the deploy config into a neutral inference package

## [1.39.0] - 2026-09-13
### Added
- feat(harness): auto-configure launches from --env alone
- feat: let fleet harness launch against a gateway with no Spinloop (#201)

## [1.38.0] - 2026-09-13
### Added
- feat(hf): write a Spinloop from a Hugging Face reference
- feat(remote): add an instance type to remote deploys and fleet nodes
- feat: add the fleet gateway, a fleet's OpenAI-compatible front door
- feat: add the fleet harness command
- feat: drop the Spinloop's FLEET and route launches by fleet file
- feat: drop the Spinloop's REMOTE keyword and name environments with --env

### Changed
- docs(alias): make the URL form discoverable in the alias help
- docs: archive add-hf-spinloop-command and sync its two specs
- test(remote): make the expired-credentials log hint test hermetic

### Other
- perf: serve the daemon's host figures from the sampler

## [1.37.0] - 2026-09-08
### Added
- feat(daemon): silence successful API request summaries at default level (#183)
- feat(remote): add auth --store for a persistent control-plane credential
- feat: default the metrics formats to gauge

### Changed
- build(deps): bump pnpm/action-setup in the github-actions group
- build(deps): bump the go-dependencies group with 7 updates
- docs(openspec): archive daemon-log-level change
- docs: align Muse Glimmer model name
- test(fleet): pin the clock and settle the poll count in two tests

### Fixed
- fix(remote): guard start against seeding and count only alive seeds as in flight

## [1.36.0] - 2026-09-07
### Added
- feat(serve): show the engine's metrics and log in a full-screen view

## [1.35.0] - 2026-09-07
### Added
- feat(fleet): drop seconds from the keep figure
- feat(fleet): make dashboard key hints respect node state

### Changed
- docs(examples): use one shared key in the fleet examples
- docs: archive drop-seconds-from-keep-for and sync the remote-stats spec
- docs: archive state-aware-dashboard-keys and sync the fleet-client spec

### Fixed
- fix: cap the bar sparkline one grade short of full height

## [1.34.0] - 2026-09-06
### Added
- feat(fleet): set and show a keep deadline from the dashboard

## [1.33.0] - 2026-09-06
### Added
- feat: add spinloop up to start a directory's engine
- feat: add the sparkline bar format, rename bar to gauge

### Changed
- chore: bumps openspec commands and skills to 1.8.0
- docs: progressively disclose serve options
- docs: show a real fleet dashboard in the README

### Fixed
- fix: use a valid mtplx scheduler mode in the example preset

## [1.32.0] - 2026-09-04
### Added
- feat(fleet): rebuild the dashboard's start and status logic
- feat: add -f as the short form of the --fleet flag
- feat: add fleet deploy, and let start/stop take --all
- feat: add the mtplx engine for serving and fleet wake

### Changed
- build(deps): bump the go-dependencies group across 1 directory with 10 updates
- build(fleet-docker): bump the build image to golang 1.25
- docs: clarify scope in the introduction
- docs: list more harnesses

### Fixed
- fix(fleet): retire a start's capacity wait once the next attempt goes out

## [1.31.1] - 2026-09-03
### Fixed
- fix(remote): grant the deploy role secretsmanager:PutSecretValue

## [1.31.0] - 2026-09-03
### Added
- feat(remote): create or rotate an environment's engine key

## [1.30.1] - 2026-09-03
### Fixed
- fix(remote): share the follow cursor between remote logs and the fleet dashboard

## [1.30.0] - 2026-09-02
### Added
- feat(remote): split the AMI bake out of bootstrap into its own command
- feat: grey the dashboard dot for undeployed nodes

### Changed
- docs: deduplicate AGENTS.md content vs specs
- test: fix the fleet integration suite's flaky waits

### Fixed
- fix(remote): let the image recipe survive a new base AMI
- fix(remote): report a boot that died instead of waiting it out

## [1.29.1] - 2026-09-01
### Changed
- docs: cover fleet routing, seeding and the fleet file's newer fields
- docs: lead the readme with orchestration and an install line
- docs: name the inference engines in the readme's strapline
- docs: say what spinloop spans in the readme's strapline

### Other
- perf: remove the page-cache pre-warm after the live check (#136)

## [1.29.0] - 2026-08-31
### Added
- feat: show dashboard node health, backed by real engine readiness

### Changed
- chore: align package name with product

## [1.28.0] - 2026-08-24
### Added
- feat(daemon): log the spinloop version at startup
- feat(remote): install spinloop at boot instead of baking it into the AMI

### Fixed
- fix(remote): zero-byte seed files and vLLM attention backend

## [1.27.0] - 2026-08-24
### Added
- feat(fleet): add a full-screen node detail view to the dashboard

### Changed
- docs(examples): add a llama.cpp example for Muse-Glimmer-30B (#79)

### Fixed
- fix: move dashboard selection by grid row and column
- fix: move node uptime to dashboard tile's state line (#129)

## [1.26.1] - 2026-08-23
### Fixed
- fix(fleet): show a node's report beside an in-flight action

## [1.26.0] - 2026-08-23
### Added
- feat(fleet): add interactive fleet dashboard

### Other
- perf: prewarm model loads and provision gp3 throughput (#121)

## [1.25.0] - 2026-08-22
### Added
- feat(fleet): drive a remote environment as a fleet node
- feat(remote): add spinloop remote restart with a forced stop option
- feat(remote): rework model weight seeding into a supervised job

### Changed
- chore(openspec): archive converge-serving-argv
- chore(openspec): archive unify-remote-fleet-node
- refactor(serve): converge engine argv assembly on one builder

## [1.24.2] - 2026-08-21
### Fixed
- fix(remote): pass spinloopVersion when bootstrapping a release build

## [1.24.1] - 2026-08-21
### Changed
- docs: correct remote log path
- refactor: migrate the CLI to cobra and viper

### Fixed
- fix(remote): relay daemon version through the stats Lambda

## [1.24.0] - 2026-08-20
### Added
- feat(remote): add keep subcommand and start --keep flag
- feat(remote): tiered idle shutdown and remote pause

### Changed
- build(deps): bump the go-dependencies group with 8 updates
- docs(openspec): archive start-heartbeat-in-flight and sync its spec
- docs(openspec): archive tiered-idle-shutdown and graceful-instance-stop changes

### Fixed
- fix(remote): report in-flight start attempts to the heartbeat

## [1.23.0] - 2026-08-17
### Added
- feat(remote): carry companion weights and add deploy --reseed
- feat: add --loopback shorthand to spinloop daemon
- feat: allow an spinloop path, alias, PRESET, and REMOTE to be a URL
- feat: configure parallelism in spinloop files

## [1.22.0] - 2026-08-15
### Added
- feat(examples): add Qwen3.8-27B on llama.cpp, deployable to AWS (#96)
- feat: add version field to daemon status response

### Changed
- build(deps): bump the go-dependencies group across 1 directory with 2 updates

## [1.21.0] - 2026-08-13
### Added
- feat(daemon): summarise every control-API request on a levelled logger
- feat(daemon): take the engine key from the client, read no spinloop
- feat(fleet): route a harness launch at a fleet node (#73)

### Changed
- chore: archive add-daemon-request-logging and sync its spec
- chore: archive add-fleet-logs-command and sync its specs
- chore: archive client-driven-daemon and sync its specs
- chore: archive fleet-harness-routing and sync its specs
- chore: ignore the binary the fleet example's tests build
- docs: warn on merging un-archived changes
- test(fleet): fix the data race in the follow-logs tests

## [1.20.0] - 2026-08-11
### Added
- feat(fleet): add spinloop fleet logs and a daemon logs endpoint

### Changed
- chore(remote): bump image recipe to 3.3.3 for spinloop 1.19.0 (#84)
- chore: archive the remote logs change and sync its spec (#85)

## [1.19.0] - 2026-08-10
### Added
- feat(remote): add spinloop remote logs to read an environment's shipped logs (#60)
- feat: let spinloop_ALIAS name the spinloop every command defaults to
- feat: report last-active time from the metrics views and remote status (#82)

### Changed
- chore(remote): bump image recipe to 3.3.2 for spinloop 1.18.0 (#71)
- docs(openspec): correct stale remote stats references to metrics

## [1.18.0] - 2026-08-10
### Added
- feat: add spinloop fleet to observe and drive engines across machines (#69)

### Changed
- build(deps): bump pnpm/action-setup
- build(deps): bump the go-dependencies group across 1 directory with 6 updates
- ci: fail when a spec's Purpose is a placeholder
- docs(openspec): fix spec validation, fill in placeholder purposes, gate it in CI
- docs(openspec): sync the lucinate harness specs and archive the change (#70)

### Fixed
- fix(harness): fetch the remote API key before applying an spinloop

## [1.17.0] - 2026-08-10
### Added
- feat(daemon): own idle detection, and publish an API contract (#64)
- feat: add lucinate harness

### Changed
- build(deps): bump the github-actions group with 2 updates
- build(deps): bump the go-dependencies group with 2 updates
- build: add opencode config for openspec
- refactor: rename 'shared infrastructure' to 'control plane'

### Fixed
- fix(remote): bump image recipe to 3.3.1 and refuse an empty spinloopVersion

## [1.16.0] - 2026-08-09
### Added
- feat: add spinloop_CONFIG_DIR to override spinloop's config directory

## [1.15.0] - 2026-08-09
### Added
- feat(remote): ship engine and boot logs to CloudWatch (#44)
- feat: add spinloop daemon with control API and host the remote instance under it

## [1.14.0] - 2026-08-04
### Added
- feat(remote): add bar graph metrics format and clear screen in watch mode
- feat(remote): warn when start succeeds but endpoint is unreachable (#43)

### Changed
- chore: archive remote-env-command change
- chore: sync specs and archive bar-metrics-format change
- docs: add openspec artifacts for bar-metrics-format change
- docs: add remote metrics screenshot
- docs: sync remote-env-command delta specs to main
- refactor(remote): accept io.Writer in metrics formatters and restore buffered watch

### Fixed
- fix(remote): invoke package.json scripts via run in bootstrap

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
- feat: add spinloop remote stats command
- feat: label a remote harness provider distinctly from the local engine
- feat: name the harness provider after the remote environment
- feat: respect the spinloop's local environment in remote commands

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
- feat: add spinloop remote deploy
- feat: add remote command to control the cloud GPU instance
- feat: bring the cloud GPU deployment into the repo as remote/
- feat: find the remote config via an spinloop REMOTE instruction
- feat: report progress while the remote endpoint starts
- feat: resolve API keys without writing secrets to disk

### Changed
- build(deps): bump actions/setup-go in the github-actions group
- chore: initialise OpenSpec and seed capability specs
- ci: run the remote deployment's checks in their own workflow
- docs: document spinloop remote for users, and spec the change
- docs: restructure docs directory as a user manual
- docs: specify the deployment and archive the change

### Fixed
- fix: wire an API key through the llamacpp provider

## [1.8.0] - 2026-07-28
### Added
- feat: name an spinloop with alias/unalias and complete it with TAB (#18)

## [1.7.0] - 2026-07-27
### Added
- feat(cli): apply an spinloop before launching from `spinloop harness` (#17)

## [1.6.0] - 2026-07-14
### Added
- feat(cli): accept a directory as an spinloop path

### Changed
- docs: harness demo line, declarative wording, backticked spinloop
- docs: rewrite README to lead with why, not how (#14)

## [1.5.0] - 2026-06-27
### Added
- feat(catalog): add Pi provider config and BuildPiProvider
- feat(cli): add show command to report a harness's configured providers
- feat(cli): launch the harness from `spinloop harness`
- feat(cli): route commands through the active harness
- feat: add internal/harness abstraction with opencode and pi adapters
- feat: add internal/pi package for Pi models.json IO
- feat: add unapply command to revert an spinloop

### Changed
- docs: document multi-harness support
- docs: update AGENTS.md for the harness abstraction

## [1.4.0] - 2026-06-25
### Added
- feat: add serve command to run llama-server from a preset
- feat: let spinloop values override the preset under serve
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
- feat: add declarative spinloop files for provider config
- feat: allow API base URL override via flag or env var (#1)

### Changed
- docs: add Homebrew installation instructions

### Other
- refactor!: rename tool, binary and module to spinloop

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
