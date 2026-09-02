#!/bin/sh

set -eu

umask 077

max_secret_size=4097
partial_path=""

cleanup() {
    status="$?"

    if [ -n "$partial_path" ] &&
        [ -e "$partial_path" ]; then
        rm -f -- "$partial_path"
    fi

    unset PGPASSWORD
    exit "$status"
}

trap cleanup EXIT
trap 'exit 1' HUP INT TERM

: "${PGHOST:?PGHOST is required}"
: "${PGPORT:?PGPORT is required}"
: "${PGDATABASE:?PGDATABASE is required}"
: "${PGUSER:?PGUSER is required}"
: "${JOSHBOT_DATABASE_PASSWORD_FILE:?JOSHBOT_DATABASE_PASSWORD_FILE is required}"
: "${JOSHBOT_BACKUP_DIRECTORY:?JOSHBOT_BACKUP_DIRECTORY is required}"

password_file="$JOSHBOT_DATABASE_PASSWORD_FILE"
backup_directory="$JOSHBOT_BACKUP_DIRECTORY"

if [ ! -r "$password_file" ]; then
    printf 'joshbot backup: password file is unreadable\n' >&2
    exit 1
fi

password_size="$(
    wc -c <"$password_file" |
        tr -d '[:space:]'
)"

if [ "$password_size" -eq 0 ]; then
    printf 'joshbot backup: password file is empty\n' >&2
    exit 1
fi

if [ "$password_size" -gt "$max_secret_size" ]; then
    printf 'joshbot backup: password file is too large\n' >&2
    exit 1
fi

PGPASSWORD="$(cat "$password_file")"

if [ -z "$PGPASSWORD" ]; then
    printf 'joshbot backup: password file is empty\n' >&2
    exit 1
fi

export PGPASSWORD

if [ ! -d "$backup_directory" ]; then
    printf 'joshbot backup: backup directory does not exist\n' >&2
    exit 1
fi

if [ ! -w "$backup_directory" ]; then
    printf 'joshbot backup: backup directory is not writable\n' >&2
    exit 1
fi

timestamp="$(date -u '+%Y%m%dT%H%M%SZ')"
archive_name="joshbot-${timestamp}.dump"
final_path="${backup_directory}/${archive_name}"
partial_path="${final_path}.partial"

if [ -e "$final_path" ] ||
    [ -e "$partial_path" ]; then
    printf 'joshbot backup: timestamped archive already exists\n' >&2
    exit 1
fi

pg_dump \
    --host="$PGHOST" \
    --port="$PGPORT" \
    --username="$PGUSER" \
    --dbname="$PGDATABASE" \
    --no-password \
    --format=custom \
    --no-owner \
    --no-privileges \
    --file="$partial_path"

pg_restore \
    --list \
    "$partial_path" \
    >/dev/null

chmod 0600 "$partial_path"
mv -- "$partial_path" "$final_path"
partial_path=""

printf 'backup created: %s\n' "$final_path"
