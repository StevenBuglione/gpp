package interop

import (
	"errors"
	"strings"
)

var ErrLookup = errors.New("entry not found")

func NativeName(name string) string {
	return strings.TrimSpace(name)
}

func NativeLookup(name string) (string, error) {
	if name == "" {
		return "", ErrLookup
	}
	return NativeName(name), nil
}
