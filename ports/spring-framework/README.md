# Spring Framework for GPP

This project owns the Spring Framework port. Its source directories follow
the upstream Gradle module names. The upstream release and commit are pinned
in `upstream.lock`. License terms for the ported source are in `LICENSE.txt`.

The GPP compiler and Go 1.27 or newer are the build dependencies. The module
path is declared in this directory's `go.mod`.

```bash
./scripts/check.sh
```

The check compiles the port into a temporary Go module and tests it from a
separate module using stock Go. Set `GPP` to an absolute path if the compiler
is outside your PATH. The check works from a standalone copy of this project.

The Spring Core ASM and CGLIB metadata classes and Spring JDBC's
`TableParameterMetaData` are reproducible from the pinned Framework Java sources.
Build `:spring-core:compileJava` and `:spring-jdbc:compileJava` in the reference
checkout, then compare the translated sources with the port. Set `JAVA` to the
Java launcher from a JDK when it is outside your PATH.

```bash
./scripts/check-translated-classes.sh /path/to/spring-framework
```
