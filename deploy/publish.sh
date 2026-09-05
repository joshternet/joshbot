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
readonly compose_file

snapshot_directory=""

fail() {
  printf 'publish: %s\n' "$*" >&2
  exit 1
}

require_environment() {
  local name
  local value

  name="$1"
  value="${!name-}"

  if [[ -z "$value" ]]; then
    fail "$name is required"
  fi
}

cleanup() {
  local status="$?"
  local cleanup_parent
  local cleanup_name

  trap - EXIT HUP INT TERM
  set +e

  if [[ -n "$snapshot_directory" ]]; then
    cleanup_parent="$(dirname -- "$snapshot_directory")"
    cleanup_name="$(basename -- "$snapshot_directory")"

    if [[ "$cleanup_parent" != "$export_directory" ]]; then
      printf 'publish: refused cleanup outside the export directory\n' >&2

      if [[ "$status" -eq 0 ]]; then
        status=1
      fi
    elif [[ "$cleanup_name" != .joshbot-publish-* ]]; then
      printf 'publish: refused cleanup of an unexpected directory\n' >&2

      if [[ "$status" -eq 0 ]]; then
        status=1
      fi
    elif ! rm -rf -- "$snapshot_directory"; then
      printf 'publish: could not remove the temporary snapshot\n' >&2

      if [[ "$status" -eq 0 ]]; then
        status=1
      fi
    fi
  fi

  exit "$status"
}

trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

for required_command in \
  basename \
  date \
  dirname \
  docker \
  openssl \
  rm; do
  command -v "$required_command" >/dev/null 2>&1 ||
    fail "required command is unavailable: $required_command"
done

if [[ ! -f "$compose_file" ]]; then
  fail "compose.yaml is unavailable"
fi

require_environment JOSHBOT_EXPORT_DIR
require_environment JOSHBOT_GITHUB_TOKEN_FILE
require_environment JOSHBOT_PUBLISH_GITHUB_OWNER
require_environment JOSHBOT_PUBLISH_GITHUB_REPOSITORY

case "$JOSHBOT_EXPORT_DIR" in
  /*)
    ;;
  *)
    fail "JOSHBOT_EXPORT_DIR must be an absolute path"
    ;;
esac

if [[ ! -d "$JOSHBOT_EXPORT_DIR" ]]; then
  fail "JOSHBOT_EXPORT_DIR must be an existing directory"
fi

if [[ -L "$JOSHBOT_EXPORT_DIR" ]]; then
  fail "JOSHBOT_EXPORT_DIR must not be a symbolic link"
fi

export_directory="$(
  CDPATH='' cd -- "$JOSHBOT_EXPORT_DIR" &&
    pwd -P
)"
readonly export_directory

if [[ "$export_directory" == "/" ]]; then
  fail "JOSHBOT_EXPORT_DIR must not be the filesystem root"
fi

if [[ ! -f "$JOSHBOT_GITHUB_TOKEN_FILE" ]]; then
  fail "JOSHBOT_GITHUB_TOKEN_FILE must be a regular file"
fi

if [[ -L "$JOSHBOT_GITHUB_TOKEN_FILE" ]]; then
  fail "JOSHBOT_GITHUB_TOKEN_FILE must not be a symbolic link"
fi

if [[ ! -s "$JOSHBOT_GITHUB_TOKEN_FILE" ]]; then
  fail "JOSHBOT_GITHUB_TOKEN_FILE must not be empty"
fi

docker compose \
  --file "$compose_file" \
  --profile tools \
  --profile publisher \
  config \
  --quiet

snapshot_name="$(
  printf '.joshbot-publish-%s-%s-%s' \
    "$(date -u '+%Y%m%dT%H%M%SZ')" \
    "$$" \
    "$(openssl rand -hex 16)"
)"
readonly snapshot_name

case "$snapshot_name" in
  .joshbot-publish-[0-9T-Z-]*)
    ;;
  *)
    fail "generated an unsafe snapshot name"
    ;;
esac

snapshot_directory="$export_directory/$snapshot_name"
container_snapshot="/exports/$snapshot_name"

if [[ -e "$snapshot_directory" ]] ||
  [[ -L "$snapshot_directory" ]]; then
  fail "temporary snapshot destination already exists"
fi

docker compose \
  --file "$compose_file" \
  --profile tools \
  run \
  --rm \
  --no-deps \
  tools \
  export \
  --output "$container_snapshot"

if [[ ! -d "$snapshot_directory" ]] ||
  [[ -L "$snapshot_directory" ]]; then
  fail "export did not create a safe snapshot directory"
fi

docker compose \
  --file "$compose_file" \
  --profile publisher \
  run \
  --rm \
  --no-deps \
  publisher \
  publish \
  --input "$container_snapshot"