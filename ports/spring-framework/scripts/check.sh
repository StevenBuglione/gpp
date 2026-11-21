#!/usr/bin/env bash
set -euo pipefail

port_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
temp_root="$(mktemp -d "${TMPDIR:-/tmp}/spring-framework-gpp.XXXXXX")"
trap 'rm -rf "$temp_root"' EXIT
gpp="${GPP:-gpp}"

export GOWORK=off
cp -R "$port_root/testdata/consumer" "$temp_root/consumer"

(
    cd "$port_root"
    "$gpp" compile -output "$temp_root/generated" ./spring-core ./spring-jdbc
)
(
    cd "$temp_root/consumer"
    go build ./...
    go test ./...
)
