# Spring Boot for GPP

This project owns the Spring Boot port and its upstream behavior fixtures.
Its upstream release and commit are pinned in `upstream.lock`. The Spring
Framework port is a separate project. Boot integrations should reference it
by its Go module path, with a workspace or a temporary replacement for
development against an unreleased version.

The native `spring-boot` diagnostics port uses the Java runtime provided by
the generated Framework module. Its Go test passes a Framework JDBC name and
typed cause through Boot and checks that the original references come back.
An ordinary Go implementation can satisfy the `FailureAnalyzer` and
`FailureAnalysisReporter` interfaces.
Run it with the output of `gpp compile` for the Framework `spring-core` and
`spring-jdbc` directories.

```bash
./scripts/check.sh /path/to/generated-spring-framework
```

The Java reference probe lives in `testdata/reference`. The GPP repository's
reference runner can execute this probe against the pinned upstream Boot
checkout. Framework implementation and GPP compiler code belong in their
respective projects.

Run the fixture from this project root against the pinned Java release.

```bash
GPP_SPRING_PORT_ROOT="$PWD" /path/to/gpp/scripts/check-spring-reference.sh \
  boot /path/to/upstream-checkout --boot-probe
```
