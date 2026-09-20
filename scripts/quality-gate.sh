#!/usr/bin/env bash

set -Eeuo pipefail

BARF_HOST="${JOSHBOT_QUALITY_BARF_HOST:-twb@barf.lan}"
POSTGRES_CONTAINER="${JOSHBOT_QUALITY_POSTGRES_CONTAINER:-joshbot-issue17-test-postgres-1}"
DATABASE_NETWORK="${JOSHBOT_QUALITY_DATABASE_NETWORK:-joshbot-issue17-test_database}"
GO_IMAGE="${JOSHBOT_QUALITY_GO_IMAGE:-golang:1.27.0-trixie}"
PACKAGE_TIMEOUT="${JOSHBOT_QUALITY_PACKAGE_TIMEOUT:-10m}"
REPEAT_COUNT="${JOSHBOT_QUALITY_REPEAT_COUNT:-10}"
STORE_PARALLELISM="${JOSHBOT_QUALITY_STORE_PARALLELISM:-4}"

RUN_ID="$(date -u +%Y%m%d%H%M%S)-$$"
TEST_DATABASE="joshbot_quality_${RUN_ID//-/_}"
TEST_ROLE="joshbot_quality_${RUN_ID//-/_}"
TEST_PASSWORD="integration-test-password"
REMOTE_WORKSPACE="/tmp/joshbot-quality-$RUN_ID"
REMOTE_RESULTS="/tmp/joshbot-quality-results-$RUN_ID"
LOCAL_COVERAGE_DIR="/tmp/joshbot-unit-coverage-$RUN_ID"

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

STATUS_BEFORE="$(git status --porcelain=v1 -uall)"
CURRENT_STAGE="startup"

line() {
  printf '%s\n' '============================================================'
}

section() {
  printf '\n=== %s ===\n\n' "$1"
}

step() {
  CURRENT_STAGE="$1"
  printf '==> %s\n' "$1"
}

pass() {
  printf 'PASS: %s\n\n' "$1"
}

on_error() {
  local status=$?
  local line_number=$1

  printf '\n' >&2
  line >&2
  printf 'JOSHBOT QUALITY GATE FAILED\n' >&2
  printf 'stage: %s\n' "$CURRENT_STAGE" >&2
  printf 'line: %s\n' "$line_number" >&2
  printf 'exit: %s\n' "$status" >&2
  line >&2

  exit "$status"
}

trap 'on_error "$LINENO"' ERR

check_exact_coverage() {
  local profile=$1
  local label=$2
  local report=$3
  local uncovered
  local function_gaps
  local covered_statements
  local total_statements

  go tool cover -func="$profile" | tee "$report"

  uncovered="$(
    awk '
      /^mode:/ { next }
      $2 > 0 && $3 == 0 { print }
    ' "$profile"
  )"

  if [[ -n "$uncovered" ]]; then
    printf 'FAIL: %s has uncovered statement blocks:\n' "$label" >&2
    printf '%s\n' "$uncovered" >&2
    exit 1
  fi

  function_gaps="$(
    awk '
      $1 ~ /^github\.com\/joshternet\/joshbot\// && $NF != "100.0%" {
        print
      }
    ' "$report"
  )"

  if [[ -n "$function_gaps" ]]; then
    printf 'FAIL: %s has function coverage gaps:\n' "$label" >&2
    printf '%s\n' "$function_gaps" >&2
    exit 1
  fi

  read -r covered_statements total_statements < <(
    awk '
      /^mode:/ { next }
      {
        total += $2
        if ($3 > 0) {
          covered += $2
        }
      }
      END {
        printf "%d %d\n", covered, total
      }
    ' "$profile"
  )

  if [[ "$covered_statements" != "$total_statements" ]]; then
    printf 'FAIL: %s statements = %s/%s\n' \
      "$label" \
      "$covered_statements" \
      "$total_statements" >&2
    exit 1
  fi

  printf 'PASS: %s statement coverage = %s/%s\n' \
    "$label" \
    "$covered_statements" \
    "$total_statements"
  printf 'PASS: %s function coverage = 100%%\n' "$label"
}

explicit_integration_test_names() {
  local package=$1
  local package_dir

  package_dir="$(go list -f '{{.Dir}}' "$package")"

  find "$package_dir" \
    -maxdepth 1 \
    -type f \
    -name '*_integration_test.go' \
    -print0 |
    xargs -0 -r grep -hE '^func Test[A-Za-z0-9_]+' |
    sed -E 's/^func (Test[A-Za-z0-9_]+).*/\1/' |
    sort -u
}

unit_run_pattern() {
  local package=$1
  local explicit_tests
  local all_tests

  explicit_tests="$(explicit_integration_test_names "$package")"
  if [[ -z "$explicit_tests" ]]; then
    printf '%s\n' ''
    return 0
  fi

  all_tests="$(
    go test "$package" -list '^Test' |
      awk '/^Test[A-Za-z0-9_]+$/ { print }'
  )"

  awk '
    NR == FNR {
      excluded[$0] = 1
      next
    }
    !($0 in excluded) {
      print
    }
  ' \
    <(printf '%s\n' "$explicit_tests") \
    <(printf '%s\n' "$all_tests") |
    paste -sd'|' -
}

section 'LOCAL SOURCE QUALITY GATES'

step 'shell syntax'
bash -n "$0"
pass 'shell syntax'

step 'gofmt'
unformatted="$(gofmt -l .)"
if [[ -n "$unformatted" ]]; then
  printf 'FAIL: these files need gofmt:\n' >&2
  printf '%s\n' "$unformatted" >&2
  exit 1
fi
pass 'gofmt'

step 'go mod tidy'
go mod tidy -diff
pass 'go mod tidy'

step 'go mod verify'
go mod verify
pass 'go mod verify'

step 'go vet'
go vet ./...
pass 'go vet'

step 'release readiness'
sh ./scripts/release-check.sh
pass 'release readiness'

step 'local non-database test suite'
unset JOSHBOT_TEST_DATABASE_URL || true
unset JOSHBOT_REQUIRE_DATABASE_TESTS || true
unset JOSHBOT_UPDATE_GOLDEN || true
go test -count=1 -timeout=5m ./...
pass 'local non-database test suite'

section 'INDEPENDENT UNIT COVERAGE'

step 'prepare unit coverage profiles'
rm -rf "$LOCAL_COVERAGE_DIR"
mkdir -p "$LOCAL_COVERAGE_DIR"
go list ./... > "$LOCAL_COVERAGE_DIR/packages.txt"
printf 'mode: atomic\n' > "$LOCAL_COVERAGE_DIR/coverage.out"
pass 'unit coverage profiles ready'

step 'run unit coverage by package'
unit_index=0
while IFS= read -r package; do
  unit_index=$((unit_index + 1))
  profile="$LOCAL_COVERAGE_DIR/coverage-${unit_index}.out"
  run_pattern="$(unit_run_pattern "$package")"

  printf 'UNIT PACKAGE: %s\n' "$package"

  if [[ -n "$run_pattern" ]]; then
    go test \
      -count=1 \
      -timeout=5m \
      -run "^(${run_pattern})$" \
      -covermode=atomic \
      -coverprofile="$profile" \
      "$package"
  elif [[ -n "$(explicit_integration_test_names "$package")" ]]; then
    go test \
      -count=1 \
      -timeout=5m \
      -run '^$' \
      -covermode=atomic \
      -coverprofile="$profile" \
      "$package"
  else
    go test \
      -count=1 \
      -timeout=5m \
      -covermode=atomic \
      -coverprofile="$profile" \
      "$package"
  fi

  if [[ -f "$profile" ]]; then
    tail -n +2 "$profile" >> "$LOCAL_COVERAGE_DIR/coverage.out"
  fi
done < "$LOCAL_COVERAGE_DIR/packages.txt"
pass 'unit coverage packages passed'

step 'enforce exact unit coverage'
check_exact_coverage \
  "$LOCAL_COVERAGE_DIR/coverage.out" \
  'unit' \
  "$LOCAL_COVERAGE_DIR/coverage.txt"
pass 'independent unit coverage'

rm -rf "$LOCAL_COVERAGE_DIR"

section 'LOCAL FUZZ GATES'

run_fuzz() {
  local package=$1
  local fuzz=$2
  local label="$package $fuzz"

  step "$label"
  go test \
    "$package" \
    -timeout=2m \
    -run '^$' \
    -fuzz "$fuzz" \
    -fuzztime=10s
  pass "$label"
}

run_fuzz ./internal/origin '^FuzzParseRoundTrip$'
run_fuzz ./internal/declaration '^FuzzParseDeclaration$'
run_fuzz ./internal/discovery '^FuzzExtract$'
run_fuzz ./internal/discovery '^FuzzExtractPageLinks$'
run_fuzz ./internal/robots '^FuzzParseAndEvaluate$'

step 'git diff check'
git diff --check
pass 'git diff --check'

section 'BARF CONNECTIVITY'

step 'isolated Barf test infrastructure'
ssh "$BARF_HOST" \
  "docker inspect '$POSTGRES_CONTAINER' >/dev/null &&
   docker network inspect '$DATABASE_NETWORK' >/dev/null"
pass 'isolated Barf test infrastructure exists'

section 'PREPARE CURRENT WORKTREE'

step 'create remote quality-gate workspace'
ssh "$BARF_HOST" \
  "rm -rf '$REMOTE_WORKSPACE' '$REMOTE_RESULTS' &&
   mkdir -p '$REMOTE_WORKSPACE' '$REMOTE_RESULTS'"
pass 'remote quality-gate workspace created'

step 'stream current DarkHelmet worktree to Barf'

export COPYFILE_DISABLE=1
export COPY_EXTENDED_ATTRIBUTES_DISABLE=1

git ls-files \
  -z \
  --cached \
  --others \
  --exclude-standard |
while IFS= read -r -d '' path; do
  case "$path" in
    ._*|*/._*|.DS_Store|*/.DS_Store)
      continue
      ;;
  esac

  if [[ -e "$path" || -L "$path" ]]; then
    printf '%s\0' "$path"
  fi
done |
tar \
  --null \
  -czf - \
  -T - |
ssh "$BARF_HOST" \
  "tar --warning=no-unknown-keyword -xzf - -C '$REMOTE_WORKSPACE'"
pass 'current worktree streamed to Barf'

step 'remove macOS metadata from transferred tree'
ssh "$BARF_HOST" "
  find '$REMOTE_WORKSPACE' \\
    -type f \\
    \\( -name '._*' -o -name '.DS_Store' \\) \\
    -delete

  if find '$REMOTE_WORKSPACE' \\
    -type f \\
    -name '._*' \\
    -print \\
    -quit | grep -q .
  then
    echo 'FAIL: AppleDouble files remain in remote workspace' >&2
    exit 1
  fi
"
pass 'current worktree transferred without AppleDouble files'

section 'ISOLATED POSTGRESQL QUALITY GATE'

CURRENT_STAGE='isolated PostgreSQL quality gate on Barf'

ssh "$BARF_HOST" \
  RUN_ID="$RUN_ID" \
  TEST_DATABASE="$TEST_DATABASE" \
  TEST_ROLE="$TEST_ROLE" \
  TEST_PASSWORD="$TEST_PASSWORD" \
  REMOTE_WORKSPACE="$REMOTE_WORKSPACE" \
  REMOTE_RESULTS="$REMOTE_RESULTS" \
  POSTGRES_CONTAINER="$POSTGRES_CONTAINER" \
  DATABASE_NETWORK="$DATABASE_NETWORK" \
  GO_IMAGE="$GO_IMAGE" \
  PACKAGE_TIMEOUT="$PACKAGE_TIMEOUT" \
  REPEAT_COUNT="$REPEAT_COUNT" \
  STORE_PARALLELISM="$STORE_PARALLELISM" \
  'bash -s' <<'REMOTE'
set -Eeuo pipefail

remote_line() {
  printf '%s\n' '============================================================'
}

remote_section() {
  printf '\n=== %s ===\n\n' "$1"
}

remote_step() {
  printf '==> %s\n' "$1"
}

remote_pass() {
  printf 'PASS: %s\n\n' "$1"
}

cleanup() {
  local status=$?

  printf '\n'
  remote_line
  printf 'CLEANUP\n'
  remote_line
  remote_step 'clean isolated Barf test resources'

  docker exec "$POSTGRES_CONTAINER" \
    sh -ec '
      dropdb \
        --if-exists \
        --force \
        --username="${POSTGRES_USER:-postgres}" \
        "$1" \
        >/dev/null 2>&1 || true

      dropuser \
        --if-exists \
        --username="${POSTGRES_USER:-postgres}" \
        "$2" \
        >/dev/null 2>&1 || true
    ' sh "$TEST_DATABASE" "$TEST_ROLE" \
    >/dev/null 2>&1 || true

  rm -rf \
    "$REMOTE_WORKSPACE" \
    "$REMOTE_RESULTS"

  if [[ "$status" -eq 0 ]]; then
    printf 'PASS: isolated resources cleaned\n'
  else
    printf 'INFO: cleanup completed after failure; preserving original exit %s\n' "$status"
  fi

  printf '\n'
  exit "$status"
}

trap cleanup EXIT

remote_step 'verify transferred source'

if find "$REMOTE_WORKSPACE" \
  -type f \
  -name '._*' \
  -print \
  -quit | grep -q .
then
  printf 'FAIL: AppleDouble file exists in Barf worktree\n' >&2
  find "$REMOTE_WORKSPACE" -type f -name '._*' -print >&2
  exit 1
fi

if find "$REMOTE_WORKSPACE" \
  -type f \
  -name '.DS_Store' \
  -print \
  -quit | grep -q .
then
  printf 'FAIL: .DS_Store exists in Barf worktree\n' >&2
  exit 1
fi

remote_pass 'transferred source contains no macOS metadata'

remote_step 'verify isolated PostgreSQL health'

health="$(
  docker inspect \
    --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' \
    "$POSTGRES_CONTAINER"
)"

if [[ "$health" != 'healthy' ]]; then
  printf 'FAIL: %s health = %s\n' \
    "$POSTGRES_CONTAINER" \
    "$health" >&2
  exit 1
fi

remote_pass 'PostgreSQL is healthy'

remote_step "create fresh isolated database role $TEST_ROLE"

docker exec -i "$POSTGRES_CONTAINER" \
  sh -ec '
    psql \
      --username="${POSTGRES_USER:-postgres}" \
      --dbname="${POSTGRES_DB:-postgres}" \
      --set=ON_ERROR_STOP=1 \
      --set=role_name="$1" \
      --set=role_password="$2"
  ' sh "$TEST_ROLE" "$TEST_PASSWORD" <<'SQL'
CREATE ROLE :"role_name"
    LOGIN
    PASSWORD :'role_password'
    NOSUPERUSER
    NOCREATEDB
    NOCREATEROLE
    NOREPLICATION;
SQL

remote_pass 'fresh test role created'

remote_step "create fresh database $TEST_DATABASE owned by $TEST_ROLE"

docker exec "$POSTGRES_CONTAINER" \
  sh -ec '
    createdb \
      --username="${POSTGRES_USER:-postgres}" \
      --owner="$2" \
      "$1"
  ' sh "$TEST_DATABASE" "$TEST_ROLE"

remote_pass 'fresh test database created'

remote_step 'verify temporary PostgreSQL credential over TCP'

docker exec \
  --env "PGPASSWORD=$TEST_PASSWORD" \
  "$POSTGRES_CONTAINER" \
  psql \
    --host=127.0.0.1 \
    --username="$TEST_ROLE" \
    --dbname="$TEST_DATABASE" \
    --tuples-only \
    --no-align \
    --command='SELECT 1' |
  grep -qx '1'

remote_pass 'temporary PostgreSQL credential works'

TEST_DATABASE_URL="postgres://${TEST_ROLE}@${POSTGRES_CONTAINER}:5432/${TEST_DATABASE}?sslmode=disable"
DB_PASSWORD="$TEST_PASSWORD"

remote_step 'ensure Go caches exist'
docker volume create joshbot-issue17-gomodcache >/dev/null
docker volume create joshbot-issue17-gocache >/dev/null
remote_pass 'Go caches ready'

remote_step 'download modules outside isolated DB network'

docker run \
  --rm \
  --volume "$REMOTE_WORKSPACE:/workspace:ro" \
  --volume 'joshbot-issue17-gomodcache:/go/pkg/mod' \
  --volume 'joshbot-issue17-gocache:/root/.cache/go-build' \
  --workdir /workspace \
  "$GO_IMAGE" \
  go mod download

remote_pass 'module cache ready'

remote_section 'POSTGRESQL-BACKED RACE / REPEATABILITY'
printf 'repeat count: %s\n' "$REPEAT_COUNT"
printf 'package timeout: %s\n' "$PACKAGE_TIMEOUT"
printf 'store parallelism: %s\n\n' "$STORE_PARALLELISM"

docker run \
  --rm \
  -i \
  --network "$DATABASE_NETWORK" \
  --volume "$REMOTE_WORKSPACE:/workspace:ro" \
  --volume "$REMOTE_RESULTS:/results" \
  --volume 'joshbot-issue17-gomodcache:/go/pkg/mod' \
  --volume 'joshbot-issue17-gocache:/root/.cache/go-build' \
  --workdir /workspace \
  --env "JOSHBOT_TEST_DATABASE_URL=$TEST_DATABASE_URL" \
  --env 'JOSHBOT_REQUIRE_DATABASE_TESTS=1' \
  --env "PGPASSWORD=$DB_PASSWORD" \
  --env "PACKAGE_TIMEOUT=$PACKAGE_TIMEOUT" \
  --env "REPEAT_COUNT=$REPEAT_COUNT" \
  --env "STORE_PARALLELISM=$STORE_PARALLELISM" \
  "$GO_IMAGE" \
  bash -s <<'CONTAINER'
set -Eeuo pipefail

separator() {
  printf '%s\n' '------------------------------------------------------------'
}

go list ./... > /results/packages.txt

while IFS= read -r package; do
  started="$(date +%s)"

  printf '\n'
  separator
  printf 'PACKAGE: %s\n' "$package"
  printf 'REPEAT:  %s\n' "$REPEAT_COUNT"
  printf 'TIMEOUT: %s\n' "$PACKAGE_TIMEOUT"
  separator

  if [[ "$package" == */internal/store ]]; then
    parallel_args=( -parallel="$STORE_PARALLELISM" )
    printf 'PARALLEL: %s (store integration cap)\n' "$STORE_PARALLELISM"
  else
    parallel_args=()
    printf 'PARALLEL: default\n'
  fi

  printf 'MODE:     %s separate race-tested passes\n' "$REPEAT_COUNT"

  package_pass=1
  while (( package_pass <= REPEAT_COUNT )); do
    pass_started="$(date +%s)"
    printf 'PASS RUN: %s/%s\n' "$package_pass" "$REPEAT_COUNT"

    go test \
      -count=1 \
      -race \
      "${parallel_args[@]}" \
      -timeout="$PACKAGE_TIMEOUT" \
      "$package"

    pass_elapsed=$(( $(date +%s) - pass_started ))
    printf 'PASS: run %s/%s (%ss)\n' \
      "$package_pass" "$REPEAT_COUNT" "$pass_elapsed"

    package_pass=$((package_pass + 1))
  done

  elapsed=$(( $(date +%s) - started ))
  printf 'PASS: %s (%ss)\n' "$package" "$elapsed"
done < /results/packages.txt
CONTAINER

remote_pass 'PostgreSQL-backed race/repeatability suite'

remote_section 'INDEPENDENT INTEGRATION COVERAGE'
printf '%s\n' \
  'integration surface: explicit *_integration_test.go plus established PostgreSQL-backed legacy tests' \
  'coverage denominator: every first-party production statement'

docker run \
  --rm \
  -i \
  --network "$DATABASE_NETWORK" \
  --volume "$REMOTE_WORKSPACE:/workspace:ro" \
  --volume "$REMOTE_RESULTS:/results" \
  --volume 'joshbot-issue17-gomodcache:/go/pkg/mod' \
  --volume 'joshbot-issue17-gocache:/root/.cache/go-build' \
  --workdir /workspace \
  --env "JOSHBOT_TEST_DATABASE_URL=$TEST_DATABASE_URL" \
  --env 'JOSHBOT_REQUIRE_DATABASE_TESTS=1' \
  --env "PGPASSWORD=$DB_PASSWORD" \
  --env "PACKAGE_TIMEOUT=$PACKAGE_TIMEOUT" \
  --env "STORE_PARALLELISM=$STORE_PARALLELISM" \
  "$GO_IMAGE" \
  bash -s <<'CONTAINER'
set -Eeuo pipefail

raw=/results/integration-coverage-raw.out
merged=/results/integration-coverage.out
report=/results/integration-coverage.txt
baseline=/results/integration-baseline.out
packages=/results/integration-packages.txt

printf 'mode: atomic\n' > "$raw"
go list ./... > "$packages"

printf '\n==> build zero-count repository baseline\n'
go test \
  -count=1 \
  -run '^$' \
  -covermode=atomic \
  -coverpkg=./... \
  -coverprofile="$baseline" \
  ./...

tail -n +2 "$baseline" >> "$raw"

integration_files_for_package() {
  local package=$1
  local package_dir

  package_dir="$(go list -f '{{.Dir}}' "$package")"

  find "$package_dir" \
    -maxdepth 1 \
    -type f \
    -name '*_integration_test.go' \
    -print

  case "$package" in
    github.com/joshternet/joshbot/cmd/joshbot)
      if [[ -f "$package_dir/integration_test.go" ]]; then
        printf '%s\n' "$package_dir/integration_test.go"
      fi
      ;;

    github.com/joshternet/joshbot/internal/publicdata)
      if [[ -f "$package_dir/store_test.go" ]]; then
        printf '%s\n' "$package_dir/store_test.go"
      fi
      ;;

    github.com/joshternet/joshbot/internal/reporting)
      for name in \
        postgres_test.go \
        postgres_audit_test.go \
        postgres_pagination_test.go \
        postgres_source_detail_test.go
      do
        if [[ -f "$package_dir/$name" ]]; then
          printf '%s\n' "$package_dir/$name"
        fi
      done
      ;;

    github.com/joshternet/joshbot/internal/store)
      find "$package_dir" \
        -maxdepth 1 \
        -type f \
        -name '*_test.go' \
        ! -name '*_unit_test.go' \
        ! -name '*_golden_test.go' \
        ! -name '*_coverage_test.go' \
        ! -name '*_constructor_test.go' \
        ! -name '*_integration_test.go' \
        -print0 |
        xargs -0 -r grep -El \
          'JOSHBOT_TEST_DATABASE_URL|JOSHBOT_REQUIRE_DATABASE_TESTS|new(Store|SerialStore|ConcurrentStore|EmptyStore|CrawlSourceMigration)TestPool'
      ;;
  esac
}

index=0
while IFS= read -r package; do
  mapfile -t integration_files < <(
    integration_files_for_package "$package" |
      sort -u
  )

  if (( ${#integration_files[@]} == 0 )); then
    continue
  fi

  printf '\nINTEGRATION PACKAGE: %s\n' "$package"
  printf 'INTEGRATION FILES:\n'
  printf '  %s\n' "${integration_files[@]}"

  integration_tests="$(
    {
      grep -hE '^func Test[A-Za-z0-9_]+' "${integration_files[@]}" || true
    } |
      sed -E 's/^func (Test[A-Za-z0-9_]+).*/\1/' |
      awk '$0 != "TestMain"' |
      sort -u
  )"

  if [[ -z "$integration_tests" ]]; then
    printf 'FAIL: no integration tests for %s\n' "$package" >&2
    exit 1
  fi

  duplicate_tests="$(
    while IFS= read -r test_name; do
      occurrences="$(
        grep -hE '^func Test[A-Za-z0-9_]+' \
          "$(go list -f '{{.Dir}}' "$package")"/*_test.go |
          sed -E 's/^func (Test[A-Za-z0-9_]+).*/\1/' |
          grep -c "^${test_name}$" || true
      )"

      if (( occurrences > 1 )); then
        printf '%s\n' "$test_name"
      fi
    done <<< "$integration_tests"
  )"

  if [[ -n "$duplicate_tests" ]]; then
    printf 'FAIL: integration test names collide with other tests in %s:\n' \
      "$package" >&2
    printf '%s\n' "$duplicate_tests" >&2
    exit 1
  fi

  integration_pattern="$(
    printf '%s\n' "$integration_tests" |
      paste -sd'|' -
  )"

  if [[ "$package" == */internal/store ]]; then
    parallel_args=( -parallel="$STORE_PARALLELISM" )
  else
    parallel_args=()
  fi

  index=$((index + 1))
  profile="/results/integration-coverage-${index}.out"

  go test \
    -count=1 \
    "${parallel_args[@]}" \
    -timeout="$PACKAGE_TIMEOUT" \
    -run "^(${integration_pattern})$" \
    -covermode=atomic \
    -coverpkg=./... \
    -coverprofile="$profile" \
    "$package"

  tail -n +2 "$profile" >> "$raw"
done < "$packages"

if (( index == 0 )); then
  printf 'FAIL: no integration tests were discovered\n' >&2
  exit 1
fi

printf '\n==> merge integration coverage by source block\n'
{
  printf 'mode: atomic\n'

  awk '
    /^mode:/ {
      next
    }

    {
      block = $1

      if (block in statements && statements[block] != $2) {
        printf "FAIL: conflicting statement count for %s: %s vs %s\n", \
          block, statements[block], $2 > "/dev/stderr"
        conflict = 1
        next
      }

      statements[block] = $2
      counts[block] += $3
    }

    END {
      if (conflict) {
        exit 1
      }

      for (block in statements) {
        printf "%s %d %d\n", block, statements[block], counts[block]
      }
    }
  ' "$raw" |
    sort
} > "$merged"

printf '\n==> enforce exact independent integration coverage\n'
go tool cover -func="$merged" | tee "$report"

uncovered="$(
  awk '
    /^mode:/ { next }
    $2 > 0 && $3 == 0 { print }
  ' "$merged"
)"

if [[ -n "$uncovered" ]]; then
  printf 'FAIL: integration coverage has uncovered statement blocks:\n' >&2
  printf '%s\n' "$uncovered" >&2
  exit 1
fi

function_gaps="$(
  awk '
    $1 ~ /^github\.com\/joshternet\/joshbot\// && $NF != "100.0%" {
      print
    }
  ' "$report"
)"

if [[ -n "$function_gaps" ]]; then
  printf 'FAIL: integration coverage has function gaps:\n' >&2
  printf '%s\n' "$function_gaps" >&2
  exit 1
fi

read -r covered_statements total_statements < <(
  awk '
    /^mode:/ { next }
    {
      total += $2
      if ($3 > 0) {
        covered += $2
      }
    }
    END {
      printf "%d %d\n", covered, total
    }
  ' "$merged"
)

if [[ "$covered_statements" != "$total_statements" ]]; then
  printf 'FAIL: integration statements = %s/%s\n' \
    "$covered_statements" \
    "$total_statements" >&2
  exit 1
fi

printf 'PASS: integration statement coverage = %s/%s\n' \
  "$covered_statements" \
  "$total_statements"
printf 'PASS: integration function coverage = 100%%\n'
CONTAINER

remote_pass 'independent integration coverage'
REMOTE

section 'FINAL LOCAL INTEGRITY CHECK'

step 'git diff check after quality gate'
git diff --check
pass 'git diff check after quality gate'

step 'verify quality gate did not change the working tree'
STATUS_AFTER="$(git status --porcelain=v1 -uall)"

if [[ "$STATUS_AFTER" != "$STATUS_BEFORE" ]]; then
  printf 'FAIL: quality gate changed the working tree\n\n' >&2
  printf '%s\n' '--- status before ---' >&2
  printf '%s\n' "$STATUS_BEFORE" >&2
  printf '%s\n' '--- status after ---' >&2
  printf '%s\n' "$STATUS_AFTER" >&2
  exit 1
fi

pass 'working tree unchanged by quality gate'

printf '\n'
line
printf 'JOSHBOT QUALITY GATE PASSED\n'
line
printf '\n'

git status --short --branch