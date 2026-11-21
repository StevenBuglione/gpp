#!/usr/bin/env bash
set -euo pipefail

if [[ "$#" != 1 || ! -f "$1/go.mod" ]]; then
    printf 'Usage: %s <generated-spring-framework-go-module>\n' "$0" >&2
    exit 2
fi
framework="$(cd "$1" && pwd)"
port_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
temp_root="$(mktemp -d "${TMPDIR:-/tmp}/spring-boot-gpp.XXXXXX")"
trap 'rm -rf "$temp_root"' EXIT
gpp="${GPP:-gpp}"

cp -R "$port_root/testdata/consumer" "$temp_root/consumer"
(
    cd "$port_root"
    "$gpp" compile -output "$temp_root/boot" \
        -java-runtime-import github.com/StevenBuglione/spring-framework-gpp/gpp/java ./core/spring-boot
)
test ! -d "$temp_root/boot/gpp/java"

(
    cd "$temp_root"
    unset GOWORK
    go work init "$framework" ./boot ./consumer
    export GOWORK="$temp_root/go.work"
    cd consumer
    go test ./...
)
