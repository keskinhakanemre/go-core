package config

import "log/slog"

const redacted = "******"

// Secret is a string that masks itself when printed, logged or JSON encoded.
// Use it for passwords, tokens and DSNs in config structs so that logging the
// whole config (by accident) never leaks credentials. Call Value to read it.
type Secret string

// Value returns the plain secret.
func (s Secret) Value() string { return string(s) }

func (s Secret) String() string {
	if s == "" {
		return ""
	}
	return redacted
}

func (s Secret) GoString() string { return `config.Secret("` + s.String() + `")` }

func (s Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + s.String() + `"`), nil }

func (s Secret) LogValue() slog.Value { return slog.StringValue(s.String()) }
