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
core="$checkout/spring-core"
classes="$core/build/classes/java/main"
if [[ ! -d "$classes" ]]; then
    printf '%s\n' 'Build the upstream :spring-core:compileJava task first.' >&2
    exit 2
fi

for class in org/springframework/asm/SpringAsmInfo org/springframework/cglib/SpringCglibInfo; do
    "${GPP:-gpp}" migrate java --emit-gpp --java "${JAVA:-java}" --release 17 \
        --class-path "$classes" "$core/src/main/java/$class.java" | \
        diff -u --label "$class.gpp (checked in)" --label "$class.gpp (translated)" \
            <(sed -n '/^package /,$p' "$port_root/spring-core/$class.gpp") -
done
