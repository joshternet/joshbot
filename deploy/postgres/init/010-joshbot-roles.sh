#!/bin/sh

set -eu

max_secret_size=4097

read_secret() {
    secret_path="$1"
    secret_name="$2"

    if [ ! -r "$secret_path" ]; then
        printf 'joshbot init: %s is unreadable\n' "$secret_name" >&2
        return 1
    fi

    secret_size="$(
        wc -c <"$secret_path" |
            tr -d '[:space:]'
    )"

    if [ "$secret_size" -eq 0 ]; then
        printf 'joshbot init: %s is empty\n' "$secret_name" >&2
        return 1
    fi

    if [ "$secret_size" -gt "$max_secret_size" ]; then
        printf 'joshbot init: %s is too large\n' "$secret_name" >&2
        return 1
    fi

    secret_value="$(cat "$secret_path")"

    if [ -z "$secret_value" ]; then
        printf 'joshbot init: %s is empty\n' "$secret_name" >&2
        return 1
    fi

    printf '%s' "$secret_value"
}

migrator_password="$(
    read_secret \
        /run/secrets/joshbot_migrator_password \
        joshbot_migrator_password
)"

app_password="$(
    read_secret \
        /run/secrets/joshbot_app_password \
        joshbot_app_password
)"

backup_password="$(
    read_secret \
        /run/secrets/joshbot_backup_password \
        joshbot_backup_password
)"

psql \
    --username "$POSTGRES_USER" \
    --dbname "$POSTGRES_DB" \
    --set=database_name="$POSTGRES_DB" \
    --set=migrator_password="$migrator_password" \
    --set=app_password="$app_password" \
    --set=backup_password="$backup_password" \
    --set=ON_ERROR_STOP=1 <<'SQL'
CREATE ROLE joshbot_migrator
    LOGIN
    PASSWORD :'migrator_password'
    NOSUPERUSER
    NOCREATEDB
    NOCREATEROLE
    NOREPLICATION;

CREATE ROLE joshbot_app
    LOGIN
    PASSWORD :'app_password'
    NOSUPERUSER
    NOCREATEDB
    NOCREATEROLE
    NOREPLICATION;

CREATE ROLE joshbot_backup
    LOGIN
    PASSWORD :'backup_password'
    NOSUPERUSER
    NOCREATEDB
    NOCREATEROLE
    NOREPLICATION;

REVOKE ALL
    ON DATABASE :"database_name"
    FROM PUBLIC;

GRANT CONNECT
    ON DATABASE :"database_name"
    TO joshbot_migrator,
       joshbot_app,
       joshbot_backup;

REVOKE ALL
    ON SCHEMA public
    FROM PUBLIC;

ALTER SCHEMA public
    OWNER TO joshbot_migrator;

GRANT USAGE, CREATE
    ON SCHEMA public
    TO joshbot_migrator;

GRANT USAGE
    ON SCHEMA public
    TO joshbot_app,
       joshbot_backup;

ALTER DEFAULT PRIVILEGES
    FOR ROLE joshbot_migrator
    IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE
    ON TABLES
    TO joshbot_app;

ALTER DEFAULT PRIVILEGES
    FOR ROLE joshbot_migrator
    IN SCHEMA public
    GRANT USAGE, SELECT, UPDATE
    ON SEQUENCES
    TO joshbot_app;

ALTER DEFAULT PRIVILEGES
    FOR ROLE joshbot_migrator
    IN SCHEMA public
    GRANT SELECT
    ON TABLES
    TO joshbot_backup;

ALTER DEFAULT PRIVILEGES
    FOR ROLE joshbot_migrator
    IN SCHEMA public
    GRANT SELECT
    ON SEQUENCES
    TO joshbot_backup;

ALTER DEFAULT PRIVILEGES
    FOR ROLE joshbot_migrator
    IN SCHEMA public
    GRANT USAGE
    ON TYPES
    TO joshbot_app,
       joshbot_backup;

ALTER ROLE joshbot_migrator
    SET search_path TO public;

ALTER ROLE joshbot_app
    SET search_path TO public;

ALTER ROLE joshbot_backup
    SET search_path TO public;
SQL

unset migrator_password
unset app_password
unset backup_password
