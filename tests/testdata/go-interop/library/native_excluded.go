//go:build gpp_interop_excluded

package interop

func NativeName(name string) string {
	return "excluded: " + name
}
