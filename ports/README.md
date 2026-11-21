# Spring port repositories

`spring-framework/` and `spring-boot/` are independent project roots. Each
owns its implementation, upstream revision, license, module identity, and
project-specific fixtures. Arrange new code within the matching upstream
module so source provenance follows the implementation during extraction.

Spring Boot uses the Framework port's public module path. Local testing can
point the Go toolchain at generated Framework packages through a workspace
or a temporary replacement. The Framework port has no Boot dependency.

The GPP repository owns the language compiler, Java importer, general
interoperability checks, and shared reference runner. The runner reads the
selected port's upstream lock and can take an independent project root
through `GPP_SPRING_PORT_ROOT`. Port implementation cannot import GPP's
private Go packages. Its generated Go packages use the port module path.
