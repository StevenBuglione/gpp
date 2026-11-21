#!/usr/bin/env bash
set -euo pipefail

if [[ "$(go env GOOS)" != linux || -z "${JAVA_HOME:-}" || ! -x "$JAVA_HOME/bin/javac" ]]; then
    printf 'The JNI probe needs Linux and JAVA_HOME pointing to a JDK.\n' >&2
    exit 2
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixtures="$repo_root/tests/testdata/jvm-interop"
temp_root="$(mktemp -d "${TMPDIR:-/tmp}/gpp-jvm-probe.XXXXXX")"
trap 'rm -rf "$temp_root"' EXIT
mkdir -p "$temp_root/base" "$temp_root/extension" "$temp_root/go"

"$JAVA_HOME/bin/javac" --release 17 -d "$temp_root/base" "$fixtures"/java/port/*.java
"$JAVA_HOME/bin/javac" --release 17 -cp "$temp_root/base" -d "$temp_root/extension" "$fixtures/java/extension/Extension.java"
"$JAVA_HOME/bin/jar" --create --file "$temp_root/extension.jar" -C "$temp_root/extension" .

cp "$fixtures"/go/* "$temp_root/go"
cd "$temp_root/go"
export GOWORK=off CGO_ENABLED=1
export CGO_CFLAGS="'-I$JAVA_HOME/include' '-I$JAVA_HOME/include/linux'"
export CGO_LDFLAGS="'-L$JAVA_HOME/lib/server' -ljvm"
export LD_LIBRARY_PATH="$JAVA_HOME/lib/server${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
go build -o "$temp_root/probe" .
"$temp_root/probe" "$temp_root/base" "$temp_root/extension.jar"
