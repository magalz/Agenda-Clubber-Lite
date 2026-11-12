package domain

// FieldErrors maps a snake_case field key to a pt-BR message.
type FieldErrors map[string]string

func (e FieldErrors) Error() string { return "" }

// Add records msg for field.
func (e *FieldErrors) Add(field, msg string) {}

// Err returns nil when no field failed, so a caller never sees an empty map
// wrapped in a non-nil error.
func (e FieldErrors) Err() error { return nil }

// NormalizeEmail returns the canonical form of an email address.
func NormalizeEmail(s string) (string, error) { return "", nil }

// NameKey returns the comparison key of a collective name.
func NameKey(name string) string { return "" }

func (u User) Validate() error          { return nil }
func (c Collective) Validate() error    { return nil }
func (m Membership) Validate() error    { return nil }
func (t Token) Validate() error         { return nil }
func (s Session) Validate() error       { return nil }
func (m OutboxMessage) Validate() error { return nil }
