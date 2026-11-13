package web

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
	webfs "github.com/magalz/agenda-clubber-lite/web"
)

// Messages the /coletivos page and its two forms show (see routes_collectives.go).
const (
	colEmptyText    = "você ainda não faz parte de nenhum coletivo ou produtora"
	colMsgJoin      = "pedido enviado. você recebe um e-mail quando ele for decidido."
	colMsgNew       = "pedido de coletivo novo enviado ao admin."
	colPickOne      = "escolha um coletivo da lista"
	colNoCollective = "coletivo não encontrado"
	colAlreadyIn    = "você já faz parte deste coletivo ou já pediu para entrar"
	colNeedRole     = "escolha membro ou administrador"
	colNameExists   = "já existe; peça para entrar"
	colNameInUse    = "nome já em uso; escolha outro"
	colNameLength   = "use de 2 a 80 caracteres"
	colNeedKind     = "escolha coletivo ou produtora"
	colLocked       = "este coletivo não está aceitando pedidos agora."
	colInternal     = "erro interno"
)

// colEnv is an App on a database of its own and a signed-in person.
type colEnv struct {
	app    *App
	store  domain.Store
	r      http.Handler
	user   domain.User
	cookie *http.Cookie
}

func newColEnv(t *testing.T) colEnv {
	t.Helper()
	app, store := newStoreApp(t)
	user := insertWebUser(t, store, "ana@example.com")
	return colEnv{app: app, store: store, r: app.Router(), user: user, cookie: loginAs(t, app, user)}
}

func (e colEnv) get(target string, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	return serve(e.r, http.MethodGet, target, append([]func(*http.Request){withCookie(e.cookie)}, mods...)...)
}

func (e colEnv) post(target string, form url.Values, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	return post(e.r, target, form.Encode(), append([]func(*http.Request){withCookie(e.cookie)}, mods...)...)
}

// memberships lists the signed-in person's memberships.
func (e colEnv) memberships(t *testing.T) []domain.MembershipView {
	t.Helper()
	list, err := e.store.Repos().Memberships.ListByUser(context.Background(), e.user.ID)
	if err != nil {
		t.Fatalf("list memberships: %v", err)
	}
	return list
}

func colCollective(t *testing.T, store domain.Store, name string, kind domain.CollectiveKind, status domain.CollectiveStatus, mutate ...func(*domain.Collective)) domain.Collective {
	t.Helper()
	c := domain.Collective{
		Name: name, NameKey: domain.NameKey(name), Kind: kind, Status: status,
		CreatedAt: webTestNow, UpdatedAt: webTestNow,
	}
	for _, f := range mutate {
		f(&c)
	}
	if err := store.Repos().Collectives.Create(context.Background(), &c); err != nil {
		t.Fatalf("insert collective %q: %v", name, err)
	}
	return c
}

func colMembership(t *testing.T, store domain.Store, userID, collectiveID string, role domain.Role, status domain.MembershipStatus, mutate ...func(*domain.Membership)) {
	t.Helper()
	m := domain.Membership{UserID: userID, CollectiveID: collectiveID, Role: role, Status: status, RequestedAt: webTestNow}
	for _, f := range mutate {
		f(&m)
	}
	if err := store.Repos().Memberships.Create(context.Background(), m); err != nil {
		t.Fatalf("insert membership: %v", err)
	}
}

func colCreatedBy(id string) func(*domain.Collective) {
	return func(c *domain.Collective) { c.CreatedBy = id }
}

// colSide marks one side of a pending membership as approved: the site admin's
// (admin true) or the collective's Administrador's, by the user with ID by.
func colSide(admin bool, by string) func(*domain.Membership) {
	return func(m *domain.Membership) {
		at := webTestNow
		if admin {
			m.AdminApprovedAt, m.AdminApprovedBy = &at, by
			return
		}
		m.OwnerApprovedAt, m.OwnerApprovedBy = &at, by
	}
}

var (
	colTitleRE  = regexp.MustCompile(`class="item-list__title">([^<]*)<`)
	colMetaRE   = regexp.MustCompile(`class="item-list__meta">([^<]*)<`)
	colStatusRE = regexp.MustCompile(`class="[^"]*item-list__status[^"]*"[^>]*>([^<]*)<`)
)

// colRows returns the markup of every list item, in page order.
func colRows(body string) []string {
	var rows []string
	for _, chunk := range strings.Split(body, `class="item-list__item"`)[1:] {
		row, _, _ := strings.Cut(chunk, "</li>")
		rows = append(rows, row)
	}
	return rows
}

// colRow returns the list item titled name.
func colRow(t *testing.T, body, name string) string {
	t.Helper()
	for _, row := range colRows(body) {
		if m := colTitleRE.FindStringSubmatch(row); m != nil && m[1] == name {
			return row
		}
	}
	t.Fatalf("no list item titled %q in:\n%s", name, body)
	return ""
}

func colField(t *testing.T, row string, re *regexp.Regexp, what string) string {
	t.Helper()
	m := re.FindStringSubmatch(row)
	if m == nil {
		t.Fatalf("no %s in the row:\n%s", what, row)
	}
	return strings.TrimSpace(m[1])
}

// colErrorMsg returns the text of the field error with the given id, or "".
func colErrorMsg(body, field string) string {
	_, rest, found := strings.Cut(body, `id="erro-`+field+`">`)
	if !found {
		return ""
	}
	msg, _, _ := strings.Cut(rest, "</p>")
	return msg
}

func colWantError(t *testing.T, body, field, want string) {
	t.Helper()
	if got := colErrorMsg(body, field); got != want {
		t.Errorf("error under %q = %q, want %q", field, got, want)
	}
}

func TestColetivosNeedsASession(t *testing.T) {
	r := newTestApp(t).Router()

	rec := serve(r, http.MethodGet, "/coletivos")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/entrar?next=%2Fcoletivos" {
		t.Errorf("GET /coletivos: %d, Location %q; want 303 to /entrar?next=%%2Fcoletivos", rec.Code, rec.Header().Get("Location"))
	}
	for _, target := range []string{"/coletivos/entrar", "/coletivos/novo"} {
		rec := post(r, target, "name=x")
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/entrar" {
			t.Errorf("POST %s: %d, Location %q; want 303 to /entrar", target, rec.Code, rec.Header().Get("Location"))
		}
	}
}

func TestColetivosEmptyPage(t *testing.T) {
	e := newColEnv(t)

	rec := e.get("/coletivos")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %q", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	body := rec.Body.String()
	wantAll(t, "page", body,
		"<title>Meus coletivos · AGENDA CLUBBER</title>",
		`href="/coletivos" aria-current="page"`,
		colEmptyText,
		`<form method="post" action="/coletivos/entrar"`,
		`<form method="post" action="/coletivos/novo"`,
	)
	if rows := colRows(body); len(rows) != 0 {
		t.Errorf("%d list items for a person with no membership, want none", len(rows))
	}
	for _, text := range []string{colMsgJoin, colMsgNew, "status--ok", "status--error"} {
		if strings.Contains(body, text) {
			t.Errorf("the page shows %q without a message or an error", text)
		}
	}
}

func TestColetivosListsTheMemberships(t *testing.T) {
	e := newColEnv(t)
	other := insertWebUser(t, e.store, "bia@example.com")
	me := e.user.ID

	alfa := colCollective(t, e.store, "Alfa Casa", domain.CollectiveKindColetivo, domain.CollectiveStatusApproved, colCreatedBy(other.ID))
	colMembership(t, e.store, me, alfa.ID, domain.RoleMember, domain.MembershipStatusApproved)
	beta := colCollective(t, e.store, "Beta Studio", domain.CollectiveKindProdutora, domain.CollectiveStatusApproved, colCreatedBy(other.ID))
	colMembership(t, e.store, me, beta.ID, domain.RoleOwner, domain.MembershipStatusApproved)
	// A collective the person asked for: pending, with the owner side of their own membership already set.
	delta := colCollective(t, e.store, "Delta Estreia", domain.CollectiveKindColetivo, domain.CollectiveStatusPending, colCreatedBy(me))
	colMembership(t, e.store, me, delta.ID, domain.RoleOwner, domain.MembershipStatusPending, colSide(false, other.ID))
	epsilon := colCollective(t, e.store, "Epsilon Lado Aberto", domain.CollectiveKindColetivo, domain.CollectiveStatusApproved, colCreatedBy(other.ID))
	colMembership(t, e.store, me, epsilon.ID, domain.RoleOwner, domain.MembershipStatusPending)
	gama := colCollective(t, e.store, "Gama Chefe", domain.CollectiveKindColetivo, domain.CollectiveStatusApproved, colCreatedBy(me))
	colMembership(t, e.store, me, gama.ID, domain.RoleOwner, domain.MembershipStatusApproved)
	kappa := colCollective(t, e.store, "Kappa Metade A", domain.CollectiveKindColetivo, domain.CollectiveStatusApproved, colCreatedBy(other.ID))
	colMembership(t, e.store, me, kappa.ID, domain.RoleMember, domain.MembershipStatusPending, colSide(true, other.ID))
	omega := colCollective(t, e.store, "Omega Metade B", domain.CollectiveKindColetivo, domain.CollectiveStatusApproved, colCreatedBy(other.ID))
	colMembership(t, e.store, me, omega.ID, domain.RoleMember, domain.MembershipStatusPending, colSide(false, other.ID))
	zeta := colCollective(t, e.store, "Zeta Fora", domain.CollectiveKindColetivo, domain.CollectiveStatusApproved, colCreatedBy(other.ID))
	colMembership(t, e.store, me, zeta.ID, domain.RoleMember, domain.MembershipStatusRejected)
	// Someone else's membership never shows.
	stranger := colCollective(t, e.store, "Estranho Total", domain.CollectiveKindColetivo, domain.CollectiveStatusApproved)
	colMembership(t, e.store, other.ID, stranger.ID, domain.RoleOwner, domain.MembershipStatusApproved)

	rec := e.get("/coletivos")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %q", rec.Code, rec.Body)
	}
	body := rec.Body.String()

	var titles []string
	for _, row := range colRows(body) {
		titles = append(titles, colField(t, row, colTitleRE, "title"))
	}
	wantTitles := "Alfa Casa|Beta Studio|Delta Estreia|Epsilon Lado Aberto|Gama Chefe|Kappa Metade A|Omega Metade B|Zeta Fora"
	if got := strings.Join(titles, "|"); got != wantTitles {
		t.Errorf("titles = %q, want %q (by name, only the person's)", got, wantTitles)
	}
	if strings.Contains(body, colEmptyText) {
		t.Error("the empty state shows next to memberships")
	}

	tests := []struct {
		name     string
		id       string
		meta     string
		status   string
		wantLink bool
	}{
		{"Alfa Casa", alfa.ID, "Coletivo · Membro", "aprovado", false},
		{"Beta Studio", beta.ID, "Produtora · Administrador", "aprovado", true},
		{"Delta Estreia", delta.ID, "Coletivo · Master", "aguardando o admin (coletivo novo)", false},
		{"Epsilon Lado Aberto", epsilon.ID, "Coletivo · Administrador", "aguardando o admin e um administrador", false},
		{"Gama Chefe", gama.ID, "Coletivo · Master", "aprovado", true},
		{"Kappa Metade A", kappa.ID, "Coletivo · Membro", "aguardando um administrador do coletivo", false},
		{"Omega Metade B", omega.ID, "Coletivo · Membro", "aguardando o admin", false},
		{"Zeta Fora", zeta.ID, "Coletivo · Membro", "recusado", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := colRow(t, body, tt.name)
			if got := colField(t, row, colMetaRE, "meta line"); got != tt.meta {
				t.Errorf("meta = %q, want %q", got, tt.meta)
			}
			if got := colField(t, row, colStatusRE, "status badge"); got != tt.status {
				t.Errorf("status = %q, want %q", got, tt.status)
			}
			link := `href="/coletivo/` + tt.id + `"`
			if got := strings.Contains(row, link); got != tt.wantLink {
				t.Errorf("row has the link %s = %v, want %v", link, got, tt.wantLink)
			}
		})
	}
}

func TestColetivosMessageCodes(t *testing.T) {
	e := newColEnv(t)

	tests := []struct {
		query string
		want  string // "" for no message at all
	}{
		{"?msg=pedido-enviado", colMsgJoin},
		{"?msg=coletivo-pedido", colMsgNew},
		{"?msg=zzmarker", ""},
		{"?msg=%3Cb%3Ezzmarker", ""},
		{"?msg=", ""},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			rec := e.get("/coletivos" + tt.query)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			body := rec.Body.String()
			if tt.want != "" {
				wantAll(t, "page", body, `class="status status--ok"`, `role="status"`, tt.want)
				return
			}
			for _, leak := range []string{"status--ok", "zzmarker", "<b>", "&lt;b&gt;"} {
				if strings.Contains(body, leak) {
					t.Errorf("the page shows %q for an unknown message code", leak)
				}
			}
		})
	}
}

func TestColetivosJoin(t *testing.T) {
	for _, role := range []domain.Role{domain.RoleMember, domain.RoleOwner} {
		t.Run(string(role), func(t *testing.T) {
			e := newColEnv(t)
			c := colCollective(t, e.store, "Casa Verde", domain.CollectiveKindColetivo, domain.CollectiveStatusApproved)

			rec := e.post("/coletivos/entrar", url.Values{"collective_name": {"Casa Verde"}, "collective_id": {c.ID}, "role": {string(role)}})
			if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/coletivos?msg=pedido-enviado" {
				t.Fatalf("POST: %d, Location %q; want 303 to /coletivos?msg=pedido-enviado; body %q", rec.Code, rec.Header().Get("Location"), rec.Body)
			}
			list := e.memberships(t)
			if len(list) != 1 || list[0].CollectiveID != c.ID || list[0].Role != role || list[0].Status != domain.MembershipStatusPending {
				t.Fatalf("memberships = %+v, want one pending %s membership in %s", list, role, c.ID)
			}

			// The page the redirect leads to shows the request and the message.
			body := e.get(rec.Header().Get("Location")).Body.String()
			wantAll(t, "page", body, colMsgJoin)
			row := colRow(t, body, "Casa Verde")
			if got := colField(t, row, colStatusRE, "status badge"); got != "aguardando o admin e um administrador" {
				t.Errorf("status = %q, want the request waiting on both sides", got)
			}
		})
	}

	t.Run("htmx", func(t *testing.T) {
		e := newColEnv(t)
		c := colCollective(t, e.store, "Casa Verde", domain.CollectiveKindColetivo, domain.CollectiveStatusApproved)
		rec := e.post("/coletivos/entrar", url.Values{"collective_id": {c.ID}, "role": {"member"}}, hx)
		if rec.Code != http.StatusNoContent || rec.Header().Get("HX-Redirect") != "/coletivos?msg=pedido-enviado" {
			t.Errorf("POST: %d, HX-Redirect %q; want 204 with HX-Redirect /coletivos?msg=pedido-enviado", rec.Code, rec.Header().Get("HX-Redirect"))
		}
		if n := len(e.memberships(t)); n != 1 {
			t.Errorf("%d memberships, want 1", n)
		}
	})
}

func TestColetivosJoinFieldErrors(t *testing.T) {
	tests := []struct {
		name    string
		form    func(t *testing.T, e colEnv) url.Values
		field   string
		message string
		kept    []string // what the re-rendered form must still carry
	}{
		{
			"no collective picked", func(*testing.T, colEnv) url.Values {
				return url.Values{"collective_name": {"Casa Verde"}, "collective_id": {""}, "role": {"member"}}
			},
			"collective", colPickOne, []string{`value="Casa Verde"`, `value="member" checked`},
		},
		{
			"an id that is no id", func(*testing.T, colEnv) url.Values {
				return url.Values{"collective_name": {"Casa Verde"}, "collective_id": {"not-an-id"}, "role": {"member"}}
			},
			"collective", colNoCollective, []string{`value="Casa Verde"`},
		},
		{
			"a collective that is not approved", func(t *testing.T, e colEnv) url.Values {
				c := colCollective(t, e.store, "Casa Nova", domain.CollectiveKindColetivo, domain.CollectiveStatusPending)
				return url.Values{"collective_name": {"Casa Nova"}, "collective_id": {c.ID}, "role": {"member"}}
			},
			"collective", colNoCollective, []string{`value="Casa Nova"`},
		},
		{
			"asking twice", func(t *testing.T, e colEnv) url.Values {
				c := colCollective(t, e.store, "Casa Verde", domain.CollectiveKindColetivo, domain.CollectiveStatusApproved)
				colMembership(t, e.store, e.user.ID, c.ID, domain.RoleMember, domain.MembershipStatusRejected)
				return url.Values{"collective_name": {"Casa Verde"}, "collective_id": {c.ID}, "role": {"owner"}}
			},
			"collective", colAlreadyIn, []string{`value="Casa Verde"`, `value="owner" checked`},
		},
		{
			"no role", func(t *testing.T, e colEnv) url.Values {
				c := colCollective(t, e.store, "Casa Verde", domain.CollectiveKindColetivo, domain.CollectiveStatusApproved)
				return url.Values{"collective_name": {"Casa Verde"}, "collective_id": {c.ID}}
			},
			"role", colNeedRole, []string{`value="Casa Verde"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newColEnv(t)
			form := tt.form(t, e)
			before := len(e.memberships(t))

			rec := e.post("/coletivos/entrar", form)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body %q", rec.Code, rec.Body)
			}
			body := rec.Body.String()
			colWantError(t, body, tt.field, tt.message)
			wantAll(t, "page", body, tt.kept...)
			wantAll(t, "page", body, `action="/coletivos/entrar"`, `action="/coletivos/novo"`)
			if after := len(e.memberships(t)); after != before {
				t.Errorf("%d memberships after the refused request, want %d", after, before)
			}
		})
	}
}

func TestColetivosJoinLockedCollective(t *testing.T) {
	e := newColEnv(t)
	mine := colCollective(t, e.store, "Alfa Casa", domain.CollectiveKindColetivo, domain.CollectiveStatusApproved)
	colMembership(t, e.store, e.user.ID, mine.ID, domain.RoleMember, domain.MembershipStatusApproved)
	locked := colCollective(t, e.store, "Casa Trancada", domain.CollectiveKindColetivo, domain.CollectiveStatusApproved,
		func(c *domain.Collective) { c.Locked = true })

	rec := e.post("/coletivos/entrar", url.Values{"collective_name": {"Casa Trancada"}, "collective_id": {locked.ID}, "role": {"member"}})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body %q", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	wantAll(t, "page", body, `class="status status--error"`, `role="alert"`, colLocked)
	colRow(t, body, "Alfa Casa") // the page still lists the person's collectives
	if n := len(e.memberships(t)); n != 1 {
		t.Errorf("%d memberships, want only the first", n)
	}
}

func TestColetivosNew(t *testing.T) {
	tests := []struct {
		kind  domain.CollectiveKind
		label string
	}{
		{domain.CollectiveKindColetivo, "Coletivo"},
		{domain.CollectiveKindProdutora, "Produtora"},
	}
	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			e := newColEnv(t)
			rec := e.post("/coletivos/novo", url.Values{"name": {"Nova Casa"}, "kind": {string(tt.kind)}})
			if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/coletivos?msg=coletivo-pedido" {
				t.Fatalf("POST: %d, Location %q; want 303 to /coletivos?msg=coletivo-pedido; body %q", rec.Code, rec.Header().Get("Location"), rec.Body)
			}

			body := e.get(rec.Header().Get("Location")).Body.String()
			wantAll(t, "page", body, colMsgNew)
			row := colRow(t, body, "Nova Casa")
			if got := colField(t, row, colMetaRE, "meta line"); got != tt.label+" · Master" {
				t.Errorf("meta = %q, want %q", got, tt.label+" · Master")
			}
			if got := colField(t, row, colStatusRE, "status badge"); got != "aguardando o admin (coletivo novo)" {
				t.Errorf("status = %q, want the new collective waiting on the admin", got)
			}
			if strings.Contains(row, `href="/coletivo/`) {
				t.Error("a collective the admin has not approved links to its page")
			}
		})
	}

	t.Run("htmx", func(t *testing.T) {
		e := newColEnv(t)
		rec := e.post("/coletivos/novo", url.Values{"name": {"Nova Casa"}, "kind": {"coletivo"}}, hx)
		if rec.Code != http.StatusNoContent || rec.Header().Get("HX-Redirect") != "/coletivos?msg=coletivo-pedido" {
			t.Errorf("POST: %d, HX-Redirect %q; want 204 with HX-Redirect /coletivos?msg=coletivo-pedido", rec.Code, rec.Header().Get("HX-Redirect"))
		}
	})
}

func TestColetivosNewFieldErrors(t *testing.T) {
	t.Run("nothing filled", func(t *testing.T) {
		e := newColEnv(t)
		rec := e.post("/coletivos/novo", url.Values{"name": {""}, "kind": {""}})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422; body %q", rec.Code, rec.Body)
		}
		body := rec.Body.String()
		colWantError(t, body, "name", colNameLength)
		colWantError(t, body, "kind", colNeedKind)
		if n := len(e.memberships(t)); n != 0 {
			t.Errorf("%d memberships, want none", n)
		}
	})

	t.Run("an approved name asks to join instead", func(t *testing.T) {
		e := newColEnv(t)
		colCollective(t, e.store, "Casa Verde", domain.CollectiveKindColetivo, domain.CollectiveStatusApproved)
		rec := e.post("/coletivos/novo", url.Values{"name": {"casa  verde"}, "kind": {"produtora"}})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422; body %q", rec.Code, rec.Body)
		}
		body := rec.Body.String()
		colWantError(t, body, "name", colNameExists)
		wantAll(t, "page", body, `value="casa  verde"`, `value="produtora" checked`)
		if n := len(e.memberships(t)); n != 0 {
			t.Errorf("%d memberships, want none", n)
		}
	})

	t.Run("a pending name is taken", func(t *testing.T) {
		e := newColEnv(t)
		colCollective(t, e.store, "Casa Nova", domain.CollectiveKindColetivo, domain.CollectiveStatusPending)
		rec := e.post("/coletivos/novo", url.Values{"name": {"Casa Nova"}, "kind": {"coletivo"}})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422; body %q", rec.Code, rec.Body)
		}
		colWantError(t, rec.Body.String(), "name", colNameInUse)
	})
}

func TestColetivosRateLimit(t *testing.T) {
	validJoin := func(t *testing.T, e colEnv) url.Values {
		c := colCollective(t, e.store, "Casa Verde", domain.CollectiveKindColetivo, domain.CollectiveStatusApproved)
		return url.Values{"collective_id": {c.ID}, "role": {"member"}}
	}
	t.Run("counts every post, valid or not, and refuses the eleventh", func(t *testing.T) {
		e := newColEnv(t)
		for i := range 10 {
			if rec := e.post("/coletivos/entrar", url.Values{"collective_id": {""}}); rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("post %d: status = %d, want 422", i+1, rec.Code)
			}
		}
		rec := e.post("/coletivos/novo", url.Values{"name": {"Nova Casa"}, "kind": {"coletivo"}})
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("eleventh post: status = %d, want 429 (the two forms share one budget)", rec.Code)
		}
		wantAll(t, "page", rec.Body.String(), msgTooMany, `class="status status--error"`)
		if n := len(e.memberships(t)); n != 0 {
			t.Errorf("%d memberships, want none", n)
		}
	})

	for _, target := range []string{"/coletivos/entrar", "/coletivos/novo"} {
		t.Run("exhausted budget refuses "+target, func(t *testing.T) {
			e := newColEnv(t)
			form := validJoin(t, e)
			if target == "/coletivos/novo" {
				form = url.Values{"name": {"Nova Casa"}, "kind": {"coletivo"}}
			}
			for range 10 {
				e.app.Limits.Requests.Allow("request:"+e.user.ID, webTestNow)
			}
			rec := e.post(target, form)
			if rec.Code != http.StatusTooManyRequests {
				t.Fatalf("status = %d, want 429; body %q", rec.Code, rec.Body)
			}
			wantAll(t, "page", rec.Body.String(), msgTooMany, `action="/coletivos/entrar"`)
			if n := len(e.memberships(t)); n != 0 {
				t.Errorf("%d memberships, want none: nothing may be created over the limit", n)
			}
			// Reading the page is not counted.
			if got := e.get("/coletivos").Code; got != http.StatusOK {
				t.Errorf("GET after the limit: status = %d, want 200", got)
			}
		})
	}
}

func TestColetivosBadBody(t *testing.T) {
	e := newColEnv(t)
	for _, target := range []string{"/coletivos/entrar", "/coletivos/novo"} {
		rec := post(e.r, target, "name=%zz", withCookie(e.cookie))
		if rec.Code != http.StatusBadRequest || rec.Body.String() != "requisição inválida" {
			t.Errorf("POST %s: %d %q; want 400 requisição inválida", target, rec.Code, rec.Body)
		}
	}
	if n := len(e.memberships(t)); n != 0 {
		t.Errorf("%d memberships, want none", n)
	}
}

// colFailingStore is a store whose transactions fail, so a request that passed
// validation ends in a server error.
type colFailingStore struct{ domain.Store }

func (colFailingStore) WithTx(context.Context, func(context.Context, domain.Repos) error) error {
	return errors.New("the database is down")
}

func TestColetivosServerError(t *testing.T) {
	e := newColEnv(t)
	c := colCollective(t, e.store, "Casa Verde", domain.CollectiveKindColetivo, domain.CollectiveStatusApproved)
	e.app.Store = colFailingStore{e.store}
	logs := captureLogs(e.app)
	e.r = e.app.Router()

	tests := []struct {
		target string
		form   url.Values
		secret string // typed by the person, so it must not reach the log
	}{
		{"/coletivos/entrar", url.Values{"collective_name": {"Typed Secret Name"}, "collective_id": {c.ID}, "role": {"member"}}, c.ID},
		{"/coletivos/novo", url.Values{"name": {"Typed Secret Name"}, "kind": {"coletivo"}}, "Typed Secret Name"},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			logs.Reset()
			rec := e.post(tt.target, tt.form)
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
			for _, leak := range []string{tt.secret, "Typed Secret Name", "ana@example.com"} {
				if strings.Contains(logs.String(), leak) {
					t.Errorf("the log carries %q: %s", leak, logs)
				}
			}
		})
	}
}

func TestColetivosMarkup(t *testing.T) {
	e := newColEnv(t)
	alfa := colCollective(t, e.store, "Alfa Casa", domain.CollectiveKindColetivo, domain.CollectiveStatusApproved)
	colMembership(t, e.store, e.user.ID, alfa.ID, domain.RoleOwner, domain.MembershipStatusApproved)

	pages := map[string]string{
		"the page":       e.get("/coletivos").Body.String(),
		"a join error":   e.post("/coletivos/entrar", url.Values{"collective_id": {""}}).Body.String(),
		"a new error":    e.post("/coletivos/novo", url.Values{"name": {""}}).Body.String(),
		"a known notice": e.get("/coletivos?msg=pedido-enviado").Body.String(),
	}
	js := signupUIAppJS(t)
	for name, body := range pages {
		t.Run(name, func(t *testing.T) {
			if v := cspViolations(body); len(v) > 0 {
				t.Errorf("%s in:\n%s", strings.Join(v, "; "), body)
			}
			for _, a := range alpineAttr.FindAllStringSubmatch(body, -1) {
				if !alpineName.MatchString(a[2]) {
					t.Errorf("%s=%q is not a plain name: the Alpine CSP build cannot evaluate it", a[1], a[2])
				}
				if a[1] == "x-data" && !strings.Contains(js, "Alpine.data('"+a[2]+"'") {
					t.Errorf("x-data=%q is not registered with Alpine.data() in app.js", a[2])
				}
			}
			if got := strings.Count(body, `x-data="collectivePicker"`); got != 1 {
				t.Errorf(`%d x-data="collectivePicker", want 1`, got)
			}
			if !strings.Contains(body, `data-mode="join"`) {
				t.Error(`the picker lacks data-mode="join"`)
			}
			// The picker spans the join form's name, hidden id, suggestions and role radios; the new collective form follows it.
			inOrder(t, body,
				`action="/coletivos/entrar"`, `x-data="collectivePicker"`, `id="collective_name"`, `id="collective_id"`,
				`id="collective-suggestions-box"`, `name="role"`, `action="/coletivos/novo"`, `name="name"`, `name="kind"`)
			if got := strings.Count(body, `id="collective_id"`); got != 1 {
				t.Errorf(`%d id="collective_id", want 1`, got)
			}
			if !regexp.MustCompile(`<input type="hidden" id="collective_id" name="collective_id"`).MatchString(body) {
				t.Error("collective_id is not a hidden input")
			}
			// The join picker has no role hint to swap, no disabled Membro and no kind radios.
			for _, absent := range []string{`id="role-hint"`, "membroDisabled", "collective_kind", "collective-name-hint\">comece"} {
				if strings.Contains(body, absent) {
					t.Errorf("the page has %q, which belongs to the signup picker", absent)
				}
			}
		})
	}
}

func TestColetivosPickerJoinMode(t *testing.T) {
	js := signupUIAppJS(t)
	for _, want := range []string{"Alpine.data('collectivePicker'", "HINT_NEW"} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js lost %q", want)
		}
	}
	if !regexp.MustCompile(`dataset\.mode\s*===\s*'join'`).MatchString(js) {
		t.Error(`app.js does not read data-mode="join" on the picker root`)
	}
}

func TestColetivosComponentsAreStyled(t *testing.T) {
	css, err := fs.ReadFile(webfs.Static, "static/css/components.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, selector := range []string{
		".item-list", ".item-list__item", ".item-list__main", ".item-list__title", ".item-list__meta",
		".item-list__status", ".item-list__actions", ".reason-form", ".reason-form summary",
	} {
		if !strings.Contains(string(css), selector+" {") {
			t.Errorf("components.css has no rule for %s", selector)
		}
	}
	if !strings.Contains(string(css), ".reason-form summary:focus-visible") {
		t.Error("components.css has no focus ring for .reason-form summary")
	}
}
