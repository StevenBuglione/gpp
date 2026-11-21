# Tooling

The `gpp` CLI treats compiler tooling as part of the language experience.

## Formatting

Format one file, a project, or standard input/output:

```bash
gpp fmt app.gpp
gpp fmt --check .
gpp fmt --stdout app.gpp
```

## Fixing imports

`gpp fiximports` adds missing imports, removes unused named imports, and resolves
package-name clashes when a unique available package matches a qualified name.
It prefers bundled GPP packages; if both GPP and standard-library packages with
the same package name are used, the standard package gets a `std`-prefixed alias
such as `stdhttp`. It does not change dependencies or guess when multiple
packages at the same priority match.

```sh
gpp fiximports app.gpp
gpp fiximports --check ./...
gpp fiximports --stdout app.gpp
```

Formatting is deterministic, comment-preserving, and shared with the language
server. `--check` is suitable for CI.

## Testing

GPP tests use the bundled `gpp/test` library and ordinary Go test execution:

```bash
gpp test examples/testing.gpp
gpp test --tag crud examples/testing.gpp
gpp test --priority high examples/testing.gpp
```

## Source documentation

Inspect source-level declarations without browsing generated Go:

```bash
gpp doc gpp/http.Server
gpp doc string.TrimSpace
gpp doc --json gpp/http.Server
gpp doc --search template
```

## Language server

Start the LSP server over standard input/output:

```bash
gpp lsp
gpp lsp --log=/tmp/gpp-lsp.log
```

The server supports diagnostics, completion, hover, navigation, references,
rename, symbols, formatting, and signature help. Open-document overlays stay
in memory, so editor feedback does not require a Go backend build on every
change.

## Visual Studio Code syntax highlighting

For GPP syntax highlighting in Visual Studio Code, see the
[GPP VS Code extension](https://github.com/telgatech/gpp-vscode).

## Diagnostics

When compilation fails, the compiler should point to the offending line in the
original `.gpp` source, including when the error comes from generated Go. You
should not have to track down the corresponding generated file yourself. If a
diagnostic points to the wrong line or only points into generated Go, please
raise a ticket with the source and full error output so it can be fixed.

## Java migration analysis

Use a JDK that supports the Java source release you are analyzing.

```bash
gpp migrate java --analyze --release 17 --class-path "$COMPILE_CLASSPATH" src/main/java
```

The versioned JSON report records source declarations, javac-selected call
targets, JPMS directives, typed declaration annotations, and source diagnostics.
It includes annotation retention and default values, resolved type references,
and cycles among the selected source packages. Source offsets use UTF-16 code units.
The command exits with status 1 when javac reports source errors. It only
reads analysis inputs and consumes generated Java sources from your build.

For the pinned Spring builds, set `JAVA_HOME` to JDK 25 and capture the
Java 17 source sets and classpaths from Gradle before running the analyzer.

```bash
./scripts/check-spring-reference.sh framework /tmp/gpp-spring-framework
./scripts/check-spring-reference.sh boot /tmp/gpp-spring-boot
gpp migrate java --analyze --java "$JAVA_HOME/bin/java" \
  --inputs /tmp/gpp-spring-framework/spring-core/build/gpp-reference/compileJava.json
```

Each checkout's Gradle wrapper compiles the reference modules and prints
the manifest paths it writes. Use a fresh destination or reuse a clean
checkout at the script's pinned revision.
Pass `--inventory` as the third script argument to list the complete
configured Gradle project graph and its source sets, including parent
projects and included builds. The manifest is `build/gpp-reference/projects.json`.
Pass `--all-java` to compile the main and multi-release Java source sets in
every project that supplies them, and export their compile manifests.

Verify the upstream runtime contracts for annotation aliases, generic
injection, bean identity, optional classes, configuration precedence, and
rollback rules with the pinned Boot runtime classpath.

```bash
./scripts/check-spring-reference.sh boot /tmp/gpp-spring-boot --boot-probe
```

Use `--emit-gpp` to translate public standalone Java interfaces with
primitive method signatures, integer or boolean constants, and inheritance
within the selected package. Generated Go marker methods preserve explicit
interface membership for derived Java interfaces. The emitter
preserves Java numeric widths and emits normal Go-compatible method sets.
Unsupported declarations produce source diagnostics before any output.

```bash
gpp migrate java --emit-gpp --release 17 /path/to/Ordered.java /path/to/PriorityOrdered.java
```

The emitter also translates primitive cast methods from public final utility
classes with private empty constructors. The methods take primitive arguments
and return a parameter, cast, or shift expression. Generated function names combine
the Java class and method names. Floating-point narrowing follows Java's
saturation and NaN rules through the bundled `gpp/java` package.

For public classes with primitive or Java `String` fields and method signatures, the emitter
also supports same-package single inheritance, constructor chains, instance
initialization and virtual calls during construction. It emits Go constructors
such as `NewChild()` and `NewChildWithInt(int32)`. Go callers can use exported
methods directly or request an ancestor view such as `child.GppAsBase()`.

Java `String` becomes a `*java.String` from the generated module's `gpp/java`
package. A nil pointer represents Java null. Use `StringFromGo` to decode Go
UTF-8 and `ToGo` to decode a Java name and check for null. Use `StringFromUTF16`
and `UTF16` when every code unit must survive, including unpaired surrogates.
Literals and explicit interning share references within the generated module.

## Developing this documentation site

Install the documentation dependencies and start VitePress:

```bash
npm install
npm run docs:dev
```

Build and preview the static site:

```bash
npm run docs:build
npm run docs:preview
```

The `docs:sync` step mirrors every Markdown file in `spec/` into the site’s
reference section before development and production builds.
