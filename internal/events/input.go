package events

import (
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
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

// Event is a stub until the next commit.
func (f Form) Event(now time.Time, loc *time.Location, prev *domain.Event) (domain.Event, domain.FieldErrors) {
	return domain.Event{}, nil
}
