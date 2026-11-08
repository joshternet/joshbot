#!/bin/sh

set -eu

# Always use the latest govulncheck so vulnerability DB and scanner stay current.
govulncheck_version=latest

if [ "$#" -eq 0 ]; then
	set -- ./...
fi

exec go run \
	"golang.org/x/vuln/cmd/govulncheck@${govulncheck_version}" \
	"$@"
