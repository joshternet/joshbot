#!/bin/sh

set -eu

# Always use the latest staticcheck so analyzer fixes stay current.
staticcheck_version=latest

if [ "$#" -eq 0 ]; then
	set -- ./...
fi

exec go run \
	"honnef.co/go/tools/cmd/staticcheck@${staticcheck_version}" \
	"$@"
