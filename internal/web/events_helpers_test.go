package web

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// Fixtures shared by the tests of the event pages (calendar, detail, form, day
// list, Meus Eventos, option lists). They carry the prefix ev; a page's own
// helpers carry the prefix of its file (cal…, evd…, evf…, day…, mine…, opt…),
// so pages written in parallel do not collide. calPlace is the one exception: it
// keeps its name because calendar_ui_test.go uses it.

// evCollective stores an approved collective of kind coletivo created by
// createdBy (a user ID, or empty). mutate adjusts it before it is stored, for
// example to set EventsBlocked.
func evCollective(t *testing.T, store domain.Store, name, createdBy string, mutate ...func(*domain.Collective)) domain.Collective {
	t.Helper()
	approvedAt := webTestNow
	c := domain.Collective{
		Name:       name,
		NameKey:    domain.NameKey(name),
		Kind:       domain.CollectiveKindColetivo,
		Status:     domain.CollectiveStatusApproved,
		CreatedBy:  createdBy,
		ApprovedAt: &approvedAt,
		CreatedAt:  webTestNow,
		UpdatedAt:  webTestNow,
	}
	for _, f := range mutate {
		f(&c)
	}
	if err := store.Repos().Collectives.Create(context.Background(), &c); err != nil {
		t.Fatalf("insert collective %q: %v", name, err)
	}
	return c
}

// evMember stores an approved membership of the user in the collective with the
// given role, both approval sides set.
func evMember(t *testing.T, store domain.Store, userID, collectiveID string, role domain.Role) {
	t.Helper()
	decided := webTestNow
	m := domain.Membership{
		UserID:          userID,
		CollectiveID:    collectiveID,
		Role:            role,
		Status:          domain.MembershipStatusApproved,
		AdminApprovedAt: &decided,
		OwnerApprovedAt: &decided,
		RequestedAt:     webTestNow,
		DecidedAt:       &decided,
	}
	if err := store.Repos().Memberships.Create(context.Background(), m); err != nil {
		t.Fatalf("insert membership of %s in %s: %v", userID, collectiveID, err)
	}
}

// evEvent stores a valid event of the collective created by createdBy and
// returns it with its ID and version. By default it is a Festa in Fortaleza/CE
// on Saturday 14/11/2026 at 22:00 in Fortaleza (01:00 UTC on the 15th), for six
// hours, R$ 25,00, style techno. mutate adjusts it before it is stored.
func evEvent(t *testing.T, store domain.Store, collectiveID, createdBy string, mutate ...func(*domain.Event)) domain.Event {
	t.Helper()
	e := domain.Event{
		CollectiveID: collectiveID,
		Name:         "Noite Verde",
		Type:         domain.EventTypeFesta,
		StartsAt:     time.Date(2026, 11, 15, 1, 0, 0, 0, time.UTC),
		DurationMin:  360,
		PriceCents:   2500,
		Style:        "techno",
		State:        "CE",
		City:         "Fortaleza",
		CreatedBy:    createdBy,
		UpdatedBy:    createdBy,
		CreatedAt:    webTestNow,
		UpdatedAt:    webTestNow,
	}
	for _, f := range mutate {
		f(&e)
	}
	if err := store.Repos().Events.Create(context.Background(), &e); err != nil {
		t.Fatalf("insert event %q: %v", e.Name, err)
	}
	return e
}

// evWorld is the cast most event page tests need, in an App on a fresh store
// whose clock is webTestNow (Thursday 12/11/2026, 13:00 in Fortaleza):
//   - Admin: the site admin, with no membership.
//   - Owner: Administrador (approved) of Alfa and of Delta.
//   - Member: Membro (approved) of Alfa.
//   - Outsider: an active account with no membership.
//   - Alfa: an approved collective; Delta: an approved collective with its
//     events blocked.
//
// Each user has a session cookie in Cookie, and r is the App's router.
type evWorld struct {
	App                            *App
	Store                          domain.Store
	r                              http.Handler
	Admin, Owner, Member, Outsider domain.User
	Alfa, Delta                    domain.Collective
	Cookie                         map[string]*http.Cookie // by user ID
}

// newEvWorld stores the cast described on evWorld and logs everyone in.
func newEvWorld(t *testing.T) evWorld {
	t.Helper()
	app, store := newStoreApp(t)
	w := evWorld{App: app, Store: store, r: app.Router(), Cookie: map[string]*http.Cookie{}}
	w.Admin = insertWebUser(t, store, "admin@agenda.test", func(u *domain.User) { u.Name = "Admin Teste"; u.IsAdmin = true })
	w.Owner = insertWebUser(t, store, "dono@agenda.test", func(u *domain.User) { u.Name = "Dono Teste" })
	w.Member = insertWebUser(t, store, "membro@agenda.test", func(u *domain.User) { u.Name = "Membro Teste" })
	w.Outsider = insertWebUser(t, store, "fora@agenda.test", func(u *domain.User) { u.Name = "Fora Teste" })
	w.Alfa = evCollective(t, store, "Coletivo Alfa", w.Owner.ID)
	w.Delta = evCollective(t, store, "Coletivo Delta", w.Owner.ID, func(c *domain.Collective) { c.EventsBlocked = true })
	evMember(t, store, w.Owner.ID, w.Alfa.ID, domain.RoleOwner)
	evMember(t, store, w.Owner.ID, w.Delta.ID, domain.RoleOwner)
	evMember(t, store, w.Member.ID, w.Alfa.ID, domain.RoleMember)
	for _, u := range []domain.User{w.Admin, w.Owner, w.Member, w.Outsider} {
		w.Cookie[u.ID] = loginAs(t, app, u)
	}
	return w
}

// event stores an event of Alfa, created by the Owner, called name that starts at
// the instant.
func (w evWorld) event(t *testing.T, name string, starts time.Time, mutate ...func(*domain.Event)) domain.Event {
	t.Helper()
	set := func(ev *domain.Event) { ev.Name, ev.StartsAt = name, starts }
	return evEvent(t, w.Store, w.Alfa.ID, w.Owner.ID, append([]func(*domain.Event){set}, mutate...)...)
}

// get sends a GET as the user.
func (w evWorld) get(u domain.User, target string, mod ...func(*http.Request)) *httptest.ResponseRecorder {
	return serve(w.r, http.MethodGet, target, append([]func(*http.Request){withCookie(w.Cookie[u.ID])}, mod...)...)
}

// body answers the GET as the Outsider, who owns nothing (the pages that show the
// same to everyone signed in go out as her), and fails unless it is a 200.
func (w evWorld) body(t *testing.T, target string, mod ...func(*http.Request)) string {
	t.Helper()
	rec := w.get(w.Outsider, target, mod...)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %.200q; want 200", target, rec.Code, rec.Body)
	}
	return rec.Body.String()
}

// send posts the values to target as u, from the app's own origin.
func (w evWorld) send(u domain.User, target string, v url.Values, mod ...func(*http.Request)) *httptest.ResponseRecorder {
	return post(w.r, target, v.Encode(), append([]func(*http.Request){withCookie(w.Cookie[u.ID])}, mod...)...)
}

// calPlace puts the event in a place. It keeps the name of the calendar file it
// came from because calendar_ui_test.go uses it.
func calPlace(state, city string) func(*domain.Event) {
	return func(ev *domain.Event) { ev.State, ev.City = state, city }
}

// evAttrURL reads an attribute of a tag (its attributes, or the whole tag) as a
// URL, the way a browser does: unescaped, then parsed.
func evAttrURL(t *testing.T, tag, name string) *url.URL {
	t.Helper()
	m := regexp.MustCompile(`(?:^|\s)` + name + `="([^"]*)"`).FindStringSubmatch(tag)
	if m == nil {
		t.Fatalf("no %s in %q", name, tag)
	}
	u, err := url.Parse(html.UnescapeString(m[1]))
	if err != nil {
		t.Fatalf("%s=%q: %v", name, m[1], err)
	}
	return u
}

// evHref is the href of the first link in s whose text starts with prefix.
func evHref(t *testing.T, s, prefix string) *url.URL {
	t.Helper()
	m := regexp.MustCompile(`<a ([^>]*)>\s*` + regexp.QuoteMeta(prefix)).FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("no link whose text starts with %q in:\n%s", prefix, s)
	}
	return evAttrURL(t, m[1], "href")
}

// evOption is one <option> of a fragment or a <select>, as the person reads it.
type evOption struct {
	Value, Label string
	Selected     bool
}

var evOptionRe = regexp.MustCompile(`<option value="([^"]*)"( selected)?>([^<]*)</option>`)

// evOptions reads the options of a fragment or a <select>, with the HTML escapes undone.
func evOptions(body string) []evOption {
	var out []evOption
	for _, m := range evOptionRe.FindAllStringSubmatch(body, -1) {
		out = append(out, evOption{Value: html.UnescapeString(m[1]), Label: html.UnescapeString(m[3]), Selected: m[2] != ""})
	}
	return out
}

// evValues are the values of the options of a select, in order (the empty one
// included), and the value of the one marked selected, "" for none.
func evValues(sel string) (values []string, selected string) {
	for _, o := range evOptions(sel) {
		values = append(values, o.Value)
		if o.Selected {
			selected = o.Value
		}
	}
	return values, selected
}

// TestNewEvWorld checks the shared cast against the store, so the page tests
// start from a known state.
func TestNewEvWorld(t *testing.T) {
	w := newEvWorld(t)
	for _, tc := range []struct {
		who  string
		user domain.User
		want bool
	}{
		{"owner", w.Owner, true},
		{"member", w.Member, false},
		{"outsider", w.Outsider, false},
		{"admin", w.Admin, false},
	} {
		got, err := w.Store.Repos().Memberships.UserCanCreateEvents(context.Background(), tc.user.ID)
		if err != nil || got != tc.want {
			t.Errorf("UserCanCreateEvents(%s) = %v, %v; want %v", tc.who, got, err, tc.want)
		}
	}
	e := evEvent(t, w.Store, w.Alfa.ID, w.Owner.ID)
	if e.ID == "" || e.Version != 1 {
		t.Errorf("evEvent = ID %q, version %d; want an ID and version 1", e.ID, e.Version)
	}
}
