#!/bin/sh

set -eu

staticcheck_version=v0.8.2

if [ "$#" -eq 0 ]; then
	set -- ./...
fi

exec go run \
	"honnef.co/go/tools/cmd/staticcheck@${staticcheck_version}" \
	"$@"
