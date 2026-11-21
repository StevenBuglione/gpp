# Spring Boot for GPP

This project owns the Spring Boot port and its upstream behavior fixtures.
Its upstream release and commit are pinned in `upstream.lock`. The Spring
Framework port is a separate project. Boot integrations should reference it
by its Go module path, with a workspace or a temporary replacement for
development against an unreleased version.

The Java reference probe lives in `testdata/reference`. The GPP repository's
reference runner can execute this probe against the pinned upstream Boot
checkout. Framework implementation and GPP compiler code belong in their
respective projects.

Run the fixture from this project root against the pinned Java release.

```bash
GPP_SPRING_PORT_ROOT="$PWD" /path/to/gpp/scripts/check-spring-reference.sh \
  boot /path/to/upstream-checkout --boot-probe
```
