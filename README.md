# JoshBot

JoshBot is the discovery, verification, and public registry crawler for the
[Joshternet](https://joshternet.org).

It finds public web origins, verifies their Joshternet declarations, remembers
operational state in PostgreSQL, and produces a deterministic public registry.
Publication to GitHub happens through a separate, credential-isolated runtime.

The current supported release is `v1.1.0`.

## What is the Joshternet?

The Joshternet is an opt-in web network defined by the
[Joshternet specifications](https://github.com/joshternet/spec).

A site participates by publishing a valid declaration at:

```text
/.well-known/josh
```

JoshBot implements discovery, verification, and registry publication. It does
not define the protocol by itself, and an implementation change in this
repository must not silently redefine a Joshternet specification.

## What JoshBot does

JoshBot:

- crawls independently verified origins;
- crawls operator-curated discovery seeds;
- can promote discovered origins into bounded private crawl sources;
- discovers external web origins through public links;
- verifies each candidate's Joshternet declaration independently;
- remembers verification, scheduling, discovery, crawl, and queue state in
  PostgreSQL;
- applies backpressure when automatic discovery would overfill the verification
  probe queue;
- records sanitized crawl telemetry without retaining page bodies;
- exposes a private authenticated operational reporting API;
- exposes a separately authenticated operator control API;
- exports deterministic public registry files;
- publishes exact registry trees through the GitHub API;
- avoids creating publication commits when the registry has not changed.

The short version is that links create candidates, while valid declarations
create participants.

## How it works

```text
eligible crawl source
        |
        v
same-origin pages within crawl limits
        |
        v
external public link
        |
        v
canonical candidate origin
        |
        v
verification probe
        |
        v
GET /.well-known/josh
        |
        v
valid version 1 declaration?
        |
        v
participant in deterministic public registry
```

Same-origin links may extend the current page frontier. External links do not
extend that crawl directly. Their origins become verification candidates.

A candidate is not trusted merely because another site linked to it. Every
candidate must publish and pass its own declaration verification before it can
appear in the public registry.

Crawling a page, extracting a link, retaining discovery evidence, scheduling a
probe, or admitting a private crawl source never by itself means that an origin
is a Joshternet participant.

## Participation

JoshBot recognizes these version 1 declarations:

```json
{"version":1}
```

```json
{"version":1,"josh":true}
```

```json
{"version":1,"josh":false}
```

All three are valid participation declarations.

The optional `josh` member describes identity:

| Declaration | Identity state |
| --- | --- |
| `{"version":1}` | Undeclared |
| `{"version":1,"josh":true}` | Affirmed |
| `{"version":1,"josh":false}` | Declined |

`Declined` does not mean nonparticipating. It means the origin participates
while explicitly declining the Josh identity assertion.

Likewise, `Undeclared` means the declaration does not make that identity
assertion. It remains a valid participation declaration.

Robots permission and Joshternet participation are separate. A declaration
does not override `robots.txt`, and `robots.txt` does not create or remove a
Joshternet declaration.

## Crawl sources

JoshBot distinguishes crawl-source status from Joshternet participation.

A source may be:

- a verified Joshternet participant;
- an operator-curated seed;
- an automatically discovered crawl source;
- more than one of the above;
- explicitly blocked from crawling by an operator.

### Verified participants

An origin with an effective valid Joshternet declaration is independently
verified and may be used as a crawl source.

Verification is what determines participation in the public registry.

### Curated seeds

An operator can explicitly add an origin as a private curated crawl seed.

A curated seed grants permission to use that origin as a discovery source. It
does not declare that the origin participates in the Joshternet and does not
place it in the public registry.

### Automatically discovered crawl sources

When automatic crawling is enabled, JoshBot can promote discovered candidate
origins into private crawl sources under bounded policy.

Automatic source promotion is operational state only. It does not:

- assert Josh identity;
- create a Joshternet declaration;
- make the source a verified participant;
- add the source to the public registry;
- bypass robots policy;
- bypass network safety checks;
- bypass crawl budgets or politeness controls.

Automatically discovered sources can be excluded by configured host policy and
can be blocked explicitly by an operator.

Automatic discovery also observes verification-queue backpressure. When the
number of pending probe jobs reaches the configured threshold, JoshBot stops
claiming additional automatic discovery work until queue pressure falls below
the limit.

## Crawling

Each crawl:

- starts at the source origin's root path;
- follows eligible same-origin links breadth-first;
- stays within configured page, depth, response-size, redirect, timeout, and
  delay limits;
- evaluates robots permission for each requested URI;
- sends requests through the network safety guard;
- processes requests sequentially for that source;
- records sanitized page-attempt telemetry;
- turns external HTTP and HTTPS links into candidate origins.

JoshBot preserves existing queue, lease, retry, robots, politeness, and SSRF
safety boundaries regardless of how a source became crawl-eligible.

Automatic source promotion does not create an unbounded recursive crawl.
Discovered origins remain subject to policy, queue pressure, crawl limits,
robots rules, and network safety checks before work proceeds.

See [Crawler behavior](docs/crawler.md) for the detailed site-owner and
operator contract.

## Automatic crawl controls

Automatic crawl-source behavior is configured with:

```text
JOSHBOT_AUTOMATIC_CRAWL_ENABLED
JOSHBOT_AUTOMATIC_CRAWL_MAX_PENDING_PROBES
JOSHBOT_AUTOMATIC_CRAWL_MAX_PROMOTIONS_PER_RUN
JOSHBOT_AUTOMATIC_CRAWL_EXCLUDED_HOSTS
```

Automatic crawling is disabled unless explicitly enabled.

The pending-probe limit provides backpressure between discovery and declaration
verification. This prevents discovery from producing verification work faster
than the verification queue can reasonably absorb.

Runtime defaults are:

```dotenv
JOSHBOT_AUTOMATIC_CRAWL_ENABLED=false
JOSHBOT_AUTOMATIC_CRAWL_MAX_PENDING_PROBES=1000
JOSHBOT_AUTOMATIC_CRAWL_MAX_PROMOTIONS_PER_RUN=100
JOSHBOT_AUTOMATIC_CRAWL_EXCLUDED_HOSTS=
JOSHBOT_CRAWL_TELEMETRY_RETENTION=720h
```

Candidate origins and provenance edges are committed before automatic
admission. Each crawl run allocates at most its configured promotion limit in
canonical-origin order. Unallocated evidence remains durable for a later
run. Immediately before admission, each allocated origin is resolved again and
every returned address must pass the public-address guard. A transient
resolution failure is deferred durably; an unsafe address is retained as
evidence but rejected from automatic admission.

Operators can inspect and override individual private source eligibility with:

```text
joshbot source block <origin>
joshbot source allow <origin>
joshbot source list
```

Blocking a source prevents JoshBot from crawling it as a discovery source. It
does not rewrite that origin's declaration history or participation state.

## Crawl observability

JoshBot records operational crawl telemetry in PostgreSQL.

Crawl-run records include:

- source origin;
- start and finish times;
- outcome and stop reason;
- pages attempted and parsed;
- candidates discovered;
- whether a crawl budget was exhausted;
- the crawl limits used for that run.

Ordered page-attempt records include sanitized operational information such as:

- requested and final URL;
- crawl depth;
- start time and duration;
- HTTP status code;
- response byte count;
- sanitized content type;
- redirect count;
- robots decision;
- internal and external link counts;
- page-attempt outcome.

JoshBot does not store response bodies, cookies, response headers, or arbitrary
page content as crawl telemetry.

Telemetry retention is controlled with:

```text
JOSHBOT_CRAWL_TELEMETRY_RETENTION
```

## Queue observability and backpressure

JoshBot records verification queue transitions so operators can understand how
work moves through the system.

Queue events include transitions such as:

- scheduled;
- claimed;
- renewed;
- rescheduled;
- completed;
- removed.

JoshBot also persists discovery and verification pause state and service
heartbeats.

This operational data makes queue pressure, worker activity, discovery
activity, and automatic-crawl backpressure observable without changing the
public registry format.

Transient verification, root-page crawl, and automatic-admission resolution
failures receive at most three immediate attempts in one processing cycle.
Durable consecutive-failure delays are `5m`, `30m`, `2h`, `12h`, then `24h`
for later failures. `Retry-After` can extend a verification or root-page delay
but is capped at `24h`. Successful or terminal outcomes reset the durable
streak; a crawl that parsed at least one page is treated as successful for
source retry state even if another page failed.

Transient categories are `dns`, `transport`, `timeout`, `robots_temporary`,
`http_408`, `http_429`, `http_5xx`, `declaration_unavailable`, `processor`, and
`store`. Terminal categories are retained for telemetry but do not create a
durable retry streak.

## Operational reporting API

JoshBot includes a private reporting runtime:

```text
joshbot report
```

The reporting service is intended for dashboards, monitoring systems, and other
operator tooling. PostgreSQL remains JoshBot's durable internal source of
truth, while the reporting API provides a supported read interface so external
tools do not need to depend directly on JoshBot's database schema.

The service binds to:

```text
127.0.0.1:8788
```

by default.

The listen address can be changed with:

```text
JOSHBOT_REPORT_LISTEN_ADDRESS
```

The reporting bearer token is loaded from the file named by:

```text
JOSHBOT_REPORT_TOKEN_FILE
```

The token itself is not supplied directly through an environment variable.

### Reporting endpoints

The reporting API currently provides:

```text
GET /healthz
GET /api/v1/status
GET /api/v1/sources
GET /api/v1/sources/detail
GET /api/v1/crawls
GET /api/v1/crawls/{id}
GET /api/v1/queue
GET /api/v1/queue/events
GET /api/v1/audit
GET /api/v1/services
GET /metrics
```

`/healthz` is a process-liveness endpoint.

Operational API routes and `/metrics` require bearer-token authentication.

Responses are sent with cache prevention and content-type protection headers.
Reporting failures return generic client-facing errors rather than leaking
database or internal implementation details.

The list routes `/api/v1/sources`, `/api/v1/crawls`, `/api/v1/queue`,
`/api/v1/queue/events`, and `/api/v1/audit` use stable keyset pagination.
`limit` defaults to `100` and must be from `1` through `1000`; `cursor` is an
opaque route-specific value. Each response body is a JSON array, including an
empty result. When another page exists, its cursor is returned in the
`X-JoshBot-Next-Cursor` response header. Audit events are ordered newest first.

## Operator control API

JoshBot provides a separate, mutation-only operator runtime:

```text
joshbot control
```

It binds to `127.0.0.1:8789` by default and uses
`JOSHBOT_CONTROL_LISTEN_ADDRESS` when configured. Its independent,
high-entropy bearer token is read from `JOSHBOT_OPERATOR_TOKEN_FILE`; the
reporting token never authorizes control.

```text
GET /healthz
POST /api/v1/control/processors/{discovery|verification}/{pause|resume}
POST /api/v1/control/domain-avoid
DELETE /api/v1/control/domain-avoid/{pattern}
POST /api/v1/control/origins/block
POST /api/v1/control/origins/allow
```

Authenticated attempts are durably audited. Successful mutations and their
success audits commit atomically, while rejected attempts are audited after
rollback. Responses never include token, reason, or database error details.
Exact-origin block intent is stored independently from derived domain-avoid
policy, so removing a rule restores eligible automatic sources while retaining
explicit blocks and other matching rules.

The metrics endpoint exposes operational, low-cardinality measurements for
crawler state. Crawl-history measurements are retained-window gauges because
telemetry retention can reduce them; they are not monotonic counters.
The current metric families are:

```text
joshbot_discovery_paused
joshbot_verification_paused
joshbot_automatic_crawl_enabled
joshbot_backpressure_active
joshbot_pending_probes
joshbot_pending_probe_limit
joshbot_verification_queue{mode="all|probe|recurring"}
joshbot_verification_leases
joshbot_crawl_sources{classification="all|seeded|automatic|verified|blocked|crawl_eligible"}
joshbot_retained_candidates
joshbot_retained_discovery_edges
joshbot_retained_crawl_runs
joshbot_retained_pages_attempted
joshbot_retained_pages_parsed
joshbot_retained_origins_found
joshbot_retained_origins_promoted
joshbot_retained_origins_deferred
joshbot_retained_failures
joshbot_retained_robots_denials
joshbot_service_heartbeat_age_seconds{service,state}
```

The heartbeat age is computed from the latest row for each service name.
Worker and discovery instances report `starting`, `running`, `idle`, `paused`,
`failed`, and `stopping` as applicable. A `failed` state can include a bounded
machine-readable message; the current origin is omitted when no origin is
active.

Private origins are not emitted as metric labels.

## Network and security model

JoshBot processes untrusted URLs and web content. Its safety boundaries
include:

- strict origin parsing and canonicalization;
- DNS resolution before connection;
- rejection of loopback, private, link-local, multicast, shared, unspecified,
  and other non-public addresses;
- validation of every resolved address;
- a fresh all-address DNS validation immediately before automatic admission;
- guarded redirects;
- robots-aware HTTP retrieval;
- response-size and crawl-budget limits;
- bounded automatically discovered crawl sources;
- verification-queue backpressure;
- PostgreSQL leases and transactional completion;
- explicit operator blocking of private crawl sources;
- separate database and publication credentials;
- a publisher with no database access;
- a private authenticated reporting interface;
- a separately authenticated operator control interface;
- deterministic public output with no private discovery state.

These controls reduce risk but do not make arbitrary crawling risk-free.
Operators remain responsible for host networking, firewall policy, credentials,
storage, schedules, legal requirements, and the origins they configure for
crawling.

## Data JoshBot keeps

JoshBot stores semantic and operational data including:

- canonical origins;
- declaration verification observations;
- effective participant state;
- first and latest observation times;
- verification queue state;
- verification queue event history;
- lease ownership and expiration;
- curated seed status;
- automatically discovered source status;
- operator crawl-block status;
- discovery-source attempt times;
- source-to-candidate origin relationships;
- first and latest candidate discovery times;
- bounded crawl-run history;
- sanitized ordered page-attempt telemetry;
- discovery and verification pause state;
- service heartbeats;
- applied database migration metadata.

This state supports verification, retries, politeness, discovery provenance,
backpressure, operational reporting, and deterministic registry construction.

## Data JoshBot does not keep

JoshBot does not operate as a page archive.

It does not retain:

- HTML archives;
- complete page response bodies;
- complete declaration response bodies;
- page titles as archival content;
- anchor text as archival content;
- screenshots;
- cookies;
- response headers;
- arbitrary page content.

Operational crawl telemetry may retain sanitized request URLs, crawl depth,
status, timing, byte counts, redirect counts, content type, robots decisions,
link counts, and outcomes.

URL fragments and sensitive request data are not intentionally retained as
crawl telemetry.

Private crawl-source state, queue state, crawl history, discovery timing,
leases, service heartbeats, backpressure state, and candidate provenance do
not appear in the public registry.

## Public registry

A database-connected runtime projects verified participant state into a
deterministic directory containing `registry.json` and per-origin node files.

Equivalent participant state produces byte-identical output. File paths and
ordering are deterministic.

Publication occurs separately:

1. The tools runtime exports the snapshot without GitHub credentials.
2. The publisher receives a read-only snapshot and GitHub credentials.
3. The publisher verifies the machine-managed target branch and sentinel.
4. It creates the exact desired Git tree.
5. It updates the branch without force.
6. If the exact tree already exists, it creates no commit.

The JoshBot registry format is an implementation compatibility boundary. It is
not itself a Joshternet RFC.

Only verified Joshternet participants belong in the public registry.
Automatically discovered crawl sources, curated seeds, blocked sources, queue
state, and reporting telemetry remain private operational state.

## Crawler identity

JoshBot uses this exact HTTP User-Agent:

```text
Joshternet-Joshbot (+https://joshternet.org/joshbot)
```

The public crawler information page is:

```text
https://joshternet.org/joshbot
```

The HTTP Message Signatures directory used for Web Bot Auth is:

```text
https://joshternet.org/.well-known/http-message-signatures-directory
```

Cloudflare BotBase registration has been submitted and is awaiting review.
JoshBot is not Cloudflare Verified until that review completes and
`joshbot conformance web-bot-auth --expect verified` passes.

To block JoshBot completely, a site can publish:

```text
User-agent: Joshternet-Joshbot
Disallow: /
```

For unexpected crawling, robots behavior, or traffic, open a
[crawler behavior report](https://github.com/joshternet/joshbot/issues/new?template=crawler_report.yml).

Crawler reports are welcome even when the site's robots configuration appears
correct. Do not include credentials, private server logs, or sensitive
infrastructure details in a public issue.

Security vulnerabilities must be reported privately under the
[Security policy](SECURITY.md).

## Requirements

Development requires:

- Go 1.27;
- Git;
- a POSIX shell;
- PostgreSQL 18 for database integration tests.

Deployment additionally requires Docker Engine and Docker Compose v2.

## Getting started

Build JoshBot:

```bash
go build ./cmd/joshbot
```

Show the current command reference:

```bash
./joshbot help
```

Run tests that do not require a configured database:

```bash
go test ./...
```

For the complete PostgreSQL-backed suite, configure
`JOSHBOT_TEST_DATABASE_URL` as described in
[Contributing](CONTRIBUTING.md).

Production deployment, backup, recovery, and publication are documented in
the [Deployment guide](deploy/README.md).

## Commands

```text
joshbot health
joshbot migrate
joshbot schedule <origin>
joshbot seed add <origin>
joshbot seed remove <origin>
joshbot seed list
joshbot source block <origin>
joshbot source allow <origin>
joshbot source list
joshbot worker
joshbot discover [--once]
joshbot report
joshbot control
joshbot export --output <directory>
joshbot publish --input <directory>
joshbot conformance web-bot-auth --expect unregistered|verified
joshbot help
```

### `health`

Checks PostgreSQL connectivity.

```bash
joshbot health
```

### `migrate`

Applies pending embedded database migrations.

```bash
joshbot migrate
```

Applied migrations are immutable. Schema changes require a new forward
migration.

### `schedule`

Schedules an origin for recurring declaration verification.

```bash
joshbot schedule https://example.com
```

Input paths, queries, and fragments are reduced to the canonical origin.

### `seed`

Manages operator-curated crawl seeds.

```bash
joshbot seed add https://directory.example
joshbot seed remove https://directory.example
joshbot seed list
```

A seed creates a private discovery source. It is not a declaration,
verification result, or public registry entry.

### `source`

Inspects and controls private crawl-source state.

```bash
joshbot source block https://example.com
joshbot source allow https://example.com
joshbot source list
```

Blocking or allowing a crawl source changes operational crawl eligibility. It
does not create, remove, or rewrite a Joshternet declaration.

### `worker`

Runs the long-lived declaration-verification worker.

```bash
joshbot worker
```

### `discover`

Runs the discovery service.

```bash
joshbot discover
```

Run one discovery attempt:

```bash
joshbot discover --once
```

### `report`

Runs the private operational reporting API.

```bash
joshbot report
```

The reporting service provides a supported interface for dashboards,
monitoring, and other operator tooling without requiring those clients to
depend directly on JoshBot's PostgreSQL schema.

The service requires `JOSHBOT_REPORT_TOKEN_FILE` and binds to
`127.0.0.1:8788` unless `JOSHBOT_REPORT_LISTEN_ADDRESS` overrides it.

### `control`

Runs the private operator mutation API.

```bash
joshbot control
```

The service requires an independent `JOSHBOT_OPERATOR_TOKEN_FILE` and binds to
`127.0.0.1:8789` unless `JOSHBOT_CONTROL_LISTEN_ADDRESS` overrides it.

### `export`

Writes a deterministic public registry snapshot. The destination directory
must not already exist.

```bash
joshbot export --output <directory>
```

### `publish`

Publishes an existing registry snapshot to the configured GitHub repository.

```bash
joshbot publish --input <directory>
```

The normal deployment uses `deploy/publish.sh` so database export and GitHub
publication occur in separate containers.

### `conformance`

Checks the deployed signature directory and sends one signed request to
Cloudflare's Web Bot Auth test endpoint. It uses the production crawler
client and refuses unsigned mode. The deployment guide has the exact
pre-registration and post-approval commands. A passing check is not
Cloudflare approval.

```bash
joshbot conformance web-bot-auth --expect unregistered|verified
```

### `help`

Prints the command reference.

```bash
joshbot help
```

## Development

The project uses test-driven changes, PostgreSQL integration testing, race and
repeatability checks, fuzzing, deployment validation, and exactly `100.0%`
statement coverage.

Read [Contributing](CONTRIBUTING.md) for the complete development workflow.

## Specifications

Joshternet protocol semantics belong to the
[Joshternet specification repository](https://github.com/joshternet/spec).

Implementation changes belong here. Protocol changes belong there.

## Documentation

- [Architecture](docs/architecture.md)
- [Crawler behavior](docs/crawler.md)
- [Deployment guide](deploy/README.md)
- [Production soak](docs/production-soak.md)
- [Changelog](CHANGELOG.md)

## Contributing

Contributions are welcome. You do not have to be named Josh; that would be an
oddly specific dependency.

Read [Contributing](CONTRIBUTING.md) before opening an issue or pull request.

For reproducible software defects, use the
[bug report form](https://github.com/joshternet/joshbot/issues/new?template=bug_report.yml).

For crawler-specific concerns, use the
[crawler behavior report](https://github.com/joshternet/joshbot/issues/new?template=crawler_report.yml).

## Community

Participation is governed by the
[Code of Conduct](CODE_OF_CONDUCT.md).

## Security

Do not disclose an unpatched vulnerability through a public issue. Follow the
[Security policy](SECURITY.md) and use GitHub Private Vulnerability Reporting.

## License

JoshBot is licensed under [BSD-3-Clause](LICENSE).

Copyright belongs to Joshua Morris. Source and binary redistributions must
retain the copyright notice, license conditions, and warranty disclaimer.
