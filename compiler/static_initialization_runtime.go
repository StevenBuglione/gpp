package compiler

import "strings"

// The embedded Java runtime and native GPP share the initialization monitor.
// Native code supplies its package's exception ABI and import aliases.
func classInitializationRuntime(runtimeAlias, syncAlias, atomicAlias, typePrefix string) string {
	source, err := officialStdlib.ReadFile("stdlib/gpp/java/class_initialization.gpp")
	if err != nil {
		panic(err)
	}
	_, declarations, found := strings.Cut(string(source), "\ntype ClassInitialization struct")
	if !found {
		panic("class initialization runtime declarations are missing")
	}
	return strings.NewReplacer(
		"runtime.Stack", runtimeAlias+".Stack",
		"sync.Mutex", syncAlias+".Mutex",
		"atomic.Bool", atomicAlias+".Bool",
		"java.lang.ExceptionInInitializerError", "java.lang.ExceptionInInitializerError",
		"java.lang.NoClassDefFoundError", "java.lang.NoClassDefFoundError",
		"ClassInitialization", typePrefix+"ClassInitialization",
		"ExceptionInInitializerError", typePrefix+"ExceptionInInitializerError",
		"NoClassDefFoundError", typePrefix+"NoClassDefFoundError",
	).Replace("\ntype ClassInitialization struct" + declarations)
}
