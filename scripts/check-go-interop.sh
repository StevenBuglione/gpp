#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
temp_root="$(mktemp -d "${TMPDIR:-/tmp}/gpp-go-interop.XXXXXX")"
trap 'rm -rf "$temp_root"' EXIT

go_root="$(go env GOROOT)"
git_path="$(command -v git)"
mkdir -p "$temp_root/path"
ln -s "$go_root/bin/go" "$temp_root/path/go"
ln -s "$git_path" "$temp_root/path/git"
cp -R "$repo_root/tests/testdata/go-interop/." "$temp_root"
cp -R "$repo_root/examples/mixed" "$temp_root/mixed"

assert_output() {
    local expected="$1"
    shift
    local actual
    actual="$("$@")"
    if [[ "$actual" != "$expected" ]]; then
        printf 'Command failed the output check: %s\nExpected: %s\nActual: %s\n' "$*" "$expected" "$actual" >&2
        return 1
    fi
}

# Limit runtime discovery to the selected Go toolchain and Git. The Go-only
# fixture uses the standard library and local modules throughout.
(
    export PATH="$temp_root/path" GOROOT="$go_root" GOTOOLCHAIN=local
    export GOENV=off GOWORK=off GOFLAGS= GOPROXY=off GOSUMDB=off CGO_ENABLED=0
    export GPP_CACHE="$temp_root/cache"
    unset JAVA_HOME JDK_HOME CLASSPATH
    cd "$repo_root"
    go build -o "$temp_root/gpp" .
    gpp="$temp_root/gpp"

    cd "$temp_root"
    printf '%s\n' 'Checking the existing mixed-language examples'
    assert_output $'Hello, Ada from native Go\nnative sum: 42' "$gpp" run mixed/go_from_gpp
    assert_output 'Hello Grace from GPP' "$gpp" run -module generated mixed/go_imports_gpp

    printf '%s\n' 'Checking an external Go consumer of the generated library'
    "$gpp" compile -module example.com/gpp-interop-library -output generated library/entry.gpp
    test ! -e generated/interop/native_excluded.go
    "$gpp" compile -module example.com/gpp-spring -output spring \
        "$repo_root/ports/spring-framework/spring-core/org/springframework/core/Ordering.gpp"
    (
        cd consumer
        go build ./...
        go test ./...
    )

    printf '%s\n' 'Checking native rebuilds after a resource change'
    for _attempt in 1 2; do
        "$gpp" build -module example.com/gpp-interop-library -output app-go -o app program/main.gpp library/entry.gpp
        assert_output 'baseline: Ada' ./app
    done
    printf 'updated\n' > library/label.txt
    "$gpp" build -module example.com/gpp-interop-library -output app-go -o app program/main.gpp library/entry.gpp
    assert_output 'updated: Ada' ./app
)
