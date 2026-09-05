# JoshBot deployment and recovery

This is the reference deployment I use for JoshBot.

PostgreSQL, migrations, workers, discovery, operator tools, publication, and backups are kept seperate on purpose. Each service only receives the network access, credentials, and storage it needs.

Before putting this on a real host I still need to review the actual paths, Docker firewall backend, NAS mount, publication repository, and schedules. I dont want to guess at any of those values.

## Runtime guarantees

JoshBot workers use at-least-once execution.

A verification request can physically happen more than once after a crash or expired lease. Only the worker holding the current PostgreSQL lease can commit the result.

Successful completion records the observation, releases the lease, and schedules the next verification in one PostgreSQL transaction.

Manually scheduled work is recurring. Discovery work starts as a one-shot probe. A valid declaration promotes the probe to recurring work. Every non-valid probe records its observation and leaves the queue in the same transaction.

JoshBot does not promise exactly-once network requests.

Registry publication is seperate from verification. Publication reads an already-built deterministic snapshot and does not connect to PostgreSQL.

## Services

The Compose deployment includes:

- `postgres`: persistent PostgreSQL 18.6 database;
- `migrate`: one-shot schema migrations;
- `worker`: long-running verification worker;
- `discovery`: long-running verified-homepage discovery;
- `tools`: database-backed operator commands and exports;
- `publisher`: one-shot GitHub registry publication;
- `backup`: logical PostgreSQL backups.

The network and credential split is intentional:

- `worker` and `discovery` receive the database network and egress;
- `tools` receives the database network and a writable export mount;
- `publisher` receives egress, a read-only export mount, and the GitHub token;
- `migrate`, `postgres`, and `backup` only receive the database network;
- no database service receives the GitHub token;
- the publisher receives no database URL, password, network, or storage;
- no service gets the Docker socket or host networking.

PostgreSQL does not publish port 5432 to the host.

## Host requirements

The host needs:

- Linux;
- Docker Engine;
- Docker Compose v2;
- OpenSSL;
- local persistent storage for PostgreSQL;
- an export directory;
- a seperate mounted backup destination;
- enough storage for images, PostgreSQL, exports, and backups.

Record the actual environment before deployment:

```bash
docker version
docker compose version
cat /etc/os-release
docker info
```

I also need to confirm:

- the Docker firewall backend;
- the PostgreSQL storage path;
- the export path;
- the real NAS backup mount;
- the backup schedule;
- the publication schedule.

## Host directories

The example configuration uses:

```text
/srv/joshbot/postgres
/srv/joshbot/exports
/srv/joshbot/secrets
/mnt/nas/joshbot/backups
```

These are examples, not automatic choices.

PostgreSQL data must stay on local persistent storage. It should not be placed on the NAS backup mount.

Compose uses `create_host_path: false`, so the source directories must already exist. This is intentional because I would rather fail on a typo than silently create an empty directory somewhere unexpected.

The JoshBot image runs as UID/GID `65532:65532`. The PostgreSQL image uses UID/GID `999:999`.

The tools container needs write access to the export directory. The host account running `deploy/publish.sh` also needs permission to remove publication snapshots created by UID 65532.

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

Do not create the backup directory until the NAS mount is confirmed.

## Confirm storage

Check the PostgreSQL filesystem:

```bash
findmnt \
  --target /srv/joshbot/postgres \
  --output TARGET,SOURCE,FSTYPE,OPTIONS
```

The PostgreSQL 18 image persists `/var/lib/postgresql`. Do not change the mount target to `/var/lib/postgresql/data`.

Confirm that the backup directory is really on the NAS:

```bash
findmnt \
  --target /mnt/nas/joshbot/backups \
  --output TARGET,SOURCE,FSTYPE,OPTIONS

df -h /mnt/nas/joshbot/backups
```

A directory existing does not prove the NAS is mounted. Stop if the path falls back to the hosts local filesystem.

## Environment configuration

Copy the example:

```bash
cp \
  deploy/.env.example \
  deploy/.env
```

Replace the example paths and publication target with the real values.

The actual environment file is ignored by Git. Database passwords and the GitHub token do not belong in it.

Review the worker timing values:

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

Review discovery timing too:

```text
JOSHBOT_DISCOVERY_INTERVAL > 0
JOSHBOT_DISCOVERY_POLL_INTERVAL > 0
JOSHBOT_DISCOVERY_PAGE_TIMEOUT > 0
```

The reference discovery interval is `168h`. Go durations do not accept `7d`.

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
'
```

Do not:

- commit secret files;
- put passwords in `compose.yaml`;
- put passwords in `deploy/.env`;
- put passwords in database URLs;
- print passwords in logs.

## Publication repository

The publication target must be a dedicated data repository and branch controlled entirely by JoshBot.

Do not point the publisher at:

```text
joshternet/joshbot
joshternet/joshternet.github.io
```

A small dedicated public repository such as `joshternet/index-data` is the intended shape, but the final repository name is an operator decision.

The machine-managed branch is replaced with an exact generated tree. Do not manually store README files, workflows, or anything else on that branch.

Before the first publication, the target branch must already exist and contain this root file:

```text
.joshbot-registry-target
```

Its exact contents must be:

```text
joshbot-registry-v1
```

That text includes one final newline.

The publisher will not:

- create a repository;
- create an organization;
- create a token;
- change Pages settings;
- change repository settings;
- create a workflow;
- force-update a Git reference.

## GitHub token

Use a fine-grained token restricted to the dedicated publication repository.

The required repository permission is:

```text
Contents: Read and write
```

Do not grant Administration, Actions, Workflows, Issues, Pull requests, Secrets, or unrelated permissions.

Store the token only in the configured file:

```text
/srv/joshbot/secrets/joshbot_github_token
```

Do not put it in:

- `compose.yaml`;
- `deploy/.env`;
- command arguments;
- logs;
- PostgreSQL;
- registry output;
- chat.

The publisher container receives the token file but no database credential. Database services do not receive the token.

## Validate Compose

Run:

```bash
docker compose \
  --env-file deploy/.env \
  --profile tools \
  --profile backup \
  --profile publisher \
  config \
  --quiet
```

Fix missing paths or variables before starting anything.

## Build the image

Run:

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
- contains the CA certificate bundle;
- runs as UID/GID `65532:65532`;
- has no shell;
- has no Go compiler;
- has no Git client;
- has no `gh`;
- has no curl or wget;
- has no PostgreSQL client tools.

## Start the normal runtime

Run:

```bash
docker compose \
  --env-file deploy/.env \
  up \
  --build \
  --detach \
  --wait
```

This starts PostgreSQL, applies migrations, and starts the worker and discovery services. The publisher is profile-controlled and is not a daemon.

Check the state:

```bash
docker compose \
  --env-file deploy/.env \
  ps \
  --all
```

The migration container should exit with code zero. PostgreSQL, worker, and discovery should be healthy.

## Migrations

Migrations are embedded from:

```text
internal/store/migrations/
```

Applied migrations must never be edited. Add a new forward migration instead.

Run them manually with:

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

The canonical scheduled origin becomes:

```text
https://example.com
```

## Export the public registry

The output directory must not already exist:

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

The tools container has database access and a writable export mount. It has no egress and no GitHub token.

## Publish the public registry

`deploy/publish.sh` performs one publication attempt using two separate containers:

1. `tools` exports a fresh deterministic snapshot;
2. `publisher` reads that snapshot and publishes it;
3. the temporary local snapshot is removed after success or failure.

The script expects the deployment variables to already be present in its environment. A service manager can load the reviewed `deploy/.env` as its environment file.

Run:

```bash
./deploy/publish.sh
```

A changed publication prints:

```text
published COMMIT_SHA
```

An identical publication prints:

```text
registry unchanged
```

The publisher uses the official GitHub API directly. It rejects redirects, ignores HTTP proxy environment variables, requires the target sentinel, creates an exact tree without `base_tree`, and updates the branch without force.

The temporary snapshot is transport staging. It is not a backup.

## Manual backup

First prove that the backup directory is on the intended NAS mount.

Then run:

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
- writes a `.partial` file first;
- validates it with `pg_restore --list`;
- sets mode `0600`;
- atomically renames it to `.dump`;
- removes a partial file after failure.

Retention is a seperate destructive policy decision and is not automated here.

## Recovery drill

Run:

```bash
./deploy/smoke.sh
```

The recovery smoke test:

- creates disposable directories and secrets;
- initializes PostgreSQL;
- applies migrations;
- checks database role boundaries;
- runs a no-network discovery attempt;
- creates known verification and discovery state;
- exports deterministic registry data;
- proves private discovery data does not alter public output;
- creates and validates a backup;
- restores into a fresh isolated PostgreSQL instance;
- verifies application connectivity and restored data;
- rebuilds byte-identical registry files;
- cleans containers, networks, images, storage, and secrets.

It never restores into the reference production database.

## Publication boundary smoke

Run:

```bash
./deploy/publish_test.sh
```

This test uses a fake Docker command for host-script execution. It never contacts GitHub and never needs a real token.

It verifies actual container configuration for:

- non-root execution;
- read-only root filesystem;
- dropped capabilities;
- `no-new-privileges`;
- no published ports;
- no Docker socket;
- egress without the database network;
- read-only export mount;
- GitHub secret present only in the publisher;
- database credentials absent from the publisher;
- separate exporter and publisher containers;
- snapshot cleanup after success and failure;
- preservation of the publisher failure status;
- no token logging.

## Container inspection

Inspect actual containers with:

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

Then inspect specific container IDs:

```bash
docker inspect CONTAINER_ID
```

The publisher must have:

- UID/GID `65532:65532`;
- read-only root filesystem;
- all capabilities dropped;
- `no-new-privileges`;
- only the egress network;
- a read-only `/exports`;
- `/run/secrets/joshbot_github_token`;
- no database configuration;
- no database network;
- no backup or PostgreSQL mount;
- no Docker socket;
- no published ports.

## Graceful shutdown

Stop worker and discovery first:

```bash
docker compose \
  --env-file deploy/.env \
  stop \
  worker \
  discovery
```

Then stop the remaining services without deleting host data:

```bash
docker compose \
  --env-file deploy/.env \
  down
```

Do not delete the PostgreSQL directory unless that destructive operation was explicitly approved.

## Upgrade procedure

Before upgrading:

1. verify the NAS mount;
2. create and validate a backup;
3. record the currently deployed image;
4. review new migrations;
5. confirm application compatibility.

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

Recreate the long-running services:

```bash
docker compose \
  --env-file deploy/.env \
  up \
  --detach \
  --no-deps \
  worker \
  discovery
```

The publisher remains one-shot.

## Continuous integration

The quality workflow runs:

- formatting, module, vet, PostgreSQL, normal test, fuzz, race, and coverage gates;
- production image inspection;
- publication boundary tests;
- deployment, backup, restore, and cleanup smoke tests.

CI uses fake publication infrastructure. It does not require or receive a real GitHub token.

Both jobs upload reports and add summaries to the Actions run.

## Local validation

Run all validation only after every Phase 10 file is in place:

```bash
chmod 0755 \
  deploy/backup.sh \
  deploy/postgres/init/010-joshbot-roles.sh \
  deploy/publish.sh \
  deploy/publish_test.sh \
  deploy/smoke.sh

bash -n deploy/backup.sh
bash -n deploy/postgres/init/010-joshbot-roles.sh
bash -n deploy/publish.sh
bash -n deploy/publish_test.sh
bash -n deploy/smoke.sh

Psych_command='Psych.parse_file(ARGV.fetch(0))'
ruby \
  -rpsych \
  -e "$Psych_command" \
  .github/workflows/quality.yml

./deploy/publish_test.sh
./deploy/smoke.sh
```

Then run the Go quality suite described at the end of this phase.