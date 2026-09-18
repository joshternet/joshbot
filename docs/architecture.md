# JoshBot architecture

JoshBot is a PostgreSQL-backed verification and discovery runtime with a
deterministic public registry pipeline.

The system separates untrusted network retrieval, private operational state,
public-data generation, and GitHub publication.

## Data flow

```text
operator schedule or crawl seed
            |
            v
      PostgreSQL queue
            |
            v
  guarded HTTP retrieval
     |              |
     |              +--> same-origin page frontier
     |
     +--> robots.txt
     |
     +--> /.well-known/josh
            |
            v
 verification observation
            |
            v
 effective origin state
            |
            +--> future discovery source
            |
            v
 deterministic registry export
            |
            v
 isolated GitHub publisher
```

## Runtime commands

The executable is built from `cmd/joshbot`.

It exposes:

- database health checks;
- migrations;
- verification scheduling;
- curated seed management;
- private crawl-source controls;
- verification workers;
- discovery workers;
- a read-only operational reporting service;
- a separately authenticated mutation-only control service;
- deterministic export;
- GitHub publication.

Long-running worker and discovery commands stop through context cancellation
and bounded shutdown behavior.

## Components

### `cmd/joshbot`

Owns command parsing, environment configuration, dependency construction,
process logging, and runtime lifecycle.

It does not define the underlying protocol or storage rules.

### `internal/database`

Loads and validates database configuration and opens PostgreSQL connections.

Passwords are loaded separately from database URLs so credentials do not need
to appear in command arguments, environment URLs, or logs.

### `internal/origin`

Defines the canonical web-origin model.

It accepts supported HTTP and HTTPS origins, canonicalizes host and port
representation, and rejects malformed or ambiguous input. Other packages use
this type instead of moving arbitrary URL strings across trust boundaries.

### `internal/netguard`

Resolves destination hostnames and rejects unsafe addresses before dialing.

The guard rejects loopback, private, link-local, multicast, shared,
unspecified, and other non-public destinations. Every resolved address must be
safe. Connections use validated IP literals, avoiding a second DNS lookup
between validation and dialing. Redirects are retrieved through the same
guarded path, and automatic admission performs a fresh all-address resolution
after evidence is stored and immediately before promotion.

The network guard reduces SSRF risk but does not replace host firewall policy.

### `internal/robots`

Defines the JoshBot product token and exact HTTP user agent:

```text
Joshternet-Joshbot (+https://joshternet.org/joshbot)
```

It retrieves and evaluates `robots.txt`, applies the selected JoshBot policy,
bounds response bodies, and wraps guarded HTTP access.

Robots permission answers whether JoshBot may retrieve a URI. It does not
decide whether an origin is a Joshternet participant.

### `internal/webbotauth`

Owns JoshBot's Web Bot Auth signing primitives and dedicated Ed25519 signing
identity.

The private key is loaded from an external PKCS#8 PEM secret file by the
runtime. The package derives the public Ed25519 key, exposes only the public
OKP JWK members `crv`, `kty`, and `x`, and calculates the SHA-256 JWK
thumbprint used as the Web Bot Auth key identifier.

Private key material is not exposed through the public JWK, normal formatting,
structured logging, or validation errors. Worker and discovery startup validate
a configured identity before beginning crawler work.

The signing identity is independent from other Joshternet cryptographic keys.

### `internal/declaration`

Retrieves the declaration at:

```text
/.well-known/josh
```

It parses the supported declaration format and returns a semantic outcome such
as valid, absent, invalid, unsupported, unavailable, robots denied, or
cross-origin redirect.

Raw declaration bodies and unknown JSON members are not retained.

### `internal/store`

Owns PostgreSQL persistence.

Its responsibilities include:

- embedded forward migrations;
- verification observations;
- effective origin state;
- durable queue scheduling;
- lease acquisition and renewal;
- transactional completion and rescheduling;
- discovery-source eligibility;
- curated crawl seeds;
- automatic-admission batches and retry state;
- operator pause, domain-avoid, and exact-origin block state;
- append-only operator audit events;
- bounded crawl and page-attempt telemetry;
- service heartbeats;
- candidate provenance and discovery timestamps;
- observation history.

Applied migrations are immutable.

### `internal/worker`

Claims verification leases, performs declaration checks, records outcomes, and
schedules future work.

Execution is at least once. A network request may occur more than once after a
crash or expired lease. Only the current lease owner may commit completion.

Recording the observation, releasing the lease, and scheduling subsequent work
occur transactionally.

### `internal/retry`

Defines the shared typed failure categories, three-attempt in-cycle bound,
`Retry-After` parsing and cap, and durable delay schedule of `5m`, `30m`,
`2h`, `12h`, then `24h`.

### `internal/discovery`

Extracts candidate origins and performs bounded multi-page crawling.

Each source begins at `/`. Same-origin pages use a deterministic breadth-first
frontier. External HTTP and HTTPS links become verification candidates rather
than immediate recursive crawl sources.

Every request uses the robots-aware guarded HTTP path.

The frontier is intentionally in memory. A stopped crawl starts from the root
during its next attempt.

Only a transient root-page failure receives up to three immediate attempts.
Failures after at least one page was parsed remain page telemetry and do not
turn an otherwise partially successful source crawl into a durable source
retry.

### `internal/reporting`

Defines the authenticated read-only HTTP contract and PostgreSQL adapter.
List endpoints return JSON arrays and use route-specific opaque keyset cursors.
The next cursor is carried in `X-JoshBot-Next-Cursor`, not in a response
envelope. Retained crawl metrics are gauges over the currently retained rows,
so telemetry purging can reduce them.

### `internal/control`

Defines the separately authenticated mutation-only HTTP contract. Successful
mutations commit with their audit row in one transaction. Authenticated
validation or mutation failures append a rejected audit after rollback.
Reporting credentials cannot authorize this service.

### `internal/publicdata`

Projects verified public state into deterministic registry files.

The projection excludes:

- crawl seeds;
- queue and lease state;
- discovery timing;
- internal crawl paths;
- response bodies;
- private credentials.

Directory writing and reading reject unsafe paths and filesystem structures.
Equivalent state produces byte-identical output.

### `internal/githubpublish`

Publishes an existing deterministic snapshot through the GitHub API.

The publisher:

- validates the target owner, repository, and branch;
- requires the publication sentinel;
- rejects redirects;
- ignores proxy environment variables;
- creates an exact tree;
- avoids force updates;
- detects concurrent publication conflicts;
- avoids a commit when the tree is unchanged.

The publisher receives no PostgreSQL configuration.

## PostgreSQL execution model

### Verification work

Manually scheduled work is recurring.

A discovered candidate begins as a one-shot probe. A valid declaration
promotes it to recurring work. A non-valid result records the observation and
removes the completed one-shot queue state transactionally unless the result is
a transient unavailable outcome, in which case the probe remains queued with
durable retry state.

Transient work receives at most three immediate verification attempts per
claim. Consecutive transient failures persist their category and next due time
using `5m`, `30m`, `2h`, `12h`, and a capped `24h` thereafter. A successful or
terminal completion clears that state. `Retry-After` may extend the selected
delay but cannot exceed `24h`.

### Leases

Eligible work is selected using PostgreSQL time and deterministic ordering.

A lease has an owner and expiration. Renewal and completion require matching
lease ownership. Expired work can be claimed again.

This provides durable at-least-once processing, not exactly-once network
requests.

### Origin politeness

Queue eligibility combines requested availability, lease state, and the
minimum interval for the origin. PostgreSQL time prevents worker clock skew
from bypassing the scheduling rules.

## Discovery model

A crawl source is eligible when it is either:

- an independently verified Joshternet origin; or
- an operator-curated seed; or
- admitted by the optional automatic expansion policy.

Curated seeds remain private operational configuration. Adding a seed does not
create a verification result or public registry entry. Removing seed status
does not erase existing observations or candidate provenance.

External origins found during crawling become durable candidates and provenance
edges. Crawling, link evidence, and probe scheduling do not establish
Joshternet participation. An origin becomes a participant only through its own
effective valid declaration.

When automatic expansion is enabled, candidate evidence is attributed to its
first durable crawl run before bounded admission. Eligible link candidates are
allocated in canonical-origin order, up to the admission run's snapshotted
promotion limit. Overflow remains unbatched durable evidence for a later crawl
run. Each allocated origin then receives a fresh all-address netguard
resolution. Transient resolution failures receive up to three immediate
attempts and a durable due time; unsafe addresses are retained as evidence but
recorded as network-rejected. Capacity, policy, and transient deferrals do not
erase the candidate or provenance.

The automatic-source classification remains private operational state, is
deduplicated by canonical origin, and never changes participant semantics.
Eligible automatic sources can discover further candidates across later crawl
generations, but no crawl or discovery relationship implies Joshternet
membership.

Automatic source claims use verification-queue backpressure. At the configured
pending-probe high-water mark, automatic sources remain stored but are not
claimed until workers drain the queue. Curated seeds and verified participants
remain independently eligible. The high-water mark controls outstanding work;
it does not drop candidates or limit recursive discovery over time.

## Publication boundary

Registry generation and publication are separate operations.

The database-connected tools container creates a snapshot in the export
directory. It has no GitHub token or egress network.

The publisher container receives:

- the GitHub token;
- egress;
- a read-only snapshot mount.

It does not receive:

- a database URL;
- a database password;
- the database network;
- PostgreSQL storage;
- backup storage;
- the Docker socket.

The target branch is machine-managed and must contain the exact sentinel:

```text
.joshbot-registry-target
```

with these bytes, including the final newline:

```text
joshbot-registry-v1
```

## Operational API boundaries

The reporting runtime defaults to `127.0.0.1:8788`, reads
`JOSHBOT_REPORT_TOKEN_FILE`, and uses a read-only `joshbot_reporter` database
role. The control runtime defaults to `127.0.0.1:8789`, reads the independent
`JOSHBOT_OPERATOR_TOKEN_FILE`, and uses the narrowly granted
`joshbot_operator` role. In Compose they are separately profile-gated behind
the `reporting` and `control` profiles and attach to different internal
networks.

Only each service's `/healthz` route is unauthenticated. Reporting bearer
credentials authorize operational reads and metrics only; operator credentials
authorize control mutations only. Neither service receives crawler egress or
publication credentials.

Worker and discovery processes persist a heartbeat every five seconds after an
initial `starting` write. Their lifecycle states are `running`, `idle`,
`paused`, `failed`, and `stopping` as applicable. Current-origin and failure
message fields are bounded operational state, and heartbeat persistence
failures are logged on transition into failure.

## Deployment boundaries

The Compose deployment defines four networks:

- `database`, which is internal;
- `egress`, for guarded web retrieval or GitHub publication;
- `reporting`, an internal network for the profile-gated reporting service and explicitly attached reporting clients;
- `control`, an internal network for the profile-gated operator control service and explicitly attached control clients.

PostgreSQL is not published to a host port.

The production JoshBot image runs as UID/GID `65532:65532`, with a read-only
root filesystem, all Linux capabilities dropped, and
`no-new-privileges`.

See [the deployment guide](../deploy/README.md) for the exact service and
credential layout.

## Recovery

Backups are PostgreSQL custom-format archives created by a dedicated
read-only role.

The recovery smoke test restores into a fresh isolated PostgreSQL instance,
checks role boundaries and migration records, verifies persisted operational
state, and rebuilds byte-identical public output. The current schema contains
twelve embedded migrations, numbered `0001` through `0012`. Recovery never
restores into the configured production database.