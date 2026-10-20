# Changelog

All notable JoshBot changes are documented here.

The format follows Keep a Changelog, and releases use semantic versioning.

## [Unreleased]

### Added

- The durable domain avoid list includes `centerblog.net`, so that neighborhood
  is not promoted automatically (#51).
- Operators can pause automatic expansion without pausing discovery or
  verification. Curated seeds and verified origins stay eligible for discovery
  claims, candidate verification continues, and new automatic source claims
  and promotions stay paused until expansion resumes. The durable flag is
  `crawl_control.automatic_expansion_paused`, changed through
  `POST /api/v1/control/automatic-expansion/{pause|resume}`, and visible on
  status and `joshbot_automatic_expansion_paused` (#78).
- Discovery scheduling is separate from durable discovery knowledge.
  `discovery_source_schedule` is the set of origins that currently have crawl
  intent. An origin, candidate, or provenance edge can stay known after an
  authoritative terminal miss removes that intent. A strictly newer
  rediscovery can schedule the same origin again. Known origins are not
  capped (#139).
- `registry-export` writes a fresh public registry snapshot every
  `JOSHBOT_REGISTRY_PUBLISH_INTERVAL` (default 15 minutes). It has database
  access and no GitHub token.
- `registry-publish` publishes each new snapshot. An unchanged registry
  does not create a commit. It has the GitHub token and no database access.
- The embedded schema now contains twenty-nine migrations, `0001` through
  `0029`, including durable candidate admission state, domain-avoid LIKE
  hardening, verification-queue `abandoned` events, removal of the WordPress
  domain-avoid rules, and the running build identity on service heartbeats.
- Discovery and worker heartbeats record `build_version` and `build_commit`
  for the process that is running. The image build stamps those values from
  `VERSION` and `VCS_REF`.

### Changed

- `wordpress.com` and `wordpress.org` are no longer domain-avoid rules, and
  existing blocks on those hosts are cleared. Sites that install the official
  Joshternet WordPress plugin can be probed and promoted. Other avoid rules
  stay in place.
- Report bearer tokens now require the same strength rules as operator control
  tokens: at least 32 bytes, printable ASCII only, and at least eight distinct
  characters. Weak report tokens are rejected at process start.

### Fixed

- A transient check gets one immediate retry. A second failure is scheduled for
  later, and that future retry does not occupy pending-probe capacity, so
  workers move on to sites that can be checked now.
- Curated seeds are scheduled when they are added, and discovery startup
  inserts a schedule row for any seeded origin that is missing one after the
  crawler and runner have been constructed. Removing a seed drops that
  schedule row only when the origin has no other reason to stay scheduled.
  Claim order is unchanged.
- Partial discovery crawls retain page failures and retry transient
  failures on non-root pages as well as the root (#150, #71).
- Blocking an origin preserves its verification and reprobe queue state;
  claims skip blocked origins and resume after unblocking (#151).
- Control recomputation and source curation honor configured host exclusions
  alongside durable domain-avoid rules (#152).
- Report handlers and token files reject weak bearer tokens using the same
  strength rules as operator control (#153).
- Crawl detail paginates page attempts with a scoped continuation cursor, and
  startup rejects page budgets above 10,000 (#154).
- Origin schedules have a 1,024-entry cap with idle eviction and expiry.
  Active requests and pending spacing survive eviction pressure, and cached
  robots policies restore their learned delay (#155).
- Registry export removes failed partial snapshots before retrying and stops
  on cleanup failures, including after a restart (#156).
- Integration coverage includes HTTP limit rejection, a zero dial timeout, and the default guarded transport used when no HTTP limits are set.
- Discovery startup blocks automatic sources that no longer parse as origins, instead of exiting when reconciliation finds one.
- staticcheck v0.8.2 understands Go 1.27.2 export data.
- Go 1.27.2 and `golang.org/x/net` v0.60.0 close the govulncheck HTTP/2, HTTP/1, TLS, MIME, and Windows `os.Root` findings (GO-2026-6603 through GO-2026-6617).
- Registry snapshot entropy uses full reads, publish failures back off
  exponentially (capped), and cleanup only removes valid automatic snapshot
  names. Snapshot pointer files stay mode `0644` so host-side export readers
  (including deployment smoke) can read them across the container UID boundary.
- Service heartbeat messages truncate on a UTF-8 rune boundary within the
  256-byte limit.
- GitHub publish tokens strip a trailing CR after LF, matching report and
  control token files.
- Discovery admission always completes typed candidate decisions; the
  eligible-only admit fallback is gone.
- Worker, discovery, and conformance dialers set a dial timeout aligned
  with job or page timeouts.
- When `JOSHBOT_SERVICE_INSTANCE_ID` is unset, processes generate a unique
  `service-<16hex>` id instead of sharing a hostname or fixed default.
  Compose already pins `worker` and `discovery`.
- Database construction failure returns a nil connection with the error.
- Report and control shutdown always drains the Serve goroutine after a
  shutdown failure.
- Main installs `defer stop()` for the signal context.
- Discovery DNS admission retries wait a short backoff between attempts.
- Source detail `LatestCrawl` includes the same crawl telemetry columns as the
  crawl list (promotions, failure category, URL/frontier counters).
- Reporting keyset pagination treats `HEAD` like `GET`.
- Control audit truncation is UTF-8 safe, and optional JSON bodies with unknown
  length and an empty body are accepted as empty.
- Retry-After second values are clamped before duration multiply to avoid
  overflow, and non-retryable HTTP 4xx map to the permanent `http_4xx`
  category.
- Origin parsing strips one trailing dot from domain hosts after IDNA, and
  rejects hosts that are empty or still end in `.` after that strip so
  canonical serializations always round-trip.
- Database password files strip a trailing CR the same way report tokens do.
- Web Bot Auth directory `Content-Digest` checks require a proper `sha-256`
  dictionary member and reject substring labels such as `xsha-256`.
- Invalid robots `User-agent` tokens start a new group and no longer let
  Allow/Disallow/Crawl-delay attach to the previous group.
- Robots policy fetches no longer hold the checker mutex across the network
  call, and the policy cache is bounded with idle eviction.
- Per-origin request scheduling releases the origin lock while waiting for the
  delay, without weakening politeness.
- Discovery maps oversized and unavailable page extraction correctly, retains
  Retry-After as durable retry eligibility, treats HTML parse failures as
  unavailable, and classifies canceled body reads as timeouts.
- Crawl runner rejects a nil context, and a non-zero pages-parsed count no
  longer clears finalize, store, or processor failure categories.
- Hard verification errors abandon the lease before return, and transient
  verification failures retain Retry-After as durable retry eligibility.
- Unfinished crawl runs whose discovery source lease is no longer active are
  closed as `canceled` with `stop_reason` `abandoned` (or `lease_reclaimed` on
  same-origin reclaim). Zero-page runs no longer stay `outcome=running` for
  days after the worker moves on (#127).
- Discovery source claim upserts only steal a lease when it is null or expired,
  lock schedule and source state when present, and renew takes the same claim
  advisory lock as claim.
- Domain-avoid LIKE matching escapes `_`, `%`, and `\`, and validation plus the
  durable CHECK reject those metacharacters in avoid patterns.
- Probe scheduling skips crawl-blocked origins the same way automatic admission
  does.
- Durable candidate admission outcomes (`network_rejected`, `promoted`,
  `existing`) survive crawl telemetry purge so rejected candidates are not
  revived.
- Abandoning a verification lease records an `abandoned` queue event instead of
  `completed`.
- Queue claim reads its clock inside the claim transaction.
- Concurrent queue claim tests give each database attempt its own 30-second
  limit. A shared deadline no longer cancels workers that are still claiming.
  Each claim must still succeed, and distinct origins remain required.
- Store tests borrow one of four isolated schemas, matching the four store
  tests run at once. A schema is truncated after a test that left its tables
  in place. A test that changes tables, indexes, triggers, or constraints gets
  that same schema rebuilt before it is reused. The package no longer creates
  and drops a spare schema for every test.

## [1.1.0] - 2026-09-21

### Added

- `joshbot conformance web-bot-auth` checks the public signature directory
  and Cloudflare's fixed Web Bot Auth endpoint with the production crawler
  client. `--expect unregistered` treats HTTP 401 as a pass before
  registration. `--expect verified` requires HTTP 200 after Cloudflare has
  accepted the key. The command refuses unsigned mode and does not use the
  database.
- Signature-directory validation for the published Joshternet directory:
  HTTP 200, the directory content type, one or two public Ed25519 keys,
  a matching SHA-256 content digest, and one directory signature per key.
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
- Operator production-soak guide covering existing reporting signals, the
  discovery funnel, curated seed research, and evidence-gated follow-up work.

### Changed

- Production Web Bot Auth configuration uses
  `JOSHBOT_WEB_BOT_AUTH_MODE=required` and
  `JOSHBOT_WEB_BOT_AUTH_ACTIVE_PRIVATE_KEY_FILE`, with
  `JOSHBOT_WEB_BOT_AUTH_PRIVATE_KEY_FILE` retained only as a legacy alias for
  the active path when the new variable is unset.
- Deployment documentation includes the BotBase registration procedure,
  including `joshbot conformance web-bot-auth` and the pinned
  `http-signature-directory` 0.7.0 command, plus the signing-key rotation
  runbook. Passing those checks is not Cloudflare approval.

### Fixed

- Architecture and deployment docs count fourteen embedded migrations,
  `0001` through `0014`, including
  `0014_clear_ephemeral_service_heartbeats.sql`.
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
- Nil-context contract tests pass a typed `nil` `context.Context` variable
  instead of a bare `nil` literal so `staticcheck` SA1012 stays clean without
  suppressing the check.

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

[Unreleased]: https://github.com/joshternet/joshbot/compare/v1.1.0...HEAD
[1.1.0]: https://github.com/joshternet/joshbot/releases/tag/v1.1.0
[1.0.0]: https://github.com/joshternet/joshbot/releases/tag/v1.0.0
