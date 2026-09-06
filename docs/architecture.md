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
- verification workers;
- discovery workers;
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
safe. Redirects are retrieved through the same guarded path.

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

### `internal/discovery`

Extracts candidate origins and performs bounded multi-page crawling.

Each source begins at `/`. Same-origin pages use a deterministic breadth-first
frontier. External HTTP and HTTPS links become verification candidates rather
than immediate recursive crawl sources.

Every request uses the robots-aware guarded HTTP path.

The frontier is intentionally in memory. A stopped crawl starts from the root
during its next attempt.

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
removes the completed one-shot queue state transactionally.

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
- an operator-curated seed.

Curated seeds remain private operational configuration. Adding a seed does not
create a verification result or public registry entry. Removing seed status
does not erase existing observations or candidate provenance.

External origins found during crawling are scheduled for declaration
verification. They are not recursively crawled unless they later verify or an
operator adds them as seeds.

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

## Deployment boundaries

The Compose deployment defines two networks:

- `database`, which is internal;
- `egress`, for guarded web retrieval or GitHub publication.

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
checks application access, verifies persisted operational state, and rebuilds
byte-identical public output. It never restores into the configured production
database.