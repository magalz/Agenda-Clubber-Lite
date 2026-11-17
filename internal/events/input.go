package events

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/magalz/agenda-clubber-lite/internal/catalog"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// pt-BR messages for the event form. The required, invalid and control-character
// ones come from internal/domain.
const (
	msgDate        = "use uma data válida, no formato dd/mm/aaaa"
	msgDatePast    = "a data não pode ser anterior a hoje"
	msgTime        = "use uma hora válida, no formato HH:MM"
	msgDuration    = "use horas de 0 a 23 e minutos de 0 a 59"
	msgPrice       = "use um preço como 25, 25,50 ou R$ 1.234,56"
	msgTicketURL   = "informe um endereço que comece com http:// ou https://"
	msgChooseState = "escolha o estado"
	msgChooseCity  = "escolha a cidade"
	msgStateFirst  = "escolha primeiro o estado e depois a cidade"
)

var (
	msgDurationRange = fmt.Sprintf("informe uma duração de %d minutos a %dh%02d", domain.EventDurationMin, domain.EventDurationMax/60, domain.EventDurationMax%60)
	msgPriceRange    = fmt.Sprintf("informe um preço de R$ 0,01 a R$ %d,%02d", domain.EventPriceMax/100, domain.EventPriceMax%100)
)

// Form holds the event form's fields as posted; the comment after each field is
// its form field name, which is also its key in the FieldErrors Event returns.
type Form struct {
	CollectiveID string // collective_id
	Name         string // name
	Type         string // type
	TypeOther    string // type_other
	Date         string // date: dd/mm/aaaa or AAAA-MM-DD
	Time         string // time: HH:MM (H:MM accepted)
	DurHours     string // duration_hours
	DurMinutes   string // duration_minutes (errors of both under the key "duration")
	Price        string // price
	Free         bool   // free
	TicketURL    string // ticket_url
	Style        string // style
	Substyle     string // substyle
	StyleOther   string // style_other
	State        string // state
	City         string // city
	Venue        string // venue
	Artists      string // artists
	Description  string // description
	Version      string // version (hidden; editing only)
}

// Event turns the form into an event, or says what is wrong with it. The event
// is meaningful only when the FieldErrors is nil; it then passes
// domain.Event.Validate, because every rule of Validate is checked here first,
// under the form's keys.
//
// It sets the fields the form controls: CollectiveID (creating only), Name, Type,
// TypeOther, StartsAt (in UTC), DurationMin, Free, PriceCents, TicketURL, Style,
// Substyle, StyleOther, State, City, Venue, Artists and Description. A TypeOther,
// StyleOther or Substyle that its type or style does not use is dropped. Editing
// (prev not nil) it also sets Version from the form and copies from prev what the
// form never changes: ID, CollectiveID (the form's collective_id is ignored),
// CreatedBy and CreatedAt. UpdatedBy and UpdatedAt are the service's to set.
//
// A date and a time are read in loc. Creating, the date must be today (in loc) or
// later; editing, it may also stay the date prev already had, past or not. Only
// the date matters, not the time of day.
func (f Form) Event(now time.Time, loc *time.Location, prev *domain.Event) (domain.Event, domain.FieldErrors) {
	var fe domain.FieldErrors
	e := domain.Event{Free: f.Free}

	if prev != nil {
		e.ID, e.CollectiveID, e.CreatedBy, e.CreatedAt = prev.ID, prev.CollectiveID, prev.CreatedBy, prev.CreatedAt
		if v, ok := parseVersion(f.Version); ok {
			e.Version = v
		} else {
			fe.Add("version", domain.MsgInvalid)
		}
	} else if e.CollectiveID = strings.TrimSpace(f.CollectiveID); e.CollectiveID == "" {
		fe.Add("collective_id", domain.MsgRequired)
	}

	f.identify(&e, &fe)
	f.schedule(&e, &fe, now, loc, prev)
	f.cost(&e, &fe)
	f.genre(&e, &fe)
	f.place(&e, &fe)
	f.details(&e, &fe)
	return e, fe
}

// identify reads name, type and type_other.
func (f Form) identify(e *domain.Event, fe *domain.FieldErrors) {
	e.Name = strings.TrimSpace(f.Name)
	addIf(fe, "name", domain.TextProblem(e.Name, domain.EventNameMin, domain.EventNameMax, false))

	typ := domain.EventType(strings.TrimSpace(f.Type))
	switch {
	case typ == "":
		fe.Add("type", domain.MsgRequired)
	case !typ.Valid():
		fe.Add("type", domain.MsgInvalid)
	default:
		e.Type = typ
		if typ == domain.EventTypeOutros {
			e.TypeOther = strings.TrimSpace(f.TypeOther)
			addIf(fe, "type_other", domain.TextProblem(e.TypeOther, 1, domain.EventOtherMax, false))
		}
	}
}

// schedule reads date, time and the duration, and applies the date rule.
func (f Form) schedule(e *domain.Event, fe *domain.FieldErrors, now time.Time, loc *time.Location, prev *domain.Event) {
	dateText := strings.TrimSpace(f.Date)
	date, dateOK := parseDate(dateText) // midnight UTC of the form's calendar date
	switch {
	case dateText == "":
		fe.Add("date", domain.MsgRequired)
	case !dateOK:
		fe.Add("date", msgDate)
	case date.Before(civilDate(now.In(loc))) && (prev == nil || !date.Equal(civilDate(prev.StartsAt.In(loc)))):
		fe.Add("date", msgDatePast)
	}

	clock := strings.TrimSpace(f.Time)
	hour, minute, clockOK := parseClock(clock)
	switch {
	case clock == "":
		fe.Add("time", domain.MsgRequired)
	case !clockOK:
		fe.Add("time", msgTime)
	}
	if dateOK && clockOK {
		e.StartsAt = time.Date(date.Year(), date.Month(), date.Day(), hour, minute, 0, 0, loc).UTC()
	}

	hours, minutes := strings.TrimSpace(f.DurHours), strings.TrimSpace(f.DurMinutes)
	h, hOK := parseBounded(hours, 23)
	m, mOK := parseBounded(minutes, 59)
	switch total := h*60 + m; {
	case hours == "" && minutes == "":
		fe.Add("duration", domain.MsgRequired)
	case !hOK || !mOK:
		fe.Add("duration", msgDuration)
	case total < domain.EventDurationMin || total > domain.EventDurationMax:
		fe.Add("duration", msgDurationRange)
	default:
		e.DurationMin = total
	}
}

// cost reads price and ticket_url. A free event has no price, whatever the price
// field holds.
func (f Form) cost(e *domain.Event, fe *domain.FieldErrors) {
	if !f.Free {
		cents, msg := parsePrice(f.Price)
		addIf(fe, "price", msg)
		e.PriceCents = cents
	}

	e.TicketURL = strings.TrimSpace(f.TicketURL)
	addIf(fe, "ticket_url", ticketURLProblem(e.TicketURL))
}

// genre reads style, substyle and style_other.
func (f Form) genre(e *domain.Event, fe *domain.FieldErrors) {
	style, sub := strings.TrimSpace(f.Style), strings.TrimSpace(f.Substyle)
	switch {
	case style == "":
		fe.Add("style", domain.MsgRequired)
	case !catalog.ValidStyle(style, ""):
		fe.Add("style", domain.MsgInvalid)
	case style == domain.StyleOther:
		e.Style = style
		e.StyleOther = strings.TrimSpace(f.StyleOther)
		addIf(fe, "style_other", domain.TextProblem(e.StyleOther, 1, domain.EventOtherMax, false))
	default:
		e.Style, e.Substyle = style, sub
		if !catalog.ValidStyle(style, sub) {
			fe.Add("substyle", domain.MsgInvalid)
		}
	}
}

// place reads state, city and venue: the state must be a UF and the city one of
// its cities.
func (f Form) place(e *domain.Event, fe *domain.FieldErrors) {
	state, city := strings.TrimSpace(f.State), strings.TrimSpace(f.City)
	_, stateOK := catalog.StateName(state)
	if stateOK {
		e.State = state
	} else {
		fe.Add("state", msgChooseState)
	}
	switch {
	case city == "":
		fe.Add("city", msgChooseCity)
	case !stateOK:
		fe.Add("city", msgStateFirst)
	case !catalog.ValidCity(state, city):
		fe.Add("city", fmt.Sprintf("escolha uma cidade de %s da lista", state))
	default:
		e.City = city
	}

	e.Venue = strings.TrimSpace(f.Venue)
	addIf(fe, "venue", domain.TextProblem(e.Venue, 0, domain.EventVenueMax, false))
}

// details reads artists and description, which keep their line breaks.
func (f Form) details(e *domain.Event, fe *domain.FieldErrors) {
	e.Artists = multiline(f.Artists)
	addIf(fe, "artists", domain.TextProblem(e.Artists, 0, domain.EventArtistsMax, true))
	e.Description = multiline(f.Description)
	addIf(fe, "description", domain.TextProblem(e.Description, 0, domain.EventDescriptionMax, true))
}

// addIf records msg for key unless msg is empty.
func addIf(fe *domain.FieldErrors, key, msg string) {
	if msg != "" {
		fe.Add(key, msg)
	}
}

// multiline turns the CRLF and the lone CR a browser textarea posts into LF, then
// trims the text: domain.TextProblem refuses a carriage return.
func multiline(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.TrimSpace(strings.ReplaceAll(s, "\r", "\n"))
}

// civilDate is t's calendar date in its own zone as midnight UTC, so two dates
// compare with Before and Equal.
func civilDate(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// digits reports whether s is one or more ASCII digits, nothing else (strconv
// accepts a sign and the like).
func digits(s string) bool {
	return s != "" && !strings.ContainsFunc(s, func(r rune) bool { return r < '0' || r > '9' })
}

// parseDate reads dd/mm/aaaa (d/m/aaaa too) or AAAA-MM-DD, a real calendar date,
// and returns it as midnight UTC.
func parseDate(s string) (time.Time, bool) {
	for _, layout := range []string{"2/1/2006", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// parseClock reads HH:MM or H:MM, 00:00 to 23:59.
func parseClock(s string) (hour, minute int, ok bool) {
	t, err := time.Parse("15:04", s)
	return t.Hour(), t.Minute(), err == nil
}

// parseBounded reads a count of 0 to max; an empty one counts as 0.
func parseBounded(s string, max int) (int, bool) {
	if s == "" {
		return 0, true
	}
	if len(s) > 2 || !digits(s) {
		return 0, false
	}
	n, _ := strconv.Atoi(s) // one or two digits
	return n, n <= max
}

// parseVersion reads a positive integer.
func parseVersion(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if !digits(s) {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 64)
	return v, err == nil && v > 0
}

// parsePrice reads a price in reais, "R$ 1.234,56" style, and returns cents in
// 1 to domain.EventPriceMax, or the pt-BR message for the field. It accepts an
// optional "R$" and spaces, digits with optional dots as thousands separators (in
// groups of three), and an optional comma with one or two decimal digits. Dots
// that are not thousands separators ("25.50") and commas that are not the
// decimal mark ("1,234.56") are refused, not guessed.
func parsePrice(s string) (int64, string) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "R$"))
	if s == "" {
		return 0, domain.MsgRequired
	}
	whole, frac, hasFrac := strings.Cut(s, ",")
	if !groupedDigits(whole) || (hasFrac && (len(frac) > 2 || !digits(frac))) {
		return 0, msgPrice
	}
	reais, err := strconv.ParseInt(strings.ReplaceAll(whole, ".", ""), 10, 64)
	if err != nil || reais > domain.EventPriceMax/100 { // only digits were left, so err is an overflow
		return 0, msgPriceRange
	}
	cents := reais * 100
	if hasFrac {
		n, _ := strconv.Atoi(frac) // one or two digits
		if len(frac) == 1 {
			n *= 10
		}
		cents += int64(n)
	}
	if cents < 1 || cents > domain.EventPriceMax {
		return 0, msgPriceRange
	}
	return cents, ""
}

// groupedDigits reports whether s is plain digits, or digits with a dot between
// groups of three after a first group of one to three.
func groupedDigits(s string) bool {
	if !strings.Contains(s, ".") {
		return digits(s)
	}
	for i, group := range strings.Split(s, ".") {
		if !digits(group) || (i == 0 && len(group) > 3) || (i > 0 && len(group) != 3) {
			return false
		}
	}
	return true
}

// ticketURLProblem accepts no link, or an absolute http or https one with a host
// and no whitespace. s is already trimmed.
func ticketURLProblem(s string) string {
	if s == "" {
		return ""
	}
	if msg := domain.TextProblem(s, 0, domain.EventURLMax, false); msg != "" {
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

// FormOf fills the form with e, as the edit page shows it before the person
// changes anything: the date as dd/mm/aaaa and the time as HH:MM in loc, the
// duration in hours and minutes, the price as "1.234,56" (empty when the event
// is free) and the version. It is the inverse of Event: with prev = &e,
// FormOf(e, loc).Event gives e's fields back, a past date included.
func FormOf(e domain.Event, loc *time.Location) Form {
	start := e.StartsAt.In(loc)
	f := Form{
		CollectiveID: e.CollectiveID,
		Name:         e.Name,
		Type:         string(e.Type),
		TypeOther:    e.TypeOther,
		Date:         start.Format("02/01/2006"),
		Time:         start.Format("15:04"),
		DurHours:     strconv.Itoa(e.DurationMin / 60),
		DurMinutes:   strconv.Itoa(e.DurationMin % 60),
		Free:         e.Free,
		TicketURL:    e.TicketURL,
		Style:        e.Style,
		Substyle:     e.Substyle,
		StyleOther:   e.StyleOther,
		State:        e.State,
		City:         e.City,
		Venue:        e.Venue,
		Artists:      e.Artists,
		Description:  e.Description,
		Version:      strconv.FormatInt(e.Version, 10),
	}
	if !e.Free {
		f.Price = formatCents(e.PriceCents)
	}
	return f
}

// formatCents writes a price in cents the way the price field reads it back:
// "1.234,56", "0,50".
func formatCents(cents int64) string {
	reais := strconv.FormatInt(cents/100, 10)
	var b strings.Builder
	for i, r := range reais {
		if i > 0 && (len(reais)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	return fmt.Sprintf("%s,%02d", b.String(), cents%100)
}
