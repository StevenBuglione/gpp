package consumer

import "example.com/gpp-interop-library/interop"

type messageSource interface {
	Message() string
}

var (
	_ messageSource                       = (*interop.Entry)(nil)
	_ func(string) *interop.Entry         = interop.NewEntry
	_ func(*interop.Entry) *interop.Entry = interop.Identity
	_ func(string, ...string) []string    = interop.Collect[string]
	_ func(string) (string, error)        = interop.Lookup
)
