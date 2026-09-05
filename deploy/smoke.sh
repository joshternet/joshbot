#!/usr/bin/env bash

set -Eeuo pipefail

umask 077

POSTGRES_IMAGE="postgres:18.6-bookworm@sha256:1c59e2c3c818eaa0f0628f695b36e7c9e362d6b219b36a54a32df645cbd7e1af"
readonly POSTGRES_IMAGE

script_directory="$(
  CDPATH='' cd -- "$(dirname -- "$0")" &&
    pwd -P
)"
readonly script_directory

repository_root="$(
  CDPATH='' cd -- "$script_directory/.." &&
    pwd -P
)"
readonly repository_root

compose_file="$repository_root/compose.yaml"
readonly compose_file

smoke_token="$(date -u '+%Y%m%d%H%M%S')-$$"
readonly smoke_token

smoke_root="$repository_root/external/deployment-smoke-$smoke_token"
readonly smoke_root

restore_container="joshbot-restore-postgres-$smoke_token"
readonly restore_container

restore_network="joshbot-restore-$smoke_token"
readonly restore_network

restore_database="joshbot_restore"
readonly restore_database

restore_user="joshbot_restore"
readonly restore_user

restore_password_file="$smoke_root/secrets/joshbot_restore_password"
readonly restore_password_file

expected_root="$smoke_root/expected"
readonly expected_root

restore_export_directory="$smoke_root/restore-exports"
readonly restore_export_directory

expected_node_path="nodes/10/100680ad546ce6a577f42f52df33b4cfdca756859e664b8d7de329b150d09ce9.json"
readonly expected_node_path

export COMPOSE_PROJECT_NAME="joshbot-smoke-$smoke_token"
export JOSHBOT_IMAGE="joshbot-smoke:$smoke_token"

export JOSHBOT_POSTGRES_DATA_DIR="$smoke_root/postgres"
export JOSHBOT_EXPORT_DIR="$smoke_root/exports"
export JOSHBOT_BACKUP_DIR="$smoke_root/backups"

export JOSHBOT_POSTGRES_ADMIN_PASSWORD_FILE="$smoke_root/secrets/postgres_admin_password"
export JOSHBOT_MIGRATOR_PASSWORD_FILE="$smoke_root/secrets/joshbot_migrator_password"
export JOSHBOT_APP_PASSWORD_FILE="$smoke_root/secrets/joshbot_app_password"
export JOSHBOT_BACKUP_PASSWORD_FILE="$smoke_root/secrets/joshbot_backup_password"

export JOSHBOT_WORKER_ID="smoke-worker-$smoke_token"
export JOSHBOT_LEASE_DURATION="5m"
export JOSHBOT_MIN_ORIGIN_INTERVAL="1m"
export JOSHBOT_POLL_INTERVAL="30s"
export JOSHBOT_JOB_TIMEOUT="2m"
export JOSHBOT_COMPLETION_GRACE="30s"
export JOSHBOT_RECHECK_INTERVAL="24h"
export JOSHBOT_DISCOVERY_INTERVAL="168h"
export JOSHBOT_DISCOVERY_POLL_INTERVAL="30s"
export JOSHBOT_DISCOVERY_PAGE_TIMEOUT="30s"

unset COMPOSE_FILE
unset COMPOSE_PROFILES

compose() {
  docker compose \
    --file "$compose_file" \
    "$@"
}

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

pass() {
  printf 'PASS: %s\n' "$*"
}

assert_equal() {
  local actual
  local expected
  local description

  actual="$1"
  expected="$2"
  description="$3"

  if [[ "$actual" != "$expected" ]]; then
    printf 'FAIL: %s\n' "$description" >&2
    printf '  got:  %s\n' "$actual" >&2
    printf '  want: %s\n' "$expected" >&2
    exit 1
  fi
}

assert_nonempty() {
  local actual
  local description

  actual="$1"
  description="$2"

  if [[ -z "$actual" ]]; then
    fail "$description"
  fi
}

create_secret() {
  local destination

  destination="$1"

  openssl rand -hex 32 >"$destination"
  chmod 0444 "$destination"
}

inspect_mount_destinations() {
  local container_id

  container_id="$1"

  docker inspect \
    "$container_id" \
    --format '{{range .Mounts}}{{println .Destination}}{{end}}' |
    sed '/^$/d' |
    sort
}

inspect_network_names() {
  local container_id

  container_id="$1"

  docker inspect \
    "$container_id" \
    --format '{{range $name, $_ := .NetworkSettings.Networks}}{{println $name}}{{end}}' |
    sed '/^$/d' |
    sort
}

assert_hardened_container() {
  local container_id
  local expected_user
  local description
  local network_mode

  container_id="$1"
  expected_user="$2"
  description="$3"

  assert_equal \
    "$(docker inspect "$container_id" --format '{{.Config.User}}')" \
    "$expected_user" \
    "$description user"

  assert_equal \
    "$(docker inspect "$container_id" --format '{{.HostConfig.ReadonlyRootfs}}')" \
    "true" \
    "$description read-only root filesystem"

  assert_equal \
    "$(docker inspect "$container_id" --format '{{.HostConfig.Privileged}}')" \
    "false" \
    "$description privileged state"

  assert_equal \
    "$(docker inspect "$container_id" --format '{{json .HostConfig.CapDrop}}')" \
    '["ALL"]' \
    "$description dropped capabilities"

  assert_equal \
    "$(docker inspect "$container_id" --format '{{json .HostConfig.SecurityOpt}}')" \
    '["no-new-privileges:true"]' \
    "$description no-new-privileges setting"

  assert_equal \
    "$(docker inspect "$container_id" --format '{{json .HostConfig.PortBindings}}')" \
    '{}' \
    "$description published ports"

  network_mode="$(
    docker inspect \
      "$container_id" \
      --format '{{.HostConfig.NetworkMode}}'
  )"

  if [[ "$network_mode" == "host" ]]; then
    fail "$description uses host networking"
  fi

  if inspect_mount_destinations "$container_id" |
    grep -Fxq '/var/run/docker.sock'; then
    fail "$description has access to the Docker socket"
  fi
}

expect_role_failure() {
  local role
  local statement
  local description
  local log_path

  role="$1"
  statement="$2"
  description="$3"
  log_path="$smoke_root/${description//[^a-zA-Z0-9]/-}.log"

  if compose exec \
    -T \
    postgres \
    psql \
    --username joshbot_admin \
    --dbname joshbot \
    --set=ON_ERROR_STOP=1 \
    --command="SET ROLE $role; $statement" \
    >"$log_path" \
    2>&1; then
    fail "$description unexpectedly succeeded"
  fi

  if ! grep \
    -Eq \
    'permission denied|must be owner|must be superuser' \
    "$log_path"; then
    printf 'Unexpected PostgreSQL failure:\n' >&2
    cat "$log_path" >&2
    fail "$description failed for an unexpected reason"
  fi

  pass "$description was denied"
}

run_restored_joshbot() {
  docker run \
    --rm \
    --pull never \
    --network "$restore_network" \
    --read-only \
    --cap-drop ALL \
    --security-opt no-new-privileges:true \
    --env "JOSHBOT_DATABASE_URL=postgres://$restore_user@$restore_container:5432/$restore_database?sslmode=disable" \
    --env 'JOSHBOT_DATABASE_PASSWORD_FILE=/run/secrets/joshbot_restore_password' \
    --mount "type=bind,source=$restore_password_file,target=/run/secrets/joshbot_restore_password,readonly" \
    "$JOSHBOT_IMAGE" \
    "$@"
}

cleanup() {
  local status="$?"
  local remaining_containers
  local remaining_volumes

  trap - EXIT HUP INT TERM
  set +e

  docker rm \
    --force \
    "$restore_container" \
    >/dev/null \
    2>&1

  docker network rm \
    "$restore_network" \
    >/dev/null \
    2>&1

  compose \
    --profile tools \
    --profile backup \
    down \
    --volumes \
    --remove-orphans \
    >/dev/null \
    2>&1

  remaining_containers="$(
    docker ps \
      --all \
      --quiet \
      --filter "label=com.docker.compose.project=$COMPOSE_PROJECT_NAME" \
      2>/dev/null
  )"

  remaining_volumes="$(
    docker volume ls \
      --quiet \
      --filter "label=com.docker.compose.project=$COMPOSE_PROJECT_NAME" \
      2>/dev/null
  )"

  if [[ -n "$remaining_containers" ]]; then
    printf 'FAIL: smoke-test containers remain after cleanup\n' >&2
    status=1
  fi

  if [[ -n "$remaining_volumes" ]]; then
    printf 'FAIL: smoke-test volumes remain after cleanup\n' >&2
    status=1
  fi

  if [[ -d "$smoke_root" ]] &&
    docker image inspect "$POSTGRES_IMAGE" >/dev/null 2>&1; then
    docker run \
      --rm \
      --pull never \
      --network none \
      --read-only \
      --cap-drop ALL \
      --security-opt no-new-privileges:true \
      --tmpfs '/var/lib/postgresql:ro,size=1048576,mode=0555' \
      --mount "type=bind,source=$smoke_root,target=/smoke" \
      --entrypoint /bin/sh \
      "$POSTGRES_IMAGE" \
      -c 'chmod -R a+rwX /smoke' \
      >/dev/null \
      2>&1
  fi

  case "$smoke_root" in
    "$repository_root"/external/deployment-smoke-*)
      rm -rf -- "$smoke_root"
      ;;
    *)
      printf 'FAIL: refused unsafe smoke-directory cleanup\n' >&2
      status=1
      ;;
  esac

  if [[ -d "$smoke_root" ]]; then
    printf 'FAIL: smoke directory remains after cleanup\n' >&2
    status=1
  fi

  if docker image inspect "$JOSHBOT_IMAGE" >/dev/null 2>&1; then
    docker image rm \
      "$JOSHBOT_IMAGE" \
      >/dev/null \
      2>&1

    if docker image inspect "$JOSHBOT_IMAGE" >/dev/null 2>&1; then
      printf 'FAIL: smoke-test application image remains\n' >&2
      status=1
    fi
  fi

  if [[ "$status" -eq 0 ]]; then
    pass "disposable containers, networks, volumes, image, and files were removed"
  fi

  exit "$status"
}

trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

for required_command in \
  docker \
  openssl \
  cmp \
  diff \
  find \
  grep \
  sed \
  sort \
  wc; do
  command -v "$required_command" >/dev/null 2>&1 ||
    fail "required command is unavailable: $required_command"
done

docker info >/dev/null
docker compose version >/dev/null

if [[ ! -f "$compose_file" ]]; then
  fail "compose.yaml is unavailable"
fi

if docker container inspect "$restore_container" >/dev/null 2>&1; then
  fail "restore container name already exists"
fi

if docker network inspect "$restore_network" >/dev/null 2>&1; then
  fail "restore network name already exists"
fi

mkdir "$smoke_root"

mkdir \
  "$JOSHBOT_POSTGRES_DATA_DIR" \
  "$JOSHBOT_EXPORT_DIR" \
  "$JOSHBOT_BACKUP_DIR" \
  "$smoke_root/secrets" \
  "$expected_root" \
  "$restore_export_directory"

chmod 0700 \
  "$smoke_root" \
  "$JOSHBOT_POSTGRES_DATA_DIR" \
  "$smoke_root/secrets" \
  "$expected_root"

chmod 0777 \
  "$JOSHBOT_EXPORT_DIR" \
  "$JOSHBOT_BACKUP_DIR" \
  "$restore_export_directory"

create_secret "$JOSHBOT_POSTGRES_ADMIN_PASSWORD_FILE"
create_secret "$JOSHBOT_MIGRATOR_PASSWORD_FILE"
create_secret "$JOSHBOT_APP_PASSWORD_FILE"
create_secret "$JOSHBOT_BACKUP_PASSWORD_FILE"
create_secret "$restore_password_file"

mkdir -p "$expected_root/nodes/10"

cat >"$expected_root/registry.json" <<'JSON'
{
  "format_version": 1,
  "nodes": [
    {
      "origin": "https://example.com",
      "path": "nodes/10/100680ad546ce6a577f42f52df33b4cfdca756859e664b8d7de329b150d09ce9.json",
      "declaration": {
        "version": 1,
        "josh": true
      }
    }
  ]
}
JSON

cat >"$expected_root/$expected_node_path" <<'JSON'
{
  "format_version": 1,
  "origin": "https://example.com",
  "declaration": {
    "version": 1,
    "josh": true
  }
}
JSON

pass "prepared unique disposable smoke-test paths and secrets"

compose config --quiet

pass "Docker Compose configuration is valid"

compose up \
  --build \
  --detach \
  --wait

pass "PostgreSQL, migration, worker, and discovery services started successfully"

migrate_container="$(
  compose ps \
    --all \
    --quiet \
    migrate
)"

worker_container="$(
  compose ps \
    --all \
    --quiet \
    worker
)"

discovery_container="$(
  compose ps \
    --all \
    --quiet \
    discovery
)"

postgres_container="$(
  compose ps \
    --all \
    --quiet \
    postgres
)"

assert_nonempty \
  "$migrate_container" \
  "migration container was not created"

assert_nonempty \
  "$worker_container" \
  "worker container was not created"

assert_nonempty \
  "$discovery_container" \
  "discovery container was not created"

assert_nonempty \
  "$postgres_container" \
  "PostgreSQL container was not created"

assert_equal \
  "$(docker inspect "$migrate_container" --format '{{.State.ExitCode}}')" \
  "0" \
  "migration service exit code"

assert_equal \
  "$(docker inspect "$postgres_container" --format '{{.State.Health.Status}}')" \
  "healthy" \
  "PostgreSQL health"

assert_equal \
  "$(docker inspect "$worker_container" --format '{{.State.Health.Status}}')" \
  "healthy" \
  "worker health"

assert_equal \
  "$(docker inspect "$discovery_container" --format '{{.State.Health.Status}}')" \
  "healthy" \
  "discovery health"

compose \
  --profile tools \
  run \
  --rm \
  --no-deps \
  tools \
  discover \
  --once

pass "one-shot discovery succeeded with no verified source and made no public request"

compose stop worker discovery

pass "worker and discovery were stopped before seeding; smoke test performs no public crawl"

compose \
  --profile tools \
  run \
  --rm \
  --no-deps \
  tools \
  health

pass "application health command reached PostgreSQL"

compose \
  --profile tools \
  run \
  --rm \
  --no-deps \
  tools \
  schedule \
  'https://example.com/path'

queue_state="$(
  compose exec \
    -T \
    postgres \
    psql \
    --username joshbot_admin \
    --dbname joshbot \
    --no-align \
    --tuples-only \
    --quiet \
    --command="
      SET ROLE joshbot_app;

      SELECT
        origin
        || '|'
        || mode
        || '|'
        || lease_generation::text
        || '|'
        || (lease_owner IS NULL)::text
        || '|'
        || (lease_expires_at IS NULL)::text
      FROM verification_queue;
    "
)"

assert_equal \
  "$queue_state" \
  'https://example.com|recurring|0|true|true' \
  "scheduled canonical queue state"

pass "schedule command stored one canonical unleased recurring queue row"

compose exec \
  -T \
  postgres \
  psql \
  --username joshbot_admin \
  --dbname joshbot \
  --set=ON_ERROR_STOP=1 <<'SQL'
SET ROLE joshbot_app;

INSERT INTO origins (
    origin,
    first_observed_at
)
VALUES (
    'https://example.com',
    TIMESTAMPTZ '2026-01-02 03:04:05+00'
);

INSERT INTO verification_observations (
    origin,
    observed_at,
    outcome,
    version,
    identity
)
VALUES (
    'https://example.com',
    TIMESTAMPTZ '2026-01-02 03:04:05+00',
    'valid',
    1,
    'affirmed'
);
SQL

observation_state="$(
  compose exec \
    -T \
    postgres \
    psql \
    --username joshbot_admin \
    --dbname joshbot \
    --no-align \
    --tuples-only \
    --quiet \
    --command="
      SET ROLE joshbot_app;

      SELECT
        origin
        || '|'
        || outcome
        || '|'
        || version::text
        || '|'
        || identity
      FROM verification_observations
      ORDER BY observed_at DESC, id DESC
      LIMIT 1;
    "
)"

assert_equal \
  "$observation_state" \
  'https://example.com|valid|1|affirmed' \
  "known source observation"

migration_count="$(
  compose exec \
    -T \
    postgres \
    psql \
    --username joshbot_admin \
    --dbname joshbot \
    --no-align \
    --tuples-only \
    --quiet \
    --command="
      SELECT count(*)::text
      FROM schema_migrations;
    "
)"

assert_equal \
  "$migration_count" \
  "3" \
  "source migration count"

pass "known observation, effective state, queue state, and migrations exist"

compose \
  --profile tools \
  run \
  --rm \
  --no-deps \
  tools \
  export \
  --output /exports/source-snapshot

source_snapshot="$JOSHBOT_EXPORT_DIR/source-snapshot"

cmp \
  "$expected_root/registry.json" \
  "$source_snapshot/registry.json"

cmp \
  "$expected_root/$expected_node_path" \
  "$source_snapshot/$expected_node_path"

source_file_count="$(
  find "$source_snapshot" \
    -type f \
    -print |
    wc -l |
    tr -d '[:space:]'
)"

assert_equal \
  "$source_file_count" \
  "2" \
  "source public snapshot file count"

pass "source public registry matches the expected deterministic bytes"

compose exec \
  -T \
  postgres \
  psql \
  --username joshbot_admin \
  --dbname joshbot \
  --set=ON_ERROR_STOP=1 <<'SQL'
SET ROLE joshbot_app;

INSERT INTO discovery_source_state (
    source_origin,
    last_attempted_at
)
VALUES (
    'https://example.com',
    TIMESTAMPTZ '2026-01-03 03:04:05+00'
);

INSERT INTO discovery_candidates (
    origin,
    first_discovered_at,
    last_discovered_at
)
VALUES (
    'https://candidate.example',
    TIMESTAMPTZ '2026-01-03 03:04:05+00',
    TIMESTAMPTZ '2026-01-03 03:04:05+00'
);

INSERT INTO discovery_edges (
    source_origin,
    candidate_origin,
    kind,
    first_discovered_at,
    last_discovered_at
)
VALUES (
    'https://example.com',
    'https://candidate.example',
    'link',
    TIMESTAMPTZ '2026-01-03 03:04:05+00',
    TIMESTAMPTZ '2026-01-03 03:04:05+00'
);

INSERT INTO verification_queue (
    origin,
    available_at,
    mode
)
VALUES (
    'https://candidate.example',
    TIMESTAMPTZ '2026-01-03 03:04:05+00',
    'probe'
);

DELETE FROM verification_queue
WHERE false;
SQL

private_discovery_state="$(
  compose exec \
    -T \
    postgres \
    psql \
    --username joshbot_admin \
    --dbname joshbot \
    --no-align \
    --tuples-only \
    --quiet \
    --command="
      SET ROLE joshbot_app;

      SELECT
        discovery_source_state.source_origin
        || '|'
        || discovery_candidates.origin
        || '|'
        || discovery_edges.kind
        || '|'
        || verification_queue.mode
      FROM discovery_source_state
      JOIN discovery_edges
        ON discovery_edges.source_origin =
          discovery_source_state.source_origin
      JOIN discovery_candidates
        ON discovery_candidates.origin =
          discovery_edges.candidate_origin
      JOIN verification_queue
        ON verification_queue.origin =
          discovery_candidates.origin;
    "
)"

assert_equal \
  "$private_discovery_state" \
  'https://example.com|https://candidate.example|link|probe' \
  "private discovery state"

compose \
  --profile tools \
  run \
  --rm \
  --no-deps \
  tools \
  export \
  --output /exports/discovery-private-snapshot

private_snapshot="$JOSHBOT_EXPORT_DIR/discovery-private-snapshot"

diff \
  --recursive \
  --no-dereference \
  "$source_snapshot" \
  "$private_snapshot"

pass "private discovery state leaves the public registry byte-identical"

compose \
  --profile backup \
  run \
  --rm \
  --no-deps \
  backup

archive_count="$(
  find "$JOSHBOT_BACKUP_DIR" \
    -maxdepth 1 \
    -type f \
    -name 'joshbot-*.dump' \
    -print |
    wc -l |
    tr -d '[:space:]'
)"

assert_equal \
  "$archive_count" \
  "1" \
  "completed backup archive count"

partial_archive="$(
  find "$JOSHBOT_BACKUP_DIR" \
    -maxdepth 1 \
    -type f \
    -name '*.partial' \
    -print \
    -quit
)"

assert_equal \
  "$partial_archive" \
  "" \
  "partial backup archive cleanup"

archive_path="$(
  find "$JOSHBOT_BACKUP_DIR" \
    -maxdepth 1 \
    -type f \
    -name 'joshbot-*.dump' \
    -print \
    -quit
)"

assert_nonempty \
  "$archive_path" \
  "backup archive path is empty"

archive_mode="$(
  docker run \
    --rm \
    --pull never \
    --network none \
    --read-only \
    --cap-drop ALL \
    --security-opt no-new-privileges:true \
    --tmpfs '/var/lib/postgresql:ro,size=1048576,mode=0555' \
    --mount "type=bind,source=$archive_path,target=/archive.dump,readonly" \
    --entrypoint /usr/bin/stat \
    "$POSTGRES_IMAGE" \
    --format='%a' \
    /archive.dump
)"

assert_equal \
  "$archive_mode" \
  "600" \
  "backup archive permissions"

docker run \
  --rm \
  --pull never \
  --network none \
  --read-only \
  --cap-drop ALL \
  --security-opt no-new-privileges:true \
  --tmpfs '/var/lib/postgresql:ro,size=1048576,mode=0555' \
  --mount "type=bind,source=$archive_path,target=/archive.dump,readonly" \
  "$POSTGRES_IMAGE" \
  pg_restore \
  --list \
  /archive.dump \
  >/dev/null

pass "custom backup archive exists, is mode 0600, has no partial, and passes pg_restore --list"

expect_role_failure \
  joshbot_app \
  'CREATE TABLE forbidden_app_table (id bigint);' \
  'application role schema creation'

expect_role_failure \
  joshbot_app \
  'DROP TABLE verification_queue;' \
  'application role table deletion'

expect_role_failure \
  joshbot_app \
  'ALTER TABLE discovery_candidates ADD COLUMN forbidden text;' \
  'application role table alteration'

expect_role_failure \
  joshbot_app \
  'CREATE ROLE forbidden_app_role;' \
  'application role role creation'

expect_role_failure \
  joshbot_backup \
  "INSERT INTO verification_queue (origin, available_at) VALUES ('https://example.net', clock_timestamp());" \
  'backup role queue insertion'

expect_role_failure \
  joshbot_backup \
  "UPDATE verification_queue SET available_at = clock_timestamp() WHERE origin = 'https://example.com';" \
  'backup role queue update'

compose \
  --profile tools \
  --profile backup \
  create \
  tools \
  backup

tools_container="$(
  compose ps \
    --all \
    --quiet \
    tools
)"

backup_container="$(
  compose ps \
    --all \
    --quiet \
    backup
)"

assert_nonempty \
  "$tools_container" \
  "tools container was not created"

assert_nonempty \
  "$backup_container" \
  "backup container was not created"

assert_hardened_container \
  "$worker_container" \
  '65532:65532' \
  'worker container'

assert_hardened_container \
  "$discovery_container" \
  '65532:65532' \
  'discovery container'

assert_hardened_container \
  "$migrate_container" \
  '65532:65532' \
  'migration container'

assert_hardened_container \
  "$tools_container" \
  '65532:65532' \
  'tools container'

assert_hardened_container \
  "$backup_container" \
  'postgres' \
  'backup container'

database_network="${COMPOSE_PROJECT_NAME}_database"
egress_network="${COMPOSE_PROJECT_NAME}_egress"

assert_equal \
  "$(docker network inspect "$database_network" --format '{{.Internal}}')" \
  "true" \
  "database network internal state"

assert_equal \
  "$(docker network inspect "$egress_network" --format '{{.Internal}}')" \
  "false" \
  "egress network internal state"

expected_worker_networks="$(
  printf '%s\n' \
    "$database_network" \
    "$egress_network" |
    sort
)"

expected_discovery_networks="$expected_worker_networks"

expected_database_network="$database_network"

assert_equal \
  "$(inspect_network_names "$worker_container")" \
  "$expected_worker_networks" \
  "worker network membership"

assert_equal \
  "$(inspect_network_names "$discovery_container")" \
  "$expected_discovery_networks" \
  "discovery network membership"

assert_equal \
  "$(inspect_network_names "$migrate_container")" \
  "$expected_database_network" \
  "migration network membership"

assert_equal \
  "$(inspect_network_names "$tools_container")" \
  "$expected_database_network" \
  "tools network membership"

assert_equal \
  "$(inspect_network_names "$backup_container")" \
  "$expected_database_network" \
  "backup network membership"

assert_equal \
  "$(inspect_network_names "$postgres_container")" \
  "$expected_database_network" \
  "PostgreSQL network membership"

assert_equal \
  "$(inspect_mount_destinations "$worker_container")" \
  '/run/secrets/joshbot_app_password' \
  "worker mount boundary"

assert_equal \
  "$(inspect_mount_destinations "$discovery_container")" \
  '/run/secrets/joshbot_app_password' \
  "discovery mount boundary"

expected_migrate_mounts='/run/secrets/joshbot_migrator_password'

assert_equal \
  "$(inspect_mount_destinations "$migrate_container")" \
  "$expected_migrate_mounts" \
  "migration mount boundary"

expected_tools_mounts="$(
  printf '%s\n' \
    '/exports' \
    '/run/secrets/joshbot_app_password' |
    sort
)"

assert_equal \
  "$(inspect_mount_destinations "$tools_container")" \
  "$expected_tools_mounts" \
  "tools mount boundary"

expected_backup_mounts="$(
  printf '%s\n' \
    '/backups' \
    '/opt/joshbot/backup.sh' \
    '/run/secrets/joshbot_backup_password' \
    '/var/lib/postgresql' |
    sort
)"

assert_equal \
  "$(inspect_mount_destinations "$backup_container")" \
  "$expected_backup_mounts" \
  "backup mount boundary"

backup_pgdata_mount="$(
  docker inspect \
    "$backup_container" \
    --format '{{range .Mounts}}{{if eq .Destination "/var/lib/postgresql"}}type={{.Type}} rw={{.RW}}{{end}}{{end}}'
)"

assert_equal \
  "$backup_pgdata_mount" \
  'type=tmpfs rw=false' \
  "backup PostgreSQL data mount override"

expected_postgres_mounts="$(
  printf '%s\n' \
    '/docker-entrypoint-initdb.d/010-joshbot-roles.sh' \
    '/run/secrets/postgres_admin_password' \
    '/run/secrets/joshbot_app_password' \
    '/run/secrets/joshbot_backup_password' \
    '/run/secrets/joshbot_migrator_password' \
    '/var/lib/postgresql' |
    sort
)"

assert_equal \
  "$(inspect_mount_destinations "$postgres_container")" \
  "$expected_postgres_mounts" \
  "PostgreSQL mount boundary"

assert_equal \
  "$(docker inspect "$postgres_container" --format '{{json .HostConfig.PortBindings}}')" \
  '{}' \
  "PostgreSQL published ports"

assert_equal \
  "$(docker inspect "$postgres_container" --format '{{.HostConfig.Privileged}}')" \
  'false' \
  "PostgreSQL privileged state"

postgres_network_mode="$(
  docker inspect \
    "$postgres_container" \
    --format '{{.HostConfig.NetworkMode}}'
)"

if [[ "$postgres_network_mode" == "host" ]]; then
  fail "PostgreSQL uses host networking"
fi

if inspect_mount_destinations "$postgres_container" |
  grep -Fxq '/var/run/docker.sock'; then
  fail "PostgreSQL has access to the Docker socket"
fi

compose_volumes="$(
  docker volume ls \
    --quiet \
    --filter "label=com.docker.compose.project=$COMPOSE_PROJECT_NAME"
)"

assert_equal \
  "$compose_volumes" \
  "" \
  "Compose-managed volume count"

pass "runtime container, discovery, network, secret, port, and mount boundaries are correct"

docker network create \
  --internal \
  "$restore_network" \
  >/dev/null

docker run \
  --detach \
  --pull never \
  --name "$restore_container" \
  --label "com.joshternet.joshbot.smoke=$COMPOSE_PROJECT_NAME" \
  --network "$restore_network" \
  --tmpfs '/var/lib/postgresql:rw,size=268435456,mode=0700,uid=999,gid=999' \
  --env "POSTGRES_DB=$restore_database" \
  --env "POSTGRES_USER=$restore_user" \
  --env 'POSTGRES_PASSWORD_FILE=/run/secrets/joshbot_restore_password' \
  --env 'POSTGRES_INITDB_ARGS=--auth-host=scram-sha-256' \
  --mount "type=bind,source=$restore_password_file,target=/run/secrets/joshbot_restore_password,readonly" \
  --health-cmd="pg_isready --username=$restore_user --dbname=$restore_database" \
  --health-interval=1s \
  --health-timeout=5s \
  --health-retries=60 \
  --health-start-period=5s \
  "$POSTGRES_IMAGE" \
  >/dev/null

restore_ready='false'

for ((attempt = 1; attempt <= 90; attempt++)); do
  restore_status="$(
    docker inspect \
      "$restore_container" \
      --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}'
  )"

  if [[ "$restore_status" == "healthy" ]]; then
    restore_ready='true'
    break
  fi

  if [[ "$restore_status" == "unhealthy" ]]; then
    docker logs "$restore_container" >&2
    fail "disposable restore PostgreSQL became unhealthy"
  fi

  sleep 1
done

if [[ "$restore_ready" != "true" ]]; then
  docker logs "$restore_container" >&2
  fail "disposable restore PostgreSQL did not become healthy"
fi

assert_equal \
  "$(docker inspect "$restore_container" --format '{{json .HostConfig.PortBindings}}')" \
  '{}' \
  "restore PostgreSQL published ports"

assert_equal \
  "$(inspect_network_names "$restore_container")" \
  "$restore_network" \
  "restore PostgreSQL network membership"

restore_pgdata_mount="$(
  docker inspect \
    "$restore_container" \
    --format '{{index .HostConfig.Tmpfs "/var/lib/postgresql"}}'
)"

assert_equal \
  "$restore_pgdata_mount" \
  'rw,size=268435456,mode=0700,uid=999,gid=999' \
  "restore PostgreSQL data mount"

pass "fresh restore PostgreSQL is isolated, internal-only, unpublished, and disposable"

docker run \
  --rm \
  --pull never \
  --network "$restore_network" \
  --read-only \
  --cap-drop ALL \
  --security-opt no-new-privileges:true \
  --tmpfs '/var/lib/postgresql:ro,size=1048576,mode=0555' \
  --env "PGHOST=$restore_container" \
  --env 'PGPORT=5432' \
  --env "PGDATABASE=$restore_database" \
  --env "PGUSER=$restore_user" \
  --env 'PGPASSWORD_FILE=/run/secrets/joshbot_restore_password' \
  --mount "type=bind,source=$restore_password_file,target=/run/secrets/joshbot_restore_password,readonly" \
  --mount "type=bind,source=$archive_path,target=/restore/joshbot.dump,readonly" \
  --entrypoint /bin/sh \
  "$POSTGRES_IMAGE" \
  -eu \
  -c '
    PGPASSWORD="$(cat "$PGPASSWORD_FILE")"

    if [ -z "$PGPASSWORD" ]; then
        printf "restore password is empty\n" >&2
        exit 1
    fi

    export PGPASSWORD

    pg_restore \
        --host="$PGHOST" \
        --port="$PGPORT" \
        --username="$PGUSER" \
        --dbname="$PGDATABASE" \
        --no-password \
        --format=custom \
        --no-owner \
        --no-privileges \
        --exit-on-error \
        --single-transaction \
        /restore/joshbot.dump

    unset PGPASSWORD
  '

pass "backup restored transactionally into fresh PostgreSQL"

run_restored_joshbot migrate

pass "restored database accepts the current embedded migrations"

run_restored_joshbot health

pass "application code connected to the restored database"

restored_observation_state="$(
  docker exec \
    "$restore_container" \
    psql \
    --username "$restore_user" \
    --dbname "$restore_database" \
    --no-align \
    --tuples-only \
    --quiet \
    --command="
      SELECT
        origin
        || '|'
        || outcome
        || '|'
        || version::text
        || '|'
        || identity
      FROM verification_observations
      ORDER BY observed_at DESC, id DESC
      LIMIT 1;
    "
)"

assert_equal \
  "$restored_observation_state" \
  'https://example.com|valid|1|affirmed' \
  "restored observation state"

restored_effective_state="$(
  docker exec \
    "$restore_container" \
    psql \
    --username "$restore_user" \
    --dbname "$restore_database" \
    --no-align \
    --tuples-only \
    --quiet \
    --command="
      SELECT
        effective.origin
        || '|'
        || effective.outcome
        || '|'
        || effective.version::text
        || '|'
        || effective.identity
      FROM (
        SELECT DISTINCT ON (origin)
          origin,
          outcome,
          version,
          identity
        FROM verification_observations
        WHERE outcome IN (
          'valid',
          'absent',
          'invalid',
          'unsupported_version',
          'cross_origin_redirect'
        )
        ORDER BY
          origin ASC,
          observed_at DESC,
          id DESC
      ) AS effective;
    "
)"

assert_equal \
  "$restored_effective_state" \
  'https://example.com|valid|1|affirmed' \
  "restored effective state"

restored_queue_state="$(
  docker exec \
    "$restore_container" \
    psql \
    --username "$restore_user" \
    --dbname "$restore_database" \
    --no-align \
    --tuples-only \
    --quiet \
    --command="
      SELECT
        origin
        || '|'
        || mode
        || '|'
        || lease_generation::text
        || '|'
        || (lease_owner IS NULL)::text
        || '|'
        || (lease_expires_at IS NULL)::text
      FROM verification_queue
      ORDER BY origin;
    "
)"

assert_equal \
  "$restored_queue_state" \
  $'https://candidate.example|probe|0|true|true\nhttps://example.com|recurring|0|true|true' \
  "restored queue state"

restored_discovery_state="$(
  docker exec \
    "$restore_container" \
    psql \
    --username "$restore_user" \
    --dbname "$restore_database" \
    --no-align \
    --tuples-only \
    --quiet \
    --command="
      SELECT
        discovery_source_state.source_origin
        || '|'
        || discovery_candidates.origin
        || '|'
        || discovery_edges.kind
      FROM discovery_source_state
      JOIN discovery_edges
        ON discovery_edges.source_origin =
          discovery_source_state.source_origin
      JOIN discovery_candidates
        ON discovery_candidates.origin =
          discovery_edges.candidate_origin;
    "
)"

assert_equal \
  "$restored_discovery_state" \
  'https://example.com|https://candidate.example|link' \
  "restored discovery state"

restored_migration_count="$(
  docker exec \
    "$restore_container" \
    psql \
    --username "$restore_user" \
    --dbname "$restore_database" \
    --no-align \
    --tuples-only \
    --quiet \
    --command="
      SELECT count(*)::text
      FROM schema_migrations;
    "
)"

assert_equal \
  "$restored_migration_count" \
  "3" \
  "restored migration count"

pass "known observation, discovery provenance, queue modes, and migration metadata survived restore"

docker run \
  --rm \
  --pull never \
  --network "$restore_network" \
  --read-only \
  --cap-drop ALL \
  --security-opt no-new-privileges:true \
  --env "JOSHBOT_DATABASE_URL=postgres://$restore_user@$restore_container:5432/$restore_database?sslmode=disable" \
  --env 'JOSHBOT_DATABASE_PASSWORD_FILE=/run/secrets/joshbot_restore_password' \
  --mount "type=bind,source=$restore_password_file,target=/run/secrets/joshbot_restore_password,readonly" \
  --mount "type=bind,source=$restore_export_directory,target=/exports" \
  "$JOSHBOT_IMAGE" \
  export \
  --output /exports/restored-snapshot

restored_snapshot="$restore_export_directory/restored-snapshot"

cmp \
  "$expected_root/registry.json" \
  "$restored_snapshot/registry.json"

cmp \
  "$expected_root/$expected_node_path" \
  "$restored_snapshot/$expected_node_path"

diff \
  --recursive \
  "$source_snapshot" \
  "$restored_snapshot"

restored_file_count="$(
  find "$restored_snapshot" \
    -type f \
    -print |
    wc -l |
    tr -d '[:space:]'
)"

assert_equal \
  "$restored_file_count" \
  "2" \
  "restored public snapshot file count"

pass "restored application produced the exact expected public registry bytes"

pass "deployment smoke, least privilege, backup, restore, and application recovery all passed"
