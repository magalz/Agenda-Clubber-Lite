package events

import (
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// formOfEvent is a stored event as the edit page receives it: past, after
// midnight, with every optional field set.
func formOfEvent() domain.Event {
	return domain.Event{
		ID:           "6735f0c2a1b2c3d4e5f60718",
		CollectiveID: "6735f0c2a1b2c3d4e5f60719",
		Name:         "Noite Verde",
		Type:         domain.EventTypeOutros,
		TypeOther:    "Sarau",
		StartsAt:     time.Date(2026, 11, 10, 2, 5, 0, 0, time.UTC), // 09/11/2026 23:05 in Fortaleza (past for testNow)
		DurationMin:  425,
		PriceCents:   123456,
		TicketURL:    "https://ingressos.example/noite",
		Style:        "techno",
		Substyle:     "acid techno",
		State:        "CE",
		City:         "Fortaleza",
		Venue:        "Galpão 3",
		Artists:      "DJ Um\nDJ Dois",
		Description:  "Primeira linha.\nSegunda linha.",
		CreatedBy:    "6735f0c2a1b2c3d4e5f60720",
		CreatedAt:    testNow.Add(-72 * time.Hour),
		Version:      4,
	}
}

func TestFormOfFormatsTheFields(t *testing.T) {
	f := FormOf(formOfEvent(), testLoc)
	want := Form{
		CollectiveID: "6735f0c2a1b2c3d4e5f60719",
		Name:         "Noite Verde",
		Type:         "outros",
		TypeOther:    "Sarau",
		Date:         "09/11/2026",
		Time:         "23:05",
		DurHours:     "7",
		DurMinutes:   "5",
		Price:        "1.234,56",
		TicketURL:    "https://ingressos.example/noite",
		Style:        "techno",
		Substyle:     "acid techno",
		State:        "CE",
		City:         "Fortaleza",
		Venue:        "Galpão 3",
		Artists:      "DJ Um\nDJ Dois",
		Description:  "Primeira linha.\nSegunda linha.",
		Version:      "4",
	}
	if f != want {
		t.Errorf("FormOf =\n%+v\nwant\n%+v", f, want)
	}
}

func TestFormOfFreeAndSmallPrices(t *testing.T) {
	for _, tc := range []struct {
		free  bool
		cents int64
		want  string
	}{
		{true, 0, ""},
		{false, 1, "0,01"},
		{false, 50, "0,50"},
		{false, 2500, "25,00"},
		{false, 9999999, "99.999,99"},
	} {
		e := formOfEvent()
		e.Free, e.PriceCents = tc.free, tc.cents
		f := FormOf(e, testLoc)
		if f.Free != tc.free || f.Price != tc.want {
			t.Errorf("FormOf(free %v, %d cents): Free %v, Price %q; want %v, %q", tc.free, tc.cents, f.Free, f.Price, tc.free, tc.want)
		}
	}
}

// TestFormOfRoundTrips checks that an unchanged edit form gives the event back:
// Form.Event(FormOf(e)) with prev e has no field errors and the same fields,
// even for a past event.
func TestFormOfRoundTrips(t *testing.T) {
	plain := formOfEvent()
	plain.Type, plain.TypeOther = domain.EventTypeFesta, ""
	plain.Style, plain.Substyle = domain.StyleOther, ""
	plain.StyleOther = "brega funk"
	plain.Free, plain.PriceCents = true, 0
	plain.TicketURL, plain.Venue, plain.Artists, plain.Description = "", "", "", ""

	for name, e := range map[string]domain.Event{"full": formOfEvent(), "plain": plain} {
		t.Run(name, func(t *testing.T) {
			got, fe := FormOf(e, testLoc).Event(testNow, testLoc, &e)
			if fe != nil {
				t.Fatalf("field errors %v", fe)
			}
			got.UpdatedBy, got.UpdatedAt = e.UpdatedBy, e.UpdatedAt
			if got != e {
				t.Errorf("round trip =\n%+v\nwant\n%+v", got, e)
			}
		})
	}
}
