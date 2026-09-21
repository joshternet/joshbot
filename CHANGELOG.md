# Changelog

All notable JoshBot changes are documented here.

The format follows Keep a Changelog, and releases use semantic versioning.

## [Unreleased]

### Added

- Web Bot Auth active and optional transition signing identities with an
  explicit `required` / `unsigned` mode so production crawler traffic cannot
  silently fall back to unsigned requests during key rotation.
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

### Changed

- Production Web Bot Auth configuration uses
  `JOSHBOT_WEB_BOT_AUTH_MODE=required` and
  `JOSHBOT_WEB_BOT_AUTH_ACTIVE_PRIVATE_KEY_FILE`, with
  `JOSHBOT_WEB_BOT_AUTH_PRIVATE_KEY_FILE` retained only as a legacy alias for
  the active path when the new variable is unset.
- Deployment documentation includes a BotBase meet-or-exceed checklist and a
  cross-repository signing-key rotation runbook aligned with the public
  HTTP Message Signature directory.

### Fixed

- Crawl service heartbeats use a stable logical slot
  (`JOSHBOT_SERVICE_INSTANCE_ID`) and replace that slot when a newer process
  starts, instead of leaving a stale card for every previous worker or
  container identity (#39).
- Worker and discovery processor loops no longer apply the durable origin
  retry schedule (`5m`→`24h`) after a claimed item fails; unrelated due work
  continues immediately, and pre-claim infrastructure failures wait for the
  configured poll interval (#32).
- Verification workers renew queue leases only when remaining wall-clock time
  cannot hold another job timeout plus completion grace, and complete with the
  latest authoritative lease (#32).
- Crawler redirect-loop tracking is local to each fetch attempt so a
  same-origin redirect that fails transiently can be retried without being
  treated as a malformed redirect loop; crawl-wide visited state updates only
  after a successful fetch attempt (#35).
- Deployment smoke runs one-shot discovery through the `discovery` service so
  fail-closed Web Bot Auth remains satisfied without mounting crawler signing
  credentials into `tools`.
- Discovery source claims use expiring leases so a crashed crawl can be
  reclaimed; `last_attempted_at` advances only on completion, and stale lease
  tokens cannot renew or complete over a newer claim (#34).
- Crawl-source transient retry completion updates streak, failure category,
  and next-attempt timestamp atomically.
- Deployment smoke cleanup tears down the reporting and control Compose
  profiles so disposable containers and the smoke application image are
  removed.
- Retry migration constraint checks look up definitions in the current
  test schema so parallel schema teardown cannot yield stale OIDs.

### Security

- Required Web Bot Auth mode fails closed on missing, unreadable, malformed,
  unsupported, or inconsistent signing identities, and on duplicate
  active/transition thumbprints, before crawler work begins.
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