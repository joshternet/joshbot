#!/usr/bin/env bash

set -Eeuo pipefail

umask 077

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
publish_script="$script_directory/publish.sh"

test_token="$(date -u '+%Y%m%d%H%M%S')-$$"
test_root="$repository_root/external/publication-boundary-test-$test_token"
fake_bin="$test_root/fake-bin"
fake_docker_log="$test_root/fake-docker.log"

export_directory="$test_root/exports"
backup_directory="$test_root/backups"
postgres_directory="$test_root/postgres"
secret_directory="$test_root/secrets"

export COMPOSE_PROJECT_NAME="joshbot-publish-test-$test_token"
export JOSHBOT_IMAGE="joshbot-publish-test:$test_token"

export JOSHBOT_POSTGRES_DATA_DIR="$postgres_directory"
export JOSHBOT_EXPORT_DIR="$export_directory"
export JOSHBOT_BACKUP_DIR="$backup_directory"

export JOSHBOT_POSTGRES_ADMIN_PASSWORD_FILE="$secret_directory/postgres_admin_password"
export JOSHBOT_MIGRATOR_PASSWORD_FILE="$secret_directory/joshbot_migrator_password"
export JOSHBOT_APP_PASSWORD_FILE="$secret_directory/joshbot_app_password"
export JOSHBOT_BACKUP_PASSWORD_FILE="$secret_directory/joshbot_backup_password"
export JOSHBOT_GITHUB_TOKEN_FILE="$secret_directory/joshbot_github_token"

export JOSHBOT_PUBLISH_GITHUB_OWNER="joshternet"
export JOSHBOT_PUBLISH_GITHUB_REPOSITORY="index-data"
export JOSHBOT_PUBLISH_GITHUB_BRANCH="main"

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

container_for_service() {
  local service

  service="$1"

  compose \
    --profile tools \
    --profile backup \
    --profile publisher \
    ps \
    --all \
    --quiet \
    "$service"
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

inspect_environment() {
  local container_id

  container_id="$1"

  docker inspect \
    "$container_id" \
    --format '{{range .Config.Env}}{{println .}}{{end}}' |
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

assert_no_github_credentials() {
  local container_id
  local description
  local environment

  container_id="$1"
  description="$2"

  if inspect_mount_destinations "$container_id" |
    grep -Fxq '/run/secrets/joshbot_github_token'; then
    fail "$description received the GitHub token secret"
  fi

  environment="$(inspect_environment "$container_id")"

  if printf '%s\n' "$environment" |
    grep -Eq '^JOSHBOT_(GITHUB_TOKEN_FILE|PUBLISH_GITHUB_)'; then
    fail "$description received GitHub publication configuration"
  fi
}

count_fake_call() {
  local service
  local command

  service="$1"
  command="$2"

  awk \
    -F '\t' \
    -v service="$service" \
    -v command="$command" '
      {
        for (field_index = 1; field_index < NF; field_index++) {
          if ($field_index == service && $(field_index + 1) == command) {
            count++
          }
        }
      }

      END {
        print count + 0
      }
    ' \
    "$fake_docker_log"
}

fake_call_line() {
  local service
  local command

  service="$1"
  command="$2"

  awk \
    -F '\t' \
    -v service="$service" \
    -v command="$command" '
      {
        for (field_index = 1; field_index < NF; field_index++) {
          if ($field_index == service && $(field_index + 1) == command) {
            print NR
            exit
          }
        }
      }
    ' \
    "$fake_docker_log"
}

fake_call_path() {
  local service
  local command
  local option

  service="$1"
  command="$2"
  option="$3"

  awk \
    -F '\t' \
    -v service="$service" \
    -v command="$command" \
    -v option="$option" '
      {
        matched = 0

        for (field_index = 1; field_index < NF; field_index++) {
          if ($field_index == service && $(field_index + 1) == command) {
            matched = 1
          }

          if (matched == 1 && $field_index == option) {
            print $(field_index + 1)
            exit
          }
        }
      }
    ' \
    "$fake_docker_log"
}

assert_cleaned_snapshot() {
  local container_path
  local snapshot_name
  local host_path
  local description

  container_path="$1"
  description="$2"

  case "$container_path" in
    /exports/.joshbot-publish-*)
      ;;
    *)
      fail "$description used an unsafe container path: $container_path"
      ;;
  esac

  snapshot_name="${container_path#/exports/}"

  if [[ "$snapshot_name" == */* ]] ||
    [[ -z "$snapshot_name" ]]; then
    fail "$description was not a direct child of the export directory"
  fi

  host_path="$export_directory/$snapshot_name"

  if [[ -e "$host_path" ]] ||
    [[ -L "$host_path" ]]; then
    fail "$description snapshot remains after cleanup"
  fi
}

cleanup() {
  local status="$?"
  local remaining_containers
  local remaining_volumes

  trap - EXIT HUP INT TERM
  set +e

  compose \
    --profile tools \
    --profile backup \
    --profile publisher \
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
    printf 'FAIL: publication-test containers remain\n' >&2
    status=1
  fi

  if [[ -n "$remaining_volumes" ]]; then
    printf 'FAIL: publication-test volumes remain\n' >&2
    status=1
  fi

  if docker image inspect "$JOSHBOT_IMAGE" >/dev/null 2>&1; then
    docker image rm \
      "$JOSHBOT_IMAGE" \
      >/dev/null \
      2>&1

    if docker image inspect "$JOSHBOT_IMAGE" >/dev/null 2>&1; then
      printf 'FAIL: publication-test image remains\n' >&2
      status=1
    fi
  fi

  case "$test_root" in
    "$repository_root"/external/publication-boundary-test-*)
      rm -rf -- "$test_root"
      ;;
    *)
      printf 'FAIL: refused unsafe test cleanup\n' >&2
      status=1
      ;;
  esac

  if [[ -e "$test_root" ]]; then
    printf 'FAIL: publication-test directory remains\n' >&2
    status=1
  fi

  if [[ "$status" -eq 0 ]]; then
    pass "publication boundary resources were removed"
  fi

  exit "$status"
}

trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

for required_command in \
  awk \
  bash \
  docker \
  find \
  grep \
  openssl \
  sed \
  sort; do
  command -v "$required_command" >/dev/null 2>&1 ||
    fail "required command is unavailable: $required_command"
done

docker info >/dev/null
docker compose version >/dev/null

if [[ ! -f "$compose_file" ]]; then
  fail "compose.yaml is unavailable"
fi

mkdir "$test_root"

mkdir \
  "$fake_bin" \
  "$export_directory" \
  "$backup_directory" \
  "$postgres_directory" \
  "$secret_directory"

chmod 0700 \
  "$test_root" \
  "$fake_bin" \
  "$postgres_directory" \
  "$secret_directory"

chmod 0777 \
  "$export_directory" \
  "$backup_directory"

create_secret "$JOSHBOT_POSTGRES_ADMIN_PASSWORD_FILE"
create_secret "$JOSHBOT_MIGRATOR_PASSWORD_FILE"
create_secret "$JOSHBOT_APP_PASSWORD_FILE"
create_secret "$JOSHBOT_BACKUP_PASSWORD_FILE"

printf '%s\n' \
  'github-token-for-publication-boundary-test' \
  >"$JOSHBOT_GITHUB_TOKEN_FILE"

chmod 0444 "$JOSHBOT_GITHUB_TOKEN_FILE"

: >"$fake_docker_log"

compose \
  --profile tools \
  --profile backup \
  --profile publisher \
  config \
  --quiet

pass "Docker Compose configuration is valid"

service_names="$(
  compose \
    --profile tools \
    --profile backup \
    --profile publisher \
    config \
    --services
)"

if ! printf '%s\n' "$service_names" |
  grep -Fxq publisher; then
  fail "publisher service is missing"
fi

compose \
  --profile publisher \
  build \
  --pull \
  publisher

compose \
  --profile tools \
  --profile backup \
  --profile publisher \
  create \
  --no-build \
  postgres \
  migrate \
  worker \
  discovery \
  tools \
  backup \
  publisher

postgres_container="$(container_for_service postgres)"
migrate_container="$(container_for_service migrate)"
worker_container="$(container_for_service worker)"
discovery_container="$(container_for_service discovery)"
tools_container="$(container_for_service tools)"
backup_container="$(container_for_service backup)"
publisher_container="$(container_for_service publisher)"

assert_nonempty "$postgres_container" "PostgreSQL container was not created"
assert_nonempty "$migrate_container" "migration container was not created"
assert_nonempty "$worker_container" "worker container was not created"
assert_nonempty "$discovery_container" "discovery container was not created"
assert_nonempty "$tools_container" "tools container was not created"
assert_nonempty "$backup_container" "backup container was not created"
assert_nonempty "$publisher_container" "publisher container was not created"

assert_hardened_container \
  "$publisher_container" \
  '65532:65532' \
  'publisher container'

database_network="${COMPOSE_PROJECT_NAME}_database"
egress_network="${COMPOSE_PROJECT_NAME}_egress"

assert_equal \
  "$(inspect_network_names "$publisher_container")" \
  "$egress_network" \
  "publisher network membership"

expected_publisher_mounts="$(
  printf '%s\n' \
    '/exports' \
    '/run/secrets/joshbot_github_token' |
    sort
)"

assert_equal \
  "$(inspect_mount_destinations "$publisher_container")" \
  "$expected_publisher_mounts" \
  "publisher mount boundary"

assert_equal \
  "$(
    docker inspect \
      "$publisher_container" \
      --format '{{range .Mounts}}{{if eq .Destination "/exports"}}{{.RW}}{{end}}{{end}}'
  )" \
  "false" \
  "publisher export mount write access"

assert_equal \
  "$(
    docker inspect \
      "$publisher_container" \
      --format '{{range .Mounts}}{{if eq .Destination "/run/secrets/joshbot_github_token"}}{{.RW}}{{end}}{{end}}'
  )" \
  "false" \
  "publisher GitHub secret write access"

publisher_environment="$(inspect_environment "$publisher_container")"

for required_environment in \
  'JOSHBOT_GITHUB_TOKEN_FILE=/run/secrets/joshbot_github_token' \
  'JOSHBOT_PUBLISH_GITHUB_BRANCH=main' \
  'JOSHBOT_PUBLISH_GITHUB_OWNER=joshternet' \
  'JOSHBOT_PUBLISH_GITHUB_REPOSITORY=index-data'; do
  if ! printf '%s\n' "$publisher_environment" |
    grep -Fxq "$required_environment"; then
    fail "publisher is missing environment: $required_environment"
  fi
done

if printf '%s\n' "$publisher_environment" |
  grep -Eq '^(JOSHBOT_DATABASE_|JOSHBOT_POSTGRES_|POSTGRES_|PGDATA=|PGHOST=|PGPORT=|PGDATABASE=|PGUSER=|PGPASSWORD=)'; then
  fail "publisher received database configuration"
fi

if printf '%s\n' "$publisher_environment" |
  grep -Fq 'github-token-for-publication-boundary-test'; then
  fail "publisher token was placed in its environment"
fi

if inspect_mount_destinations "$publisher_container" |
  grep -Eq '^(/backups|/var/lib/postgresql|/var/run/docker\.sock)$'; then
  fail "publisher received a forbidden mount"
fi

for service_record in \
  "$postgres_container|PostgreSQL" \
  "$migrate_container|migration" \
  "$worker_container|worker" \
  "$discovery_container|discovery" \
  "$tools_container|tools" \
  "$backup_container|backup"; do
  service_container="${service_record%%|*}"
  service_description="${service_record#*|}"

  assert_no_github_credentials \
    "$service_container" \
    "$service_description service"
done

assert_equal \
  "$(inspect_network_names "$tools_container")" \
  "$database_network" \
  "tools network membership"

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

assert_equal \
  "$(
    docker inspect \
      "$tools_container" \
      --format '{{range .Mounts}}{{if eq .Destination "/exports"}}{{.RW}}{{end}}{{end}}'
  )" \
  "true" \
  "tools export mount write access"

pass "publisher and database credentials stay in seperate containers"

if [[ ! -f "$publish_script" ]]; then
  fail "deploy/publish.sh is unavailable"
fi

if [[ ! -x "$publish_script" ]]; then
  fail "deploy/publish.sh is not executable"
fi

bash -n "$publish_script"

if grep -Eq '(^|[;[:space:]])eval[[:space:]]' "$publish_script"; then
  fail "deploy/publish.sh uses eval"
fi

if grep -Eq '(^|[;[:space:]])source[[:space:]]' "$publish_script"; then
  fail "deploy/publish.sh sources another file"
fi

cat >"$fake_bin/docker" <<'FAKE_DOCKER'
#!/usr/bin/env bash

set -eu

: "${JOSHBOT_FAKE_DOCKER_LOG:?}"
: "${JOSHBOT_EXPORT_DIR:?}"

{
  printf 'CALL'

  for argument in "$@"; do
    printf '\t%s' "$argument"
  done

  printf '\n'
} >>"$JOSHBOT_FAKE_DOCKER_LOG"

if [[ "${1-}" != "compose" ]]; then
  exit 64
fi

arguments=("$@")
argument_count="${#arguments[@]}"

for ((index = 0; index < argument_count; index++)); do
  if [[ "${arguments[$index]}" == "config" ]]; then
    exit 0
  fi
done

for ((index = 0; index + 1 < argument_count; index++)); do
  if [[ "${arguments[$index]}" == "tools" ]] &&
    [[ "${arguments[$((index + 1))]}" == "export" ]]; then
    output_path=""

    for ((option_index = index + 2; option_index + 1 < argument_count; option_index++)); do
      if [[ "${arguments[$option_index]}" == "--output" ]]; then
        output_path="${arguments[$((option_index + 1))]}"
        break
      fi
    done

    case "$output_path" in
      /exports/.joshbot-publish-*)
        ;;
      *)
        exit 65
        ;;
    esac

    relative_path="${output_path#/exports/}"

    if [[ "$relative_path" == */* ]]; then
      exit 66
    fi

    host_output="$JOSHBOT_EXPORT_DIR/$relative_path"

    if [[ -e "$host_output" ]]; then
      exit 67
    fi

    mkdir "$host_output"

    printf '%s\n' \
      '{"format_version":1,"nodes":[]}' \
      >"$host_output/registry.json"

    exit 0
  fi
done

for ((index = 0; index + 1 < argument_count; index++)); do
  if [[ "${arguments[$index]}" == "publisher" ]] &&
    [[ "${arguments[$((index + 1))]}" == "publish" ]]; then
    input_path=""

    for ((option_index = index + 2; option_index + 1 < argument_count; option_index++)); do
      if [[ "${arguments[$option_index]}" == "--input" ]]; then
        input_path="${arguments[$((option_index + 1))]}"
        break
      fi
    done

    case "$input_path" in
      /exports/.joshbot-publish-*)
        ;;
      *)
        exit 68
        ;;
    esac

    relative_path="${input_path#/exports/}"

    if [[ "$relative_path" == */* ]]; then
      exit 69
    fi

    host_input="$JOSHBOT_EXPORT_DIR/$relative_path"

    if [[ ! -f "$host_input/registry.json" ]]; then
      exit 70
    fi

    exit "${JOSHBOT_FAKE_PUBLISH_STATUS:-0}"
  fi
done

exit 71
FAKE_DOCKER

chmod 0755 "$fake_bin/docker"

marker_path="$export_directory/operator-file"
printf '%s\n' 'must survive cleanup' >"$marker_path"

success_stdout="$test_root/publish-success.stdout"
success_stderr="$test_root/publish-success.stderr"

: >"$fake_docker_log"

env \
  PATH="$fake_bin:$PATH" \
  JOSHBOT_FAKE_DOCKER_LOG="$fake_docker_log" \
  "$publish_script" \
  >"$success_stdout" \
  2>"$success_stderr"

assert_equal \
  "$(count_fake_call tools export)" \
  "1" \
  "successful export call count"

assert_equal \
  "$(count_fake_call publisher publish)" \
  "1" \
  "successful publisher call count"

success_export_line="$(fake_call_line tools export)"
success_publish_line="$(fake_call_line publisher publish)"

assert_nonempty "$success_export_line" "export call was not recorded"
assert_nonempty "$success_publish_line" "publisher call was not recorded"

if ((success_export_line >= success_publish_line)); then
  fail "publisher ran before the exporter"
fi

success_output_path="$(fake_call_path tools export --output)"
success_input_path="$(fake_call_path publisher publish --input)"

assert_equal \
  "$success_input_path" \
  "$success_output_path" \
  "successful publisher input"

assert_cleaned_snapshot \
  "$success_output_path" \
  "successful publication"

if [[ ! -f "$marker_path" ]]; then
  fail "successful cleanup removed an operator file"
fi

failure_stdout="$test_root/publish-failure.stdout"
failure_stderr="$test_root/publish-failure.stderr"

: >"$fake_docker_log"

set +e

env \
  PATH="$fake_bin:$PATH" \
  JOSHBOT_FAKE_DOCKER_LOG="$fake_docker_log" \
  JOSHBOT_FAKE_PUBLISH_STATUS="23" \
  "$publish_script" \
  >"$failure_stdout" \
  2>"$failure_stderr"

failure_status="$?"

set -e

assert_equal \
  "$failure_status" \
  "23" \
  "failed publisher exit status"

assert_equal \
  "$(count_fake_call tools export)" \
  "1" \
  "failed export call count"

assert_equal \
  "$(count_fake_call publisher publish)" \
  "1" \
  "failed publisher call count"

failure_output_path="$(fake_call_path tools export --output)"
failure_input_path="$(fake_call_path publisher publish --input)"

assert_equal \
  "$failure_input_path" \
  "$failure_output_path" \
  "failed publisher input"

assert_cleaned_snapshot \
  "$failure_output_path" \
  "failed publication"

if [[ "$failure_output_path" == "$success_output_path" ]]; then
  fail "publication reused a temporary snapshot name"
fi

if [[ ! -f "$marker_path" ]]; then
  fail "failed cleanup removed an operator file"
fi

if grep -Fq \
  'github-token-for-publication-boundary-test' \
  "$success_stdout" \
  "$success_stderr" \
  "$failure_stdout" \
  "$failure_stderr" \
  "$fake_docker_log"; then
  fail "publication logged the GitHub token"
fi

: >"$fake_docker_log"

set +e

env \
  PATH="$fake_bin:$PATH" \
  JOSHBOT_FAKE_DOCKER_LOG="$fake_docker_log" \
  JOSHBOT_PUBLISH_GITHUB_OWNER="" \
  "$publish_script" \
  >"$test_root/missing-config.stdout" \
  2>"$test_root/missing-config.stderr"

missing_configuration_status="$?"

set -e

if [[ "$missing_configuration_status" -eq 0 ]]; then
  fail "publication accepted missing GitHub owner configuration"
fi

if [[ -s "$fake_docker_log" ]]; then
  fail "publication invoked Docker before rejecting missing configuration"
fi

remaining_staging="$(
  find "$export_directory" \
    -mindepth 1 \
    -maxdepth 1 \
    -type d \
    -name '.joshbot-publish-*' \
    -print \
    -quit
)"

assert_equal \
  "$remaining_staging" \
  "" \
  "remaining publication staging directory"

pass "publication container isolation and cleanup passed"
