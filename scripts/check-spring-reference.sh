#!/usr/bin/env bash
set -euo pipefail

if [[ "$#" -lt 2 || "$#" -gt 3 ]]; then
    printf 'Usage: %s framework|boot <checkout-directory> [--inventory|--all-java|--boot-probe]\n' "$0" >&2
    exit 2
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
case "$1" in
    framework)
        tasks=(
            :spring-core:gppReferenceInputs
            :spring-beans:gppReferenceInputs
            :spring-context:gppReferenceInputs
            :spring-aop:gppReferenceInputs
            :spring-expression:gppReferenceInputs
        )
        ;;
    boot)
        tasks=(
            :core:spring-boot:gppReferenceInputs
            :core:spring-boot-autoconfigure:gppReferenceInputs
        )
        ;;
    *)
        printf 'Unknown Spring reference project: %s\n' "$1" >&2
        exit 2
        ;;
esac

port_root="$(cd "${GPP_SPRING_PORT_ROOT:-$repo_root/ports/spring-$1}" && pwd)"
read -r repository tag revision < "$port_root/upstream.lock"
if [[ -z "$repository" || -z "$tag" || ! "$revision" =~ ^[a-f0-9]{40}$ ]]; then
    printf 'Invalid upstream lock in %s.\n' "$port_root" >&2
    exit 2
fi

case "${3:-}" in
    "") ;;
    --inventory) tasks=(:gppReferenceProjects) ;;
    --all-java) tasks=(--continue gppReferenceInputs) ;;
    --boot-probe)
        if [[ "$1" != boot ]]; then
            printf '%s\n' '--boot-probe requires the boot project.' >&2
            exit 2
        fi
        tasks=(
            "-Dgpp.springBootProbe=$port_root/testdata/reference/SpringBootProbe.java"
            :module:spring-boot-transaction:gppBootProbe
        )
        ;;
    *) printf 'Unknown Spring reference mode: %s\n' "$3" >&2; exit 2 ;;
esac

checkout="$2"
if [[ ! -e "$checkout" ]]; then
    git clone --depth 1 --branch "$tag" "$repository" "$checkout"
fi
if [[ "$(git -C "$checkout" rev-parse HEAD)" != "$revision" ]] ||
    [[ -n "$(git -C "$checkout" status --porcelain)" ]]; then
    printf 'Expected a clean checkout of %s at %s (%s).\n' "$repository" "$tag" "$revision" >&2
    exit 1
fi

# The pinned Spring builds use a JDK 25 toolchain. Select it through JAVA_HOME
# and use the project's Gradle wrapper to reproduce the reference compiler.
cd "$checkout"
./gradlew --no-daemon --max-workers=2 --console=plain \
    --init-script "$repo_root/scripts/spring-reference.init.gradle" "${tasks[@]}"
