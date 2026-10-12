#!/bin/sh

set -eu

umask 077

max_secret_size=4097

cleanup() {
	status="$?"
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
: "${JOSHBOT_BACKUP_ARCHIVE:?JOSHBOT_BACKUP_ARCHIVE is required}"

if [ "${JOSHBOT_RESTORE_DISPOSABLE:-}" != "yes" ]; then
	printf 'joshbot restore: set JOSHBOT_RESTORE_DISPOSABLE=yes to restore into a disposable database only\n' >&2
	exit 1
fi

password_file="$JOSHBOT_DATABASE_PASSWORD_FILE"
archive_path="$JOSHBOT_BACKUP_ARCHIVE"

if [ ! -r "$password_file" ]; then
	printf 'joshbot restore: password file is unreadable\n' >&2
	exit 1
fi

password_size="$(
	wc -c <"$password_file" |
		tr -d '[:space:]'
)"

if [ "$password_size" -eq 0 ]; then
	printf 'joshbot restore: password file is empty\n' >&2
	exit 1
fi

if [ "$password_size" -gt "$max_secret_size" ]; then
	printf 'joshbot restore: password file is too large\n' >&2
	exit 1
fi

if [ ! -r "$archive_path" ]; then
	printf 'joshbot restore: backup archive is unreadable\n' >&2
	exit 1
fi

PGPASSWORD="$(cat "$password_file")"

if [ -z "$PGPASSWORD" ]; then
	printf 'joshbot restore: password file is empty\n' >&2
	exit 1
fi

export PGPASSWORD
export PGCONNECT_TIMEOUT="${PGCONNECT_TIMEOUT:-10}"

pg_restore \
	--list \
	"$archive_path" \
	>/dev/null

pg_restore \
	--host="$PGHOST" \
	--port="$PGPORT" \
	--username="$PGUSER" \
	--dbname="$PGDATABASE" \
	--no-password \
	--clean \
	--if-exists \
	--no-owner \
	--no-privileges \
	"$archive_path"

printf 'restore completed into disposable database %s\n' "$PGDATABASE"
