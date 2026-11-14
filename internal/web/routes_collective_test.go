package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// The fixed texts of /coletivo/:id (see routes_collective.go and the template
// pages/app/coletivo.html). The ?msg= codes are the only way a notice appears: the
// URL never supplies text.
const (
	cpLockedNotice  = "membros e nome congelados pelo admin: nada pode ser mudado aqui por enquanto."
	cpBlockedNotice = "eventos bloqueados pelo admin."
	cpNoRequests    = "nenhum pedido para decidir"
	cpNotFoundBody  = "404 page not found"
	cpNoID          = "000000000000000000000000"
)

var cpNotices = map[string]string{
	"pedido-aprovado": "aprovação registrada.",
	"pedido-recusado": "pedido recusado. a pessoa recebe um e-mail.",
	"promovido":       "membro promovido a administrador.",
	"rebaixado":       "administrador rebaixado a membro.",
	"nome-alterado":   "nome alterado.",
}

// cpPerson is a user and the cookie of their session.
type cpPerson struct {
	domain.User
	cookie *http.Cookie
}

// cpEnv is an App on a database of its own and the cast of one collective, Casa
// Verde (approved, not locked):
//
//	master      Maria Master, the creator and an Administrador
//	ana         Ana Admin, an Administrador
//	coAdmin     Caco Admin, an Administrador
//	member      Bia Membro, a Membro
//	wantsMember Caio Pedido, asks to join as a Membro (the site admin already approved)
//	wantsOwner  Duda Pedido, asks to join as an Administrador (the site admin already approved)
//
// outsider has no tie to it, and siteAdmin is the site admin, not a member.
type cpEnv struct {
	app   *App
	store domain.Store
	r     http.Handler
	c     domain.Collective

	master, ana, coAdmin, member, wantsMember, wantsOwner, outsider, siteAdmin cpPerson
}

func newCPEnv(t *testing.T) cpEnv {
	t.Helper()
	app, store := newStoreApp(t)
	e := cpEnv{app: app, store: store}
	person := func(name, email string, mutate ...func(*domain.User)) cpPerson {
		u := insertWebUser(t, store, email, append([]func(*domain.User){func(u *domain.User) { u.Name = name }}, mutate...)...)
		return cpPerson{User: u, cookie: loginAs(t, app, u)}
	}
	e.siteAdmin = person("Admin Site", "site@example.com", func(u *domain.User) { u.IsAdmin = true })
	e.master = person("Maria Master", "maria@example.com")
	e.ana = person("Ana Admin", "ana@example.com")
	e.coAdmin = person("Caco Admin", "caco@example.com")
	e.member = person("Bia Membro", "bia@example.com")
	e.wantsMember = person("Caio Pedido", "caio@example.com")
	e.wantsOwner = person("Duda Pedido", "duda@example.com")
	e.outsider = person("Otto Fora", "otto@example.com")

	e.c = colCollective(t, store, "Casa Verde", domain.CollectiveKindColetivo, domain.CollectiveStatusApproved, colCreatedBy(e.master.ID))
	for _, p := range []cpPerson{e.master, e.ana, e.coAdmin} {
		colMembership(t, store, p.ID, e.c.ID, domain.RoleOwner, domain.MembershipStatusApproved)
	}
	colMembership(t, store, e.member.ID, e.c.ID, domain.RoleMember, domain.MembershipStatusApproved)
	colMembership(t, store, e.wantsMember.ID, e.c.ID, domain.RoleMember, domain.MembershipStatusPending, colSide(true, e.siteAdmin.ID))
	colMembership(t, store, e.wantsOwner.ID, e.c.ID, domain.RoleOwner, domain.MembershipStatusPending, colSide(true, e.siteAdmin.ID))
	e.r = app.Router()
	return e
}

func (e cpEnv) path(suffix string) string { return "/coletivo/" + e.c.ID + suffix }

func (e cpEnv) get(p cpPerson, target string, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	return serve(e.r, http.MethodGet, target, append([]func(*http.Request){withCookie(p.cookie)}, mods...)...)
}

func (e cpEnv) post(p cpPerson, target string, form url.Values, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	return post(e.r, target, form.Encode(), append([]func(*http.Request){withCookie(p.cookie)}, mods...)...)
}

// membership reads the membership of the person in Casa Verde.
func (e cpEnv) membership(t *testing.T, p cpPerson) (domain.Membership, error) {
	t.Helper()
	m, err := e.store.Repos().Memberships.Get(context.Background(), p.ID, e.c.ID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("read membership of %s: %v", p.Name, err)
	}
	return m, err
}

// wantRole fails unless the person is an approved member of Casa Verde with role.
func (e cpEnv) wantRole(t *testing.T, p cpPerson, role domain.Role) {
	t.Helper()
	m, err := e.membership(t, p)
	if err != nil || m.Role != role || m.Status != domain.MembershipStatusApproved {
		t.Errorf("%s: membership %+v (err %v), want an approved %s", p.Name, m, err, role)
	}
}

// wantPending fails unless the person's request to join Casa Verde is untouched.
func (e cpEnv) wantPending(t *testing.T, p cpPerson) {
	t.Helper()
	m, err := e.membership(t, p)
	if err != nil || m.Status != domain.MembershipStatusPending || m.OwnerApprovedAt != nil {
		t.Errorf("%s: membership %+v (err %v), want the request still pending and undecided by an Administrador", p.Name, m, err)
	}
}

// wantName fails unless Casa Verde still has the name.
func (e cpEnv) wantName(t *testing.T, name string) {
	t.Helper()
	c, err := e.store.Repos().Collectives.ByID(context.Background(), e.c.ID)
	if err != nil {
		t.Fatalf("read collective: %v", err)
	}
	if c.Name != name {
		t.Errorf("collective name = %q, want %q", c.Name, name)
	}
}

// cpActions are the five posts of the page with a payload that succeeds on a fresh
// cpEnv for Ana: the path suffix and the form.
func (e cpEnv) actions() map[string]struct {
	suffix string
	form   url.Values
} {
	type action = struct {
		suffix string
		form   url.Values
	}
	return map[string]action{
		"approve": {"/pedido/aprovar", url.Values{"user_id": {e.wantsMember.ID}}},
		"reject":  {"/pedido/recusar", url.Values{"user_id": {e.wantsMember.ID}, "reason": {""}}},
		"promote": {"/membro/promover", url.Values{"user_id": {e.member.ID}}},
		"demote":  {"/membro/rebaixar", url.Values{"user_id": {e.coAdmin.ID}}},
		"rename":  {"/nome", url.Values{"name": {"Casa Azul"}}},
	}
}

// cpWantUnregistered fails unless rec answers exactly like want, the answer of a
// path no route serves: its status, body and Content-Type. Any other header that
// differs is logged (go test -v), not failed: the brief fixes the three.
func cpWantUnregistered(t *testing.T, who string, rec, want *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != want.Code {
		t.Errorf("%s: status = %d, want %d like an unregistered path", who, rec.Code, want.Code)
	}
	if got := rec.Body.String(); got != want.Body.String() {
		t.Errorf("%s: body = %q, want %q like an unregistered path", who, got, want.Body)
	}
	if got, w := rec.Header().Get("Content-Type"), want.Header().Get("Content-Type"); got != w {
		t.Errorf("%s: Content-Type = %q, want %q like an unregistered path", who, got, w)
	}
	keys := map[string]bool{}
	for k := range rec.Header() {
		keys[k] = true
	}
	for k := range want.Header() {
		keys[k] = true
	}
	for k := range keys {
		if k == "Date" {
			continue
		}
		if g, w := rec.Header().Values(k), want.Header().Values(k); !slices.Equal(g, w) {
			t.Logf("%s: header %s = %q, an unregistered path has %q", who, k, g, w)
		}
	}
}

// The first thing the page owes: nobody who may not manage the collective can tell
// that it exists. Every refusal is the 404 of a path no route serves.
func TestColetivoPageIsInvisibleToEveryoneButItsAdministradores(t *testing.T) {
	e := newCPEnv(t)
	want := e.get(e.ana, "/coletivo-que-nao-existe")
	if want.Code != http.StatusNotFound || want.Body.String() != cpNotFoundBody || want.Header().Get("Content-Type") != "text/plain" {
		t.Fatalf("an unregistered path answers %d %q (%s), want 404 %q (text/plain)", want.Code, want.Body, want.Header().Get("Content-Type"), cpNotFoundBody)
	}
	pending := colCollective(t, e.store, "Casa Nova", domain.CollectiveKindColetivo, domain.CollectiveStatusPending, colCreatedBy(e.ana.ID))
	colMembership(t, e.store, e.ana.ID, pending.ID, domain.RoleOwner, domain.MembershipStatusApproved)

	tests := []struct {
		name string
		who  cpPerson
		id   string
	}{
		{"a Membro", e.member, e.c.ID},
		{"somebody outside", e.outsider, e.c.ID},
		{"the site admin, who is not an Administrador of it", e.siteAdmin, e.c.ID},
		{"an Administrador of a collective that is still pending", e.ana, pending.ID},
		{"somebody whose request to join as a Membro is pending", e.wantsMember, e.c.ID},
		{"somebody whose request to join as an Administrador is pending", e.wantsOwner, e.c.ID},
		{"an unknown id", e.ana, cpNoID},
		{"an id that is not one", e.ana, "nao-e-um-id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpWantUnregistered(t, tt.name, e.get(tt.who, "/coletivo/"+tt.id), want)
		})
	}

	t.Run("an Administrador reads it", func(t *testing.T) {
		for _, p := range []cpPerson{e.master, e.ana, e.coAdmin} {
			if rec := e.get(p, e.path("")); rec.Code != http.StatusOK {
				t.Errorf("%s: status = %d, want 200", p.Name, rec.Code)
			}
		}
	})

	t.Run("a visitor is sent to sign in", func(t *testing.T) {
		rec := serve(e.r, http.MethodGet, e.path(""))
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/entrar?next=%2Fcoletivo%2F"+e.c.ID {
			t.Errorf("anonymous GET: %d, Location %q; want 303 to /entrar?next=%%2Fcoletivo%%2F%s", rec.Code, rec.Header().Get("Location"), e.c.ID)
		}
		for suffix := range map[string]bool{"/pedido/aprovar": true, "/pedido/recusar": true, "/membro/promover": true, "/membro/rebaixar": true, "/nome": true} {
			rec := post(e.r, e.path(suffix), "user_id="+e.member.ID)
			if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/entrar" {
				t.Errorf("anonymous POST %s: %d, Location %q; want 303 to /entrar", suffix, rec.Code, rec.Header().Get("Location"))
			}
		}
	})
}

var (
	cpUserIDRE   = regexp.MustCompile(`name="user_id" value="([^"]*)"`)
	cpTextareaRE = regexp.MustCompile(`<textarea[^>]*\sid="([^"]+)"`)
	cpLabelForRE = regexp.MustCompile(`<label[^>]*\sfor="([^"]+)"`)
)

func TestColetivoPageShowsTheMembersTheRequestsAndTheForms(t *testing.T) {
	e := newCPEnv(t)
	rec := e.get(e.ana, e.path(""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %q", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	wantAll(t, "page", body, "Casa Verde", "Coletivo", `href="/coletivos" aria-current="page"`)
	for _, absent := range []string{cpLockedNotice, cpBlockedNotice, `role="alert"`, `status--ok`} {
		if strings.Contains(body, absent) {
			t.Errorf("a plain page has %q", absent)
		}
	}
	if v := cspViolations(body); len(v) > 0 {
		t.Errorf("%s in:\n%s", strings.Join(v, "; "), body)
	}
	// Administradores first, then the Membros, each by name (the Master is one of the Administradores); requests the same way.
	inOrder(t, body, "Ana Admin", "Caco Admin", "Maria Master", "Bia Membro", "Duda Pedido", "Caio Pedido", `action="`+e.path("/nome")+`"`)

	hasAction := func(p cpPerson, suffix string) bool {
		row := colRow(t, body, p.Name)
		if !strings.Contains(row, `action="`+e.path(suffix)+`"`) {
			return false
		}
		if got := cpUserIDRE.FindStringSubmatch(row); got == nil || got[1] != p.ID {
			t.Errorf("%s: the %s form carries user_id %v, want %q", p.Name, suffix, got, p.ID)
		}
		return true
	}

	t.Run("members", func(t *testing.T) {
		if got := len(colRows(body)); got != 6 {
			t.Errorf("%d rows, want 6 (four members, two requests)", got)
		}
		for p, want := range map[string]string{e.master.Name: "Master", e.ana.Name: "Administrador", e.coAdmin.Name: "Administrador", e.member.Name: "Membro"} {
			if got := colField(t, colRow(t, body, p), colStatusRE, "role"); got != want {
				t.Errorf("%s is shown as %q, want %q", p, got, want)
			}
		}
		// The Master can be neither demoted nor promoted; an Administrador is only demoted; a Membro only promoted.
		for _, p := range []cpPerson{e.master} {
			for _, suffix := range []string{"/membro/rebaixar", "/membro/promover"} {
				if hasAction(p, suffix) {
					t.Errorf("%s (the Master) has a form to %s", p.Name, suffix)
				}
			}
		}
		for _, p := range []cpPerson{e.ana, e.coAdmin} {
			if !hasAction(p, "/membro/rebaixar") || hasAction(p, "/membro/promover") {
				t.Errorf("%s (an Administrador) must have a demote form and no promote form", p.Name)
			}
		}
		if !hasAction(e.member, "/membro/promover") || hasAction(e.member, "/membro/rebaixar") {
			t.Errorf("%s (a Membro) must have a promote form and no demote form", e.member.Name)
		}
		// The button names the person it acts on for a screen reader.
		for _, p := range []cpPerson{e.ana, e.member} {
			if !regexp.MustCompile(`class="sr-only">[^<]*` + regexp.QuoteMeta(p.Name)).MatchString(colRow(t, body, p.Name)) {
				t.Errorf("%s: the action button has no sr-only name", p.Name)
			}
		}
	})

	t.Run("requests", func(t *testing.T) {
		for p, want := range map[string]string{e.wantsMember.Name: "Membro", e.wantsOwner.Name: "Administrador"} {
			meta := colField(t, colRow(t, body, p), colMetaRE, "meta")
			if !strings.Contains(meta, want) {
				t.Errorf("%s: meta %q does not say the requested role %q", p, meta, want)
			}
		}
		for _, p := range []cpPerson{e.wantsMember, e.wantsOwner} {
			if !hasAction(p, "/pedido/aprovar") || !hasAction(p, "/pedido/recusar") {
				t.Errorf("%s: a request needs an approve and a reject form", p.Name)
			}
			row := colRow(t, body, p.Name)
			wantAll(t, p.Name+" reject form", row, `class="reason-form"`, `name="reason"`, "<summary>")
			if hasAction(p, "/membro/promover") || hasAction(p, "/membro/rebaixar") {
				t.Errorf("%s: a request has a member action", p.Name)
			}
		}
		// Each reason has a textarea of its own with a label that points at it.
		var ids []string
		for _, m := range cpTextareaRE.FindAllStringSubmatch(body, -1) {
			ids = append(ids, m[1])
		}
		if len(ids) != 2 || ids[0] == ids[1] {
			t.Errorf("textarea ids = %q, want two different ones", ids)
		}
		for _, id := range ids {
			var labelled bool
			for _, m := range cpLabelForRE.FindAllStringSubmatch(body, -1) {
				labelled = labelled || m[1] == id
			}
			if !labelled {
				t.Errorf("no label for textarea %q", id)
			}
		}
	})

	t.Run("rename", func(t *testing.T) {
		wantAll(t, "rename form", body, `action="`+e.path("/nome")+`"`, `name="name" value="Casa Verde"`)
	})

	t.Run("a produtora", func(t *testing.T) {
		prod := colCollective(t, e.store, "Selo Azul", domain.CollectiveKindProdutora, domain.CollectiveStatusApproved, colCreatedBy(e.ana.ID))
		colMembership(t, e.store, e.ana.ID, prod.ID, domain.RoleOwner, domain.MembershipStatusApproved)
		got := e.get(e.ana, "/coletivo/"+prod.ID).Body.String()
		wantAll(t, "page", got, "Selo Azul", "Produtora")
		if strings.Contains(got, "Maria Master") {
			t.Error("the page lists a member of another collective")
		}
	})
}

func TestColetivoPageWithoutRequests(t *testing.T) {
	e := newCPEnv(t)
	solo := colCollective(t, e.store, "Selo Solo", domain.CollectiveKindColetivo, domain.CollectiveStatusApproved, colCreatedBy(e.ana.ID))
	colMembership(t, e.store, e.ana.ID, solo.ID, domain.RoleOwner, domain.MembershipStatusApproved)
	body := e.get(e.ana, "/coletivo/"+solo.ID).Body.String()
	wantAll(t, "page", body, `class="empty"`, cpNoRequests)
	if strings.Contains(body, "/pedido/") {
		t.Error("a page without requests has a request form")
	}
	// Somebody whose request an Administrador already decided is not listed.
	e.wantPending(t, e.wantsMember)
}

func TestColetivoPageLockedAndEventsBlocked(t *testing.T) {
	e := newCPEnv(t)
	setFlags := func(locked, blocked bool) {
		c, err := e.store.Repos().Collectives.ByID(context.Background(), e.c.ID)
		if err != nil {
			t.Fatal(err)
		}
		c.Locked, c.EventsBlocked = locked, blocked
		if err := e.store.Repos().Collectives.Update(context.Background(), c); err != nil {
			t.Fatalf("update collective: %v", err)
		}
	}

	t.Run("locked: the notice, the people, and nothing to act with", func(t *testing.T) {
		setFlags(true, false)
		rec := e.get(e.ana, e.path(""))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		body := rec.Body.String()
		wantAll(t, "page", body, cpLockedNotice, "Maria Master", "Bia Membro", "Caio Pedido")
		for _, absent := range []string{`action="/coletivo/` + e.c.ID + `/`, `name="user_id"`, `class="reason-form"`, `name="name"`, cpBlockedNotice} {
			if strings.Contains(body, absent) {
				t.Errorf("a locked page has %q", absent)
			}
		}
		if v := cspViolations(body); len(v) > 0 {
			t.Errorf("%s", strings.Join(v, "; "))
		}
	})

	t.Run("events blocked: the notice, and the page still works", func(t *testing.T) {
		setFlags(false, true)
		body := e.get(e.ana, e.path("")).Body.String()
		wantAll(t, "page", body, cpBlockedNotice, `action="`+e.path("/nome")+`"`, `action="`+e.path("/membro/promover")+`"`)
		if strings.Contains(body, cpLockedNotice) {
			t.Error("a page that is not locked says it is")
		}
	})

	t.Run("both", func(t *testing.T) {
		setFlags(true, true)
		body := e.get(e.ana, e.path("")).Body.String()
		wantAll(t, "page", body, cpLockedNotice, cpBlockedNotice)
	})
}

func TestColetivoNoticeCodes(t *testing.T) {
	e := newCPEnv(t)
	for code, text := range cpNotices {
		body := e.get(e.ana, e.path("?msg="+code)).Body.String()
		wantAll(t, "?msg="+code, body, `class="status status--ok"`, text)
	}
	for _, msg := range []string{"", "desconhecido", "<script>alert(1)</script>", "pedido-aprovado%20extra", "Casa%20hackeada"} {
		body := e.get(e.ana, e.path("?msg="+msg)).Body.String()
		if strings.Contains(body, `status--ok`) || strings.Contains(body, "hackeada") || strings.Contains(body, "<script>alert") {
			t.Errorf("?msg=%s shows a notice or echoes the URL: %s", msg, body)
		}
	}
}

func TestColetivoActions(t *testing.T) {
	e := newCPEnv(t)
	tests := []struct {
		name    string
		suffix  string
		form    url.Values
		code    string
		checked func(*testing.T)
	}{
		{"approve a request", "/pedido/aprovar", url.Values{"user_id": {e.wantsMember.ID}}, "pedido-aprovado", func(t *testing.T) {
			e.wantRole(t, e.wantsMember, domain.RoleMember)
		}},
		{"reject a request, with a reason", "/pedido/recusar", url.Values{"user_id": {e.wantsOwner.ID}, "reason": {"agora não"}}, "pedido-recusado", func(t *testing.T) {
			if _, err := e.membership(t, e.wantsOwner); !errors.Is(err, domain.ErrNotFound) {
				t.Errorf("the rejected request is still there (err %v)", err)
			}
		}},
		{"promote a Membro", "/membro/promover", url.Values{"user_id": {e.member.ID}}, "promovido", func(t *testing.T) {
			e.wantRole(t, e.member, domain.RoleOwner)
		}},
		{"demote an Administrador", "/membro/rebaixar", url.Values{"user_id": {e.coAdmin.ID}}, "rebaixado", func(t *testing.T) {
			e.wantRole(t, e.coAdmin, domain.RoleMember)
		}},
		{"rename the collective", "/nome", url.Values{"name": {"Casa Azul"}}, "nome-alterado", func(t *testing.T) {
			e.wantName(t, "Casa Azul")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := e.post(e.ana, e.path(tt.suffix), tt.form)
			if want := e.path("?msg=" + tt.code); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != want {
				t.Fatalf("POST %s: %d, Location %q; want 303 to %q; body %q", tt.suffix, rec.Code, rec.Header().Get("Location"), want, rec.Body)
			}
			tt.checked(t)
			// The redirect lands on the page with the notice.
			if got := e.get(e.ana, e.path("?msg="+tt.code)).Body.String(); !strings.Contains(got, cpNotices[tt.code]) {
				t.Errorf("the page after the redirect lacks %q", cpNotices[tt.code])
			}
		})
	}

	t.Run("an HTMX post is redirected with a header", func(t *testing.T) {
		e := newCPEnv(t)
		rec := e.post(e.ana, e.path("/membro/promover"), url.Values{"user_id": {e.member.ID}}, hx)
		if rec.Code != http.StatusNoContent || rec.Header().Get("HX-Redirect") != e.path("?msg=promovido") {
			t.Errorf("HTMX POST: %d, HX-Redirect %q; want 204 to %q", rec.Code, rec.Header().Get("HX-Redirect"), e.path("?msg=promovido"))
		}
	})
}

// An Administrador may demote themself while another one remains. The page they
// leave is the 404 of a collective they may not manage, so the redirect goes to
// Meus Coletivos with a notice of its own, not back to the page.
func TestColetivoSelfDemotion(t *testing.T) {
	const to = "/coletivos?msg=voce-rebaixado"

	t.Run("an Administrador demotes themself", func(t *testing.T) {
		e := newCPEnv(t)
		rec := e.post(e.ana, e.path("/membro/rebaixar"), url.Values{"user_id": {e.ana.ID}})
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != to {
			t.Fatalf("POST: %d, Location %q; want 303 to %q; body %q", rec.Code, rec.Header().Get("Location"), to, rec.Body)
		}
		e.wantRole(t, e.ana, domain.RoleMember)

		landing := e.get(e.ana, to)
		if landing.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d, want 200", to, landing.Code)
		}
		wantAll(t, "page", landing.Body.String(), `class="status status--ok"`, colMsgDemoted)

		// The notice is why they are not sent to the page: it answers them with a 404 now.
		if page := e.get(e.ana, e.path("")); page.Code != http.StatusNotFound {
			t.Errorf("the collective's page after the demotion: status = %d, want 404", page.Code)
		}
	})

	t.Run("an HTMX post is redirected with a header", func(t *testing.T) {
		e := newCPEnv(t)
		rec := e.post(e.ana, e.path("/membro/rebaixar"), url.Values{"user_id": {e.ana.ID}}, hx)
		if rec.Code != http.StatusNoContent || rec.Header().Get("HX-Redirect") != to {
			t.Errorf("HTMX POST: %d, HX-Redirect %q; want 204 to %q", rec.Code, rec.Header().Get("HX-Redirect"), to)
		}
	})

	t.Run("demoting somebody else goes back to the page", func(t *testing.T) {
		e := newCPEnv(t)
		rec := e.post(e.ana, e.path("/membro/rebaixar"), url.Values{"user_id": {e.coAdmin.ID}})
		if want := e.path("?msg=rebaixado"); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != want {
			t.Errorf("POST: %d, Location %q; want 303 to %q", rec.Code, rec.Header().Get("Location"), want)
		}
	})

	t.Run("the Master cannot demote themself", func(t *testing.T) {
		e := newCPEnv(t)
		rec := e.post(e.master, e.path("/membro/rebaixar"), url.Values{"user_id": {e.master.ID}})
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409; body %q", rec.Code, rec.Body)
		}
		wantAll(t, "page", rec.Body.String(), approvalsMsgMaster, `role="alert"`)
		e.wantRole(t, e.master, domain.RoleOwner)
	})
}

// A request or a member that is gone by the time the post arrives (another
// Administrador decided the request, the member was removed) is "already decided":
// the answer is the 409 and the page read afresh. It is not the 404 that hides a
// collective from somebody who may not manage it: the handler knows the person may.
func TestColetivoStaleItems(t *testing.T) {
	tests := []struct {
		name   string
		stale  func(t *testing.T, e cpEnv) // makes the item go away
		suffix string
		item   func(e cpEnv) cpPerson // whose request or membership it was
	}{
		{"approve a request another Administrador rejected", func(t *testing.T, e cpEnv) {
			if rec := e.post(e.coAdmin, e.path("/pedido/recusar"), url.Values{"user_id": {e.wantsMember.ID}}); rec.Code != http.StatusSeeOther {
				t.Fatalf("the other Administrador's rejection: %d; body %q", rec.Code, rec.Body)
			}
		}, "/pedido/aprovar", func(e cpEnv) cpPerson { return e.wantsMember }},
		{"reject a request another Administrador rejected", func(t *testing.T, e cpEnv) {
			if rec := e.post(e.coAdmin, e.path("/pedido/recusar"), url.Values{"user_id": {e.wantsMember.ID}}); rec.Code != http.StatusSeeOther {
				t.Fatalf("the other Administrador's rejection: %d; body %q", rec.Code, rec.Body)
			}
		}, "/pedido/recusar", func(e cpEnv) cpPerson { return e.wantsMember }},
		{"promote a Membro whose membership is gone", func(t *testing.T, e cpEnv) {
			if err := e.store.Repos().Memberships.Delete(context.Background(), e.member.ID, e.c.ID); err != nil {
				t.Fatal(err)
			}
		}, "/membro/promover", func(e cpEnv) cpPerson { return e.member }},
		{"demote an Administrador whose membership is gone", func(t *testing.T, e cpEnv) {
			if err := e.store.Repos().Memberships.Delete(context.Background(), e.coAdmin.ID, e.c.ID); err != nil {
				t.Fatal(err)
			}
		}, "/membro/rebaixar", func(e cpEnv) cpPerson { return e.coAdmin }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newCPEnv(t)
			tt.stale(t, e)
			item := tt.item(e)
			form := url.Values{"user_id": {item.ID}}

			rec := e.post(e.ana, e.path(tt.suffix), form)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409; body %q", rec.Code, rec.Body)
			}
			body := rec.Body.String()
			wantAll(t, "page", body, approvalsMsgDecided, `role="alert"`, "Maria Master", `action="`+e.path("/nome")+`"`)
			if strings.Contains(body, item.Name) {
				t.Errorf("the refreshed page still lists %s", item.Name)
			}

			// The refusals that hide or deny the page do not change: a stranger still gets
			// the 404, and somebody who is no approved Administrador (Duda Pedido, whose
			// request is open) the 403.
			want := e.get(e.ana, "/coletivo-que-nao-existe")
			cpWantUnregistered(t, "a stranger", e.post(e.outsider, e.path(tt.suffix), form), want)
			if rec := e.post(e.wantsOwner, e.path(tt.suffix), form); rec.Code != http.StatusForbidden {
				t.Errorf("a person who is not an Administrador yet: status = %d, want 403", rec.Code)
			}
		})
	}
}

func TestColetivoForgedPostsAreRefused(t *testing.T) {
	t.Run("demote the Master", func(t *testing.T) {
		e := newCPEnv(t)
		rec := e.post(e.ana, e.path("/membro/rebaixar"), url.Values{"user_id": {e.master.ID}})
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409; body %q", rec.Code, rec.Body)
		}
		wantAll(t, "page", rec.Body.String(), approvalsMsgMaster, `role="alert"`, `action="`+e.path("/nome")+`"`)
		e.wantRole(t, e.master, domain.RoleOwner)
	})

	t.Run("the last Administrador leaves", func(t *testing.T) {
		e := newCPEnv(t)
		// No Master: Ana is the only Administrador, so nothing but the count protects her seat.
		solo := colCollective(t, e.store, "Selo Solo", domain.CollectiveKindColetivo, domain.CollectiveStatusApproved)
		colMembership(t, e.store, e.ana.ID, solo.ID, domain.RoleOwner, domain.MembershipStatusApproved)
		rec := e.post(e.ana, "/coletivo/"+solo.ID+"/membro/rebaixar", url.Values{"user_id": {e.ana.ID}})
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409; body %q", rec.Code, rec.Body)
		}
		wantAll(t, "page", rec.Body.String(), approvalsMsgLastOwner, `role="alert"`, "Ana Admin")
		m, err := e.store.Repos().Memberships.Get(context.Background(), e.ana.ID, solo.ID)
		if err != nil || m.Role != domain.RoleOwner {
			t.Errorf("Ana's membership = %+v (err %v), want still an Administrador", m, err)
		}
	})

	t.Run("a decided request", func(t *testing.T) {
		e := newCPEnv(t)
		if rec := e.post(e.ana, e.path("/pedido/aprovar"), url.Values{"user_id": {e.wantsMember.ID}}); rec.Code != http.StatusSeeOther {
			t.Fatalf("first approval: %d", rec.Code)
		}
		rec := e.post(e.coAdmin, e.path("/pedido/aprovar"), url.Values{"user_id": {e.wantsMember.ID}})
		if rec.Code != http.StatusConflict {
			t.Fatalf("second approval: status = %d, want 409; body %q", rec.Code, rec.Body)
		}
		wantAll(t, "page", rec.Body.String(), approvalsMsgDecided, "Bia Membro")
		if strings.Contains(rec.Body.String(), "Caio Pedido") && strings.Contains(colRow(t, rec.Body.String(), "Caio Pedido"), "/pedido/") {
			t.Error("the refreshed page still offers to decide the request")
		}
	})

	t.Run("everything on a locked collective", func(t *testing.T) {
		e := newCPEnv(t)
		c, _ := e.store.Repos().Collectives.ByID(context.Background(), e.c.ID)
		c.Locked = true
		if err := e.store.Repos().Collectives.Update(context.Background(), c); err != nil {
			t.Fatal(err)
		}
		for name, a := range e.actions() {
			rec := e.post(e.ana, e.path(a.suffix), a.form)
			if rec.Code != http.StatusConflict {
				t.Errorf("%s: status = %d, want 409; body %q", name, rec.Code, rec.Body)
				continue
			}
			wantAll(t, name+" page", rec.Body.String(), approvalsMsgLocked, cpLockedNotice)
		}
		e.wantPending(t, e.wantsMember)
		e.wantRole(t, e.member, domain.RoleMember)
		e.wantRole(t, e.coAdmin, domain.RoleOwner)
		e.wantName(t, "Casa Verde")
	})

	t.Run("a Membro posting", func(t *testing.T) {
		e := newCPEnv(t)
		for name, a := range e.actions() {
			rec := e.post(e.member, e.path(a.suffix), a.form)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s: status = %d, want 403; body %q", name, rec.Code, rec.Body)
			}
		}
		e.wantPending(t, e.wantsMember)
		e.wantRole(t, e.member, domain.RoleMember)
		e.wantRole(t, e.coAdmin, domain.RoleOwner)
		e.wantName(t, "Casa Verde")
	})

	// Somebody the page answers with a 404 gets the same 404 to a post, so a forged
	// post does not tell a collective that exists from one that does not.
	t.Run("a stranger posting", func(t *testing.T) {
		e := newCPEnv(t)
		want := e.get(e.ana, "/coletivo-que-nao-existe")
		for _, who := range []struct {
			name string
			p    cpPerson
			id   string
		}{
			{"to an unknown collective", e.outsider, cpNoID},
			{"to a collective they have no tie to", e.outsider, e.c.ID},
			{"the site admin, who is not a member", e.siteAdmin, e.c.ID},
			{"the site admin, to an unknown collective", e.siteAdmin, cpNoID},
		} {
			for name, a := range e.actions() {
				rec := e.post(who.p, "/coletivo/"+who.id+a.suffix, a.form)
				cpWantUnregistered(t, who.name+", "+name, rec, want)
			}
		}
		e.wantPending(t, e.wantsMember)
		e.wantRole(t, e.member, domain.RoleMember)
		e.wantRole(t, e.coAdmin, domain.RoleOwner)
		e.wantName(t, "Casa Verde")
	})

	// The Administrador is one (the handler checked), so what is gone is the request or
	// the member: the answer is the fresh page and the same "already decided" alert.
	t.Run("a person who is not in the collective", func(t *testing.T) {
		e := newCPEnv(t)
		for _, suffix := range []string{"/pedido/aprovar", "/pedido/recusar", "/membro/promover", "/membro/rebaixar"} {
			rec := e.post(e.ana, e.path(suffix), url.Values{"user_id": {e.outsider.ID}})
			if rec.Code != http.StatusConflict {
				t.Errorf("%s for somebody outside: status = %d, want 409; body %q", suffix, rec.Code, rec.Body)
				continue
			}
			wantAll(t, suffix+" page", rec.Body.String(), approvalsMsgDecided, `role="alert"`)
		}
	})

	t.Run("posts need the app's Origin", func(t *testing.T) {
		e := newCPEnv(t)
		for name, a := range e.actions() {
			for how, mod := range map[string]func(*http.Request){
				"no Origin":    func(*http.Request) {},
				"other Origin": header("Origin", "https://evil.test"),
			} {
				rec := do(e.r, http.MethodPost, e.path(a.suffix), strings.NewReader(a.form.Encode()), header("Content-Type", "application/x-www-form-urlencoded"), withCookie(e.ana.cookie), mod)
				if rec.Code != http.StatusForbidden {
					t.Errorf("%s with %s: status = %d, want 403", name, how, rec.Code)
				}
			}
		}
		e.wantPending(t, e.wantsMember)
		e.wantRole(t, e.member, domain.RoleMember)
		e.wantRole(t, e.coAdmin, domain.RoleOwner)
		e.wantName(t, "Casa Verde")
	})

	t.Run("a body that cannot be read", func(t *testing.T) {
		e := newCPEnv(t)
		for name, a := range e.actions() {
			rec := post(e.r, e.path(a.suffix), "user_id=%zz", withCookie(e.ana.cookie))
			if rec.Code != http.StatusBadRequest || rec.Body.String() != "requisição inválida" {
				t.Errorf("%s: %d %q; want 400 requisição inválida", name, rec.Code, rec.Body)
			}
		}
	})
}

func TestColetivoFieldErrors(t *testing.T) {
	t.Run("a name another collective has", func(t *testing.T) {
		e := newCPEnv(t)
		colCollective(t, e.store, "Casa Azul", domain.CollectiveKindColetivo, domain.CollectiveStatusApproved)
		rec := e.post(e.ana, e.path("/nome"), url.Values{"name": {"Casa Azul"}})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422; body %q", rec.Code, rec.Body)
		}
		body := rec.Body.String()
		colWantError(t, body, "name", colNameInUse)
		wantAll(t, "page", body, `name="name" value="Casa Azul"`, `aria-describedby="erro-name"`, "Maria Master")
		e.wantName(t, "Casa Verde")
	})

	t.Run("a name that is empty or too short", func(t *testing.T) {
		e := newCPEnv(t)
		for name, want := range map[string]string{"": colNameRequired, "a": colNameLength} {
			rec := e.post(e.ana, e.path("/nome"), url.Values{"name": {name}})
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("name %q: status = %d, want 422", name, rec.Code)
			}
			colWantError(t, rec.Body.String(), "name", want)
		}
		e.wantName(t, "Casa Verde")
	})

	t.Run("a reason that is too long is an alert", func(t *testing.T) {
		e := newCPEnv(t)
		rec := e.post(e.ana, e.path("/pedido/recusar"), url.Values{"user_id": {e.wantsMember.ID}, "reason": {strings.Repeat("x", domain.AuditTextMax+1)}})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422; body %q", rec.Code, rec.Body)
		}
		wantAll(t, "page", rec.Body.String(), `role="alert"`, "use no máximo 500 caracteres", "Caio Pedido")
		e.wantPending(t, e.wantsMember)
	})
}

func TestColetivoRenameRateLimit(t *testing.T) {
	e := newCPEnv(t)
	for i := range 10 {
		// The same name: a rename that changes nothing still counts against the budget.
		if rec := e.post(e.ana, e.path("/nome"), url.Values{"name": {"Casa Verde"}}); rec.Code != http.StatusSeeOther {
			t.Fatalf("rename %d: status = %d, want 303", i+1, rec.Code)
		}
	}
	rec := e.post(e.ana, e.path("/nome"), url.Values{"name": {"Casa Azul"}})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("eleventh rename: status = %d, want 429; body %q", rec.Code, rec.Body)
	}
	wantAll(t, "page", rec.Body.String(), msgTooMany, `role="alert"`, `name="name" value="Casa Azul"`)
	e.wantName(t, "Casa Verde")
	// Reading the page is not counted.
	if got := e.get(e.ana, e.path("")).Code; got != http.StatusOK {
		t.Errorf("GET after the limit: status = %d, want 200", got)
	}
}

// colFailingStore (routes_collectives_test.go) makes every transaction fail.
func TestColetivoServerError(t *testing.T) {
	e := newCPEnv(t)
	e.app.Store = colFailingStore{e.store}
	logs := captureLogs(e.app)
	e.r = e.app.Router()
	const typed = "Typed Secret Name"

	for name, a := range e.actions() {
		t.Run(name, func(t *testing.T) {
			logs.Reset()
			form := url.Values{}
			for k, v := range a.form {
				form[k] = v
			}
			if _, ok := form["name"]; ok {
				form.Set("name", typed)
			}
			rec := e.post(e.ana, e.path(a.suffix), form)
			if rec.Code != http.StatusInternalServerError || rec.Body.String() != colInternal {
				t.Fatalf("POST: %d %q; want 500 %q", rec.Code, rec.Body, colInternal)
			}
			var logged bool
			for _, l := range logLines(t, logs) {
				if l["level"] == "ERROR" && l["msg"] != "request" {
					logged = true
				}
			}
			if !logged {
				t.Errorf("no error was logged: %s", logs)
			}
			for _, leak := range []string{typed, "ana@example.com"} {
				if strings.Contains(logs.String(), leak) {
					t.Errorf("the log carries %q: %s", leak, logs)
				}
			}
		})
	}
}

// Every page the handlers render passes the CSP and has nothing for the Alpine CSP
// build to evaluate: the page is plain forms and works without JavaScript.
func TestColetivoPagesMarkup(t *testing.T) {
	e := newCPEnv(t)
	colCollective(t, e.store, "Casa Azul", domain.CollectiveKindColetivo, domain.CollectiveStatusApproved)
	pages := map[string]*httptest.ResponseRecorder{
		"the page":     e.get(e.ana, e.path("")),
		"a notice":     e.get(e.ana, e.path("?msg=promovido")),
		"a 409":        e.post(e.ana, e.path("/membro/rebaixar"), url.Values{"user_id": {e.master.ID}}),
		"a name error": e.post(e.ana, e.path("/nome"), url.Values{"name": {"Casa Azul"}}),
		"a reason":     e.post(e.ana, e.path("/pedido/recusar"), url.Values{"user_id": {e.wantsMember.ID}, "reason": {strings.Repeat("x", 501)}}),
	}
	for name, rec := range pages {
		t.Run(name, func(t *testing.T) {
			body := rec.Body.String()
			if v := cspViolations(body); len(v) > 0 {
				t.Errorf("%s in:\n%s", strings.Join(v, "; "), body)
			}
			for _, a := range alpineAttr.FindAllStringSubmatch(body, -1) {
				if a[1] == "x-data" {
					continue // the shell's own components
				}
				if !alpineName.MatchString(a[2]) {
					t.Errorf("%s=%q is not a plain name: the Alpine CSP build cannot evaluate it", a[1], a[2])
				}
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
			// Every form on the page is an ordinary post.
			for _, f := range regexp.MustCompile(`<form[^>]*action="/coletivo/[^"]*"[^>]*>`).FindAllString(body, -1) {
				if !strings.Contains(f, `method="post"`) {
					t.Errorf("form %s is not a post", f)
				}
			}
		})
	}
}
