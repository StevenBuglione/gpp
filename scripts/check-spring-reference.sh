#!/usr/bin/env bash
set -euo pipefail

if [[ "$#" -lt 2 || "$#" -gt 3 ]]; then
    printf 'Usage: %s framework|boot <checkout-directory> [--inventory|--all-java|--boot-probe]\n' "$0" >&2
    exit 2
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
case "$1" in
    framework)
        repository=spring-framework
        tag=v7.1.0
        revision=f2e4863de60e6a20e827a0aa3c524ffb3ae96ad4
        tasks=(
            :spring-core:gppReferenceInputs
            :spring-beans:gppReferenceInputs
            :spring-context:gppReferenceInputs
            :spring-aop:gppReferenceInputs
            :spring-expression:gppReferenceInputs
        )
        ;;
    boot)
        repository=spring-boot
        tag=v4.2.0
        revision=3a36e91f4d92d2e5986092baf8fa1e4848098f01
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
            "-Dgpp.springBootProbe=$repo_root/tests/testdata/spring-boot-reference/SpringBootProbe.java"
            :module:spring-boot-transaction:gppBootProbe
        )
        ;;
    *) printf 'Unknown Spring reference mode: %s\n' "$3" >&2; exit 2 ;;
esac

checkout="$2"
if [[ ! -e "$checkout" ]]; then
    git clone --depth 1 --branch "$tag" "https://github.com/spring-projects/$repository.git" "$checkout"
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
