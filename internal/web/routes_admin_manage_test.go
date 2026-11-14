package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// The management pages of the site admin (routes_admin_manage.go): accounts,
// collectives and their blocks, and the decision history. They share the /admin
// group with the queue, so the access tests below cover all three.

// The pages, and the notices each one shows for the ?msg= code of the redirect
// that follows a post (the URL never supplies text).
var (
	mngPages   = []string{"/admin/contas", "/admin/coletivos", "/admin/historico"}
	mngNotices = []struct{ page, code, text string }{
		{"/admin/contas", "conta-bloqueada", "conta bloqueada."},
		{"/admin/contas", "conta-desbloqueada", "conta desbloqueada."},
		{"/admin/coletivos", "bloqueios-salvos", "bloqueios salvos."},
	}
)

// mngActionLabels is the pt-BR label of each audit action, in the order of the
// action constants.
var mngActionLabels = []struct {
	action domain.AuditAction
	label  string
}{
	{domain.AuditAccountApproved, "conta aprovada"},
	{domain.AuditAccountRejected, "cadastro recusado"},
	{domain.AuditAccountBlocked, "conta bloqueada"},
	{domain.AuditAccountUnblocked, "conta desbloqueada"},
	{domain.AuditCollectiveRequested, "coletivo pedido"},
	{domain.AuditCollectiveApproved, "coletivo aprovado"},
	{domain.AuditCollectiveRejected, "coletivo recusado"},
	{domain.AuditCollectiveRenamed, "coletivo renomeado"},
	{domain.AuditCollectiveBlocks, "bloqueios do coletivo"},
	{domain.AuditJoinRequested, "pedido de entrada"},
	{domain.AuditJoinAdminApproved, "entrada aprovada pelo admin"},
	{domain.AuditJoinOwnerApproved, "entrada aprovada por administrador"},
	{domain.AuditJoinRejected, "entrada recusada"},
	{domain.AuditMemberPromoted, "promovido a administrador"},
	{domain.AuditMemberDemoted, "rebaixado a membro"},
}

// mngWorld is the admin (Admin Geral) plus four accounts and five collectives.
// Ana and Cris (also an admin) are active and Bia is blocked; Paula awaits
// approval, so she belongs to neither list. Of the collectives, Alfa Total has
// both blocks, Beta Eventos (a produtora) only the events block, Casa Congelada
// only the freeze and Delta Livre none; Gama Pendente is not approved yet.
type mngWorld struct {
	admEnv
	ana, bia, cris, paula         domain.User
	alfa, beta, casa, delta, gama domain.Collective
}

func newMngWorld(t *testing.T) mngWorld {
	t.Helper()
	e := newAdmEnv(t)
	store := e.store
	user := func(email, name string, minutes int, mutate func(*domain.User)) domain.User {
		return insertWebUser(t, store, email, func(u *domain.User) {
			u.Name, u.Phone = name, "81911112222"
			u.CreatedAt = webTestNow.Add(time.Duration(minutes) * time.Minute)
			mutate(u)
		})
	}
	w := mngWorld{admEnv: e}
	w.ana = user("ana@example.com", "Ana Ativa", 1, func(u *domain.User) { u.PhoneIsWhatsApp = true })
	w.cris = user("cris@example.com", "Cris Admin", 2, func(u *domain.User) { u.IsAdmin = true })
	w.bia = user("bia@example.com", "Bia Bloqueada", 3, func(u *domain.User) {
		u.Phone, u.Status = "81933334444", domain.UserStatusDisabled
	})
	w.paula = user("paula@example.com", "Paula Pendente", 4, func(u *domain.User) { u.Status = domain.UserStatusPendingApproval })

	collective := func(name string, kind domain.CollectiveKind, status domain.CollectiveStatus, events, locked bool) domain.Collective {
		return colCollective(t, store, name, kind, status, colCreatedBy(e.admin.ID), func(c *domain.Collective) {
			c.EventsBlocked, c.Locked = events, locked
			if status == domain.CollectiveStatusApproved {
				at := webTestNow
				c.ApprovedBy, c.ApprovedAt = e.admin.ID, &at
			}
		})
	}
	const approved, pending = domain.CollectiveStatusApproved, domain.CollectiveStatusPending
	w.alfa = collective("Alfa Total", domain.CollectiveKindColetivo, approved, true, true)
	w.beta = collective("Beta Eventos", domain.CollectiveKindProdutora, approved, true, false)
	w.casa = collective("Casa Congelada", domain.CollectiveKindColetivo, approved, false, true)
	w.delta = collective("Delta Livre", domain.CollectiveKindColetivo, approved, false, false)
	w.gama = collective("Gama Pendente", domain.CollectiveKindColetivo, pending, false, false)
	return w
}

// posts has one valid post for each action of the pages.
func (w mngWorld) posts() map[string]url.Values {
	return map[string]url.Values{
		"/admin/conta/bloquear":     {"user_id": {w.ana.ID}, "reason": {"motivo"}},
		"/admin/conta/desbloquear":  {"user_id": {w.bia.ID}},
		"/admin/coletivo/bloqueios": {"collective_id": {w.delta.ID}, "events_blocked": {"1"}, "locked": {"1"}, "reason": {"motivo"}},
	}
}

func (w mngWorld) audit(t *testing.T) []domain.AuditEntry {
	t.Helper()
	entries, err := w.store.Repos().AuditEntries.List(context.Background(), domain.ListLimitMax)
	if err != nil {
		t.Fatalf("list the audit log: %v", err)
	}
	return entries
}

func (w mngWorld) status(t *testing.T, u domain.User) domain.UserStatus {
	t.Helper()
	got, err := w.store.Repos().Users.ByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("read %s: %v", u.Name, err)
	}
	return got.Status
}

func (w mngWorld) collective(t *testing.T, c domain.Collective) domain.Collective {
	t.Helper()
	got, err := w.store.Repos().Collectives.ByID(context.Background(), c.ID)
	if err != nil {
		t.Fatalf("read %s: %v", c.Name, err)
	}
	return got
}

// requireUntouched fails unless every account and collective is as newMngWorld
// left it and the audit log is empty.
func (w mngWorld) requireUntouched(t *testing.T) {
	t.Helper()
	for _, u := range []domain.User{w.admin, w.ana, w.bia, w.cris, w.paula} {
		if got := w.status(t, u); got != u.Status {
			t.Errorf("status of %s = %q, want it untouched (%q)", u.Name, got, u.Status)
		}
	}
	for _, c := range []domain.Collective{w.alfa, w.beta, w.casa, w.delta, w.gama} {
		got := w.collective(t, c)
		if got.EventsBlocked != c.EventsBlocked || got.Locked != c.Locked || got.Status != c.Status {
			t.Errorf("%s = events %v, locked %v, status %q; want it untouched", c.Name, got.EventsBlocked, got.Locked, got.Status)
		}
	}
	if entries := w.audit(t); len(entries) != 0 {
		t.Errorf("the audit log has %d entries, want none: %+v", len(entries), entries)
	}
}

// mngAudit stores an audit entry done by Admin Geral at the given time.
func mngAudit(t *testing.T, store domain.Store, at time.Time, action domain.AuditAction, mutate ...func(*domain.AuditEntry)) {
	t.Helper()
	e := domain.AuditEntry{At: at, Action: action, ActorID: "actor-1", ActorName: "Admin Geral"}
	for _, f := range mutate {
		f(&e)
	}
	if err := store.Repos().AuditEntries.Create(context.Background(), &e); err != nil {
		t.Fatalf("insert audit entry %s: %v", action, err)
	}
}

// mngSection returns the markup of the section whose heading has the id.
func mngSection(t *testing.T, body, id string) string {
	t.Helper()
	_, rest, found := strings.Cut(body, `aria-labelledby="`+id+`"`)
	if !found {
		t.Fatalf("no section labelled %q in:\n%s", id, body)
	}
	section, _, _ := strings.Cut(rest, "</section>")
	return section
}

// mngTitles returns the titles of the rows of the markup, in page order.
func mngTitles(markup string) []string {
	var titles []string
	for _, m := range colTitleRE.FindAllStringSubmatch(markup, -1) {
		titles = append(titles, m[1])
	}
	return titles
}

// mngMetas returns the meta lines of a row, in order.
func mngMetas(row string) []string {
	var metas []string
	for _, m := range colMetaRE.FindAllStringSubmatch(row, -1) {
		metas = append(metas, m[1])
	}
	return metas
}

func mngEqual(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s = %q, want %q", what, got, want)
	}
}

// mngChecked reports whether the checkbox called name of the form is checked.
func mngChecked(t *testing.T, form, name string) bool {
	t.Helper()
	tag := regexp.MustCompile(`<input type="checkbox" name="` + name + `" value="1"[^>]*>`).FindString(form)
	if tag == "" {
		t.Fatalf("no checkbox %q (value 1) in the form:\n%s", name, form)
	}
	return strings.Contains(tag, " checked")
}

// mngAlert is the markup of an alert with the text.
func mngAlert(text string) string {
	return `<p class="status status--error" role="alert">` + text + `</p>`
}

func TestMngPagesNeedAnAdmin(t *testing.T) {
	w := newMngWorld(t)
	ana := loginAs(t, w.app, w.ana)

	for _, page := range mngPages {
		rec := serve(w.r, http.MethodGet, page)
		if want := "/entrar?next=" + url.QueryEscape(page); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != want {
			t.Errorf("anonymous GET %s: %d, Location %q; want 303 to %s", page, rec.Code, rec.Header().Get("Location"), want)
		}
		if rec := serve(w.r, http.MethodGet, page, withCookie(ana)); rec.Code != http.StatusForbidden {
			t.Errorf("GET %s as a person who is not the admin = %d, want 403", page, rec.Code)
		}
		if rec := w.get(page); rec.Code != http.StatusOK {
			t.Errorf("GET %s as the admin = %d, want 200", page, rec.Code)
		}
	}

	for target, form := range w.posts() {
		rec := post(w.r, target, form.Encode())
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/entrar" {
			t.Errorf("anonymous POST %s: %d, Location %q; want 303 to /entrar", target, rec.Code, rec.Header().Get("Location"))
		}
		// The session and the Origin are right: only requireAdmin refuses it.
		if rec := post(w.r, target, form.Encode(), withCookie(ana)); rec.Code != http.StatusForbidden {
			t.Errorf("POST %s as a person who is not the admin = %d, want 403", target, rec.Code)
		}
	}
	w.requireUntouched(t)
}

func TestMngPostsNeedTheAppsOrigin(t *testing.T) {
	w := newMngWorld(t)
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

func TestMngAccountsListsActiveAndBlocked(t *testing.T) {
	w := newMngWorld(t)
	rec := w.get("/admin/contas")
	body := rec.Body.String()
	wantAll(t, "the accounts page", body, "<title>Admin · Contas · AGENDA CLUBBER</title>", `<nav class="admin-nav"`)

	active, blocked := mngSection(t, body, "contas-ativas"), mngSection(t, body, "contas-bloqueadas")
	mngEqual(t, "active accounts", mngTitles(active), []string{"Admin Geral", "Ana Ativa", "Cris Admin"})
	mngEqual(t, "blocked accounts", mngTitles(blocked), []string{"Bia Bloqueada"})
	if strings.Contains(body, "Paula Pendente") {
		t.Error("an account that awaits approval is listed")
	}

	// The admin's own row: the Admin badge and no way to block oneself.
	own := colRow(t, active, "Admin Geral")
	if got := colField(t, own, colStatusRE, "badge"); got != "Admin" {
		t.Errorf("badge of the admin's row = %q, want Admin", got)
	}
	if strings.Contains(own, "<form") {
		t.Errorf("the admin's own row has a form:\n%s", own)
	}

	ana := colRow(t, active, "Ana Ativa")
	if strings.Contains(ana, "item-list__status") {
		t.Errorf("a person who is not an admin has a badge:\n%s", ana)
	}
	if got := colField(t, ana, colMetaRE, "meta line"); got != "ana@example.com · 81911112222 (WhatsApp)" {
		t.Errorf("meta line of Ana = %q", got)
	}
	form := admForm(ana, "/admin/conta/bloquear")
	if form == "" {
		t.Fatalf("Ana's row has no block form:\n%s", ana)
	}
	wantAll(t, "Ana's block form", form, admHidden("user_id", w.ana.ID), `name="reason"`, `maxlength="500"`, "[✕] BLOQUEAR", `<span class="sr-only"> Ana Ativa</span>`)
	if strings.Contains(ana, "/admin/conta/desbloquear") {
		t.Error("an active account has an unblock form")
	}

	// Another admin can be blocked, and still shows the badge.
	cris := colRow(t, active, "Cris Admin")
	if got := colField(t, cris, colStatusRE, "badge"); got != "Admin" {
		t.Errorf("badge of Cris = %q, want Admin", got)
	}
	if got := colField(t, cris, colMetaRE, "meta line"); got != "cris@example.com · 81911112222" {
		t.Errorf("meta line of Cris = %q", got)
	}
	if admForm(cris, "/admin/conta/bloquear") == "" {
		t.Error("another admin's row has no block form")
	}

	bia := colRow(t, blocked, "Bia Bloqueada")
	if got := colField(t, bia, colMetaRE, "meta line"); got != "bia@example.com · 81933334444" {
		t.Errorf("meta line of Bia = %q", got)
	}
	form = admForm(bia, "/admin/conta/desbloquear")
	if form == "" {
		t.Fatalf("Bia's row has no unblock form:\n%s", bia)
	}
	wantAll(t, "Bia's unblock form", form, admHidden("user_id", w.bia.ID), "[→] DESBLOQUEAR", `<span class="sr-only"> Bia Bloqueada</span>`)
	if strings.Contains(bia, "/admin/conta/bloquear") || strings.Contains(form, `name="reason"`) {
		t.Errorf("a blocked account has a block form:\n%s", bia)
	}
}

func TestMngAccountsEmptyBlockedList(t *testing.T) {
	e := newAdmEnv(t)
	body := e.get("/admin/contas").Body.String()
	wantAll(t, "the accounts page", body, `<div class="empty">nenhuma conta bloqueada</div>`)
	mngEqual(t, "active accounts", mngTitles(mngSection(t, body, "contas-ativas")), []string{"Admin Geral"})
}

func TestMngBlockAccount(t *testing.T) {
	w := newMngWorld(t)
	anaCookie := loginAs(t, w.app, w.ana)
	if rec := serve(w.r, http.MethodGet, "/coletivos", withCookie(anaCookie)); rec.Code != http.StatusOK {
		t.Fatalf("GET /coletivos as Ana before the block = %d, want 200", rec.Code)
	}

	rec := w.post("/admin/conta/bloquear", url.Values{"user_id": {w.ana.ID}, "reason": {"  spam  "}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/contas?msg=conta-bloqueada" {
		t.Fatalf("block: %d, Location %q; want 303 to /admin/contas?msg=conta-bloqueada", rec.Code, rec.Header().Get("Location"))
	}
	if got := w.status(t, w.ana); got != domain.UserStatusDisabled {
		t.Errorf("status of Ana = %q, want disabled", got)
	}
	// Her sessions ended: the cookie she held no longer signs her in.
	if rec := serve(w.r, http.MethodGet, "/coletivos", withCookie(anaCookie)); rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/entrar") {
		t.Errorf("GET /coletivos with Ana's old cookie: %d, Location %q; want 303 to /entrar", rec.Code, rec.Header().Get("Location"))
	}
	entries := w.audit(t)
	if len(entries) != 1 || entries[0].Action != domain.AuditAccountBlocked || entries[0].UserName != "Ana Ativa" ||
		entries[0].ActorName != "Admin Geral" || entries[0].Reason != "spam" {
		t.Errorf("audit log = %+v, want one account_blocked entry of Ana by Admin Geral with the reason spam", entries)
	}

	body := w.get("/admin/contas?msg=conta-bloqueada").Body.String()
	wantAll(t, "the page after the block", body, `<p class="status status--ok" role="status">conta bloqueada.</p>`)
	mngEqual(t, "blocked accounts", mngTitles(mngSection(t, body, "contas-bloqueadas")), []string{"Ana Ativa", "Bia Bloqueada"})
}

func TestMngUnblockAccount(t *testing.T) {
	w := newMngWorld(t)
	rec := w.post("/admin/conta/desbloquear", url.Values{"user_id": {w.bia.ID}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/contas?msg=conta-desbloqueada" {
		t.Fatalf("unblock: %d, Location %q; want 303 to /admin/contas?msg=conta-desbloqueada", rec.Code, rec.Header().Get("Location"))
	}
	if got := w.status(t, w.bia); got != domain.UserStatusActive {
		t.Errorf("status of Bia = %q, want active", got)
	}
	entries := w.audit(t)
	if len(entries) != 1 || entries[0].Action != domain.AuditAccountUnblocked || entries[0].UserName != "Bia Bloqueada" || entries[0].ActorName != "Admin Geral" {
		t.Errorf("audit log = %+v, want one account_unblocked entry of Bia by Admin Geral", entries)
	}
}

func TestMngAccountRefusals(t *testing.T) {
	w := newMngWorld(t)
	cases := []struct {
		name, target string
		form         url.Values
		status       int
		alert        string
	}{
		{"a forged block of the admin's own account", "/admin/conta/bloquear", url.Values{"user_id": {w.admin.ID}}, http.StatusConflict, approvalsMsgSelf},
		{"a block of an account that is blocked already", "/admin/conta/bloquear", url.Values{"user_id": {w.bia.ID}}, http.StatusConflict, approvalsMsgDecided},
		{"a block of an account that awaits approval", "/admin/conta/bloquear", url.Values{"user_id": {w.paula.ID}}, http.StatusConflict, approvalsMsgDecided},
		{"an unblock of an active account", "/admin/conta/desbloquear", url.Values{"user_id": {w.ana.ID}}, http.StatusConflict, approvalsMsgDecided},
		{"a block of an id that names nothing", "/admin/conta/bloquear", url.Values{"user_id": {"nope"}}, http.StatusNotFound, approvalsMsgNotFound},
		{"an unblock of an id that names nothing", "/admin/conta/desbloquear", url.Values{"user_id": {"nope"}}, http.StatusNotFound, approvalsMsgNotFound},
		{"a block with no id", "/admin/conta/bloquear", url.Values{}, http.StatusNotFound, approvalsMsgNotFound},
		{"a reason with a control character", "/admin/conta/bloquear", url.Values{"user_id": {w.ana.ID}, "reason": {"a\x07b"}}, http.StatusUnprocessableEntity, admReasonText},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			rec := w.post(tt.target, tt.form)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d; body %q", rec.Code, tt.status, rec.Body)
			}
			// The handler answers with its own page, not the middleware's bare line.
			wantAll(t, "the answer", rec.Body.String(), mngAlert(tt.alert), `<nav class="admin-nav"`, `aria-labelledby="contas-ativas"`)
		})
	}
	w.requireUntouched(t)

	rec := post(w.r, "/admin/conta/bloquear", "user_id=%zz", withCookie(w.cookie))
	if rec.Code != http.StatusBadRequest || rec.Body.String() != "requisição inválida" {
		t.Errorf("a malformed body = %d %q, want 400 requisição inválida", rec.Code, rec.Body)
	}
}

func TestMngCollectivesListsTheApprovedOnes(t *testing.T) {
	w := newMngWorld(t)
	body := w.get("/admin/coletivos").Body.String()
	wantAll(t, "the collectives page", body, "<title>Admin · Coletivos · AGENDA CLUBBER</title>", `<nav class="admin-nav"`)
	if strings.Contains(body, "Gama Pendente") {
		t.Error("a collective that is not approved is listed")
	}
	mngEqual(t, "collectives", mngTitles(body), []string{"Alfa Total", "Beta Eventos", "Casa Congelada", "Delta Livre"})

	const eventsBadge, lockedBadge = `item-list__status">eventos bloqueados</span>`, `item-list__status">membros e nome congelados</span>`
	for _, tt := range []struct {
		c                 domain.Collective
		kind              string
		events, locked    bool
		wantEventsChecked bool
	}{
		{w.alfa, "Coletivo", true, true, true},
		{w.beta, "Produtora", true, false, true},
		{w.casa, "Coletivo", false, true, false},
		{w.delta, "Coletivo", false, false, false},
	} {
		t.Run(tt.c.Name, func(t *testing.T) {
			row := colRow(t, body, tt.c.Name)
			if got := colField(t, row, colMetaRE, "meta line"); got != tt.kind {
				t.Errorf("meta line = %q, want %q", got, tt.kind)
			}
			if got := strings.Contains(row, eventsBadge); got != tt.events {
				t.Errorf("events badge shown = %v, want %v", got, tt.events)
			}
			if got := strings.Contains(row, lockedBadge); got != tt.locked {
				t.Errorf("locked badge shown = %v, want %v", got, tt.locked)
			}

			form := admForm(row, "/admin/coletivo/bloqueios")
			if form == "" {
				t.Fatalf("no blocks form in the row:\n%s", row)
			}
			wantAll(t, "the blocks form", form, admHidden("collective_id", tt.c.ID), `name="reason"`, `maxlength="500"`,
				"bloquear eventos", "congelar membros e nome", "[→] SALVAR", `<span class="sr-only"> `+tt.c.Name+`</span>`,
				// One line says what each checkbox does.
				"não podem criar nem editar eventos",
				"ninguém decide pedidos de entrada, promove, rebaixa nem renomeia, e novos pedidos de entrada são recusados")
			if got := mngChecked(t, form, "events_blocked"); got != tt.events {
				t.Errorf("events_blocked checked = %v, want %v", got, tt.events)
			}
			if got := mngChecked(t, form, "locked"); got != tt.locked {
				t.Errorf("locked checked = %v, want %v", got, tt.locked)
			}
		})
	}
}

func TestMngCollectivesEmptyList(t *testing.T) {
	e := newAdmEnv(t)
	wantAll(t, "the collectives page", e.get("/admin/coletivos").Body.String(), `<div class="empty">nenhum coletivo aprovado</div>`)
}

func TestMngSetCollectiveBlocks(t *testing.T) {
	w := newMngWorld(t)
	const wantLocation = "/admin/coletivos?msg=bloqueios-salvos"

	// Every combination, each a change from the one before.
	steps := []struct {
		events, locked bool
		detail         string
	}{
		{true, false, "eventos bloqueados"},
		{false, true, "eventos liberados; membros e nome congelados"},
		{true, true, "eventos bloqueados"},
		{false, false, "eventos liberados; membros e nome liberados"},
	}
	for i, s := range steps {
		form := url.Values{"collective_id": {w.delta.ID}, "reason": {"passo " + strconv.Itoa(i)}}
		if s.events {
			form.Set("events_blocked", "1")
		}
		if s.locked {
			form.Set("locked", "1")
		}
		rec := w.post("/admin/coletivo/bloqueios", form)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != wantLocation {
			t.Fatalf("step %d: %d, Location %q; want 303 to %s", i, rec.Code, rec.Header().Get("Location"), wantLocation)
		}
		if got := w.collective(t, w.delta); got.EventsBlocked != s.events || got.Locked != s.locked {
			t.Errorf("step %d: events %v, locked %v; want %v, %v", i, got.EventsBlocked, got.Locked, s.events, s.locked)
		}
		entries := w.audit(t)
		if len(entries) != i+1 {
			t.Fatalf("step %d: %d audit entries, want %d", i, len(entries), i+1)
		}
		if e := entries[0]; e.Action != domain.AuditCollectiveBlocks || e.CollectiveName != "Delta Livre" || e.Detail != s.detail || e.Reason != "passo "+strconv.Itoa(i) {
			t.Errorf("step %d: newest audit entry = %+v", i, e)
		}
	}

	// Saving what a collective has already changes and records nothing, and still
	// answers like a save.
	rec := w.post("/admin/coletivo/bloqueios", url.Values{"collective_id": {w.delta.ID}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != wantLocation {
		t.Errorf("an unchanged save: %d, Location %q; want 303 to %s", rec.Code, rec.Header().Get("Location"), wantLocation)
	}
	if n := len(w.audit(t)); n != len(steps) {
		t.Errorf("%d audit entries after an unchanged save, want %d", n, len(steps))
	}

	// The page shows what was saved.
	w.post("/admin/coletivo/bloqueios", url.Values{"collective_id": {w.delta.ID}, "locked": {"1"}})
	form := admForm(colRow(t, w.get("/admin/coletivos").Body.String(), "Delta Livre"), "/admin/coletivo/bloqueios")
	if mngChecked(t, form, "events_blocked") || !mngChecked(t, form, "locked") {
		t.Errorf("the form after a save of locked alone:\n%s", form)
	}
}

func TestMngCollectiveRefusals(t *testing.T) {
	w := newMngWorld(t)
	cases := []struct {
		name   string
		form   url.Values
		status int
		alert  string
	}{
		{"a collective that is not approved", url.Values{"collective_id": {w.gama.ID}, "events_blocked": {"1"}}, http.StatusForbidden, approvalsMsgNotAllowed},
		{"an id that names nothing", url.Values{"collective_id": {"nope"}, "locked": {"1"}}, http.StatusNotFound, approvalsMsgNotFound},
		{"no id", url.Values{"locked": {"1"}}, http.StatusNotFound, approvalsMsgNotFound},
		{"a reason with a control character", url.Values{"collective_id": {w.delta.ID}, "locked": {"1"}, "reason": {"a\x07b"}}, http.StatusUnprocessableEntity, admReasonText},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			rec := w.post("/admin/coletivo/bloqueios", tt.form)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d; body %q", rec.Code, tt.status, rec.Body)
			}
			body := rec.Body.String()
			wantAll(t, "the answer", body, mngAlert(tt.alert), `<nav class="admin-nav"`)
			// The page comes back with the collectives as they are.
			mngEqual(t, "collectives in the answer", mngTitles(body), []string{"Alfa Total", "Beta Eventos", "Casa Congelada", "Delta Livre"})
		})
	}
	w.requireUntouched(t)

	rec := post(w.r, "/admin/coletivo/bloqueios", "collective_id=%zz", withCookie(w.cookie))
	if rec.Code != http.StatusBadRequest || rec.Body.String() != "requisição inválida" {
		t.Errorf("a malformed body = %d %q, want 400 requisição inválida", rec.Code, rec.Body)
	}
}

func TestMngHistoryEmpty(t *testing.T) {
	e := newAdmEnv(t)
	wantAll(t, "the history page", e.get("/admin/historico").Body.String(),
		"<title>Admin · Histórico · AGENDA CLUBBER</title>", `<nav class="admin-nav"`, `<div class="empty">nenhuma decisão registrada</div>`)
}

func TestMngHistoryShowsTheDecisionsNewestFirst(t *testing.T) {
	w := newMngWorld(t)
	at := func(h, m int) time.Time { return time.Date(2026, 11, 12, h, m, 0, 0, time.UTC) }
	// Stored out of order: the store lists by time, not by insertion, and the page keeps that order.
	mngAudit(t, w.store, at(13, 5), domain.AuditCollectiveBlocks, func(e *domain.AuditEntry) {
		e.CollectiveID, e.CollectiveName, e.Detail, e.Reason = "c-1", "Alfa Total", "eventos bloqueados", "festa de rua"
	})
	mngAudit(t, w.store, admSignedUp, domain.AuditAccountApproved, func(e *domain.AuditEntry) {
		e.UserID, e.UserName = "u-1", "Paula Pendente"
	})
	mngAudit(t, w.store, at(15, 30), domain.AuditAccountBlocked, func(e *domain.AuditEntry) {
		e.ActorName, e.UserID, e.UserName, e.Reason = "<b>Admin</b>", "u-2", "<i>Eva</i>", "<script>alert(1)</script>"
	})
	mngAudit(t, w.store, at(15, 0), domain.AuditJoinRequested, func(e *domain.AuditEntry) {
		e.ActorName, e.UserID, e.UserName = "Ana Ativa", w.ana.ID, "Ana Ativa"
		e.CollectiveID, e.CollectiveName = "c-2", "Beta Eventos"
	})

	body := w.get("/admin/historico").Body.String()
	mngEqual(t, "entries", mngTitles(body), []string{"conta bloqueada", "pedido de entrada", "bloqueios do coletivo", "conta aprovada"})
	rows := colRows(body)
	if len(rows) != 4 {
		t.Fatalf("%d rows, want 4:\n%s", len(rows), body)
	}
	// The time is in America/Fortaleza (UTC-3), so the sign-up at 01:30 UTC is
	// still on the day before.
	mngEqual(t, "the metas of the blocked account", mngMetas(rows[0]), []string{
		"12/11/2026 12:30 · por &lt;b&gt;Admin&lt;/b&gt; · pessoa: &lt;i&gt;Eva&lt;/i&gt;",
		"motivo: &lt;script&gt;alert(1)&lt;/script&gt;",
	})
	mngEqual(t, "the metas of the request", mngMetas(rows[1]), []string{
		"12/11/2026 12:00 · por Ana Ativa · pessoa: Ana Ativa · coletivo: Beta Eventos",
	})
	mngEqual(t, "the metas of the blocks", mngMetas(rows[2]), []string{
		"12/11/2026 10:05 · por Admin Geral · coletivo: Alfa Total", "eventos bloqueados", "motivo: festa de rua",
	})
	mngEqual(t, "the metas of the approval", mngMetas(rows[3]), []string{
		"11/11/2026 22:30 · por Admin Geral · pessoa: Paula Pendente",
	})

	// Names and reasons are text, never markup.
	for _, raw := range []string{"<b>Admin</b>", "<i>Eva</i>", "<script>alert(1)"} {
		if strings.Contains(body, raw) {
			t.Errorf("the page carries %q unescaped", raw)
		}
	}
	// An entry never shows an address, even of a person who has an account.
	for _, u := range []domain.User{w.ana, w.bia, w.cris, w.paula} {
		if strings.Contains(body, u.Email) {
			t.Errorf("the page shows the address %s", u.Email)
		}
	}
}

func TestMngHistoryLabelsEveryAction(t *testing.T) {
	e := newAdmEnv(t)
	for i, l := range mngActionLabels {
		mngAudit(t, e.store, webTestNow.Add(time.Duration(i)*time.Minute), l.action)
	}
	titles := mngTitles(e.get("/admin/historico").Body.String())
	if len(titles) != len(mngActionLabels) {
		t.Fatalf("%d entries, want %d: %q", len(titles), len(mngActionLabels), titles)
	}
	for i, l := range mngActionLabels {
		// The newest first, so the last action stored is the first title.
		if got := titles[len(titles)-1-i]; got != l.label {
			t.Errorf("label of %s = %q, want %q", l.action, got, l.label)
		}
	}
}

func TestMngAuditActionLabel(t *testing.T) {
	for _, l := range mngActionLabels {
		if got := auditActionLabel(l.action); got != l.label {
			t.Errorf("auditActionLabel(%s) = %q, want %q", l.action, got, l.label)
		}
	}
	// A value the code does not know shows as it is.
	if got := auditActionLabel("account_teleported"); got != "account_teleported" {
		t.Errorf("auditActionLabel(unknown) = %q, want the raw value", got)
	}
}

func TestMngHistoryShowsTheLatest200(t *testing.T) {
	e := newAdmEnv(t)
	for i := 0; i <= 200; i++ {
		n := strconv.Itoa(i)
		mngAudit(t, e.store, webTestNow.Add(time.Duration(i)*time.Minute), domain.AuditAccountApproved, func(a *domain.AuditEntry) { a.Detail = "n=" + n })
	}
	body := e.get("/admin/historico").Body.String()
	if rows := colRows(body); len(rows) != 200 {
		t.Errorf("%d rows, want 200", len(rows))
	}
	for n, want := range map[string]bool{"0": false, "1": true, "200": true} {
		if got := strings.Contains(body, `item-list__meta">n=`+n+`<`); got != want {
			t.Errorf("entry n=%s shown = %v, want %v", n, got, want)
		}
	}
}

func TestMngNoticesComeFromFixedCodes(t *testing.T) {
	w := newMngWorld(t)
	known := map[string]bool{}
	for _, n := range mngNotices {
		known[n.page+"?msg="+n.code] = true
		body := w.get(n.page + "?msg=" + n.code).Body.String()
		wantAll(t, n.page+"?msg="+n.code, body, `<p class="status status--ok" role="status">`+n.text+`</p>`)
	}
	for _, page := range mngPages {
		for _, code := range []string{"nope", "", "<b>x</b>", "conta-bloqueada%3Cb%3E", "Conta-Bloqueada", "conta-bloqueada", "conta-desbloqueada", "bloqueios-salvos", "pessoa-aprovada"} {
			if known[page+"?msg="+code] {
				continue
			}
			body := w.get(page + "?msg=" + code).Body.String()
			if strings.Contains(body, "status--ok") || strings.Contains(body, `role="status"`) {
				t.Errorf("%s?msg=%s shows a notice", page, code)
			}
			if strings.Contains(body, "&lt;b&gt;") || strings.Contains(body, "<b>x") {
				t.Errorf("%s?msg=%s is echoed back", page, code)
			}
		}
	}
}

func TestMngPageMarkup(t *testing.T) {
	w := newMngWorld(t)
	mngAudit(t, w.store, webTestNow, domain.AuditAccountBlocked, func(e *domain.AuditEntry) { e.UserName, e.Reason = "Bia Bloqueada", "motivo" })
	pages := map[string]string{
		"the accounts":                w.get("/admin/contas").Body.String(),
		"the accounts with a notice":  w.get("/admin/contas?msg=conta-bloqueada").Body.String(),
		"the accounts with an alert":  w.post("/admin/conta/bloquear", url.Values{"user_id": {w.admin.ID}}).Body.String(),
		"the collectives":             w.get("/admin/coletivos").Body.String(),
		"the collectives with notice": w.get("/admin/coletivos?msg=bloqueios-salvos").Body.String(),
		"the collectives with alert":  w.post("/admin/coletivo/bloqueios", url.Values{"collective_id": {w.gama.ID}}).Body.String(),
		"the history":                 w.get("/admin/historico").Body.String(),
	}
	for name, body := range pages {
		t.Run(name, func(t *testing.T) {
			if v := cspViolations(body); len(v) > 0 {
				t.Errorf("%s in:\n%s", strings.Join(v, "; "), body)
			}
			// Many forms share a page, so their ids must not repeat, and every label
			// points at a control.
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
			for _, m := range regexp.MustCompile(`aria-describedby="([^"]*)"`).FindAllStringSubmatch(body, -1) {
				for _, id := range strings.Fields(m[1]) {
					if !ids[id] {
						t.Errorf(`aria-describedby=%q points at no id`, id)
					}
				}
			}
			if !strings.Contains(body, `<nav class="admin-nav"`) {
				t.Error("the page has no admin nav")
			}
		})
	}
}

func TestMngNavMarksTheCurrentPage(t *testing.T) {
	w := newMngWorld(t)
	for _, tt := range []struct{ page, current string }{
		{"/admin/contas", `<a href="/admin/contas" aria-current="page">Contas</a>`},
		{"/admin/coletivos", `<a href="/admin/coletivos" aria-current="page">Coletivos</a>`},
		{"/admin/historico", `<a href="/admin/historico" aria-current="page">Histórico</a>`},
	} {
		t.Run(tt.page, func(t *testing.T) {
			body := w.get(tt.page).Body.String()
			// The sidebar of the layout marks its own link, so the nav is looked at alone.
			_, rest, found := strings.Cut(body, `<nav class="admin-nav"`)
			if !found {
				t.Fatalf("no admin nav in:\n%s", body)
			}
			nav, _, _ := strings.Cut(rest, "</nav>")
			if !strings.Contains(nav, tt.current) || strings.Count(nav, `aria-current="page"`) != 1 {
				t.Errorf("the nav does not mark %s alone as current:\n%s", tt.current, nav)
			}
			// The sidebar marks Admin, never Meus Coletivos.
			if !strings.Contains(body, `<a class="nav-item" href="/admin" aria-current="page">Admin</a>`) {
				t.Error("the sidebar does not mark Admin as current")
			}
			if strings.Contains(body, `<a class="nav-item" href="/coletivos" aria-current="page"`) {
				t.Error("the sidebar marks Meus Coletivos as current")
			}
		})
	}
}

// A post that fails for a reason of the server answers 500 "erro interno",
// changes nothing, and logs the error but not what was posted.
func TestMngServerError(t *testing.T) {
	w := newMngWorld(t)
	const secret = "segredo digitado pelo admin"
	w.app.Store = colFailingStore{w.store}
	logs := captureLogs(w.app)
	w.r = w.app.Router()

	for target, form := range w.posts() {
		t.Run(target, func(t *testing.T) {
			logs.Reset()
			if form.Has("reason") {
				form.Set("reason", secret)
			}
			rec := w.post(target, form)
			if rec.Code != http.StatusInternalServerError || rec.Body.String() != "erro interno" {
				t.Fatalf("POST %s: %d %q; want 500 erro interno", target, rec.Code, rec.Body)
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
			for _, leak := range []string{secret, w.ana.ID, w.bia.ID, w.delta.ID} {
				if strings.Contains(logs.String(), leak) {
					t.Errorf("the log carries %q: %s", leak, logs)
				}
			}
		})
	}
	w.requireUntouched(t)
}

// mngFailReads is a store whose lists fail, to reach the error path of each page.
type mngFailReads struct{ domain.Store }

type (
	mngFailUsers       struct{ domain.Users }
	mngFailCollectives struct{ domain.Collectives }
	mngFailAudit       struct{ domain.AuditEntries }
)

var errMngRead = errors.New("the database is down")

func (mngFailUsers) ListByStatus(context.Context, domain.UserStatus, int) ([]domain.User, error) {
	return nil, errMngRead
}

func (mngFailCollectives) ListByStatus(context.Context, domain.CollectiveStatus, int) ([]domain.Collective, error) {
	return nil, errMngRead
}

func (mngFailAudit) List(context.Context, int) ([]domain.AuditEntry, error) { return nil, errMngRead }

func (s mngFailReads) Repos() domain.Repos {
	r := s.Store.Repos()
	r.Users, r.Collectives, r.AuditEntries = mngFailUsers{r.Users}, mngFailCollectives{r.Collectives}, mngFailAudit{r.AuditEntries}
	return r
}

func TestMngPagesThatCannotReadAnswer500(t *testing.T) {
	w := newMngWorld(t)
	w.app.Store = mngFailReads{w.store}
	logs := captureLogs(w.app)
	w.r = w.app.Router()

	for _, page := range mngPages {
		t.Run(page, func(t *testing.T) {
			logs.Reset()
			rec := w.get(page)
			if rec.Code != http.StatusInternalServerError || rec.Body.String() != "erro interno" {
				t.Fatalf("GET %s: %d %q; want 500 erro interno", page, rec.Code, rec.Body)
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
		})
	}
}
