#!/usr/bin/env bash
set -euo pipefail

port_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ "$#" != 1 ]]; then
    printf 'Usage: %s <built-spring-framework-checkout>\n' "$0" >&2
    exit 2
fi
checkout="$1"
read -r _ tag revision < "$port_root/upstream.lock"
if [[ "$(git -C "$checkout" rev-parse HEAD)" != "$revision" ]] ||
    [[ -n "$(git -C "$checkout" status --porcelain)" ]]; then
    printf 'Expected a clean Framework checkout at %s (%s).\n' "$tag" "$revision" >&2
    exit 1
fi
sources=(
    spring-core/org/springframework/asm/SpringAsmInfo
    spring-core/org/springframework/cglib/SpringCglibInfo
    spring-jdbc/org/springframework/jdbc/core/metadata/TableParameterMetaData
)
for path in "${sources[@]}"; do
    module="${path%%/*}"
    class="${path#*/}"
    classes="$checkout/$module/build/classes/java/main"
    if [[ ! -d "$classes" ]]; then
        printf 'Build the upstream :%s:compileJava task first.\n' "$module" >&2
        exit 2
    fi
    "${GPP:-gpp}" migrate java --emit-gpp --java "${JAVA:-java}" --release 17 \
        --class-path "$classes" "$checkout/$module/src/main/java/$class.java" | \
        diff -u --label "$path.gpp (checked in)" --label "$path.gpp (translated)" \
            <(sed -n '/^package /,$p' "$port_root/$path.gpp") -
done
