#!/bin/sh

set -eu

govulncheck_version=v1.8.0

if [ "$#" -eq 0 ]; then
	set -- ./...
fi

exec go run \
	"golang.org/x/vuln/cmd/govulncheck@${govulncheck_version}" \
	"$@"
