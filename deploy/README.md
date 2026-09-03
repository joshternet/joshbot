# JoshBot deployment and recovery

This is the reference deployment I use for JoshBot.

I keep PostgreSQL, migrations, verification workers, discovery, operator tools, and backups seperate on purpose. Each service only gets the network access, credentials, and storage it actually needs.

This is still a reference setup. Before putting it on a real host, I need to review the actual host paths, firewall backend, Docker versions, and NAS mount instead of guessing.

## Runtime guarantee

JoshBot workers use at-least-once execution.

A verification request may physically happen more than once after a crash or expired lease. Only the worker holding the current PostgreSQL lease can commit the result.

Successful completion records the observation, releases the lease, and schedules the next verification in one PostgreSQL transaction.

Manually scheduled work is recurring. Discovery work starts as a one-shot probe. A valid declaration promotes the probe to recurring work. Every non-valid probe records its observation and then leaves the queue in the same transaction.

JoshBot does not promise exactly-once network requests.

## Services

The Compose deployment includes:

- `postgres`: persistent PostgreSQL 18.6 database;
- `migrate`: one-shot schema migration service;
- `worker`: long-running verification worker;
- `discovery`: long-running verified-homepage discovery service;
- `tools`: operator commands for health, scheduling, and exports;
- `backup`: logical PostgreSQL backup utility.

Only the worker and discovery services are connected to the egress network.

PostgreSQL, migrations, tools, and backups only use the internal database network. PostgreSQL does not publish port 5432 to the host.

No service gets the Docker socket or host networking.

## Host information I need before deployment

Before deploying on the real host, I need to record and review:

```bash
docker version
docker compose version
cat /etc/os-release
docker info
```

I also need to confirm:

- the Docker firewall backend;
- the local PostgreSQL storage path;
- the export path;
- the actual NAS backup mount;
- the backup schedule.

I dont want to add firewall rules or scheduled backups until those values are confirmed on the real host.

## Host requirements

The host needs:

- Linux;
- Docker Engine;
- Docker Compose v2;
- OpenSSL for generating secrets;
- local persistent storage for PostgreSQL;
- a seperate mounted backup destination;
- enough space for images, database data, exports, and backups.

Check the Compose version:

```bash
docker compose version
```

After the environment file is configured, always validate Compose before starting anything:

```bash
docker compose \
  --env-file deploy/.env \
  config \
  --quiet
```

## Host directories

The example configuration uses:

```text
/srv/joshbot/postgres
/srv/joshbot/exports
/srv/joshbot/secrets
/mnt/nas/joshbot/backups
```

These are examples. The real host paths need to be chosen deliberately.

Live PostgreSQL data must stay on local persistent storage. It should not be placed on the NAS backup mount.

Compose uses `create_host_path: false`, so the source directories must already exist. This is intentional because I would rather fail on a typo than silently create an empty directory in the wrong place.

For the example paths:

```bash
sudo install \
  -d \
  -m 0700 \
  -o 999 \
  -g 999 \
  /srv/joshbot/postgres

sudo install \
  -d \
  -m 0750 \
  -o "$(id -u)" \
  -g "$(id -g)" \
  /srv/joshbot/exports

sudo install \
  -d \
  -m 0700 \
  -o root \
  -g root \
  /srv/joshbot/secrets
```

Do not create the backup directory until the NAS mount is confirmed.

## Confirm local PostgreSQL storage

Check the filesystem containing the PostgreSQL directory:

```bash
findmnt \
  --target /srv/joshbot/postgres \
  --output TARGET,SOURCE,FSTYPE,OPTIONS
```

Make sure its the intended local filesystem.

The PostgreSQL 18 image persists `/var/lib/postgresql`. PostgreSQL 18 stores its versioned data directory below that location.

Do not change the mount target to `/var/lib/postgresql/data`.

## Confirm the NAS backup mount

A directory existing does not prove the NAS is mounted.

Before running a backup, inspect the selected path:

```bash
findmnt \
  --target /mnt/nas/joshbot/backups \
  --output TARGET,SOURCE,FSTYPE,OPTIONS

df -h /mnt/nas/joshbot/backups
```

Confirm that:

- the source is the intended NAS;
- the filesystem type is correct;
- the path is not falling back to the host root filesystem;
- the mount is writable;
- there is enough free space.

Stop if the NAS is not mounted. A missing NAS should never cause backups to quietly fill the hosts local disk.

## Environment configuration

Copy the example file:

```bash
cp \
  deploy/.env.example \
  deploy/.env
```

Edit `deploy/.env` and replace the example paths with the actual host paths.

The real environment file is ignored by Git.

Database URLs in `compose.yaml` do not contain passwords. Passwords are read from mounted secret files.

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

A worker needs enough lease time left to commit its result after verification finishes.

Review the discovery timing values too:

```text
JOSHBOT_DISCOVERY_INTERVAL > 0
JOSHBOT_DISCOVERY_POLL_INTERVAL > 0
JOSHBOT_DISCOVERY_PAGE_TIMEOUT > 0
```

The reference discovery interval is `168h`. Go durations do not accept `7d`.

## Create secrets

Generate four different passwords:

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

The JoshBot image runs as UID/GID `65532:65532`.

The PostgreSQL image uses UID/GID `999:999` for the PostgreSQL process.

The PostgreSQL initialization process needs to read the migrator, application, and backup passwords. The application containers also need to read the specific secret assigned to them.

For the reference Linux ownership setup:

```bash
sudo chown \
  root:root \
  /srv/joshbot/secrets/postgres_admin_password

sudo chmod \
  0400 \
  /srv/joshbot/secrets/postgres_admin_password

sudo chown \
  65532:999 \
  /srv/joshbot/secrets/joshbot_migrator_password \
  /srv/joshbot/secrets/joshbot_app_password

sudo chmod \
  0440 \
  /srv/joshbot/secrets/joshbot_migrator_password \
  /srv/joshbot/secrets/joshbot_app_password

sudo chown \
  999:999 \
  /srv/joshbot/secrets/joshbot_backup_password

sudo chmod \
  0400 \
  /srv/joshbot/secrets/joshbot_backup_password
```

Do not:

- commit secret files;
- put passwords in `compose.yaml`;
- put passwords in `deploy/.env`;
- put passwords in `JOSHBOT_DATABASE_URL`;
- print passwords in logs.

## Validate the configuration

Run:

```bash
docker compose \
  --env-file deploy/.env \
  config \
  --quiet
```

Fix any missing path, secret, or required variable before starting the deployment.

## Build the JoshBot image

Run:

```bash
docker compose \
  --env-file deploy/.env \
  build \
  --pull \
  migrate \
  worker \
  discovery \
  tools
```

The final JoshBot image:

- contains a statically linked binary;
- contains the CA certificate bundle needed for HTTPS;
- runs as UID/GID `65532:65532`;
- has no shell;
- has no Go compiler;
- has no Git client;
- has no curl or wget;
- has no PostgreSQL client tools.

PostgreSQL backup tools stay in the seperate PostgreSQL utility image.

## First startup

Start the normal service set:

```bash
docker compose \
  --env-file deploy/.env \
  up \
  --build \
  --detach \
  --wait
```

This starts PostgreSQL, waits for it to become healthy, runs migrations, and starts the worker and discovery services after migration completes succesfully.

Check the service state:

```bash
docker compose \
  --env-file deploy/.env \
  ps \
  --all
```

The migration container should exit with code zero. PostgreSQL, the worker, and discovery should be healthy.

## Migration behavior

Migrations are embedded from:

```text
internal/store/migrations/
```

The migration runner:

- serializes concurrent migration attempts with a PostgreSQL advisory lock;
- applies pending migrations transactionally;
- records migration versions, names, checksums, and application times;
- rejects changes to migrations that were already applied;
- treats an already-current schema as a successful no-op.

Do not modify an applied migration. Add a new forward migration instead.

Run migrations manually with:

```bash
docker compose \
  --env-file deploy/.env \
  run \
  --rm \
  --no-deps \
  migrate
```

## Health check

Check database connectivity through the JoshBot application:

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

The health command does not contact the public Internet.

## Schedule an origin

Schedule an origin through the canonical origin parser:

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

The canonical queue value becomes:

```text
https://example.com
```

Scheduling an origin does not create a verification observation by itself.

Manual scheduling always means recurring verification. If the origin already has an active discovery probe lease, scheduling promotes it to recurring without invalidating that lease.

## Worker operation

The worker:

- claims due origins using PostgreSQL lease authority;
- runs verification with a bounded context;
- leaves enough lease time for atomic completion;
- uses the existing network guard, robots checker, and declaration verifier;
- commits only while its lease is still authoritative;
- attempts more work immediately after successful completion;
- waits without busy-spinning when no work is available;
- handles SIGINT and SIGTERM through context cancellation.

If the worker dies before completion, it does not create a fake observation. The lease expires and another worker can recover the work.

View worker logs:

```bash
docker compose \
  --env-file deploy/.env \
  logs \
  --follow \
  worker
```

Restart only the worker:

```bash
docker compose \
  --env-file deploy/.env \
  restart \
  worker
```

## Discovery operation

Discovery starts only from origins whose current effective authoritative declaration is valid. Undeclared, affirmed, and declined identities are all valid participants and are equally eligible discovery sources.

A discovery attempt fetches only the canonical homepage `/`, plus at most five same-origin redirects needed to reach that homepage representation. Every request goes through the existing robots checker and network guard. Same-origin links are ignored and never crawled. A cross-origin homepage redirect is not followed; its canonical origin becomes one redirect candidate instead.

A hyperlink is not proof of Joshness, participation, trust, ownership, or endorsement. Discovery only says that an origin may be worth independently probing.

The static HTML parser:

- executes no JavaScript, CSS, or remote resources;
- accepts HTML and XHTML responses;
- reads at most 1 MiB of response bytes;
- permits at most 2 MiB after character-set conversion;
- keeps the first 64 unique external origins encountered;
- returns those origins in canonical sorted order;
- honors the first document `<base href>`;
- extracts only `<a href>` and `<area href>` destinations.

JoshBot stores only the canonical source origin, canonical candidate origin, discovery kind, and first/last discovery timestamps. It does not store raw hrefs, page paths, anchor text, HTML, headers, addresses, DNS answers, or TLS details. Each source can retain at most 1024 distinct historical candidate origins.

Discovery creates a `probe` queue row only when the candidate has no queue row. It never changes an existing probe and never demotes recurring work. A valid probe becomes recurring. A non-valid probe records the observation and is removed until a later permitted discovery sees the candidate again.

Candidate provenance, discovery timing, and queue mode stay private. None of those fields enter the public registry. A candidate appears publicly only after its own declaration independently verifies as valid.

View discovery logs:

```bash
docker compose \
  --env-file deploy/.env \
  logs \
  --follow \
  discovery
```

Run at most one due discovery source and exit:

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

If no verified source is due, this succeeds without making a network request.

## Export the public registry

The export destination must not already exist.

Create a unique snapshot name:

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

The snapshot appears below the configured host export directory.

The worker and discovery services cannot access this mount.

## Create a manual backup

First prove the backup directory is really on the intended NAS mount.

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

- connects through the internal database network;
- uses the read-only backup role;
- creates a PostgreSQL custom-format archive;
- writes a `.partial` file first;
- validates it with `pg_restore --list`;
- sets mode `0600`;
- atomically renames the validated file to `.dump`;
- removes the partial file if something fails.

The backup does not copy live PostgreSQL data.

The worker, discovery, tools, and PostgreSQL services do not have access to the backup directory.

## Validate a backup archive

List the available archives:

```bash
find \
  /mnt/nas/joshbot/backups \
  -maxdepth 1 \
  -type f \
  -name 'joshbot-*.dump' \
  -print
```

Choose the exact archive deliberately, then validate it:

```bash
docker compose \
  --env-file deploy/.env \
  --profile backup \
  run \
  --rm \
  --no-deps \
  --entrypoint pg_restore \
  backup \
  --list \
  /backups/joshbot-YYYYMMDDTHHMMSSZ.dump
```

This deployment does not automatically delete old backups. Retention is a seperate destructive policy decision.

## Safe recovery drill

Run the disposable recovery test:

```bash
./deploy/smoke.sh
```

The smoke test:

- creates unique disposable directories and secrets;
- uses a unique Compose project;
- initializes PostgreSQL 18.6;
- applies migrations through discovery migration 0003;
- verifies least-privilege roles;
- runs one-shot discovery with no verified source, so no public request occurs;
- schedules a fake origin without crawling it;
- creates known observations, private provenance, and probe/recurring queue state;
- exports deterministic registry data;
- proves private discovery state leaves the public export byte-identical;
- creates and validates a logical backup;
- starts a seperate internal-only PostgreSQL restore instance;
- restores the backup transactionally;
- reruns current migrations;
- verifies application connectivity;
- verifies observations, effective state, private provenance, queue modes, and migration metadata;
- rebuilds byte-identical public registry files;
- removes its containers, networks, image, database, exports, backups, and secrets.

The smoke test never restores into the reference production PostgreSQL service.

## Recovering a real backup

Do not restore a real archive into the running production database.

A real recovery requires:

1. selecting and recording the exact backup archive;
2. creating a new empty PostgreSQL 18.6 instance;
3. confirming its container name, network, credentials, and storage are seperate;
4. restoring with `pg_restore --no-owner --no-privileges`;
5. running `joshbot migrate`;
6. running `joshbot health`;
7. verifying observations, effective state, queue rows, and exports;
8. switching the application only after the restored system is verified.

Stop and make a host-specific recovery plan before doing this. `pg_restore` can be destructive when it is pointed at the wrong database.

## Container security checks

List the deployed containers:

```bash
docker compose \
  --env-file deploy/.env \
  ps \
  --all \
  --quiet
```

Inspect each container:

```bash
docker inspect CONTAINER_ID
```

Confirm:

- JoshBot services run as UID/GID `65532:65532`;
- worker, discovery, migration, and tools root filesystems are read-only;
- configured services drop all Linux capabilities;
- `no-new-privileges` is enabled;
- no service is privileged;
- no service uses host networking;
- no service mounts `/var/run/docker.sock`;
- only the worker and discovery services are connected to egress;
- only tools receives the export mount;
- only backup receives the backup mount;
- PostgreSQL does not publish a host port;
- worker and discovery have no export, backup, or PostgreSQL data mount.

The deployment smoke test checks these properties against the actual containers instead of only trusting the YAML.

## Host firewall review

JoshBot still uses its network guard inside the worker and discovery service. The host firewall should provide a second boundary against private, LAN, management, metadata, and other special-purpose destinations.

Do not apply firewall rules until the actual Docker firewall backend and host network layout are known.

Collect read-only information:

```bash
docker info

sudo test ! -f /etc/docker/daemon.json ||
  sudo cat /etc/docker/daemon.json

sudo nft list ruleset

sudo iptables-save
```

Review:

- whether Docker uses iptables or nftables;
- the worker and discovery egress bridge;
- Docker DNS requirements;
- host LAN networks;
- host management networks;
- metadata and special-purpose networks.

If Docker uses iptables, use the current Docker-supported operator filtering path.

If Docker uses nftables, do not assume `DOCKER-USER` exists. Do not modify Docker-owned nftables tables. Use a seperate operator-owned table and chain appropriate for the confirmed backend.

Firewall changes require their own human review.

## Graceful shutdown

Stop the worker and discovery services first:

```bash
docker compose \
  --env-file deploy/.env \
  stop \
  worker \
  discovery
```

SIGTERM cancels current bounded work. If a job has not completed, its lease remains in PostgreSQL and becomes recoverable after it expires.

Create and validate a final logical backup if one is required.

Stop the remaining services without removing host data:

```bash
docker compose \
  --env-file deploy/.env \
  down
```

Do not delete the PostgreSQL host directory unless that destructive operation was explicitly approved.

## Upgrade procedure

Before upgrading:

1. verify the NAS mount;
2. create a logical backup;
3. validate the backup with `pg_restore --list`;
4. record the currently deployed image;
5. review new migrations and compatibility.

Build the new image:

```bash
docker compose \
  --env-file deploy/.env \
  build \
  --pull \
  migrate \
  worker \
  discovery \
  tools
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

Recreate the worker and discovery services:

```bash
docker compose \
  --env-file deploy/.env \
  up \
  --detach \
  --no-deps \
  worker \
  discovery
```

Check health and logs before calling the upgrade complete.

## Rollback and recovery

Database migrations are forward-only.

Do not assume an older application image is still compatible after a migration.

If an application rollback is unsafe:

- stop the worker and discovery services;
- preserve the failed deployment state;
- select a validated pre-upgrade backup;
- restore into a fresh PostgreSQL instance;
- test the restored database with the intended application image;
- switch only after application-level validation.

Never overwrite the only recoverable database while trying to diagnose a failed upgrade.

## Backup scheduling

This phase does not create or enable a backup scheduler.

After manual backup and recovery are proven on the real host, I still need to choose:

- the backup schedule;
- the retention policy;
- the confirmed NAS mount;
- the systemd unit location.

A future systemd backup service should require the selected mount with `RequiresMountsFor=` or another reviewed mechanism.

Do not enable a timer until the NAS mount and schedule are approved.

## Continuous integration

The quality workflow runs two independent jobs:

- Go, PostgreSQL, race, repeatability, and 100% coverage;
- production image, container security, deployment, backup, restore, and cleanup.

Both jobs upload reports and add summaries to the GitHub Actions run.

A pull request is not ready just because the Go tests pass. The deployment and recovery job also has to pass, and I need to inspect its downloaded report.

## Local validation

Run:

```bash
chmod 0755 \
  deploy/backup.sh \
  deploy/postgres/init/010-joshbot-roles.sh \
  deploy/smoke.sh

bash -n deploy/backup.sh
bash -n deploy/postgres/init/010-joshbot-roles.sh
bash -n deploy/smoke.sh

Psych_command='Psych.parse_file(ARGV.fetch(0))'
ruby \
  -rpsych \
  -e "$Psych_command" \
  .github/workflows/quality.yml

./deploy/smoke.sh

export PATH="/opt/homebrew/opt/postgresql@18/bin:$PATH"
export JOSHBOT_TEST_DATABASE_URL="postgres://$(whoami)@localhost:55432/joshbot_test?host=%2Ftmp&sslmode=disable"

JOSHBOT_REQUIRE_DATABASE_TESTS=1 \
go test \
  -count=1 \
  ./...

JOSHBOT_REQUIRE_DATABASE_TESTS=1 \
go test \
  -count=10 \
  -race \
  -coverprofile=/tmp/joshbot-phase9-final-coverage.out \
  ./...

go tool cover \
  -func=/tmp/joshbot-phase9-final-coverage.out

go test \
  ./internal/discovery \
  -run '^$' \
  -fuzz '^FuzzExtract$' \
  -fuzztime=10s

go vet ./...
go mod tidy -diff
gofmt -l .
git diff --check
git status --short --branch
```

Coverage must remain exactly `100.0%`.
