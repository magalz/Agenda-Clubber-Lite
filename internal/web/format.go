package web

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// The pt-BR formatting the templates use. Every function takes a time.Time
// (the store keeps UTC) and shows it in loc, the configured APP_TZ. The names
// come from the tables below, not from time.Format, so the output does not
// depend on the operating system's locale.

// weekdaysPT is indexed by time.Weekday, which starts on Sunday.
var weekdaysPT = [...]string{
	"domingo", "segunda-feira", "terça-feira", "quarta-feira", "quinta-feira", "sexta-feira", "sábado",
}

// monthsPT is indexed by time.Month-1 and already uppercase, because the
// labels are uppercase text and not a CSS effect.
var monthsPT = [...]string{
	"JANEIRO", "FEVEREIRO", "MARÇO", "ABRIL", "MAIO", "JUNHO",
	"JULHO", "AGOSTO", "SETEMBRO", "OUTUBRO", "NOVEMBRO", "DEZEMBRO",
}

// fmtDate is "14/11/2026".
func fmtDate(t time.Time, loc *time.Location) string {
	return t.In(loc).Format("02/01/2006")
}

// fmtDateLong is "sábado, 14/11/2026".
func fmtDateLong(t time.Time, loc *time.Location) string {
	l := t.In(loc)
	return weekdaysPT[l.Weekday()] + ", " + l.Format("02/01/2006")
}

// fmtMonthYear is "NOVEMBRO 2026".
func fmtMonthYear(t time.Time, loc *time.Location) string {
	l := t.In(loc)
	return monthsPT[l.Month()-1] + " " + l.Format("2006")
}

// fmtClock is "22:00" (24 h).
func fmtClock(t time.Time, loc *time.Location) string {
	return t.In(loc).Format("15:04")
}

// fmtUntil is the end of an event that starts at start and lasts durationMin
// minutes: "até 23:30", or "até 06:00 (+1)" when the end falls on the next
// calendar day in loc. The domain keeps the duration under a day, so +1 is
// the most there is.
func fmtUntil(start time.Time, durationMin int, loc *time.Location) string {
	s := start.In(loc)
	end := s.Add(time.Duration(durationMin) * time.Minute)
	out := "até " + end.Format("15:04")
	sy, sm, sd := s.Date()
	if ey, em, ed := end.Date(); ey != sy || em != sm || ed != sd {
		out += " (+1)"
	}
	return out
}

// fmtDuration is "45min", "6h", "6h30" or "1h05".
func fmtDuration(minutes int) string {
	h, m := minutes/60, minutes%60
	switch {
	case h == 0:
		return fmt.Sprintf("%dmin", m)
	case m == 0:
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%02d", h, m)
}

// fmtPrice is "Free" for a free event, else reais with a dot for thousands
// and a comma for cents: "R$ 1.234,56".
func fmtPrice(free bool, cents int64) string {
	if free {
		return "Free"
	}
	reais := strconv.FormatInt(cents/100, 10)
	var b strings.Builder
	for i := 0; i < len(reais); i++ {
		if i > 0 && (len(reais)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteByte(reais[i])
	}
	return fmt.Sprintf("R$ %s,%02d", b.String(), cents%100)
}

// fmtEventType is the label of an event's type. Stub until written.
func fmtEventType(t domain.EventType, other string) string { return "" }

// fmtEventStyle is an event's style as shown. Stub until written.
func fmtEventStyle(style, substyle, other string) string { return "" }
