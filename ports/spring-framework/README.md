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
