package mongostore_test

import (
	"maps"
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/store/mongostore"
	"github.com/magalz/agenda-clubber-lite/internal/store/mongotest"
)

// The contract of the events repository is in the suite; these tests look at how
// this backend stores an event: the keys of the document, never a null or an
// empty string for a field that has no value.

var optionalEventFields = []string{"type_other", "ticket_url", "substyle", "style_other", "venue", "artists", "description"}

// eventsFixture is a store with the user and the collective an event needs.
func eventsFixture(t *testing.T) (s *mongostore.Store, ana, bia domain.User, c domain.Collective) {
	t.Helper()
	s = mongotest.New(t)
	repos := s.Repos()
	ana, bia = newUser("ana"), newUser("bia")
	c = domain.Collective{Name: "Bloco Luz", NameKey: "bloco luz", Kind: domain.CollectiveKindColetivo,
		Status: domain.CollectiveStatusApproved, CreatedAt: now, UpdatedAt: now}
	for _, u := range []*domain.User{&ana, &bia} {
		if err := repos.Users.Create(t.Context(), u); err != nil {
			t.Fatalf("create user %s: %v", u.Name, err)
		}
	}
	if err := repos.Collectives.Create(t.Context(), &c); err != nil {
		t.Fatalf("create collective: %v", err)
	}
	return s, ana, bia, c
}

func eventToStore(c domain.Collective, ana domain.User) domain.Event {
	return domain.Event{
		CollectiveID: c.ID, Name: "Noite Verde", Type: domain.EventTypeFesta, StartsAt: now.Add(48 * time.Hour),
		DurationMin: 240, PriceCents: 3000, Style: "Techno", State: "CE", City: "Fortaleza",
		CreatedBy: ana.ID, CreatedAt: now, UpdatedAt: now,
	}
}

func fillOptionalFields(ev *domain.Event) {
	ev.Type, ev.TypeOther = domain.EventTypeOutros, "Sarau"
	ev.TicketURL = "https://example.com/ingressos"
	ev.Substyle = "Minimal"
	ev.Venue, ev.Artists, ev.Description = "Casa da Música", "Ana Luz\nBia Sol", "Primeira linha.\nSegunda linha."
}

// rawEventDoc is the stored document of the event.
func rawEventDoc(t *testing.T, s *mongostore.Store, id string) bson.M {
	t.Helper()
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		t.Fatalf("event ID %q: %v", id, err)
	}
	var raw bson.M
	if err := s.DB().Collection("events").FindOne(t.Context(), bson.M{"_id": oid}).Decode(&raw); err != nil {
		t.Fatalf("read the event %s: %v", id, err)
	}
	return raw
}

// wantEventKeys requires that the document has exactly the required keys, the
// given extra ones and _id.
func wantEventKeys(t *testing.T, what string, raw bson.M, extra ...string) {
	t.Helper()
	want := slices.Sorted(slices.Values(slices.Concat([]string{"_id"}, requiredEventFields, extra)))
	if got := slices.Sorted(maps.Keys(raw)); !slices.Equal(got, want) {
		t.Errorf("%s: keys = %v, want %v", what, got, want)
	}
	for k, v := range raw {
		if v == nil || v == "" {
			t.Errorf("%s: %s is stored as %#v, want the key left out", what, k, v)
		}
	}
}

func TestEventCreateStoresOnlyTheFieldsThatHaveAValue(t *testing.T) {
	s, ana, bia, c := eventsFixture(t)
	repo := s.Repos().Events

	bare := eventToStore(c, ana)
	full := eventToStore(c, ana)
	fillOptionalFields(&full)
	full.UpdatedBy = bia.ID
	for _, ev := range []*domain.Event{&bare, &full} {
		if err := repo.Create(t.Context(), ev); err != nil {
			t.Fatalf("Create %s: %v", ev.Name, err)
		}
	}

	raw := rawEventDoc(t, s, bare.ID)
	wantEventKeys(t, "the event with no optional fields", raw, "created_by")
	if v, ok := raw["version"].(int64); !ok || v != 1 {
		t.Errorf("version = %#v, want int64(1)", raw["version"])
	}
	if oid, ok := raw["created_by"].(bson.ObjectID); !ok || oid.Hex() != ana.ID {
		t.Errorf("created_by = %#v, want the ObjectID of %s", raw["created_by"], ana.ID)
	}

	raw = rawEventDoc(t, s, full.ID)
	wantEventKeys(t, "the event with every optional field", raw, append([]string{"created_by", "updated_by"}, optionalEventFields...)...)
	if oid, ok := raw["updated_by"].(bson.ObjectID); !ok || oid.Hex() != bia.ID {
		t.Errorf("updated_by = %#v, want the ObjectID of %s", raw["updated_by"], bia.ID)
	}
}

func TestEventUpdateRemovesTheFieldsThatBecameEmpty(t *testing.T) {
	s, ana, bia, c := eventsFixture(t)
	repo := s.Repos().Events

	ev := eventToStore(c, ana)
	fillOptionalFields(&ev)
	ev.UpdatedBy = ana.ID
	if err := repo.Create(t.Context(), &ev); err != nil {
		t.Fatalf("Create: %v", err)
	}
	created := rawEventDoc(t, s, ev.ID)

	bare := ev
	bare.Type, bare.TypeOther = domain.EventTypeFesta, ""
	bare.TicketURL, bare.Substyle = "", ""
	bare.Venue, bare.Artists, bare.Description = "", "", ""
	bare.UpdatedBy, bare.UpdatedAt = bia.ID, now.Add(time.Hour)
	if err := repo.Update(t.Context(), bare); err != nil {
		t.Fatalf("Update to empty: %v", err)
	}
	raw := rawEventDoc(t, s, ev.ID)
	wantEventKeys(t, "the event after Update to empty", raw, "created_by", "updated_by")
	if v, ok := raw["version"].(int64); !ok || v != 2 {
		t.Errorf("version = %#v after one Update, want int64(2)", raw["version"])
	}
	if oid, ok := raw["updated_by"].(bson.ObjectID); !ok || oid.Hex() != bia.ID {
		t.Errorf("updated_by = %#v, want the ObjectID of %s", raw["updated_by"], bia.ID)
	}
	for _, k := range []string{"_id", "collective_id", "created_by", "created_at"} {
		if raw[k] != created[k] {
			t.Errorf("%s = %#v after Update, want %#v as before", k, raw[k], created[k])
		}
	}

	again := bare
	again.Version = 2
	fillOptionalFields(&again)
	if err := repo.Update(t.Context(), again); err != nil {
		t.Fatalf("Update to full: %v", err)
	}
	raw = rawEventDoc(t, s, ev.ID)
	wantEventKeys(t, "the event after Update to full", raw, append([]string{"created_by", "updated_by"}, optionalEventFields...)...)
	if v, ok := raw["version"].(int64); !ok || v != 3 {
		t.Errorf("version = %#v after two Updates, want int64(3)", raw["version"])
	}
}
