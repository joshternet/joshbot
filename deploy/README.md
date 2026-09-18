# JoshBot deployment and recovery

This is the operator runbook for the reference JoshBot deployment.

The deployment intentionally separates PostgreSQL, migrations, verification,
discovery, operational reporting, operator tools, GitHub publication, and
backups. Each service receives only the network, credentials, and storage
required for its job.

Review every path, mount, credential, publication target, firewall rule, and
schedule before using this configuration on a production host.

## Runtime guarantees

JoshBot uses at-least-once execution.

A verification request may physically occur more than once after a crash or
expired lease. Only the worker holding the current PostgreSQL lease may commit
completion.

Successful completion records the observation, releases the lease, and
schedules future recurring work in one PostgreSQL transaction.

Manually scheduled work is recurring. A discovered candidate starts as a
one-shot probe. A valid declaration promotes the origin to recurring work.
Every non-valid probe records its observation and removes completed one-shot
queue state transactionally.

JoshBot does not promise exactly-once network requests.

Registry publication is separate from verification. Publication reads an
already-built deterministic snapshot and never connects to PostgreSQL.

## Crawler behavior

JoshBot crawls independently verified Joshternet participants, private
operator-curated seeds, and bounded automatically discovered crawl sources when
automatic expansion is enabled.

A seed grants permission to use an origin as a discovery source. It is not a
Joshternet declaration, verification result, or public registry entry.

An automatically discovered crawl source is also private operational state.
Promotion to a crawl source does not make an origin a Joshternet participant
and does not place it in the public registry.

Each source crawl begins at `/` and uses a deterministic breadth-first
in-memory frontier. The root is depth zero. Same-origin hyperlinks may be
followed within the configured depth and page budgets.

External HTTP and HTTPS origins become declaration-verification candidates.
They may become bounded automatic crawl sources only when automatic expansion
is enabled and the applicable safety, exclusion, blocking, and queue-pressure
rules permit it.

Every page request uses the robots-aware guarded HTTP path.

JoshBot retains bounded sanitized crawl telemetry rather than archived page
content. It does not retain HTML bodies, complete response bodies, response
headers, cookies, or arbitrary page content.

See [the crawler documentation](../docs/crawler.md).

## Services

The Compose deployment includes:

- `postgres`: persistent PostgreSQL 18.6;
- `migrate`: one-shot schema migration;
- `worker`: long-running declaration verification;
- `discovery`: long-running multi-page discovery;
- `report`: private authenticated reporting API, enabled with the `reporting`
  profile;
- `control`: private authenticated mutation API, enabled with the `control`
  profile;
- `tools`: database-backed operator commands and exports;
- `publisher`: one-shot GitHub registry publication;
- `backup`: logical PostgreSQL backups;
- `restore`: controlled logical restoration.

The reporting service is intentionally profile-gated. Existing worker and
discovery deployments do not begin exposing a new service merely because the
Compose file is upgraded.

## Network and credential separation

- `worker` and `discovery` receive the internal database network and egress.
- `report` receives the internal database network and the private reporting
  network. It receives no general egress network.
- `control` receives the internal database network and its private control
  network. It receives no general egress network.
- `tools` receives the database network and writable export storage.
- `publisher` receives egress, a read-only export mount, and the GitHub token.
- `migrate`, `postgres`, `backup`, and `restore` receive the database network.
- Database services never receive the GitHub token.
- The publisher receives no database URL, password, network, or storage.
- The report service receives no GitHub token.
- The control service receives no GitHub or reporting token.
- Workers and discovery receive neither API bearer token.
- No service receives the Docker socket or host networking.
- PostgreSQL port 5432 is not published to the host.
- Reporting port 8788 is not published to the host.

The `reporting` network is a named internal Docker network. A dashboard or
other authorized local reporting client can attach to that network explicitly
and connect to the `report` service without exposing the API through a host
port.

Authentication remains required even for clients attached to the reporting
network.

The separate `control` network follows the same pattern on port 8789. Control
uses a dedicated PostgreSQL operator role and bearer token; reporting
credentials cannot authorize mutations.

## Host requirements

The reference host needs:

- Linux;
- Docker Engine;
- Docker Compose v2;
- OpenSSL;
- local persistent PostgreSQL storage;
- an export directory;
- a separately mounted backup destination;
- enough space for images, PostgreSQL, exports, and backups.

Record the environment:

```bash
docker version
docker compose version
cat /etc/os-release
docker info
```

Confirm the Docker firewall backend and outbound policy separately.

## Host directories

The example environment uses:

```text
/srv/joshbot/postgres
/srv/joshbot/exports
/srv/joshbot/secrets
/mnt/nas/joshbot/backups
```

These paths are examples. Replace them with reviewed host paths.

PostgreSQL data belongs on local persistent storage, not the backup mount.

Compose uses `create_host_path: false`; source directories must already exist.
This prevents a typo from silently creating storage in the wrong place.

The JoshBot image runs as UID/GID `65532:65532`. The PostgreSQL image uses
UID/GID `999:999`.

One possible Linux setup is:

```bash
sudo install \
  -d \
  -m 0700 \
  -o 999 \
  -g 999 \
  /srv/joshbot/postgres

sudo install \
  -d \
  -m 0770 \
  -o 65532 \
  -g 65532 \
  /srv/joshbot/exports

sudo install \
  -d \
  -m 0700 \
  -o root \
  -g root \
  /srv/joshbot/secrets
```

Do not create the backup directory until its intended remote mount is
confirmed.

## Confirm storage

Check PostgreSQL storage:

```bash
findmnt \
  --target /srv/joshbot/postgres \
  --output TARGET,SOURCE,FSTYPE,OPTIONS
```

PostgreSQL 18 persists `/var/lib/postgresql`. Do not change the Compose target
to `/var/lib/postgresql/data`.

Confirm backup storage:

```bash
findmnt \
  --target /mnt/nas/joshbot/backups \
  --output TARGET,SOURCE,FSTYPE,OPTIONS

df -h /mnt/nas/joshbot/backups
```

A directory existing does not prove remote storage is mounted. Stop if the
path has fallen back to the host's local filesystem.

## Environment configuration

Copy the example:

```bash
cp \
  deploy/.env.example \
  deploy/.env
```

Replace example paths and the publication target.

`deploy/.env` is ignored by Git. Passwords, bearer tokens, and the GitHub token
do not belong inside it.

Review worker timing:

```text
JOSHBOT_LEASE_DURATION=5m
JOSHBOT_MIN_ORIGIN_INTERVAL=1m
JOSHBOT_POLL_INTERVAL=30s
JOSHBOT_JOB_TIMEOUT=2m
JOSHBOT_COMPLETION_GRACE=30s
JOSHBOT_RECHECK_INTERVAL=24h

JOSHBOT_JOB_TIMEOUT + JOSHBOT_COMPLETION_GRACE
    < JOSHBOT_LEASE_DURATION
```

All six durations must be positive. `JOSHBOT_WORKER_ID` is optional outside
Compose; when empty, the command generates `worker-` followed by 32 lowercase
hexadecimal characters. A configured ID must be valid UTF-8 and no more than
128 characters.

Review discovery timing:

```text
JOSHBOT_DISCOVERY_INTERVAL=168h
JOSHBOT_DISCOVERY_POLL_INTERVAL=30s
JOSHBOT_DISCOVERY_PAGE_TIMEOUT=30s
```

All three values must be positive. Go durations do not accept `7d`.

Review crawl budgets:

```text
JOSHBOT_CRAWL_MAX_DEPTH >= 0
JOSHBOT_CRAWL_MAX_PAGES > 0
JOSHBOT_CRAWL_MAX_PAGE_BYTES > 0
JOSHBOT_CRAWL_REQUEST_DELAY >= 0
JOSHBOT_CRAWL_REDIRECT_LIMIT > 0

JOSHBOT_AUTOMATIC_CRAWL_ENABLED = true or false
JOSHBOT_AUTOMATIC_CRAWL_MAX_PENDING_PROBES > 0
JOSHBOT_AUTOMATIC_CRAWL_MAX_PROMOTIONS_PER_RUN > 0
JOSHBOT_AUTOMATIC_CRAWL_EXCLUDED_HOSTS = optional additional comma-separated domain patterns
```

The defaults are:

```dotenv
JOSHBOT_CRAWL_MAX_DEPTH=4
JOSHBOT_CRAWL_MAX_PAGES=32
JOSHBOT_CRAWL_MAX_PAGE_BYTES=1048576
JOSHBOT_CRAWL_REQUEST_DELAY=1s
JOSHBOT_CRAWL_REDIRECT_LIMIT=5
JOSHBOT_CRAWL_TELEMETRY_RETENTION=720h

JOSHBOT_AUTOMATIC_CRAWL_ENABLED=false
JOSHBOT_AUTOMATIC_CRAWL_MAX_PENDING_PROBES=1000
JOSHBOT_AUTOMATIC_CRAWL_MAX_PROMOTIONS_PER_RUN=100
JOSHBOT_AUTOMATIC_CRAWL_EXCLUDED_HOSTS=
```

`JOSHBOT_CRAWL_REQUEST_DELAY` is the operator minimum for per-origin outbound
requests made by both the worker and discovery services. An applicable robots
`Crawl-delay` can increase that interval but cannot reduce it.

The durable domain avoid list is managed through the operator control API and
starts with the hosted publishing and social-platform defaults. An environment value adds
emergency rules to that list; dashboard removal cannot override an environment
rule. Use a family such as `blogspot.*` to cover regional public suffixes.

The root page is depth zero. Redirect hops do not consume additional frontier
slots. Requests within one source crawl are sequential.

Automatic expansion is disabled by default. When enabled, newly discovered
canonical origins become private crawl sources and can discover further crawl
sources in later generations. Canonical-origin deduplication updates existing
records instead of creating duplicates. Discovered sources remain independent
verification candidates and do not become Joshternet participants unless their
declarations verify.

Candidate origins and provenance are committed before automatic admission.
Each admission run allocates candidates in canonical-origin order up to its
snapshotted promotion limit; overflow remains durable and unbatched until a
later run. Immediately before admission, each allocated origin receives a
fresh DNS resolution and every returned address must pass netguard. Transient
resolution failures are retried at most three times immediately and then
deferred durably. Unsafe addresses remain in private evidence but are recorded
as network-rejected.

Verification, root-page crawling, and automatic-admission resolution make at
most three immediate attempts for transient failures in one cycle. Durable
consecutive-failure delays are exactly `5m`, `30m`, `2h`, `12h`, then `24h`.
Valid `Retry-After` values can extend verification or root-page delays to the
`24h` cap. Non-root page requests are not retried, and a crawl that parsed at
least one page clears source retry state even when a later page failed.

The pending-probe setting is a high-water mark for automatic expansion. When
the verification queue reaches that many probe jobs, JoshBot keeps every
discovered source but temporarily stops claiming automatically discovered
sources. Curated seeds and verified participants remain eligible. Automatic
claims resume as soon as the worker drains the probe queue below the mark, so
the setting limits concurrent backlog rather than the number of sources JoshBot
can discover over its lifetime.

Use the `tools` service to inspect or control private crawl sources:

```bash
docker compose --env-file deploy/.env run --rm tools source list
docker compose --env-file deploy/.env run --rm tools source block https://example.org
docker compose --env-file deploy/.env run --rm tools source allow https://example.org
```

Blocking preserves seed status, discovery provenance, and verification history.

JoshBot stores bounded crawl-run and per-page operational telemetry for the
configured retention period. Stored telemetry does not retain page bodies,
response headers, cookies, fragments, or query values.

The database also keeps processor heartbeats, persistent pause state, and
verification queue transitions so reporting clients can show current work,
history, stop reasons, and backpressure without access to response bodies or
crawler secrets.

Worker and discovery heartbeats are written every five seconds after an
initial `starting` record. Their applicable states are `running`, `idle`,
`paused`, `failed`, and `stopping`. Current-origin and machine-readable message
fields are bounded; heartbeat persistence failures are logged when the failure
first becomes active.

## Reporting configuration

The reporting service is configured with:

```dotenv
JOSHBOT_REPORT_LISTEN_ADDRESS=0.0.0.0:8788
JOSHBOT_REPORT_NETWORK_NAME=joshbot-reporting
JOSHBOT_REPORT_TOKEN_FILE=/srv/joshbot/secrets/joshbot_report_token
```

The standalone JoshBot runtime defaults to `127.0.0.1:8788`.

Compose intentionally overrides that with `0.0.0.0:8788` inside the report
container because authorized client containers need to reach it through the
private reporting network.

There is no Compose `ports` mapping for the reporting service. The API is not
published on a host interface.

`JOSHBOT_REPORT_NETWORK_NAME` gives the private reporting network a predictable
name so a separate dashboard Compose project can attach to it as an external
Docker network.

A dashboard Compose file can use a network declaration shaped like:

```yaml
networks:
  joshbot-reporting:
    external: true
    name: joshbot-reporting
```

The dashboard service can then attach to that network and use:

```text
http://report:8788
```

as its JoshBot reporting endpoint when Docker DNS can resolve the Compose
service alias.

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

`/healthz` is intentionally unauthenticated and provides process liveness only.

All operational API routes and `/metrics` require:

```text
Authorization: Bearer TOKEN
```

The reporting bearer token must come from the mounted secret file. Do not place
the token value directly in Compose environment variables.

The report runtime has no crawler egress network and no GitHub credentials.
It connects as the read-only `joshbot_reporter` role.

The list routes for sources, crawls, queue entries, queue events, and audit
events use stable keyset pagination. `limit` defaults to `100` and accepts
`1` through `1000`; `cursor` is opaque and route-specific. The body is always
a JSON array. When another page exists, the response carries
`X-JoshBot-Next-Cursor`.

## Operator control configuration

```dotenv
JOSHBOT_CONTROL_LISTEN_ADDRESS=0.0.0.0:8789
JOSHBOT_CONTROL_NETWORK_NAME=joshbot-control
JOSHBOT_OPERATOR_TOKEN_FILE=/srv/joshbot/secrets/joshbot_operator_token
```

The control service is profile-gated, has no host port mapping, and accepts
only these routes:

```text
GET /healthz
POST /api/v1/control/processors/{discovery|verification}/{pause|resume}
POST /api/v1/control/domain-avoid
DELETE /api/v1/control/domain-avoid/{pattern}
POST /api/v1/control/origins/block
POST /api/v1/control/origins/allow
```

Only `/healthz` is unauthenticated. All `/api/` routes require the independent
operator bearer token; the reporting token is not accepted. The service
connects as `joshbot_operator`, which has narrowly granted control and audit
permissions but cannot read publication data, mutate verification results or
curated seed status, or alter or delete audit rows.

JSON mutation bodies use `application/json`. Processor and DELETE requests may
omit the body or provide optional `actor` and `reason` strings. Domain-add
requests require `pattern`; origin block and allow requests require `origin`.
Successful mutations and their audit events commit atomically. Authenticated
rejections are append-only audited after rollback.

## Secrets

Generate six different database passwords and separate reporting and operator
bearer tokens:

```bash
sudo sh -c '
  set -eu
  umask 077

  openssl rand -hex 32 \
    > /srv/joshbot/secrets/postgres_admin_password

  openssl rand -hex 32 \
    > /srv/joshbot/secrets/joshbot_migrator_password

  openssl rand -hex 32 \
    > /srv/joshbot/secrets/joshbot_app_password

  openssl rand -hex 32 \
    > /srv/joshbot/secrets/joshbot_reporter_password

  openssl rand -hex 32 \
    > /srv/joshbot/secrets/joshbot_operator_password

  openssl rand -hex 32 \
    > /srv/joshbot/secrets/joshbot_backup_password

  openssl rand -hex 32 \
    > /srv/joshbot/secrets/joshbot_report_token

  openssl rand -hex 32 \
    > /srv/joshbot/secrets/joshbot_operator_token

  chmod 0444 \
    /srv/joshbot/secrets/postgres_admin_password \
    /srv/joshbot/secrets/joshbot_migrator_password \
    /srv/joshbot/secrets/joshbot_app_password \
    /srv/joshbot/secrets/joshbot_reporter_password \
    /srv/joshbot/secrets/joshbot_operator_password \
    /srv/joshbot/secrets/joshbot_backup_password \
    /srv/joshbot/secrets/joshbot_report_token \
    /srv/joshbot/secrets/joshbot_operator_token
'
```

The containing `/srv/joshbot/secrets` directory remains `0700 root:root`.
The six database password files are `0444 root:root`.

The bearer-token files are also `0444 root:root` so the non-root API containers
can read only their individually mounted secret.

The files must be readable by the non-root users inside the containers that
receive them. The `0700 root:root` parent directory prevents unrelated host
users from traversing the secrets directory.

Docker selectively mounts only the individual secret files required by each service.

Verify the host-side ownership and modes before starting PostgreSQL or the
reporting service:

```bash
sudo stat \
  --format='%a %U:%G %n' \
  /srv/joshbot/secrets \
  /srv/joshbot/secrets/postgres_admin_password \
  /srv/joshbot/secrets/joshbot_migrator_password \
  /srv/joshbot/secrets/joshbot_app_password \
  /srv/joshbot/secrets/joshbot_reporter_password \
  /srv/joshbot/secrets/joshbot_operator_password \
  /srv/joshbot/secrets/joshbot_backup_password \
  /srv/joshbot/secrets/joshbot_report_token \
  /srv/joshbot/secrets/joshbot_operator_token
```

Expected permissions are:

```text
700 root:root /srv/joshbot/secrets
444 root:root /srv/joshbot/secrets/postgres_admin_password
444 root:root /srv/joshbot/secrets/joshbot_migrator_password
444 root:root /srv/joshbot/secrets/joshbot_app_password
444 root:root /srv/joshbot/secrets/joshbot_reporter_password
444 root:root /srv/joshbot/secrets/joshbot_operator_password
444 root:root /srv/joshbot/secrets/joshbot_backup_password
444 root:root /srv/joshbot/secrets/joshbot_report_token
444 root:root /srv/joshbot/secrets/joshbot_operator_token
```

The GitHub publication token is a ninth, separately provisioned secret. It is
mounted only into the one-shot publisher and is not generated by the database
and API credential block above.

Do not:

- commit secret files;
- put passwords or bearer tokens in `compose.yaml`;
- put passwords or bearer-token values in `deploy/.env`;
- put passwords in database URLs;
- print passwords or bearer tokens in logs.

## Publication repository

The publication target must be a dedicated data repository and branch
controlled entirely by JoshBot.

Do not point it at:

```text
joshternet/joshbot
joshternet/joshternet.github.io
```

A dedicated repository such as `joshternet/index-data` is the intended shape.
The exact target is an operator decision.

The machine-managed branch is replaced with an exact generated tree. Do not
store manually maintained files on that branch.

Before the first publication, the target branch must exist and contain:

```text
.joshbot-registry-target
```

Its exact contents, including the final newline, must be:

```text
joshbot-registry-v1
```

The publisher does not create repositories, tokens, branches, workflows, or
Pages configuration. It does not force-update Git references.

## GitHub token

Use a fine-grained token restricted to the dedicated publication repository.

Required repository permission:

```text
Contents: Read and write
```

Do not grant unrelated Administration, Actions, Workflows, Issues, Pull
requests, or Secrets permissions.

Store the token only in the configured secret file:

```text
/srv/joshbot/secrets/joshbot_github_token
```

Do not put the token in environment configuration, command arguments, logs,
PostgreSQL, registry output, or chat.

## Validate Compose

Validate all optional service profiles:

```bash
docker compose \
  --env-file deploy/.env \
  --profile tools \
  --profile backup \
  --profile publisher \
  --profile reporting \
  --profile control \
  config \
  --quiet
```

Fix missing paths and variables before starting services.

## Build

Build the normal runtime:

```bash
docker compose \
  --env-file deploy/.env \
  build \
  --pull \
  migrate \
  worker \
  discovery \
  tools \
  publisher
```

Build reporting when it will be used:

```bash
docker compose \
  --env-file deploy/.env \
  --profile reporting \
  build \
  report
```

Build control when it will be used:

```bash
docker compose \
  --env-file deploy/.env \
  --profile control \
  build \
  control
```

All JoshBot services use the same minimal image.

The final image:

- contains one statically linked JoshBot binary;
- contains CA certificates;
- runs as UID/GID `65532:65532`;
- contains no shell;
- contains no Go compiler;
- contains no Git or GitHub CLI;
- contains no curl or wget;
- contains no PostgreSQL client.

## Start the runtime

Start the existing verification and discovery runtime:

```bash
docker compose \
  --env-file deploy/.env \
  up \
  --build \
  --detach \
  --wait
```

This starts PostgreSQL, applies migrations, and starts verification and
discovery.

Enable the private reporting runtime separately:

```bash
docker compose \
  --env-file deploy/.env \
  --profile reporting \
  up \
  --build \
  --detach \
  --wait \
  report
```

Enable the private operator control runtime separately:

```bash
docker compose \
  --env-file deploy/.env \
  --profile control \
  up \
  --build \
  --detach \
  --wait \
  control
```

Check state:

```bash
docker compose \
  --env-file deploy/.env \
  --profile reporting \
  --profile control \
  ps \
  --all
```

The migration container should exit zero. PostgreSQL, discovery, and any
explicitly enabled reporting or control service should be healthy. The worker
should be running with no unexpected restarts; its container process state is
its liveness signal rather than a PostgreSQL connectivity health check.

## Reporting health

The report container's Compose health check verifies database connectivity.

The reporting process also exposes an HTTP liveness endpoint at:

```text
GET /healthz
```

An authorized client already attached to the reporting network can test
liveness without a bearer token.

Operational routes require the reporting bearer token.

## Migrations

Migrations are embedded from:

```text
internal/store/migrations/
```

Never edit an applied migration. Add a forward migration.

The current image embeds twelve migrations, numbered `0001` through `0012`.
A backup restored from the current schema must retain twelve
`schema_migrations` rows.

Run migrations manually:

```bash
docker compose \
  --env-file deploy/.env \
  run \
  --rm \
  --no-deps \
  migrate
```

## Health check

```bash
docker compose \
  --env-file deploy/.env \
  --profile tools \
  run \
  --rm \
  --no-deps \
  tools \
  health
```

Expected output:

```text
healthy
```

## Schedule an origin

```bash
docker compose \
  --env-file deploy/.env \
  --profile tools \
  run \
  --rm \
  --no-deps \
  tools \
  schedule \
  https://example.com/path
```

The canonical scheduled origin is:

```text
https://example.com
```

## Manage curated seeds

Add a seed:

```bash
docker compose \
  --env-file deploy/.env \
  --profile tools \
  run \
  --rm \
  --no-deps \
  tools \
  seed add \
  https://directory.example/some/path
```

List seeds:

```bash
docker compose \
  --env-file deploy/.env \
  --profile tools \
  run \
  --rm \
  --no-deps \
  tools \
  seed list
```

Remove seed status:

```bash
docker compose \
  --env-file deploy/.env \
  --profile tools \
  run \
  --rm \
  --no-deps \
  tools \
  seed remove \
  https://directory.example
```

Adding a seed does not create a declaration observation, verification queue
row, or public registry entry.

Removing seed status does not delete observations, candidate provenance, or
queue state. An independently verified origin remains crawl-eligible.

## Manage crawl sources

List private crawl-source state:

```bash
docker compose \
  --env-file deploy/.env \
  --profile tools \
  run \
  --rm \
  --no-deps \
  tools \
  source list
```

Block an origin from being used as a crawl source:

```bash
docker compose \
  --env-file deploy/.env \
  --profile tools \
  run \
  --rm \
  --no-deps \
  tools \
  source block \
  https://example.org
```

Allow the origin again:

```bash
docker compose \
  --env-file deploy/.env \
  --profile tools \
  run \
  --rm \
  --no-deps \
  tools \
  source allow \
  https://example.org
```

Blocking a source does not delete its seed state, verification history, or
discovery provenance.

## Run one discovery attempt

```bash
docker compose \
  --env-file deploy/.env \
  run \
  --rm \
  --no-deps \
  discovery \
  discover \
  --once
```

Long-running discovery normally runs through the `discovery` service.

## Export the registry

The destination must not already exist.

```bash
snapshot="registry-$(date -u '+%Y%m%dT%H%M%SZ')"

docker compose \
  --env-file deploy/.env \
  --profile tools \
  run \
  --rm \
  --no-deps \
  tools \
  export \
  --output "/exports/$snapshot"
```

The tools container has database access and writable export storage. It has no
GitHub token or egress.

## Publish the registry

`deploy/publish.sh` performs one attempt:

1. `tools` exports a fresh deterministic snapshot.
2. `publisher` reads and publishes the snapshot.
3. The temporary snapshot is removed after success or failure.

Load the reviewed environment through the service manager or shell, then run:

```bash
set -a
. deploy/.env
set +a

./deploy/publish.sh
```

Changed output:

```text
published COMMIT_SHA
```

Unchanged output:

```text
registry unchanged
```

The publisher uses the GitHub API directly, rejects redirects, ignores proxy
environment variables, requires the sentinel, creates an exact tree, and
updates the branch without force.

The temporary snapshot is transport staging, not a backup.

## Manual backup

First prove that the backup directory is mounted correctly.

```bash
docker compose \
  --env-file deploy/.env \
  --profile backup \
  run \
  --rm \
  --no-deps \
  backup
```

The backup service:

- uses the internal database network;
- uses the read-only backup role;
- creates a custom-format PostgreSQL archive;
- writes a `.partial` file;
- validates the archive with `pg_restore --list`;
- sets mode `0600`;
- atomically renames the file to `.dump`;
- removes partial output after failure.

Retention is a separate destructive policy decision and is not automated.

## Recovery validation

```bash
./deploy/smoke.sh
```

The smoke test creates disposable storage and secrets, initializes PostgreSQL,
applies migrations, checks role boundaries, creates known state, exports the
registry, backs up PostgreSQL, restores into a fresh isolated instance,
verifies restored state, rebuilds byte-identical output, and cleans its
resources. For the current schema, both the source and restored databases must
contain twelve migration records.

It never restores into the configured production database.

## Publication-boundary validation

```bash
./deploy/publish_test.sh
```

This test uses fake publication infrastructure. It does not contact GitHub or
require a real token.

It verifies:

- non-root execution;
- read-only root filesystems;
- dropped capabilities;
- `no-new-privileges`;
- no published ports;
- no Docker socket;
- publisher egress without database access;
- read-only export storage;
- GitHub credentials only in the publisher;
- database credentials absent from the publisher;
- snapshot cleanup;
- failure-status preservation;
- no token logging.

The reporting and control services remain profile-gated and do not change the
publisher's credential or network boundary.

## Container inspection

```bash
docker compose \
  --env-file deploy/.env \
  --profile tools \
  --profile backup \
  --profile publisher \
  --profile reporting \
  --profile control \
  create

docker compose \
  --env-file deploy/.env \
  --profile tools \
  --profile backup \
  --profile publisher \
  --profile reporting \
  --profile control \
  ps \
  --all \
  --quiet
```

Inspect a specific container:

```bash
docker inspect CONTAINER_ID
```

The publisher must have:

- UID/GID `65532:65532`;
- a read-only root filesystem;
- all capabilities dropped;
- `no-new-privileges`;
- only the egress network;
- read-only `/exports`;
- `/run/secrets/joshbot_github_token`;
- no database configuration;
- no database network;
- no PostgreSQL or backup mount;
- no Docker socket;
- no published port.

The report service must have:

- UID/GID `65532:65532`;
- a read-only root filesystem;
- all capabilities dropped;
- `no-new-privileges`;
- the database network;
- the private reporting network;
- no egress network;
- `/run/secrets/joshbot_reporter_password`;
- `/run/secrets/joshbot_report_token`;
- no GitHub token;
- no export or backup mount;
- no Docker socket;
- no published host port.

The control service has the same hardening with only the database and private
control networks, `/run/secrets/joshbot_operator_password`, and
`/run/secrets/joshbot_operator_token`. It receives no reporting, application,
publication, export, backup, or host-port access.

## Graceful shutdown

Stop network workers first:

```bash
docker compose \
  --env-file deploy/.env \
  stop \
  worker \
  discovery
```

If reporting is enabled, stop it separately:

```bash
docker compose \
  --env-file deploy/.env \
  --profile reporting \
  stop \
  report
```

If operator control is enabled, stop it separately:

```bash
docker compose \
  --env-file deploy/.env \
  --profile control \
  stop \
  control
```

Stop remaining services without deleting host data:

```bash
docker compose \
  --env-file deploy/.env \
  down
```

Never delete the PostgreSQL directory unless that destructive action has been
explicitly approved and a verified recovery path exists.

## Upgrade procedure

Before upgrading:

1. Verify the backup mount.
2. Create and validate a backup.
3. Record the deployed image.
4. Review new migrations.
5. Confirm application compatibility.

Build the new image:

```bash
docker compose \
  --env-file deploy/.env \
  build \
  --pull \
  migrate \
  worker \
  discovery \
  tools \
  publisher
```

If reporting is enabled, build it from the same image:

```bash
docker compose \
  --env-file deploy/.env \
  --profile reporting \
  build \
  report
```

If operator control is enabled, build it from the same image:

```bash
docker compose \
  --env-file deploy/.env \
  --profile control \
  build \
  control
```

Apply migrations:

```bash
docker compose \
  --env-file deploy/.env \
  run \
  --rm \
  --no-deps \
  migrate
```

Recreate long-running services:

```bash
docker compose \
  --env-file deploy/.env \
  up \
  --detach \
  --no-deps \
  worker \
  discovery
```

If reporting is enabled:

```bash
docker compose \
  --env-file deploy/.env \
  --profile reporting \
  up \
  --detach \
  --no-deps \
  report
```

If operator control is enabled:

```bash
docker compose \
  --env-file deploy/.env \
  --profile control \
  up \
  --detach \
  --no-deps \
  control
```

The publisher remains a one-shot operation.

## Local validation

```bash
chmod 0755 \
  deploy/backup.sh \
  deploy/postgres/init/010-joshbot-roles.sh \
  deploy/publish.sh \
  deploy/publish_test.sh \
  deploy/smoke.sh \
  scripts/release-check.sh

sh -n deploy/backup.sh
sh -n deploy/postgres/init/010-joshbot-roles.sh
sh -n deploy/publish.sh
sh -n deploy/publish_test.sh
sh -n deploy/smoke.sh
sh -n scripts/release-check.sh

docker compose \
  --env-file deploy/.env.example \
  --profile tools \
  --profile backup \
  --profile publisher \
  --profile reporting \
  config \
  --quiet

./scripts/release-check.sh
./deploy/publish_test.sh
./deploy/smoke.sh
```

Run the complete Go quality suite from the repository root as documented in
[README.md](../README.md).
