# Changelog

All notable JoshBot changes are documented here.

The format follows Keep a Changelog, and releases use semantic versioning.

## [Unreleased]

### Added

- Private operational reporting HTTP API with bearer authentication, status,
  sources, crawls, queue, services, audit history, and metrics.
- Keyset pagination for list endpoints via `X-JoshBot-Next-Cursor`.
- Source detail reporting with provenance, queue state, latest crawl, and
  stored robots observations.
- Operator control API with independent authentication, processor pause and
  resume, domain-avoid rules, exact-origin block and allow, and append-only
  audits.
- Automatic crawl-source admission with per-run promotion budgets, deferred
  candidates, and pending-probe backpressure.
- Shared durable retry policy for transient discovery and verification
  failures.
- Service heartbeats and expanded crawl-run telemetry.
- Robots policy parsing for `Crawl-delay` with JoshBot-over-wildcard
  precedence, fractional-second support, repeated directives resolving to the
  largest applicable value, and explicit invalid-value reporting.
- Shared per-origin request scheduling for robots retrieval, declaration
  verification, discovery pages, and redirects using the greater of the
  configured request delay and applicable robots `Crawl-delay`.
- Cloudflare Web Bot Auth request-signing primitives using Ed25519 HTTP
  Message Signatures, JWK-thumbprint key identifiers, short-lived signatures,
  and per-request nonces.
- Dedicated Ed25519 Web Bot Auth signing-key configuration using an external
  PKCS#8 private-key secret, derived public OKP JWK, RFC 7638 thumbprint key
  identifier, and crawler-startup identity validation.

### Fixed

- Crawl-source transient retry completion updates streak, failure category,
  and next-attempt timestamp atomically.
- Deployment smoke cleanup tears down the reporting and control Compose
  profiles so disposable containers and the smoke application image are
  removed.
- Retry migration constraint checks look up definitions in the current
  test schema so parallel schema teardown cannot yield stale OIDs.

### Security

- Web Bot Auth private-key material remains outside normal environment values
  and public JWK output, and signing identity formatting, structured logging,
  JSON dumps, and validation errors do not expose private key bytes.
- Worker and discovery containers receive the dedicated Web Bot Auth key only
  through an individually mounted secret file.

## [1.0.0] - 2026-09-05

### Added

- Canonical HTTP and HTTPS origin parsing.
- Public-address validation and guarded dialing.
- JoshBot HTTP identity and robots exclusion enforcement.
- Joshternet declaration parsing and verification.
- PostgreSQL verification history and effective origin state.
- A durable work queue with leasing, renewal, rescheduling, and transactional
  completion.
- Deterministic discovery-source scheduling and candidate provenance.
- Multi-page same-origin crawling with bounded breadth-first traversal.
- Operator-curated private crawl seeds.
- Deterministic public registry projection and directory snapshots.
- Atomic GitHub publication with unchanged-tree detection.
- Containerized verification, discovery, migration, export, publication,
  backup, restore, and recovery workflows.
- Exact statement-coverage, race, repeatability, fuzzing, formatting, module,
  vet, deployment, and recovery quality gates.

### Security

- Network destinations are resolved and validated before dialing.
- Unsafe address ranges and unsafe redirect destinations are rejected.
- Database credentials and GitHub publication credentials are isolated in
  separate containers.
- The public registry excludes private discovery and queue state.

[Unreleased]: https://github.com/joshternet/joshbot/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/joshternet/joshbot/releases/tag/v1.0.0