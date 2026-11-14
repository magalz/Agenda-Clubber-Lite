package mongostore_test

import (
	"errors"
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

// The optional fields of an event. Substyle goes with a catalog style and
// StyleOther with the style "Outros", never both, so there are two full sets.
var (
	catalogStyleFields = []string{"type_other", "ticket_url", "substyle", "venue", "artists", "description"}
	otherStyleFields   = []string{"type_other", "ticket_url", "style_other", "venue", "artists", "description"}
)

// useOtherStyle switches the event to the style "Outros" with its own name.
func useOtherStyle(ev *domain.Event) {
	ev.Style, ev.Substyle, ev.StyleOther = domain.StyleOther, "", "Brega funk"
}

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
	other := full
	useOtherStyle(&other)
	for _, ev := range []*domain.Event{&bare, &full, &other} {
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
	wantEventKeys(t, "the event with a catalog style and every optional field", raw, append([]string{"created_by", "updated_by"}, catalogStyleFields...)...)
	if oid, ok := raw["updated_by"].(bson.ObjectID); !ok || oid.Hex() != bia.ID {
		t.Errorf("updated_by = %#v, want the ObjectID of %s", raw["updated_by"], bia.ID)
	}

	raw = rawEventDoc(t, s, other.ID)
	wantEventKeys(t, "the event with the style Outros and every optional field", raw, append([]string{"created_by", "updated_by"}, otherStyleFields...)...)
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
	wantEventKeys(t, "the event after Update to full", raw, append([]string{"created_by", "updated_by"}, catalogStyleFields...)...)
	if v, ok := raw["version"].(int64); !ok || v != 3 {
		t.Errorf("version = %#v after two Updates, want int64(3)", raw["version"])
	}

	// The style Outros swaps substyle for style_other: the old key goes away.
	swapped := again
	swapped.Version = 3
	useOtherStyle(&swapped)
	if err := repo.Update(t.Context(), swapped); err != nil {
		t.Fatalf("Update to the style Outros: %v", err)
	}
	raw = rawEventDoc(t, s, ev.ID)
	wantEventKeys(t, "the event after Update to the style Outros", raw, append([]string{"created_by", "updated_by"}, otherStyleFields...)...)
	if v, ok := raw["version"].(int64); !ok || v != 4 {
		t.Errorf("version = %#v after three Updates, want int64(4)", raw["version"])
	}
}

// An event that outlives its collective (the cascade is a later task, and a race
// can leave one behind) is still read, with no collective name.
func TestEventByIDWithoutItsCollectiveHasAnEmptyName(t *testing.T) {
	s, ana, _, c := eventsFixture(t)
	repo := s.Repos().Events

	ev := eventToStore(c, ana)
	if err := repo.Create(t.Context(), &ev); err != nil {
		t.Fatalf("Create: %v", err)
	}
	collectiveID, err := bson.ObjectIDFromHex(c.ID)
	if err != nil {
		t.Fatalf("collective ID %q: %v", c.ID, err)
	}
	if _, err := s.DB().Collection("collectives").DeleteOne(t.Context(), bson.M{"_id": collectiveID}); err != nil {
		t.Fatalf("delete the collective document: %v", err)
	}

	view, err := repo.ByID(t.Context(), ev.ID)
	if err != nil {
		t.Fatalf("ByID after the collective is gone: %v, want the event", err)
	}
	if view.Event.ID != ev.ID || view.Event.Name != ev.Name || view.Event.CollectiveID != c.ID {
		t.Errorf("ByID = %+v, want the event %s of the deleted collective", view.Event, ev.ID)
	}
	if view.CollectiveName != "" {
		t.Errorf("CollectiveName = %q, want empty", view.CollectiveName)
	}
}

func TestEventCreateThatFailsStoresNothing(t *testing.T) {
	s, ana, _, c := eventsFixture(t)
	repo := s.Repos().Events
	unknown := bson.NewObjectID().Hex()

	invalid := eventToStore(c, ana)
	invalid.Name = ""
	noCollective := eventToStore(c, ana)
	noCollective.CollectiveID = unknown
	noCreator := eventToStore(c, ana)
	noCreator.CreatedBy = ""
	unknownCreator := eventToStore(c, ana)
	unknownCreator.CreatedBy = unknown
	unknownEditor := eventToStore(c, ana)
	unknownEditor.UpdatedBy = unknown

	cases := []struct {
		name    string
		ev      domain.Event
		wantErr func(error) bool
	}{
		{"invalid event", invalid, func(err error) bool { var fe domain.FieldErrors; return errors.As(err, &fe) }},
		{"unknown collective", noCollective, func(err error) bool { return errors.Is(err, domain.ErrNotFound) }},
		{"empty CreatedBy", noCreator, func(err error) bool { return errors.Is(err, domain.ErrNotFound) }},
		{"unknown CreatedBy", unknownCreator, func(err error) bool { return errors.Is(err, domain.ErrNotFound) }},
		{"unknown UpdatedBy", unknownEditor, func(err error) bool { return errors.Is(err, domain.ErrNotFound) }},
	}
	for _, tc := range cases {
		err := repo.Create(t.Context(), &tc.ev)
		if !tc.wantErr(err) {
			t.Errorf("%s: Create error = %v, want the error of the contract", tc.name, err)
		}
		if tc.ev.ID != "" || tc.ev.Version != 0 {
			t.Errorf("%s: Create set ID %q and Version %d on a failure", tc.name, tc.ev.ID, tc.ev.Version)
		}
		n, err := s.DB().Collection("events").CountDocuments(t.Context(), bson.M{})
		if err != nil {
			t.Fatalf("%s: count events: %v", tc.name, err)
		}
		if n != 0 {
			t.Errorf("%s: %d events stored after a failed Create, want none", tc.name, n)
		}
	}
}
