package domain

import (
	"fmt"
	"maps"
	"net/mail"
	"net/url"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MsgRequired and MsgInvalid are the pt-BR messages for a missing and a rejected
// value. A store that raises a FieldErrors of its own uses them, so every
// backend words these two the same way as Validate. MsgControl is the one for a
// name that holds a line break or another control character.
const (
	MsgRequired = "campo obrigatório"
	MsgInvalid  = "valor inválido"
	MsgControl  = "não use quebras de linha nem caracteres de controle"
)

// More pt-BR messages shown next to a form field.
const (
	msgNegative      = "não pode ser negativo"
	msgEmailRequired = "informe o e-mail"
	msgEmailInvalid  = "informe um e-mail válido"
	msgEmailForm     = "use o e-mail em minúsculas e sem espaços"
	msgNameKey       = "deve ser a chave do nome"
	msgAfterCreated  = "deve ser depois da criação"
	msgNewEmailOnly  = "só se aplica à troca de e-mail"

	msgTextControl    = "use só texto, sem caracteres de controle"
	msgTypeOtherOnly  = "só se aplica ao tipo outros"
	msgStyleOtherOnly = "só se aplica ao estilo outros"
	msgSubstyleOutros = "não se aplica ao estilo outros"
	msgFreePrice      = "um evento gratuito não tem preço"
	msgTicketURL      = "informe um endereço que comece com http:// ou https://"
	msgState          = "use as duas letras da UF, em maiúsculas"
)

// MsgPriceRange is the pt-BR message for a price outside R$ 0,01 to
// EventPriceMax, shared by Event.Validate and the event form.
var MsgPriceRange = fmt.Sprintf("informe um preço de R$ 0,01 a R$ %d,%02d", EventPriceMax/100, EventPriceMax%100)

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

// AddIf records msg for field unless msg is empty, so a rule's problem text can
// be passed straight in.
func (e *FieldErrors) AddIf(field, msg string) {
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
		return MsgRequired
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

// CleanCollectiveName is the rule for a collective name typed in a form, the
// same for sign-up, a collective request and a rename. It trims the name and
// checks, in this order: valid UTF-8, no control character (a line break
// included), a length of CollectiveNameMin to CollectiveNameMax runes, and a
// NameKey that is not empty (a name of only combining marks has none). It
// returns the cleaned name and "", or "" and the pt-BR message for the field.
// What is inside the name is kept as typed.
func CleanCollectiveName(name string) (cleaned, problem string) {
	name = strings.TrimSpace(name)
	if msg := TextProblem(name, CollectiveNameMin, CollectiveNameMax, false); msg != "" {
		return "", msg
	}
	if NameKey(name) == "" {
		return "", MsgInvalid
	}
	return name, ""
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
	e.AddIf("name", lengthProblem(u.Name, UserNameMin, UserNameMax))
	e.AddIf("email", emailProblem(u.Email, true))
	e.AddIf("phone", phoneProblem(u.Phone))
	if !u.Status.Valid() {
		e.Add("status", MsgInvalid)
	}
	if u.FailedLogins < 0 {
		e.Add("failed_logins", msgNegative)
	}
	if u.PasswordHash == "" {
		e.Add("password_hash", MsgRequired)
	}
	return e.Err()
}

// Validate checks the fields of a collective. NameKey must be the key of Name.
func (c Collective) Validate() error {
	var e FieldErrors
	e.AddIf("name", lengthProblem(c.Name, CollectiveNameMin, CollectiveNameMax))
	switch {
	case c.NameKey == "":
		e.Add("name_key", MsgRequired)
	case c.NameKey != NameKey(c.Name):
		e.Add("name_key", msgNameKey)
	}
	if !c.Kind.Valid() {
		e.Add("kind", MsgInvalid)
	}
	if !c.Status.Valid() {
		e.Add("status", MsgInvalid)
	}
	return e.Err()
}

// Validate checks the fields of a membership.
func (m Membership) Validate() error {
	var e FieldErrors
	if m.UserID == "" {
		e.Add("user_id", MsgRequired)
	}
	if m.CollectiveID == "" {
		e.Add("collective_id", MsgRequired)
	}
	if !m.Role.Valid() {
		e.Add("role", MsgInvalid)
	}
	if !m.Status.Valid() {
		e.Add("status", MsgInvalid)
	}
	return e.Err()
}

// Validate checks the fields of a token. NewEmail is required, and normalized,
// exactly when the purpose is TokenPurposeChangeEmail. ExpiresAt must be after
// CreatedAt.
func (t Token) Validate() error {
	var e FieldErrors
	if t.UserID == "" {
		e.Add("user_id", MsgRequired)
	}
	if !t.Purpose.Valid() {
		e.Add("purpose", MsgInvalid)
	}
	if len(t.Hash) != TokenHashLen {
		e.Add("hash", fmt.Sprintf("deve ter %d bytes", TokenHashLen))
	}
	switch {
	case t.Purpose == TokenPurposeChangeEmail:
		e.AddIf("new_email", emailProblem(t.NewEmail, true))
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
		e.Add("user_id", MsgRequired)
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
	e.AddIf("to", emailProblem(m.To, false))
	e.AddIf("subject", lengthProblem(m.Subject, 1, OutboxSubjectMax))
	if strings.TrimSpace(m.Text) == "" {
		e.Add("text", MsgRequired)
	}
	if m.Attempts < 0 {
		e.Add("attempts", msgNegative)
	}
	return e.Err()
}

// Validate checks the fields of an audit entry. It does not check ID, which the
// backend assigns, nor the IDs of the user and of the collective, which are
// snapshots.
func (e AuditEntry) Validate() error {
	var f FieldErrors
	if !e.Action.Valid() {
		f.Add("action", MsgInvalid)
	}
	if e.At.IsZero() {
		f.Add("at", MsgRequired)
	}
	if e.ActorID == "" {
		f.Add("actor_id", MsgRequired)
	}
	f.AddIf("actor_name", lengthProblem(e.ActorName, 0, UserNameMax))
	f.AddIf("user_name", lengthProblem(e.UserName, 0, UserNameMax))
	f.AddIf("collective_name", lengthProblem(e.CollectiveName, 0, CollectiveNameMax))
	f.AddIf("detail", lengthProblem(e.Detail, 0, AuditTextMax))
	f.AddIf("reason", lengthProblem(e.Reason, 0, AuditTextMax))
	return f.Err()
}

// TextProblem checks free text typed in a form: valid UTF-8 (MsgInvalid), no
// control character, and a length of min to max runes after trimming surrounding
// space (see lengthProblem; a min of 0 makes the text optional). It returns the
// pt-BR message for the field, or "". A single-line text may hold no control
// character at all (MsgControl); a multi-line one may hold line feeds and tabs
// (msgTextControl for any other). The control check runs on s as given, so a
// line break at the end is refused in a single line even though the length
// ignores it. A carriage return is a control character: a caller that takes text
// from a browser textarea turns CRLF into LF first.
func TextProblem(s string, min, max int, multiline bool) string {
	switch {
	case !utf8.ValidString(s):
		return MsgInvalid
	case multiline && strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }):
		return msgTextControl
	case !multiline && strings.ContainsFunc(s, unicode.IsControl):
		return MsgControl
	}
	return lengthProblem(s, min, max)
}

// otherProblem checks the text that goes with a choice of "outros": it is
// required, and a single line, when chosen, and must be empty when not.
// msgUnused is the message for text with no choice of "outros" to explain.
func otherProblem(chosen bool, s, msgUnused string) string {
	switch {
	case chosen:
		return TextProblem(s, 1, EventOtherMax, false)
	case s != "":
		return msgUnused
	}
	return ""
}

// TicketURLProblem accepts no link, or an absolute http or https one with a host
// and no whitespace anywhere. It is the one rule of the ticket link: Event.Validate
// applies it to what is stored and the event form to what is typed (already
// trimmed there).
func TicketURLProblem(s string) string {
	if s == "" {
		return ""
	}
	if msg := TextProblem(s, 0, EventURLMax, false); msg != "" {
		return msg
	}
	if strings.ContainsFunc(s, unicode.IsSpace) {
		return msgTicketURL
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return msgTicketURL
	}
	return ""
}

// stateProblem accepts exactly two ASCII uppercase letters, the UF of a state.
func stateProblem(s string) string {
	if len(s) != 2 || strings.ContainsFunc(s, func(r rune) bool { return r < 'A' || r > 'Z' }) {
		return msgState
	}
	return ""
}

// Validate checks the shape of the fields of an event: what the data format
// needs, not what the product allows. It does not check ID, which the backend
// assigns, the references (CollectiveID, CreatedBy, UpdatedBy), which the
// backend checks against the rows, nor Version and UpdatedAt, which the backend
// sets. The catalog of styles, the rule that a new event is not in the past and
// who may write an event belong to the event service.
func (e Event) Validate() error {
	var f FieldErrors
	if e.CollectiveID == "" {
		f.Add("collective_id", MsgRequired)
	}
	f.AddIf("name", TextProblem(e.Name, EventNameMin, EventNameMax, false))
	if !e.Type.Valid() {
		f.Add("type", MsgInvalid)
	}
	f.AddIf("type_other", otherProblem(e.Type == EventTypeOutros, e.TypeOther, msgTypeOtherOnly))
	if e.StartsAt.IsZero() {
		f.Add("starts_at", MsgRequired)
	}
	if e.DurationMin < EventDurationMin || e.DurationMin > EventDurationMax {
		f.Add("duration_min", fmt.Sprintf("use de %d a %d minutos", EventDurationMin, EventDurationMax))
	}
	switch {
	case e.Free && e.PriceCents != 0:
		f.Add("price_cents", msgFreePrice)
	case !e.Free && (e.PriceCents < 1 || e.PriceCents > EventPriceMax):
		f.Add("price_cents", MsgPriceRange)
	}
	f.AddIf("ticket_url", TicketURLProblem(e.TicketURL))
	f.AddIf("style", TextProblem(e.Style, 1, EventStyleMax, false))
	if e.Style == StyleOther && e.Substyle != "" {
		f.Add("substyle", msgSubstyleOutros)
	} else {
		f.AddIf("substyle", TextProblem(e.Substyle, 0, EventStyleMax, false))
	}
	f.AddIf("style_other", otherProblem(e.Style == StyleOther, e.StyleOther, msgStyleOtherOnly))
	f.AddIf("state", stateProblem(e.State))
	f.AddIf("city", TextProblem(e.City, 1, EventCityMax, false))
	f.AddIf("venue", TextProblem(e.Venue, 0, EventVenueMax, false))
	f.AddIf("artists", TextProblem(e.Artists, 0, EventArtistsMax, true))
	f.AddIf("description", TextProblem(e.Description, 0, EventDescriptionMax, true))
	if e.CreatedAt.IsZero() {
		f.Add("created_at", MsgRequired)
	}
	return f.Err()
}
