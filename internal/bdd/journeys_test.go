package bdd

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/magalz/agenda-clubber-lite/internal/approvals"
	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

const testPassword = "SenhaForte#2026"

func seedActiveUser(t *testing.T, w *World, name, email string, isAdmin bool) domain.User {
	t.Helper()
	hash, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("hash password for %s: %v", email, err)
	}
	now := w.Clock.Now()
	u := domain.User{
		Name:              name,
		Email:             email,
		Phone:             "85987654321",
		PasswordHash:      hash,
		PasswordChangedAt: now,
		Status:            domain.UserStatusActive,
		IsAdmin:           isAdmin,
		EmailVerifiedAt:   &now,
		ApprovedAt:        &now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := w.Store.Repos().Users.Create(context.Background(), &u); err != nil {
		t.Fatalf("create user %s: %v", email, err)
	}
	return u
}

func seedApprovedCollective(t *testing.T, w *World, name, createdBy string) domain.Collective {
	t.Helper()
	now := w.Clock.Now()
	c := domain.Collective{
		Name:       name,
		NameKey:    domain.NameKey(name),
		Kind:       domain.CollectiveKindColetivo,
		Status:     domain.CollectiveStatusApproved,
		CreatedBy:  createdBy,
		ApprovedAt: &now,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := w.Store.Repos().Collectives.Create(context.Background(), &c); err != nil {
		t.Fatalf("create collective %s: %v", name, err)
	}
	return c
}

func seedApprovedMembership(t *testing.T, w *World, userID, collectiveID string, role domain.Role) {
	t.Helper()
	now := w.Clock.Now()
	m := domain.Membership{
		UserID:          userID,
		CollectiveID:    collectiveID,
		Role:            role,
		Status:          domain.MembershipStatusApproved,
		AdminApprovedAt: &now,
		OwnerApprovedAt: &now,
		RequestedAt:     now,
		DecidedAt:       &now,
	}
	if err := w.Store.Repos().Memberships.Create(context.Background(), m); err != nil {
		t.Fatalf("create membership: %v", err)
	}
}

func signInActor(t *testing.T, w *World, actor *Actor, email, password string) {
	t.Helper()
	resp := w.POST(actor, "/entrar", url.Values{
		"email":    {email},
		"password": {password},
	})
	if resp.Status != http.StatusSeeOther || resp.Location != "/" {
		t.Fatalf("%s login failed: status=%d location=%q", actor.Name, resp.Status, resp.Location)
	}
}

func TestJourneySignupConfirmApproveLogin(t *testing.T) {
	Feature(t, "BDD-01", "Cadastro, confirmação, aprovação e login", func(t *testing.T) {
		Scenario(t, "BDD-01-01", "Cadastro com coletivo novo até acesso ao calendário", func(s *StepContext) {
			w := NewWorld(s.T)
			admin := w.NewActor("Admin")
			applicant := w.NewActor("Candidata")
			var confirmLink string

			s.Given("um admin ativo autenticado e uma nova visitante", func() {
				seedActiveUser(s.T, w, "Admin Geral", "admin@agenda.test", true)
				signInActor(s.T, w, admin, "admin@agenda.test", testPassword)
			})

			s.When("a visitante realiza cadastro criando um coletivo novo", func() {
				resp := w.POST(applicant, "/cadastro", url.Values{
					"name":             {"Ana Silva"},
					"email":            {"ana@example.com"},
					"phone":            {"85999990000"},
					"phone_whatsapp":   {"1"},
					"password":         {testPassword},
					"password_confirm": {testPassword},
					"collective_kind":  {string(domain.CollectiveKindColetivo)},
					"collective_name":  {"Coletivo Aurora"},
					"role":             {string(domain.RoleOwner)},
				})
				if resp.Status != http.StatusSeeOther || resp.Location != "/cadastro/enviado" {
					s.T.Fatalf("POST /cadastro: status=%d location=%q", resp.Status, resp.Location)
				}
			})

			s.Then("o e-mail de confirmação na outbox usa saudação sem nome e traz link de confirmação", func() {
				msgs := w.OutboxMessages("ana@example.com")
				if len(msgs) != 1 {
					s.T.Fatalf("outbox messages for ana@example.com = %d, want 1", len(msgs))
				}
				if !strings.Contains(msgs[0].Text, "Olá!") {
					s.T.Errorf("verify email missing nameless greeting 'Olá!': %s", msgs[0].Text)
				}
				if strings.Contains(msgs[0].Text, "Ana Silva") {
					s.T.Errorf("verify email leaked recipient name: %s", msgs[0].Text)
				}
				confirmLink = w.ExtractLink(msgs[0].Text, "/confirmar")
				if confirmLink == "" {
					s.T.Fatalf("no /confirmar link found in verify email: %s", msgs[0].Text)
				}
			})

			s.When("a visitante abre o link e confirma o e-mail via POST", func() {
				getResp := w.GET(applicant, confirmLink)
				if getResp.Status != http.StatusOK {
					s.T.Fatalf("GET %s: status=%d, want 200", confirmLink, getResp.Status)
				}
				u, err := url.Parse(confirmLink)
				if err != nil {
					s.T.Fatalf("parse confirm link: %v", err)
				}
				postResp := w.POST(applicant, "/confirmar", url.Values{
					"token": {u.Query().Get("token")},
				})
				if postResp.Status != http.StatusSeeOther || postResp.Location != "/aguardando" {
					s.T.Fatalf("POST /confirmar: status=%d location=%q", postResp.Status, postResp.Location)
				}
			})

			s.And("o admin aprova a pessoa e o coletivo em /admin/pessoa/aprovar-tudo", func() {
				user, err := w.Store.Repos().Users.ByEmail(context.Background(), "ana@example.com")
				if err != nil {
					s.T.Fatalf("lookup applicant user: %v", err)
				}
				resp := w.POST(admin, "/admin/pessoa/aprovar-tudo", url.Values{
					"user_id": {user.ID},
				})
				if resp.Status != http.StatusSeeOther || resp.Location != "/admin?msg=pessoa-aprovada" {
					s.T.Fatalf("POST /admin/pessoa/aprovar-tudo: status=%d location=%q", resp.Status, resp.Location)
				}
			})

			s.Then("a usuária entra na conta e carrega o calendário com sucesso", func() {
				signInActor(s.T, w, applicant, "ana@example.com", testPassword)
				resp := w.GET(applicant, "/")
				if resp.Status != http.StatusOK {
					s.T.Fatalf("GET /: status=%d, want 200", resp.Status)
				}
				if !strings.Contains(resp.Body, "Calendário") {
					s.T.Errorf("calendar page body missing 'Calendário'")
				}
			})
		})
	})
}

func TestJourneyCollectiveConcealmentAndRename(t *testing.T) {
	Feature(t, "BDD-03", "Ocultação de página de coletivo e renomeação", func(t *testing.T) {
		Scenario(t, "BDD-03-01", "Apenas Administrador acessa e renomeia coletivo com notificação e auditoria", func(s *StepContext) {
			w := NewWorld(s.T)
			admin := w.NewActor("Admin")
			owner := w.NewActor("Administrador")
			member := w.NewActor("Membro")
			var col domain.Collective

			s.Given("um coletivo aprovado com Administrador, Membro e um Admin fora do coletivo", func() {
				seedActiveUser(s.T, w, "Admin Geral", "admin@agenda.test", true)
				ownerUser := seedActiveUser(s.T, w, "Clara Dona", "dona@agenda.test", false)
				memberUser := seedActiveUser(s.T, w, "Bruno Membro", "membro@agenda.test", false)

				col = seedApprovedCollective(s.T, w, "Coletivo Brisa", ownerUser.ID)
				seedApprovedMembership(s.T, w, ownerUser.ID, col.ID, domain.RoleOwner)
				seedApprovedMembership(s.T, w, memberUser.ID, col.ID, domain.RoleMember)

				signInActor(s.T, w, admin, "admin@agenda.test", testPassword)
				signInActor(s.T, w, owner, "dona@agenda.test", testPassword)
				signInActor(s.T, w, member, "membro@agenda.test", testPassword)
			})

			s.Then("GET /coletivo/:id responde 404 para Membro e Admin não-dono e 200 para Administrador", func() {
				path := approvals.CollectivePath(col.ID)
				if resp := w.GET(member, path); resp.Status != http.StatusNotFound {
					s.T.Errorf("member GET %s: status=%d, want 404", path, resp.Status)
				}
				if resp := w.GET(admin, path); resp.Status != http.StatusNotFound {
					s.T.Errorf("non-owner admin GET %s: status=%d, want 404", path, resp.Status)
				}
				ownerResp := w.GET(owner, path)
				if ownerResp.Status != http.StatusOK {
					s.T.Fatalf("owner GET %s: status=%d, want 200", path, ownerResp.Status)
				}
				if !strings.Contains(ownerResp.Body, "Coletivo Brisa") {
					s.T.Errorf("owner collective page missing collective name")
				}
			})

			s.When("o Administrador renomeia o coletivo", func() {
				renamePath := approvals.CollectivePath(col.ID) + "/nome"
				resp := w.POST(owner, renamePath, url.Values{
					"name": {"Coletivo Brisa Nova"},
				})
				wantLoc := approvals.CollectivePath(col.ID) + "?msg=nome-alterado"
				if resp.Status != http.StatusSeeOther || resp.Location != wantLoc {
					s.T.Fatalf("POST %s: status=%d location=%q, want 303 %q", renamePath, resp.Status, resp.Location, wantLoc)
				}
			})

			s.Then("o Admin recebe e-mail de notificação na outbox e o histórico registra a renomeação", func() {
				msgs := w.OutboxMessages("admin@agenda.test")
				if len(msgs) == 0 {
					s.T.Fatal("expected rename notification email to admin@agenda.test, got none")
				}
				found := false
				for _, m := range msgs {
					if strings.Contains(m.Text, "Coletivo Brisa") && strings.Contains(m.Text, "Coletivo Brisa Nova") {
						found = true
						break
					}
				}
				if !found {
					s.T.Errorf("admin outbox emails did not mention old and new collective names: %+v", msgs)
				}

				hist := w.GET(admin, "/admin/historico")
				if hist.Status != http.StatusOK {
					s.T.Fatalf("GET /admin/historico: status=%d, want 200", hist.Status)
				}
				if !strings.Contains(hist.Body, "coletivo renomeado") || !strings.Contains(hist.Body, "Coletivo Brisa Nova") {
					s.T.Errorf("admin history missing rename audit entry")
				}
			})
		})
	})
}

func TestJourneyEventLifecycleAndEventsBlocked(t *testing.T) {
	Feature(t, "BDD-04", "Ciclo de vida de eventos e bloqueio de eventos", func(t *testing.T) {
		Scenario(t, "BDD-04-01", "Criação, visualização, bloqueio pelo admin e edição pelo admin auditada", func(s *StepContext) {
			w := NewWorld(s.T)
			admin := w.NewActor("Admin")
			owner := w.NewActor("Administrador")
			var (
				col     domain.Collective
				eventID string
			)

			s.Given("um coletivo aprovado com Administrador e um Admin autenticados", func() {
				seedActiveUser(s.T, w, "Admin Geral", "admin@agenda.test", true)
				ownerUser := seedActiveUser(s.T, w, "Lia Dona", "lia@agenda.test", false)

				col = seedApprovedCollective(s.T, w, "Coletivo Sol", ownerUser.ID)
				seedApprovedMembership(s.T, w, ownerUser.ID, col.ID, domain.RoleOwner)

				signInActor(s.T, w, admin, "admin@agenda.test", testPassword)
				signInActor(s.T, w, owner, "lia@agenda.test", testPassword)
			})

			s.When("o Administrador cria um evento pelo formulário", func() {
				resp := w.POST(owner, "/eventos/novo", url.Values{
					"collective_id":    {col.ID},
					"name":             {"Noite Solar"},
					"type":             {string(domain.EventTypeFesta)},
					"date":             {"14/11/2026"},
					"time":             {"22:00"},
					"duration_hours":   {"6"},
					"duration_minutes": {"0"},
					"free":             {"on"},
					"style":            {"techno"},
					"state":            {"CE"},
					"city":             {"Fortaleza"},
				})
				if resp.Status != http.StatusSeeOther || !strings.HasPrefix(resp.Location, "/eventos/") {
					s.T.Fatalf("POST /eventos/novo: status=%d location=%q", resp.Status, resp.Location)
				}
				u, err := url.Parse(resp.Location)
				if err != nil {
					s.T.Fatalf("parse event location %q: %v", resp.Location, err)
				}
				eventID = strings.TrimPrefix(u.Path, "/eventos/")
				if eventID == "" {
					s.T.Fatalf("could not extract event ID from %q", resp.Location)
				}
			})

			s.Then("o evento aparece no calendário e em Analisar Data", func() {
				cal := w.GET(owner, "/?mes=2026-11")
				if cal.Status != http.StatusOK || !strings.Contains(cal.Body, "Noite Solar") {
					s.T.Fatalf("calendar GET /?mes=2026-11: status=%d, contains event=%v", cal.Status, strings.Contains(cal.Body, "Noite Solar"))
				}
				day := w.GET(owner, "/eventos/dia?data=2026-11-14")
				if day.Status != http.StatusOK || !strings.Contains(day.Body, "Noite Solar") {
					s.T.Fatalf("day GET /eventos/dia: status=%d, contains event=%v", day.Status, strings.Contains(day.Body, "Noite Solar"))
				}
			})

			s.When("o Admin ativa EventsBlocked para o coletivo", func() {
				resp := w.POST(admin, "/admin/coletivo/bloqueios", url.Values{
					"collective_id":  {col.ID},
					"events_blocked": {"1"},
					"reason":         {"revisão de agenda"},
				})
				if resp.Status != http.StatusSeeOther || resp.Location != "/admin/coletivos?msg=bloqueios-salvos" {
					s.T.Fatalf("POST /admin/coletivo/bloqueios: status=%d location=%q", resp.Status, resp.Location)
				}
			})

			s.Then("o Administrador recebe 409 com o motivo ao tentar editar o evento", func() {
				editPath := "/eventos/" + url.PathEscape(eventID) + "/editar"
				resp := w.GET(owner, editPath)
				if resp.Status != http.StatusConflict {
					s.T.Fatalf("owner GET %s: status=%d, want 409", editPath, resp.Status)
				}
				if !strings.Contains(resp.Body, "os eventos deste coletivo estão bloqueados pelo admin.") {
					s.T.Errorf("409 response missing block reason: %q", resp.Body)
				}
			})

			s.When("o Admin edita o evento mesmo com o bloqueio ativo", func() {
				editPath := "/eventos/" + url.PathEscape(eventID) + "/editar"
				resp := w.POST(admin, editPath, url.Values{
					"name":             {"Noite Solar Editada"},
					"type":             {string(domain.EventTypeFesta)},
					"date":             {"14/11/2026"},
					"time":             {"22:00"},
					"duration_hours":   {"6"},
					"duration_minutes": {"0"},
					"free":             {"on"},
					"style":            {"techno"},
					"state":            {"CE"},
					"city":             {"Fortaleza"},
					"version":          {"1"},
				})
				wantLoc := "/eventos/" + url.PathEscape(eventID) + "?msg=evento-salvo"
				if resp.Status != http.StatusSeeOther || resp.Location != wantLoc {
					s.T.Fatalf("admin POST %s: status=%d location=%q, want 303 %q", editPath, resp.Status, resp.Location, wantLoc)
				}
			})

			s.Then("o histórico do admin registra a edição feita pelo Admin", func() {
				hist := w.GET(admin, "/admin/historico")
				if hist.Status != http.StatusOK {
					s.T.Fatalf("GET /admin/historico: status=%d, want 200", hist.Status)
				}
				if !strings.Contains(hist.Body, "evento editado pelo admin") || !strings.Contains(hist.Body, "Noite Solar Editada") {
					s.T.Errorf("admin history missing admin event edit audit entry")
				}
			})
		})
	})
}
