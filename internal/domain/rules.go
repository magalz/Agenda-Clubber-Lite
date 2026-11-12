package domain

import (
	"fmt"
	"maps"
	"net/mail"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// pt-BR messages shown next to a form field.
const (
	msgRequired      = "campo obrigatório"
	msgInvalid       = "valor inválido"
	msgNegative      = "não pode ser negativo"
	msgEmailRequired = "informe o e-mail"
	msgEmailInvalid  = "informe um e-mail válido"
	msgEmailForm     = "use o e-mail em minúsculas e sem espaços"
	msgNameKey       = "deve ser a chave do nome"
	msgAfterCreated  = "deve ser depois da criação"
	msgNewEmailOnly  = "só se aplica à troca de e-mail"
)

// FieldErrors maps a snake_case field key to a pt-BR message. Validate returns
// it, and NormalizeEmail returns it with the key "email".
type FieldErrors map[string]string

// Error lists the failing fields sorted by key, so the text is repeatable.
func (e FieldErrors) Error() string {
	if len(e) == 0 {
		return "domain: no field errors"
	}
	var b strings.Builder
	for i, field := range slices.Sorted(maps.Keys(e)) {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(field + ": " + e[field])
	}
	return b.String()
}

// Add records msg for field, replacing an earlier message for the same field.
func (e *FieldErrors) Add(field, msg string) {
	if *e == nil {
		*e = FieldErrors{}
	}
	(*e)[field] = msg
}

// addIf records msg for field unless msg is empty.
func (e *FieldErrors) addIf(field, msg string) {
	if msg != "" {
		e.Add(field, msg)
	}
}

// Err returns nil when no field failed, so a caller never sees an empty map
// wrapped in a non-nil error.
func (e FieldErrors) Err() error {
	if len(e) == 0 {
		return nil
	}
	return e
}

// lengthProblem checks the length in runes of s after trimming surrounding
// space. It returns the message for a value outside [min, max], or "".
func lengthProblem(s string, min, max int) string {
	n := utf8.RuneCountInString(strings.TrimSpace(s))
	switch {
	case n == 0 && min > 0:
		return msgRequired
	case n >= min && n <= max:
		return ""
	case min <= 1:
		return fmt.Sprintf("use no máximo %d caracteres", max)
	default:
		return fmt.Sprintf("use de %d a %d caracteres", min, max)
	}
}

func phoneProblem(s string) string {
	if len(s) < PhoneMin || len(s) > PhoneMax || strings.ContainsFunc(s, func(r rune) bool { return r < '0' || r > '9' }) {
		return fmt.Sprintf("informe de %d a %d dígitos", PhoneMin, PhoneMax)
	}
	return ""
}

// NormalizeEmail returns the canonical form of an email address: trimmed,
// lowercase, a bare addr-spec (no display name, comment or angle brackets), with
// a dotted domain and 3 to 254 characters. The error is a FieldErrors with the
// key "email".
func NormalizeEmail(s string) (string, error) {
	n, problem := normalizeEmail(s)
	if problem != "" {
		return "", FieldErrors{"email": problem}
	}
	return n, nil
}

func normalizeEmail(s string) (normalized, problem string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", msgEmailRequired
	}
	if strings.ContainsFunc(s, unicode.IsSpace) {
		return "", msgEmailInvalid
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Address != s {
		return "", msgEmailInvalid
	}
	domain := s[strings.LastIndexByte(s, '@')+1:]
	if !strings.Contains(domain, ".") || strings.HasPrefix(domain, "[") {
		return "", msgEmailInvalid
	}
	s = strings.ToLower(s)
	switch n := utf8.RuneCountInString(s); {
	case n > EmailMax:
		return "", fmt.Sprintf("use no máximo %d caracteres", EmailMax)
	case n < EmailMin:
		return "", msgEmailInvalid
	}
	return s, ""
}

// emailProblem checks an email address that is about to be stored. Space around
// it is always a problem. With exact, the address must also already be in the
// form NormalizeEmail returns; otherwise only its case may differ.
func emailProblem(s string, exact bool) string {
	n, problem := normalizeEmail(s)
	switch {
	case problem != "":
		return problem
	case exact && n != s, !exact && !strings.EqualFold(n, s):
		return msgEmailForm
	}
	return ""
}

// NameKey returns the comparison key of a collective name: lowercase, without
// Portuguese diacritics, with every run of whitespace reduced to one space and
// no space at the ends. Two names that differ only in case, accents or spacing
// share a key.
func NameKey(name string) string {
	return strings.Join(strings.Fields(strings.Map(foldRune, strings.ToLower(name))), " ")
}

// foldRune maps a lowercase Portuguese letter with a diacritic to its plain
// letter and drops combining marks, so a name typed in decomposed form (a base
// letter followed by U+0303) gets the same key as the precomposed one.
func foldRune(r rune) rune {
	switch r {
	case 'á', 'à', 'â', 'ã', 'ä':
		return 'a'
	case 'é', 'è', 'ê', 'ë':
		return 'e'
	case 'í', 'ì', 'î', 'ï':
		return 'i'
	case 'ó', 'ò', 'ô', 'õ', 'ö':
		return 'o'
	case 'ú', 'ù', 'û', 'ü':
		return 'u'
	case 'ç':
		return 'c'
	case 'ñ':
		return 'n'
	}
	if r >= 0x0300 && r <= 0x036F { // combining diacritical marks
		return -1
	}
	return r
}

// Validate checks the fields of a user. It does not check ID, which the backend
// assigns, nor the references to other users.
func (u User) Validate() error {
	var e FieldErrors
	e.addIf("name", lengthProblem(u.Name, UserNameMin, UserNameMax))
	e.addIf("email", emailProblem(u.Email, true))
	e.addIf("phone", phoneProblem(u.Phone))
	if !u.Status.Valid() {
		e.Add("status", msgInvalid)
	}
	if u.FailedLogins < 0 {
		e.Add("failed_logins", msgNegative)
	}
	if u.PasswordHash == "" {
		e.Add("password_hash", msgRequired)
	}
	return e.Err()
}

// Validate checks the fields of a collective. NameKey must be the key of Name.
func (c Collective) Validate() error {
	var e FieldErrors
	e.addIf("name", lengthProblem(c.Name, CollectiveNameMin, CollectiveNameMax))
	switch {
	case c.NameKey == "":
		e.Add("name_key", msgRequired)
	case c.NameKey != NameKey(c.Name):
		e.Add("name_key", msgNameKey)
	}
	if !c.Kind.Valid() {
		e.Add("kind", msgInvalid)
	}
	if !c.Status.Valid() {
		e.Add("status", msgInvalid)
	}
	return e.Err()
}

// Validate checks the fields of a membership.
func (m Membership) Validate() error {
	var e FieldErrors
	if m.UserID == "" {
		e.Add("user_id", msgRequired)
	}
	if m.CollectiveID == "" {
		e.Add("collective_id", msgRequired)
	}
	if !m.Role.Valid() {
		e.Add("role", msgInvalid)
	}
	if !m.Status.Valid() {
		e.Add("status", msgInvalid)
	}
	return e.Err()
}

// Validate checks the fields of a token. NewEmail is required, and normalized,
// exactly when the purpose is TokenPurposeChangeEmail. ExpiresAt must be after
// CreatedAt.
func (t Token) Validate() error {
	var e FieldErrors
	if t.UserID == "" {
		e.Add("user_id", msgRequired)
	}
	if !t.Purpose.Valid() {
		e.Add("purpose", msgInvalid)
	}
	if len(t.Hash) != TokenHashLen {
		e.Add("hash", fmt.Sprintf("deve ter %d bytes", TokenHashLen))
	}
	switch {
	case t.Purpose == TokenPurposeChangeEmail:
		e.addIf("new_email", emailProblem(t.NewEmail, true))
	case t.NewEmail != "":
		e.Add("new_email", msgNewEmailOnly)
	}
	if !t.ExpiresAt.After(t.CreatedAt) {
		e.Add("expires_at", msgAfterCreated)
	}
	return e.Err()
}

// Validate checks the fields of a session. ExpiresAt must be after CreatedAt.
func (s Session) Validate() error {
	var e FieldErrors
	if s.UserID == "" {
		e.Add("user_id", msgRequired)
	}
	if len(s.Hash) != TokenHashLen {
		e.Add("hash", fmt.Sprintf("deve ter %d bytes", TokenHashLen))
	}
	if !s.ExpiresAt.After(s.CreatedAt) {
		e.Add("expires_at", msgAfterCreated)
	}
	if utf8.RuneCountInString(s.UserAgent) > UserAgentMax {
		e.Add("user_agent", fmt.Sprintf("use no máximo %d caracteres", UserAgentMax))
	}
	return e.Err()
}

// Validate checks the fields of an outbox message. The recipient may be in any
// case but must not have surrounding space.
func (m OutboxMessage) Validate() error {
	var e FieldErrors
	e.addIf("to", emailProblem(m.To, false))
	e.addIf("subject", lengthProblem(m.Subject, 1, OutboxSubjectMax))
	if strings.TrimSpace(m.Text) == "" {
		e.Add("text", msgRequired)
	}
	if m.Attempts < 0 {
		e.Add("attempts", msgNegative)
	}
	return e.Err()
}
