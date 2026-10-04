# Changelog

All notable changes to pikopod. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and releases are tagged `vX.Y.Z` on GitHub.

## [Unreleased]

## [0.2.0] - 2026-10-04

### Added

- `pikopod rule list|add|drop|check`: rules that answer a declared operation when the request or the stored state looks a certain way, validated against the spec, with provenance and versions; `requests --explain` names the rule that fired. (#99, #107)
- True response bodies: spec `example` and `default` constraints, `readOnly`, naming conventions, request echo, and the realism line at import. (#100)
- Documentation-URL import keeps every reference page, retries empty pages alone, and refuses endpoints no reference page defines. (#103)
- `pikopod agent truthfulness <upstream>`: how much of what the provider really sent the sandbox reproduces, leaf by leaf, with the worst fields and their sources; also in `agent status` and at the end of `pikopod demo`. (#109)
- Recordings answer the sandbox before the spec: stored state, then a matching recording, then synthesis; `--recordings first|fallback|off`; every recorded field is traced and the response carries `x-pikopod-source: recorded`. (#110)
- Divergence: every production exchange is replayed through a fork of the linked sandbox and a `behaviour_divergence` event with a category (`spec_drift`, `undocumented_rule`, `provider_failure`) fires on the first difference, with no warmup; `agent incidents --only divergence`; `reproduce` accepts it. (#113)
- Inbound webhook tap: `upstreams.<name>.webhooks.receiver`; the agent serves `/hooks/<name>`, forwards untouched, records redacted, and reports `webhook_duplicate` and `webhook_out_of_order`. (#117)
- Day zero: `pikopod up --spec` with no config file, seed data (`--seed-data`, `sandbox seed`), snapshot, restore and reset on the control plane, and per-test forks. (#118)
- `pkg/sandboxtest`: start a sandbox inside a Go test, set a mode, run your code, verify. (#119)
- Versioned control plane at `/_pikopod/v1/...`, examples generated from a test, and dependency-free Jest and pytest helpers. (#120)
- `pikopod rule promote <fingerprint>`: a divergence becomes a rule the sandbox keeps, after two questions. (#122)
- `pikopod agent incidents import <bundle>`: the event, a rule that answers as the provider did, and the resource the recording touched, installed locally; bundles carry the resource's last state. (#123)
- GitHub Actions `Pikopod/spec-diff-action@v1` and `Pikopod/spec-diff-action/scenario-check@v1`, with the release verified by cosign before it runs. (#121)
- Modes verify what the client sent (`mode verify`), faults carry declared bodies, transport faults, CLI sections. (#77, #78, #79, #80)
- `scenario list --format json`; `VERIFY_SEQUENCE` matchers counted in run summaries. (#105, #106)

### Fixed

- Tokens keep a digit when the value had one, so a tokenized identifier still templatizes and the behaviour tracker, baselines and replay keys never lose it. (#112)
- Armed faults match the template the agent learned and concrete paths, so reproducing an incident on an item route works. (#123)
- WARN-level divergence and webhook incidents render as WARN; the alert trailer names `pikopod reproduce`. (#113, #117)

### Changed

- `--recordings-fallback` is replaced by `--recordings first|fallback|off`; registries with the old flag load as `fallback`. (#110)
- The README leads with the outcome; the sandbox is described as corrected by what the provider actually sends. (#114)
- Every comment was removed from the Go source; the token stream of every file is unchanged. The house rule in `DEVELOPMENT.md` is now "no comments in Go code". (#62)

## [0.1.2] - 2026-09-24

### Added

- `pikopod mcp`: the checks and sandbox controls served to a coding agent over the Model Context Protocol, with `CLEAN`, `FINDINGS`, `UNVERIFIABLE` and `ERROR` verdicts. (#51)
- `pikopod spec-diff --format githubactions`: findings appear inline on the pull request diff, and same-repository relative `$ref`s resolve for file and `git:` sources. (#56)
- Documentation-URL import: `--emit-spec` writes the spec the import used, the crawl is budgeted by page group, and a `--webhooks` sidecar binds events to the calls that fire them. (#44)
- Webhook deliveries in the provider's declared envelope, wrapped and signed the way the provider documents, with the key read from the environment. (#39, #40)
- Emit-only webhook events, `pikopod webhook emit`, and the rule that only declared events are ever delivered. (#38)
- Spec examples serve responses, and create, read and modify responses follow the spec's envelope. (#42)
- `--bind role=operationId` grounds an archetype on a docs import by asserting the missing fact. (#41)
- Request journal records headers, query and virtual time; `VERIFY_SEQUENCE` proves order and spacing. (#36)
- `pikopod mode set|show|clear`: a scenario's standing state on the served sandbox, so your own tests meet the failure. (#35)
- The observed state machine (`behaviour.enabled`), rendered by `pikopod contract`. (#58)
- `pikopod incidents export` and incident bundles that `scenario reproduce` and `fix` accept on any machine. (#59)
- Spec-declared enum values survive redaction when the value is one of the declared members. (#45)

### Changed

- Severity in `spec-diff` is derived by one law over the admitted-payload set; removing a required response field is `WARN`, raised to `ERR` by traffic evidence. (#57)
- `volatile_fields` suppression is value-only and path-scoped; malformed entries are refused, and dead, stable and over-broad entries are reported. (#54)
- Dead IR fields and the unproduced trust level were deleted. (#55)
- The README was shortened and now points at docs.pikopod.com. (#60)

### Fixed

- The fail-open proxy test snapshots its counters before the request, removing an intermittent CI failure. (#43)
- Orderly teardown: the proxy, recorder and sandbox engines drain and close cleanly on shutdown. (#59)

## [0.1.1] - 2026-09-15

### Added

- Failed exchanges are captured as incidents (`upstream_error`, `upstream_unreachable`, `rate_limited`, and opt-in `client_error`) and reproduced in the sandbox with `pikopod scenario reproduce`. (#29)
- Benchmarks for the sanitize and join hot paths. (#24)

### Fixed

- `--version` reports the module version when `go install` embeds no VCS information, and the commit and build date otherwise. (#16, #19)
- Upstream failures that happen mid-response are counted. (#23)
- The sandbox no longer issues provider-shaped webhook signing secrets. (#25)
- The Homebrew `brew trust` step and the cosign identity in the README. (#22)
- Canary fixtures use reserved example domains. (#15, #18)

## [0.1.0] - 2026-09-13

### Added

- First release: a deterministic sandbox built from an OpenAPI, Swagger 2.0, Postman or GraphQL spec; eleven failure archetypes that bind to it; `pikopod chaos`; the fail-open observing agent with redaction before disk, warmup, baselines and drift alerts; `pikopod spec-diff`; `pikopod replay --ci`; `pikopod demo`.
- Release pipeline: static binaries for macOS, Linux and Windows on amd64 and arm64, an SBOM per archive, cosign-signed `SHA256SUMS` with SLSA provenance, deb and rpm packages, and a Homebrew tap.

[Unreleased]: https://github.com/Pikopod/pikopod/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/Pikopod/pikopod/compare/v0.1.2...v0.2.0
[0.1.2]: https://github.com/Pikopod/pikopod/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/Pikopod/pikopod/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/Pikopod/pikopod/releases/tag/v0.1.0
