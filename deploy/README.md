# JoshBot deployment and recovery

This is the operator runbook for the reference JoshBot deployment.

The deployment intentionally separates PostgreSQL, migrations, verification,
discovery, operator tools, GitHub publication, and backups. Each service
receives only the network, credentials, and storage required for its job.

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

JoshBot crawls independently verified Joshternet participants and private
operator-curated seeds.

A seed grants permission to use an origin as a discovery source. It is not a
Joshternet declaration, verification result, or public registry entry.

Each source crawl begins at `/` and uses a deterministic breadth-first
in-memory frontier. The root is depth zero. Same-origin hyperlinks may be
followed within the configured depth and page budgets.

External HTTP and HTTPS origins become declaration-verification candidates.
JoshBot does not recursively crawl them unless they later verify or are
explicitly added as seeds.

Every page request uses the robots-aware guarded HTTP path.

JoshBot retains origin-level operational data, not crawled pages. It does not
retain HTML, complete response bodies, titles, anchor text, headers, cookies,
page history, page depth, or old frontiers.

See [the crawler documentation](../docs/crawler.md).

## Services

The Compose deployment includes:

- `postgres`: persistent PostgreSQL 18.6;
- `migrate`: one-shot schema migration;
- `worker`: long-running declaration verification;
- `discovery`: long-running multi-page discovery;
- `tools`: database-backed operator commands and exports;
- `publisher`: one-shot GitHub registry publication;
- `backup`: logical PostgreSQL backups;
- `restore`: controlled logical restoration.

## Network and credential separation

- `worker` and `discovery` receive the internal database network and egress.
- `tools` receives the database network and writable export storage.
- `publisher` receives egress, a read-only export mount, and the GitHub token.
- `migrate`, `postgres`, `backup`, and `restore` receive the database network.
- Database services never receive the GitHub token.
- The publisher receives no database URL, password, network, or storage.
- No service receives the Docker socket or host networking.
- PostgreSQL port 5432 is not published to the host.

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
path has fallen back to the host’s local filesystem.

## Environment configuration

Copy the example:

```bash
cp \
  deploy/.env.example \
  deploy/.env
```

Replace example paths and the publication target.

`deploy/.env` is ignored by Git. Passwords and the GitHub token do not belong
inside it.

Review worker timing:

```text
JOSHBOT_POLL_INTERVAL > 0
JOSHBOT_JOB_TIMEOUT > 0
JOSHBOT_COMPLETION_GRACE > 0
JOSHBOT_RECHECK_INTERVAL > 0
JOSHBOT_LEASE_DURATION > 0
JOSHBOT_MIN_ORIGIN_INTERVAL > 0

JOSHBOT_JOB_TIMEOUT + JOSHBOT_COMPLETION_GRACE
    < JOSHBOT_LEASE_DURATION
```

Review discovery timing:

```text
JOSHBOT_DISCOVERY_INTERVAL > 0
JOSHBOT_DISCOVERY_POLL_INTERVAL > 0
JOSHBOT_DISCOVERY_PAGE_TIMEOUT > 0
```

The reference discovery interval is `168h`. Go durations do not accept `7d`.

Review crawl budgets:

```text
JOSHBOT_CRAWL_MAX_DEPTH >= 0
JOSHBOT_CRAWL_MAX_PAGES > 0
JOSHBOT_CRAWL_MAX_PAGE_BYTES > 0
JOSHBOT_CRAWL_REQUEST_DELAY >= 0
JOSHBOT_CRAWL_REDIRECT_LIMIT > 0
```

The defaults are:

```dotenv
JOSHBOT_CRAWL_MAX_DEPTH=4
JOSHBOT_CRAWL_MAX_PAGES=32
JOSHBOT_CRAWL_MAX_PAGE_BYTES=1048576
JOSHBOT_CRAWL_REQUEST_DELAY=1s
JOSHBOT_CRAWL_REDIRECT_LIMIT=5
```

The root page is depth zero. Redirect hops do not consume additional frontier
slots. Requests within one source crawl are sequential.

## Database secrets

Generate four different database passwords:

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
    > /srv/joshbot/secrets/joshbot_backup_password

  chmod 0444 \
    /srv/joshbot/secrets/postgres_admin_password \
    /srv/joshbot/secrets/joshbot_migrator_password \
    /srv/joshbot/secrets/joshbot_app_password \
    /srv/joshbot/secrets/joshbot_backup_password
'
```

The containing `/srv/joshbot/secrets` directory remains `0700 root:root`.
The four database password files are `0444 root:root`.

The files must be readable by the non-root users inside the containers that
receive them. The `0700 root:root` parent directory prevents unrelated host
users from traversing the secrets directory, while Docker selectively mounts
only the individual secret files required by each service.

Verify the host-side ownership and modes before starting PostgreSQL:

```bash
sudo stat \
  --format='%a %U:%G %n' \
  /srv/joshbot/secrets \
  /srv/joshbot/secrets/postgres_admin_password \
  /srv/joshbot/secrets/joshbot_migrator_password \
  /srv/joshbot/secrets/joshbot_app_password \
  /srv/joshbot/secrets/joshbot_backup_password
```

Expected permissions are:

```text
700 root:root /srv/joshbot/secrets
444 root:root /srv/joshbot/secrets/postgres_admin_password
444 root:root /srv/joshbot/secrets/joshbot_migrator_password
444 root:root /srv/joshbot/secrets/joshbot_app_password
444 root:root /srv/joshbot/secrets/joshbot_backup_password
```

Do not:

- commit secret files;
- put passwords in `compose.yaml`;
- put passwords in `deploy/.env`;
- put passwords in database URLs;
- print passwords in logs.

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

```bash
docker compose \
  --env-file deploy/.env \
  --profile tools \
  --profile backup \
  --profile publisher \
  config \
  --quiet
```

Fix missing paths and variables before starting services.

## Build

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

Check state:

```bash
docker compose \
  --env-file deploy/.env \
  ps \
  --all
```

The migration container should exit zero. PostgreSQL, worker, and discovery
should be healthy.

## Migrations

Migrations are embedded from:

```text
internal/store/migrations/
```

Never edit an applied migration. Add a forward migration.

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
resources.

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

## Container inspection

```bash
docker compose \
  --env-file deploy/.env \
  --profile tools \
  --profile backup \
  --profile publisher \
  create

docker compose \
  --env-file deploy/.env \
  --profile tools \
  --profile backup \
  --profile publisher \
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

## Graceful shutdown

Stop network workers first:

```bash
docker compose \
  --env-file deploy/.env \
  stop \
  worker \
  discovery
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
  config \
  --quiet

./scripts/release-check.sh
./deploy/publish_test.sh
./deploy/smoke.sh
```

Run the complete Go quality suite from the repository root as documented in
[README.md](../README.md).