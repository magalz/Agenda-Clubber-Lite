package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// Texts the /admin page shows (see routes_admin.go). The notices are chosen by
// the ?msg= code of the redirect that follows a decision.
const (
	admEmpty         = "nada esperando aprovação"
	admStatusPending = "aguardando aprovação"
	admStatusActive  = "conta ativa"
	admJoinOwnerDone = "um Administrador já aprovou"
	admJoinOwnerOpen = "aguardando um Administrador"
	admReasonText    = "use só texto, sem caracteres de controle"
)

var admNotices = []struct{ code, text string }{
	{"pessoa-aprovada", "tudo aprovado para esta pessoa."},
	{"conta-aprovada", "conta aprovada."},
	{"conta-recusada", "cadastro recusado."},
	{"coletivo-aprovado", "coletivo aprovado."},
	{"coletivo-recusado", "coletivo recusado."},
	{"pedido-aprovado", "pedido aprovado."},
	{"pedido-recusado", "pedido recusado."},
}

// admSignedUp is 01:30 UTC, which is still the day before in Fortaleza (UTC-3):
// a date shown in UTC would read 12/11/2026 instead of the 11/11/2026 it must.
var admSignedUp = time.Date(2026, 11, 12, 1, 30, 0, 0, time.UTC)

const admSignedUpShown = "11/11/2026"

// admEnv is an App on a database of its own and a signed-in site admin.
type admEnv struct {
	app    *App
	store  domain.Store
	r      http.Handler
	admin  domain.User
	cookie *http.Cookie
}

func newAdmEnv(t *testing.T) admEnv {
	t.Helper()
	app, store := newStoreApp(t)
	admin := insertWebUser(t, store, "admin@example.com", func(u *domain.User) { u.Name = "Admin Geral"; u.IsAdmin = true })
	return admEnv{app: app, store: store, r: app.Router(), admin: admin, cookie: loginAs(t, app, admin)}
}

func (e admEnv) get(target string, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	return serve(e.r, http.MethodGet, target, append([]func(*http.Request){withCookie(e.cookie)}, mods...)...)
}

func (e admEnv) post(target string, form url.Values, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	return post(e.r, target, form.Encode(), append([]func(*http.Request){withCookie(e.cookie)}, mods...)...)
}

// admWorld is a queue with two people. Paula awaits approval and asked for a
// produtora and to join Beta Casa. Caio is active and asked for a coletivo and to
// join Delta Lar as Administrador, which an Administrador of it approved already.
type admWorld struct {
	admEnv
	paula, caio         domain.User
	paulaNew, paulaJoin domain.Collective
	caioNew, caioJoin   domain.Collective
}

func admRequested(minutesAgo int) func(*domain.Membership) {
	return func(m *domain.Membership) { m.RequestedAt = webTestNow.Add(-time.Duration(minutesAgo) * time.Minute) }
}

func newAdmWorld(t *testing.T) admWorld {
	t.Helper()
	e := newAdmEnv(t)
	store := e.store
	maria := insertWebUser(t, store, "maria@example.com", func(u *domain.User) { u.Name = "Maria Master" })
	paula := insertWebUser(t, store, "paula@example.com", func(u *domain.User) {
		u.Name, u.Phone, u.PhoneIsWhatsApp = "Paula Pendente", "81987654321", true
		u.Status, u.CreatedAt = domain.UserStatusPendingApproval, admSignedUp
	})
	caio := insertWebUser(t, store, "caio@example.com", func(u *domain.User) { u.Name, u.Phone = "Caio Ativo", "81911112222" })

	approved := func(name string) domain.Collective {
		c := colCollective(t, store, name, domain.CollectiveKindColetivo, domain.CollectiveStatusApproved, colCreatedBy(maria.ID), func(c *domain.Collective) {
			at := webTestNow
			c.ApprovedBy, c.ApprovedAt = e.admin.ID, &at
		})
		colMembership(t, store, maria.ID, c.ID, domain.RoleOwner, domain.MembershipStatusApproved, colSide(true, e.admin.ID), colSide(false, maria.ID))
		return c
	}
	w := admWorld{admEnv: e, paula: paula, caio: caio}

	w.paulaNew = colCollective(t, store, "Alfa Nova", domain.CollectiveKindProdutora, domain.CollectiveStatusPending, colCreatedBy(paula.ID))
	colMembership(t, store, paula.ID, w.paulaNew.ID, domain.RoleOwner, domain.MembershipStatusPending, colSide(false, paula.ID), admRequested(60))
	w.paulaJoin = approved("Beta Casa")
	colMembership(t, store, paula.ID, w.paulaJoin.ID, domain.RoleMember, domain.MembershipStatusPending, admRequested(30))

	w.caioNew = colCollective(t, store, "Gama Nova", domain.CollectiveKindColetivo, domain.CollectiveStatusPending, colCreatedBy(caio.ID))
	colMembership(t, store, caio.ID, w.caioNew.ID, domain.RoleOwner, domain.MembershipStatusPending, colSide(false, caio.ID), admRequested(50))
	w.caioJoin = approved("Delta Lar")
	colMembership(t, store, caio.ID, w.caioJoin.ID, domain.RoleOwner, domain.MembershipStatusPending, colSide(false, maria.ID), admRequested(20))
	return w
}

// posts has one valid post for each decision path, on the people of the world.
func (w admWorld) posts() map[string]url.Values {
	return map[string]url.Values{
		"/admin/pessoa/aprovar-tudo": {"user_id": {w.paula.ID}},
		"/admin/conta/aprovar":       {"user_id": {w.paula.ID}},
		"/admin/conta/recusar":       {"user_id": {w.paula.ID}, "reason": {"motivo"}},
		"/admin/coletivo/aprovar":    {"collective_id": {w.caioNew.ID}},
		"/admin/coletivo/recusar":    {"collective_id": {w.caioNew.ID}, "reason": {"motivo"}},
		"/admin/pedido/aprovar":      {"user_id": {w.caio.ID}, "collective_id": {w.caioJoin.ID}},
		"/admin/pedido/recusar":      {"user_id": {w.caio.ID}, "collective_id": {w.caioJoin.ID}, "reason": {"motivo"}},
	}
}

func (w admWorld) user(t *testing.T, id string) (domain.User, error) {
	t.Helper()
	return w.store.Repos().Users.ByID(context.Background(), id)
}

func (w admWorld) collective(t *testing.T, id string) (domain.Collective, error) {
	t.Helper()
	return w.store.Repos().Collectives.ByID(context.Background(), id)
}

func (w admWorld) membership(t *testing.T, userID, collectiveID string) (domain.Membership, error) {
	t.Helper()
	return w.store.Repos().Memberships.Get(context.Background(), userID, collectiveID)
}

// requireUntouched fails unless every account, collective and request of the
// world is as newAdmWorld left it.
func (w admWorld) requireUntouched(t *testing.T) {
	t.Helper()
	if u, err := w.user(t, w.paula.ID); err != nil || u.Status != domain.UserStatusPendingApproval {
		t.Errorf("Paula after the posts: %+v, %v; want her account still awaiting approval", u, err)
	}
	if u, err := w.user(t, w.caio.ID); err != nil || u.Status != domain.UserStatusActive {
		t.Errorf("Caio after the posts: %+v, %v; want his account active", u, err)
	}
	for _, c := range []domain.Collective{w.paulaNew, w.caioNew} {
		if got, err := w.collective(t, c.ID); err != nil || got.Status != domain.CollectiveStatusPending {
			t.Errorf("%s after the posts: %+v, %v; want it pending", c.Name, got, err)
		}
	}
	for _, p := range []struct {
		user domain.User
		col  domain.Collective
	}{{w.paula, w.paulaNew}, {w.paula, w.paulaJoin}, {w.caio, w.caioNew}, {w.caio, w.caioJoin}} {
		if m, err := w.membership(t, p.user.ID, p.col.ID); err != nil || m.Status != domain.MembershipStatusPending || m.AdminApprovedAt != nil {
			t.Errorf("request of %s to %s after the posts: %+v, %v; want it pending with the admin side open", p.user.Name, p.col.Name, m, err)
		}
	}
}

var (
	admCardRoot = `<section class="panel panel--plain admin-person"`
	admIDAttr   = regexp.MustCompile(`\sid="([^"]*)"`)
	admForAttr  = regexp.MustCompile(`\sfor="([^"]*)"`)
)

// admCard returns the markup of the card that shows name.
func admCard(t *testing.T, body, name string) string {
	t.Helper()
	for _, chunk := range strings.Split(body, admCardRoot)[1:] {
		card, _, _ := strings.Cut(chunk, "</section>\n")
		if strings.Contains(card, name) {
			return card
		}
	}
	t.Fatalf("no card for %q in:\n%s", name, body)
	return ""
}

// admForm returns the markup of the form that posts to action, in markup, or "".
func admForm(markup, action string) string {
	_, rest, found := strings.Cut(markup, `<form method="post" action="`+action+`"`)
	if !found {
		return ""
	}
	form, _, _ := strings.Cut(rest, "</form>")
	return form
}

func admHidden(name, value string) string {
	return `<input type="hidden" name="` + name + `" value="` + value + `">`
}

func TestAdminNeedsAnAdmin(t *testing.T) {
	w := newAdmWorld(t)
	caio := loginAs(t, w.app, w.caio)

	rec := serve(w.r, http.MethodGet, "/admin")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/entrar?next=%2Fadmin" {
		t.Errorf("anonymous GET /admin: %d, Location %q; want 303 to /entrar?next=%%2Fadmin", rec.Code, rec.Header().Get("Location"))
	}
	if rec := serve(w.r, http.MethodGet, "/admin", withCookie(caio)); rec.Code != http.StatusForbidden {
		t.Errorf("GET /admin as a person who is not the admin = %d, want 403", rec.Code)
	}
	if rec := w.get("/admin"); rec.Code != http.StatusOK {
		t.Errorf("GET /admin as the admin = %d, want 200", rec.Code)
	}

	for target, form := range w.posts() {
		rec := post(w.r, target, form.Encode())
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/entrar" {
			t.Errorf("anonymous POST %s: %d, Location %q; want 303 to /entrar", target, rec.Code, rec.Header().Get("Location"))
		}
		// The session and the Origin are right: only requireAdmin refuses it.
		if rec := post(w.r, target, form.Encode(), withCookie(caio)); rec.Code != http.StatusForbidden {
			t.Errorf("POST %s as a person who is not the admin = %d, want 403", target, rec.Code)
		}
	}
	w.requireUntouched(t)
}

func TestAdminPostsNeedTheAppsOrigin(t *testing.T) {
	w := newAdmWorld(t)
	for target, form := range w.posts() {
		for name, mod := range map[string]func(*http.Request){
			"no Origin":    func(req *http.Request) { req.Header.Del("Origin") },
			"other Origin": header("Origin", "https://evil.test"),
		} {
			if rec := w.post(target, form, mod); rec.Code != http.StatusForbidden {
				t.Errorf("POST %s with %s = %d, want 403", target, name, rec.Code)
			}
		}
	}
	w.requireUntouched(t)
}

func TestAdminEmptyQueue(t *testing.T) {
	e := newAdmEnv(t)

	rec := e.get("/admin")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %q", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	body := rec.Body.String()
	wantAll(t, "the empty queue", body, "<title>Admin · AGENDA CLUBBER</title>", `<div class="empty">`+admEmpty+`</div>`, `<nav class="admin-nav"`)
	if strings.Contains(body, admCardRoot) {
		t.Error("the empty queue has a card")
	}
	if v := cspViolations(body); len(v) > 0 {
		t.Errorf("%s in:\n%s", strings.Join(v, "; "), body)
	}
}

func TestAdminQueueShowsEveryPersonOnce(t *testing.T) {
	w := newAdmWorld(t)

	rec := w.get("/admin")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %q", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if strings.Contains(body, admEmpty) {
		t.Error("the queue has people and shows the empty state")
	}
	if got := strings.Count(body, admCardRoot); got != 2 {
		t.Fatalf("%d cards, want 2 (Paula and Caio)", got)
	}
	inOrder(t, body, "Paula Pendente", "Caio Ativo") // the oldest request first

	paula := admCard(t, body, "Paula Pendente")
	wantAll(t, "Paula's card", paula, "paula@example.com", "81987654321", "(WhatsApp)", admStatusPending, admSignedUpShown)
	if strings.Contains(paula, admStatusActive) {
		t.Error("Paula's account awaits approval, and her card says it is active")
	}
	caio := admCard(t, body, "Caio Ativo")
	wantAll(t, "Caio's card", caio, "caio@example.com", "81911112222", admStatusActive, "12/11/2026")
	if strings.Contains(caio, "(WhatsApp)") || strings.Contains(caio, admStatusPending) {
		t.Errorf("Caio's phone is not a WhatsApp and his account is active:\n%s", caio)
	}

	// The items: a collective asked for, or a request to join one.
	for _, row := range []struct {
		card, title, meta string
	}{
		{paula, "coletivo novo: Alfa Nova", "Produtora · Master"},
		{paula, "Beta Casa · Membro", admJoinOwnerOpen},
		{caio, "coletivo novo: Gama Nova", "Coletivo · Master"},
		{caio, "Delta Lar · Administrador", admJoinOwnerDone},
	} {
		r := colRow(t, row.card, row.title)
		if got := colField(t, r, colMetaRE, "meta line"); got != row.meta {
			t.Errorf("meta of %q = %q, want %q", row.title, got, row.meta)
		}
	}
	// One row per request, nothing else: a person with no requests has none.
	if got := len(colRows(paula)) + len(colRows(caio)); got != 4 {
		t.Errorf("%d rows in the cards, want 4", got)
	}
}

func TestAdminActionsFollowTheAccountStatus(t *testing.T) {
	w := newAdmWorld(t)
	body := w.get("/admin").Body.String()
	paula := admCard(t, body, "Paula Pendente")
	caio := admCard(t, body, "Caio Ativo")

	// A pending account is decided as a whole, or the account alone, or rejected;
	// each request may be rejected, and none approved by itself (the server
	// refuses that for a pending person).
	wantAll(t, "Paula's card", paula,
		"[→] APROVAR TUDO", "[→] APROVAR SÓ A CONTA", "[✕] recusar cadastro",
		`action="/admin/pessoa/aprovar-tudo"`, `action="/admin/conta/aprovar"`, `action="/admin/conta/recusar"`,
		`action="/admin/coletivo/recusar"`, `action="/admin/pedido/recusar"`)
	for _, absent := range []string{`action="/admin/coletivo/aprovar"`, `action="/admin/pedido/aprovar"`} {
		if strings.Contains(paula, absent) {
			t.Errorf("Paula's account awaits approval, and her card offers %s", absent)
		}
	}
	// The ids go in hidden fields.
	wantAll(t, "Paula's forms", admForm(paula, "/admin/pessoa/aprovar-tudo"), admHidden("user_id", w.paula.ID))
	wantAll(t, "Paula's forms", admForm(paula, "/admin/conta/aprovar"), admHidden("user_id", w.paula.ID))
	reject := admForm(paula, "/admin/conta/recusar")
	wantAll(t, "the reject account form", reject, admHidden("user_id", w.paula.ID), `<details class="reason-form">`, `name="reason"`, `maxlength="500"`)
	wantAll(t, "the reject collective form", admForm(colRow(t, paula, "coletivo novo: Alfa Nova"), "/admin/coletivo/recusar"),
		admHidden("collective_id", w.paulaNew.ID), `name="reason"`)
	wantAll(t, "the reject join form", admForm(colRow(t, paula, "Beta Casa · Membro"), "/admin/pedido/recusar"),
		admHidden("user_id", w.paula.ID), admHidden("collective_id", w.paulaJoin.ID), `name="reason"`)

	// An active account has requests only: each one may be approved by itself,
	// and "Aprovar tudo" approves them all.
	wantAll(t, "Caio's card", caio, "[→] APROVAR TUDO", `action="/admin/pessoa/aprovar-tudo"`)
	for _, absent := range []string{"APROVAR SÓ A CONTA", "recusar cadastro", `action="/admin/conta/aprovar"`, `action="/admin/conta/recusar"`} {
		if strings.Contains(caio, absent) {
			t.Errorf("Caio's account is active, and his card offers %s", absent)
		}
	}
	wantAll(t, "Caio's new collective", admForm(colRow(t, caio, "coletivo novo: Gama Nova"), "/admin/coletivo/aprovar"), admHidden("collective_id", w.caioNew.ID))
	wantAll(t, "Caio's new collective", colRow(t, caio, "coletivo novo: Gama Nova"), `action="/admin/coletivo/recusar"`, "[→] APROVAR", "[✕] RECUSAR")
	join := colRow(t, caio, "Delta Lar · Administrador")
	wantAll(t, "Caio's join", admForm(join, "/admin/pedido/aprovar"), admHidden("user_id", w.caio.ID), admHidden("collective_id", w.caioJoin.ID))
	wantAll(t, "Caio's join", join, `action="/admin/pedido/recusar"`)

	// No id is ever a part of a path.
	for _, id := range []string{w.paula.ID, w.caio.ID, w.paulaNew.ID, w.paulaJoin.ID, w.caioNew.ID, w.caioJoin.ID} {
		if strings.Contains(body, "/"+id) {
			t.Errorf("the page puts the id %s in a path", id)
		}
	}
}

func TestAdminPageMarkup(t *testing.T) {
	w := newAdmWorld(t)
	pages := map[string]string{
		"the queue":    w.get("/admin").Body.String(),
		"a notice":     w.get("/admin?msg=conta-aprovada").Body.String(),
		"an alert":     w.post("/admin/conta/recusar", url.Values{"user_id": {w.paula.ID}, "reason": {"a\x07b"}}).Body.String(),
		"a 409 answer": w.post("/admin/pedido/aprovar", url.Values{"user_id": {w.paula.ID}, "collective_id": {w.paulaJoin.ID}}).Body.String(),
	}
	for name, body := range pages {
		t.Run(name, func(t *testing.T) {
			if v := cspViolations(body); len(v) > 0 {
				t.Errorf("%s in:\n%s", strings.Join(v, "; "), body)
			}
			// Many reject forms share a page, so their ids must not repeat, and every
			// label points at a control.
			ids := map[string]bool{}
			for _, m := range admIDAttr.FindAllStringSubmatch(body, -1) {
				if ids[m[1]] {
					t.Errorf("id=%q is used twice", m[1])
				}
				ids[m[1]] = true
			}
			for _, m := range admForAttr.FindAllStringSubmatch(body, -1) {
				if !ids[m[1]] {
					t.Errorf(`for=%q points at no id`, m[1])
				}
			}
			if !strings.Contains(body, admCardRoot) {
				t.Error("the page shows no card")
			}
		})
	}
}

func TestAdminNavMarksTheCurrentPage(t *testing.T) {
	links := []struct{ key, href, label string }{
		{"fila", "/admin", "Fila"},
		{"contas", "/admin/contas", "Contas"},
		{"coletivos", "/admin/coletivos", "Coletivos"},
		{"historico", "/admin/historico", "Histórico"},
	}
	app, _ := newStoreApp(t)
	for _, cur := range links {
		t.Run(cur.key, func(t *testing.T) {
			c, rec := newCtx()
			app.Render.Partial(c, http.StatusOK, "admin-nav", cur.key)
			nav := rec.Body.String()
			if got := strings.Count(nav, `aria-current="page"`); got != 1 {
				t.Errorf("%d aria-current in the nav, want 1:\n%s", got, nav)
			}
			for _, l := range links {
				want := `<a href="` + l.href + `">` + l.label + `</a>`
				if l.key == cur.key {
					want = `<a href="` + l.href + `" aria-current="page">` + l.label + `</a>`
				}
				if !strings.Contains(nav, want) {
					t.Errorf("nav for %q lacks %s:\n%s", cur.key, want, nav)
				}
			}
		})
	}

	// The queue page uses it with Fila current. The sidebar of the layout marks
	// Admin too, so the nav is looked at on its own.
	w := newAdmWorld(t)
	body := w.get("/admin").Body.String()
	_, rest, found := strings.Cut(body, `<nav class="admin-nav"`)
	if !found {
		t.Fatalf("the queue has no admin nav in:\n%s", body)
	}
	nav, _, _ := strings.Cut(rest, "</nav>")
	if !strings.Contains(nav, `<a href="/admin" aria-current="page">Fila</a>`) || strings.Count(nav, `aria-current="page"`) != 1 {
		t.Errorf("the queue's nav does not mark Fila alone as current:\n%s", nav)
	}
}

func TestAdminNoticesComeFromFixedCodes(t *testing.T) {
	w := newAdmWorld(t)
	for _, n := range admNotices {
		body := w.get("/admin?msg=" + n.code).Body.String()
		wantAll(t, "?msg="+n.code, body, `<p class="status status--ok" role="status">`+n.text+`</p>`)
	}
	for _, code := range []string{"nope", "", "<b>x</b>", "conta-aprovada%3Cb%3E", "Conta-Aprovada"} {
		body := w.get("/admin?msg=" + code).Body.String()
		if strings.Contains(body, "status--ok") || strings.Contains(body, "role=\"status\"") {
			t.Errorf("?msg=%s shows a notice", code)
		}
		if strings.Contains(body, "&lt;b&gt;") || strings.Contains(body, "<b>x") {
			t.Errorf("?msg=%s is echoed back", code)
		}
	}
}

func TestAdminDecisions(t *testing.T) {
	cases := []struct {
		path, code string
		form       func(w admWorld) url.Values
		check      func(t *testing.T, w admWorld)
	}{
		{"/admin/pessoa/aprovar-tudo", "pessoa-aprovada",
			func(w admWorld) url.Values { return url.Values{"user_id": {w.paula.ID}} },
			func(t *testing.T, w admWorld) {
				if u, err := w.user(t, w.paula.ID); err != nil || u.Status != domain.UserStatusActive {
					t.Errorf("Paula = %+v, %v; want her account active", u, err)
				}
				if c, err := w.collective(t, w.paulaNew.ID); err != nil || c.Status != domain.CollectiveStatusApproved {
					t.Errorf("Alfa Nova = %+v, %v; want it approved", c, err)
				}
				if m, err := w.membership(t, w.paula.ID, w.paulaJoin.ID); err != nil || m.AdminApprovedAt == nil {
					t.Errorf("request to Beta Casa = %+v, %v; want its admin side approved", m, err)
				}
			}},
		{"/admin/conta/aprovar", "conta-aprovada",
			func(w admWorld) url.Values { return url.Values{"user_id": {w.paula.ID}} },
			func(t *testing.T, w admWorld) {
				if u, err := w.user(t, w.paula.ID); err != nil || u.Status != domain.UserStatusActive {
					t.Errorf("Paula = %+v, %v; want her account active", u, err)
				}
				if c, err := w.collective(t, w.paulaNew.ID); err != nil || c.Status != domain.CollectiveStatusPending {
					t.Errorf("Alfa Nova = %+v, %v; want it still pending: only the account was approved", c, err)
				}
			}},
		{"/admin/conta/recusar", "conta-recusada",
			func(w admWorld) url.Values {
				return url.Values{"user_id": {w.paula.ID}, "reason": {"dados não conferem"}}
			},
			func(t *testing.T, w admWorld) {
				if _, err := w.user(t, w.paula.ID); !errors.Is(err, domain.ErrNotFound) {
					t.Errorf("Paula's account after the rejection: err = %v, want ErrNotFound", err)
				}
			}},
		{"/admin/coletivo/aprovar", "coletivo-aprovado",
			func(w admWorld) url.Values { return url.Values{"collective_id": {w.caioNew.ID}} },
			func(t *testing.T, w admWorld) {
				if c, err := w.collective(t, w.caioNew.ID); err != nil || c.Status != domain.CollectiveStatusApproved {
					t.Errorf("Gama Nova = %+v, %v; want it approved", c, err)
				}
			}},
		{"/admin/coletivo/recusar", "coletivo-recusado",
			func(w admWorld) url.Values {
				return url.Values{"collective_id": {w.caioNew.ID}, "reason": {"já existe"}}
			},
			func(t *testing.T, w admWorld) {
				if _, err := w.collective(t, w.caioNew.ID); !errors.Is(err, domain.ErrNotFound) {
					t.Errorf("Gama Nova after the rejection: err = %v, want ErrNotFound", err)
				}
			}},
		{"/admin/pedido/aprovar", "pedido-aprovado",
			func(w admWorld) url.Values {
				return url.Values{"user_id": {w.caio.ID}, "collective_id": {w.caioJoin.ID}}
			},
			func(t *testing.T, w admWorld) {
				// An Administrador had approved it already, so the admin's decision completes it.
				if m, err := w.membership(t, w.caio.ID, w.caioJoin.ID); err != nil || m.AdminApprovedAt == nil || m.Status != domain.MembershipStatusApproved {
					t.Errorf("request to Delta Lar = %+v, %v; want it approved", m, err)
				}
			}},
		{"/admin/pedido/recusar", "pedido-recusado",
			func(w admWorld) url.Values {
				return url.Values{"user_id": {w.caio.ID}, "collective_id": {w.caioJoin.ID}, "reason": {"não conheço"}}
			},
			func(t *testing.T, w admWorld) {
				if _, err := w.membership(t, w.caio.ID, w.caioJoin.ID); !errors.Is(err, domain.ErrNotFound) {
					t.Errorf("request to Delta Lar after the rejection: err = %v, want ErrNotFound", err)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			w := newAdmWorld(t)
			rec := w.post(tc.path, tc.form(w))
			if want := "/admin?msg=" + tc.code; rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != want {
				t.Fatalf("POST %s: %d, Location %q; want 303 to %s; body %q", tc.path, rec.Code, rec.Header().Get("Location"), want, rec.Body)
			}
			tc.check(t, w)

			// The page the redirect leads to says what happened.
			page := w.get(rec.Header().Get("Location")).Body.String()
			for _, n := range admNotices {
				if n.code == tc.code && !strings.Contains(page, n.text) {
					t.Errorf("the page after %s lacks the notice %q", tc.path, n.text)
				}
			}
		})
	}
}

func TestAdminDecisionsOfAnHTMXRequestRedirectWithAHeader(t *testing.T) {
	w := newAdmWorld(t)
	rec := w.post("/admin/conta/aprovar", url.Values{"user_id": {w.paula.ID}}, header("HX-Request", "true"))
	if rec.Code != http.StatusNoContent || rec.Header().Get("HX-Redirect") != "/admin?msg=conta-aprovada" {
		t.Errorf("HTMX POST: %d, HX-Redirect %q; want 204 with /admin?msg=conta-aprovada", rec.Code, rec.Header().Get("HX-Redirect"))
	}
}

func TestAdminASecondDecisionAnswers409WithAFreshQueue(t *testing.T) {
	w := newAdmWorld(t)
	form := url.Values{"collective_id": {w.caioNew.ID}}
	if rec := w.post("/admin/coletivo/aprovar", form); rec.Code != http.StatusSeeOther {
		t.Fatalf("first approval: %d, want 303; body %q", rec.Code, rec.Body)
	}

	rec := w.post("/admin/coletivo/aprovar", form)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second approval: %d, want 409; body %q", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	wantAll(t, "the 409 page", body, `<p class="status status--error" role="alert">`+approvalsMsgDecided+`</p>`, `<nav class="admin-nav"`)
	card := admCard(t, body, "Caio Ativo")
	for _, row := range colRows(card) {
		if strings.Contains(row, "Gama Nova") {
			t.Errorf("the queue still lists the decided collective:\n%s", row)
		}
	}
	if !strings.Contains(card, "Delta Lar · Administrador") {
		t.Error("the fresh queue lost Caio's other request")
	}
}

func TestAdminRefusesWhatTheServerRefuses(t *testing.T) {
	w := newAdmWorld(t)

	// A forged post that approves one request of a person whose account still
	// awaits approval: the page offers no such button, and the server refuses.
	for target, form := range map[string]url.Values{
		"/admin/coletivo/aprovar": {"collective_id": {w.paulaNew.ID}},
		"/admin/pedido/aprovar":   {"user_id": {w.paula.ID}, "collective_id": {w.paulaJoin.ID}},
	} {
		rec := w.post(target, form)
		if rec.Code != http.StatusForbidden {
			t.Errorf("POST %s for a pending person = %d, want 403", target, rec.Code)
		}
		// The 403 of the handler shows the queue; the middleware's is a bare line.
		wantAll(t, "the answer to "+target, rec.Body.String(), approvalsMsgNotAllowed, `<nav class="admin-nav"`)
	}
	w.requireUntouched(t)

	// An id that names nothing, or no id at all.
	for _, form := range []url.Values{{"user_id": {"nope"}}, {"user_id": {""}}, {}} {
		rec := w.post("/admin/conta/aprovar", form)
		if rec.Code != http.StatusNotFound {
			t.Errorf("POST /admin/conta/aprovar with %v = %d, want 404", form, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), approvalsMsgNotFound) {
			t.Errorf("POST /admin/conta/aprovar with %v: no %q in the answer", form, approvalsMsgNotFound)
		}
	}
	w.requireUntouched(t)

	// A body that cannot be read.
	rec := post(w.r, "/admin/conta/aprovar", "user_id=%zz", withCookie(w.cookie))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a malformed body = %d, want 400", rec.Code)
	}
}

func TestAdminAReasonWithAControlCharacterIs422(t *testing.T) {
	for _, target := range []string{"/admin/conta/recusar", "/admin/coletivo/recusar", "/admin/pedido/recusar"} {
		t.Run(target, func(t *testing.T) {
			w := newAdmWorld(t)
			form := w.posts()[target]
			form.Set("reason", "a\x07b")

			rec := w.post(target, form)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body %q", rec.Code, rec.Body)
			}
			wantAll(t, "the 422 page", rec.Body.String(), `<p class="status status--error" role="alert">`+admReasonText+`</p>`, `<nav class="admin-nav"`)
			w.requireUntouched(t)
		})
	}
}
