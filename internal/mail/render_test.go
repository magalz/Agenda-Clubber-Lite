package mail

import (
	"html"
	"strings"
	"testing"
)

const (
	linkConfirm = "https://agenda.example/confirmar/abc123_-xyz"
	linkLogin   = "https://agenda.example/entrar"
	linkReset   = "https://agenda.example/redefinir-senha/def456_-uvw"
	linkForgot  = "https://agenda.example/senha/esqueci"
	linkAdmin   = "https://agenda.example/admin/aprovacoes"
	xssName     = `<script>alert(1)</script>`

	linkCollectives      = "https://agenda.example/coletivos"
	linkOwnerRequests    = "https://agenda.example/coletivos/pedidos"
	linkAdminCollectives = "https://agenda.example/admin/coletivos"
	boldName             = `<b>negrito</b>`
)

// renderCase is one template with data that carries no markup.
type renderCase struct {
	name    string
	tmpl    Template
	data    any
	subject string
	links   []string // each must be a plain-text link and a clickable one
	button  string   // label of the HTML button, when the case pins it
	phrases []string // each must appear in the text and in the HTML
	absent  []string // none may appear in the text or in the HTML
}

func renderCases() []renderCase {
	return []renderCase{
		{
			name:    "verify email",
			tmpl:    TemplateVerifyEmail,
			data:    VerifyEmailData{Name: "Ana Souza", Link: linkConfirm},
			subject: "Confirme seu e-mail · Agenda Clubber",
			links:   []string{linkConfirm},
			phrases: []string{"Ana Souza", "24 horas"},
		},
		{
			name:    "existing account",
			tmpl:    TemplateExistingAccount,
			data:    ExistingAccountData{Name: "Ana Souza", LoginLink: linkLogin, ForgotPasswordLink: linkForgot},
			subject: "Você já tem uma conta · Agenda Clubber",
			links:   []string{linkLogin, linkForgot},
			phrases: []string{"Ana Souza"},
			absent:  []string{"vale por"}, // the forgot-password page does not expire
		},
		{
			name: "admin new signup",
			tmpl: TemplateAdminNewSignup,
			data: AdminNewSignupData{
				UserName:        "Ana Souza",
				UserEmail:       "ana@example.com",
				UserPhone:       "+55 85 99999-0000",
				PhoneIsWhatsApp: true,
				Collectives:     []SignupCollective{{Name: "Maré Alta", Kind: "Coletivo", Role: "Dono", New: true}},
				AdminLink:       linkAdmin,
			},
			subject: "Novo cadastro para aprovar: Ana Souza",
			links:   []string{linkAdmin},
			phrases: []string{"Ana Souza", "ana@example.com", "+55 85 99999-0000", "Maré Alta"},
		},
		{
			name:    "password reset",
			tmpl:    TemplatePasswordReset,
			data:    PasswordResetData{Name: "Ana Souza", Link: linkReset},
			subject: "Redefinir sua senha · Agenda Clubber",
			links:   []string{linkReset},
			phrases: []string{"Ana Souza", "1 hora"},
		},
		{
			name: "decision approved",
			tmpl: TemplateDecision,
			data: DecisionData{
				Name:  "Ana Souza",
				Items: []DecisionItem{{What: "sua conta", Outcome: DecisionApproved}},
				Link:  linkLogin,
			},
			subject: "Atualização do seu cadastro na Agenda Clubber",
			links:   []string{linkLogin},
			button:  "Entrar",
			phrases: []string{"Ana Souza", "sua conta: aprovado"},
			// "aprovado" is also the start of the waiting wording.
			absent: []string{"recusado", "falta um administrador", "motivo"},
		},
		{
			name: "decision rejected with a reason",
			tmpl: TemplateDecision,
			data: DecisionData{
				Name:  "Ana Souza",
				Items: []DecisionItem{{What: "o coletivo Alfa", Outcome: DecisionRejected, Reason: "o nome já existe"}},
				Link:  linkLogin,
			},
			subject: "Atualização do seu cadastro na Agenda Clubber",
			links:   []string{linkLogin},
			button:  "Entrar",
			phrases: []string{"Ana Souza", "o coletivo Alfa: recusado", "motivo: o nome já existe"},
			absent:  []string{"aprovado", "falta um administrador"},
		},
		{
			name: "decision waiting",
			tmpl: TemplateDecision,
			data: DecisionData{
				Name:  "Ana Souza",
				Items: []DecisionItem{{What: "seu pedido para entrar em Beta como Membro", Outcome: DecisionWaiting}},
				Link:  linkCollectives,
			},
			subject: "Atualização do seu cadastro na Agenda Clubber",
			links:   []string{linkCollectives},
			button:  "Entrar",
			phrases: []string{"seu pedido para entrar em Beta como Membro: aprovado pelo admin; falta um administrador do coletivo"},
			absent:  []string{"recusado", "motivo"},
		},
		{
			name: "decision with several items",
			tmpl: TemplateDecision,
			data: DecisionData{
				Name: "Ana Souza",
				Items: []DecisionItem{
					{What: "sua conta", Outcome: DecisionApproved},
					{What: "o coletivo Alfa", Outcome: DecisionRejected, Reason: "sem comprovação"},
					{What: "seu pedido para entrar em Beta como Administrador", Outcome: DecisionWaiting},
				},
				Link: linkLogin,
			},
			subject: "Atualização do seu cadastro na Agenda Clubber",
			links:   []string{linkLogin},
			phrases: []string{
				"sua conta: aprovado",
				"o coletivo Alfa: recusado",
				"motivo: sem comprovação",
				"seu pedido para entrar em Beta como Administrador: aprovado pelo admin; falta um administrador do coletivo",
			},
		},
		{
			name:    "owner join request as member",
			tmpl:    TemplateOwnerJoinRequest,
			data:    OwnerJoinRequestData{CollectiveName: "Maré Alta", UserName: "Ana Souza", Role: "Membro", Link: linkOwnerRequests},
			subject: "Novo pedido para entrar em Maré Alta",
			links:   []string{linkOwnerRequests},
			button:  "Ver pedidos",
			phrases: []string{"Ana Souza pediu para entrar em Maré Alta como Membro."},
		},
		{
			name:    "owner join request as administrador",
			tmpl:    TemplateOwnerJoinRequest,
			data:    OwnerJoinRequestData{CollectiveName: "Maré Alta", UserName: "Ana Souza", Role: "Administrador", Link: linkOwnerRequests},
			subject: "Novo pedido para entrar em Maré Alta",
			links:   []string{linkOwnerRequests},
			button:  "Ver pedidos",
			phrases: []string{"Ana Souza pediu para entrar em Maré Alta como Administrador."},
		},
		{
			name: "admin request join",
			tmpl: TemplateAdminRequest,
			data: AdminRequestData{
				Kind: AdminRequestJoin, UserName: "Ana Souza", CollectiveName: "Maré Alta", Role: "Membro", Link: linkAdmin,
			},
			subject: "Pedido de entrada: Ana Souza em Maré Alta",
			links:   []string{linkAdmin},
			button:  "Abrir aprovações",
			phrases: []string{"Ana Souza pediu para entrar em Maré Alta como Membro."},
			absent:  []string{"pediu para criar", "mudou o nome"},
		},
		{
			name: "admin request new collective",
			tmpl: TemplateAdminRequest,
			data: AdminRequestData{
				Kind: AdminRequestNewCollective, UserName: "Ana Souza", CollectiveName: "Maré Alta", Link: linkAdmin,
			},
			subject: "Coletivo novo para aprovar: Maré Alta",
			links:   []string{linkAdmin},
			button:  "Abrir aprovações",
			phrases: []string{"Ana Souza pediu para criar Maré Alta."},
			absent:  []string{"pediu para entrar", "mudou o nome"},
		},
		{
			name: "admin request rename",
			tmpl: TemplateAdminRequest,
			data: AdminRequestData{
				Kind: AdminRequestRename, UserName: "Ana Souza", OldName: "Maré Alta", CollectiveName: "Maré Alta Produções", Link: linkAdminCollectives,
			},
			subject: "Coletivo renomeado: Maré Alta → Maré Alta Produções",
			links:   []string{linkAdminCollectives},
			button:  "Ver coletivos",
			phrases: []string{"Ana Souza mudou o nome de Maré Alta para Maré Alta Produções."},
			absent:  []string{"pediu para entrar", "pediu para criar"},
		},
	}
}

func TestRender(t *testing.T) {
	for _, tc := range renderCases() {
		t.Run(tc.name, func(t *testing.T) {
			subject, text, htmlBody, err := Render(tc.tmpl, tc.data)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if subject != tc.subject {
				t.Errorf("subject = %q, want %q", subject, tc.subject)
			}
			if strings.ContainsAny(subject, "\r\n") {
				t.Errorf("subject spans more than one line: %q", subject)
			}
			for _, link := range tc.links {
				if !strings.Contains(text, link) {
					t.Errorf("text lacks the link %q:\n%s", link, text)
				}
				if !strings.Contains(htmlBody, `href="`+link+`"`) {
					t.Errorf("html lacks a button for %q", link)
				}
				// The link is also printed as text, in case the button is not shown.
				if n := strings.Count(htmlBody, link); n < 2 {
					t.Errorf("html prints %q %d time(s), want it in the button and as text", link, n)
				}
			}
			if tc.button != "" && !strings.Contains(htmlBody, "[→] "+tc.button) {
				t.Errorf("html lacks the %q button", tc.button)
			}
			shown := html.UnescapeString(htmlBody) // html/template writes a + as &#43;
			for _, phrase := range tc.phrases {
				if !strings.Contains(text, phrase) {
					t.Errorf("text lacks %q:\n%s", phrase, text)
				}
				if !strings.Contains(shown, phrase) {
					t.Errorf("html lacks %q", phrase)
				}
			}
			for _, phrase := range tc.absent {
				if strings.Contains(text, phrase) || strings.Contains(shown, phrase) {
					t.Errorf("email carries %q", phrase)
				}
			}
			for _, want := range []string{"IgnisDev", "ignisdev@magalz.space"} {
				if !strings.Contains(text, want) || !strings.Contains(htmlBody, want) {
					t.Errorf("footer lacks %q", want)
				}
			}
			for _, want := range []string{`<html lang="pt-BR"`, "◢◤ AGENDA_CLUBBER", "#050506", "#e8e8ea", "#ff2040", "<table"} {
				if !strings.Contains(htmlBody, want) {
					t.Errorf("html lacks %q", want)
				}
			}
			if strings.Contains(text, "<") {
				t.Errorf("text carries markup:\n%s", text)
			}
		})
	}
}

func TestRenderEscapesUserText(t *testing.T) {
	cases := []struct {
		name      string
		tmpl      Template
		data      any
		inSubject bool // the subject carries user text
	}{
		{"verify email", TemplateVerifyEmail, VerifyEmailData{Name: xssName, Link: linkConfirm}, false},
		{"existing account", TemplateExistingAccount, ExistingAccountData{Name: xssName, LoginLink: linkLogin, ForgotPasswordLink: linkForgot}, false},
		{"password reset", TemplatePasswordReset, PasswordResetData{Name: xssName, Link: linkReset}, false},
		{"admin new signup", TemplateAdminNewSignup, AdminNewSignupData{
			UserName:    xssName,
			UserEmail:   xssName,
			UserPhone:   xssName,
			Collectives: []SignupCollective{{Name: xssName, Kind: xssName, Role: xssName, New: true}},
			AdminLink:   linkAdmin,
		}, true},
		{"decision", TemplateDecision, DecisionData{
			Name: xssName,
			Items: []DecisionItem{
				{What: xssName, Outcome: DecisionApproved},
				{What: xssName, Outcome: DecisionRejected, Reason: xssName},
				{What: xssName, Outcome: DecisionWaiting},
			},
			Link: linkLogin,
		}, false},
		{"owner join request", TemplateOwnerJoinRequest, OwnerJoinRequestData{
			CollectiveName: xssName, UserName: xssName, Role: xssName, Link: linkOwnerRequests,
		}, true},
		{"admin request join", TemplateAdminRequest, AdminRequestData{
			Kind: AdminRequestJoin, UserName: xssName, CollectiveName: xssName, OldName: xssName, Role: xssName, Link: linkAdmin,
		}, true},
		{"admin request new collective", TemplateAdminRequest, AdminRequestData{
			Kind: AdminRequestNewCollective, UserName: xssName, CollectiveName: xssName, OldName: xssName, Role: xssName, Link: linkAdmin,
		}, true},
		{"admin request rename", TemplateAdminRequest, AdminRequestData{
			Kind: AdminRequestRename, UserName: xssName, CollectiveName: xssName, OldName: xssName, Role: xssName, Link: linkAdminCollectives,
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subject, text, htmlBody, err := Render(tc.tmpl, tc.data)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if strings.Contains(htmlBody, "<script") {
				t.Errorf("html carries a script tag from user text:\n%s", htmlBody)
			}
			if !strings.Contains(htmlBody, "&lt;script&gt;") {
				t.Errorf("html does not show the user text escaped")
			}
			if !strings.Contains(text, xssName) {
				t.Errorf("text does not carry the user text literally:\n%s", text)
			}
			if tc.inSubject && !strings.Contains(subject, xssName) {
				t.Errorf("subject = %q, want the user text literally (a subject is plain text)", subject)
			}
		})
	}
}

func TestRenderSubjectStaysOnOneLine(t *testing.T) {
	const injected = "\r\nBcc: evil@example.net"
	cases := []struct {
		name   string
		tmpl   Template
		data   any
		prefix string
	}{
		{"admin new signup", TemplateAdminNewSignup,
			AdminNewSignupData{UserName: "Ana" + injected, AdminLink: linkAdmin},
			"Novo cadastro para aprovar: Ana"},
		{"owner join request", TemplateOwnerJoinRequest,
			OwnerJoinRequestData{CollectiveName: "Maré" + injected, UserName: "Ana", Role: "Membro", Link: linkOwnerRequests},
			"Novo pedido para entrar em Maré"},
		{"admin request join", TemplateAdminRequest,
			AdminRequestData{Kind: AdminRequestJoin, UserName: "Ana" + injected, CollectiveName: "Maré Alta", Role: "Membro", Link: linkAdmin},
			"Pedido de entrada: Ana"},
		{"admin request new collective", TemplateAdminRequest,
			AdminRequestData{Kind: AdminRequestNewCollective, UserName: "Ana", CollectiveName: "Maré" + injected, Link: linkAdmin},
			"Coletivo novo para aprovar: Maré"},
		{"admin request rename, old name", TemplateAdminRequest,
			AdminRequestData{Kind: AdminRequestRename, UserName: "Ana", OldName: "Maré" + injected, CollectiveName: "Maré Alta", Link: linkAdminCollectives},
			"Coletivo renomeado: Maré"},
		{"admin request rename, new name", TemplateAdminRequest,
			AdminRequestData{Kind: AdminRequestRename, UserName: "Ana", OldName: "Maré Alta", CollectiveName: "Alta" + injected, Link: linkAdminCollectives},
			"Coletivo renomeado: Maré Alta → Alta"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subject, _, _, err := Render(tc.tmpl, tc.data)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if strings.ContainsAny(subject, "\r\n") {
				t.Errorf("subject spans more than one line: %q", subject)
			}
			if !strings.HasPrefix(subject, tc.prefix) {
				t.Errorf("subject = %q, want prefix %q", subject, tc.prefix)
			}
		})
	}
}

// itemsInOrder fails when a phrase is missing from body or comes before the
// phrase listed ahead of it.
func itemsInOrder(t *testing.T, view, body string, phrases ...string) {
	t.Helper()
	from := 0
	for _, phrase := range phrases {
		i := strings.Index(body[from:], phrase)
		if i < 0 {
			t.Errorf("%s: %q is missing or out of order:\n%s", view, phrase, body)
			return
		}
		from += i + len(phrase)
	}
}

func TestRenderDecisionKeepsEachReasonWithItsItem(t *testing.T) {
	data := DecisionData{
		Name: "Ana Souza",
		Items: []DecisionItem{
			{What: "sua conta", Outcome: DecisionApproved},
			{What: "o coletivo Alfa", Outcome: DecisionRejected, Reason: "sem comprovação"},
			{What: "o coletivo Beta", Outcome: DecisionWaiting, Reason: "falta o contato"},
			{What: "o coletivo Gama", Outcome: DecisionApproved},
		},
		Link: linkLogin,
	}
	_, text, htmlBody, err := Render(TemplateDecision, data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, view := range []struct{ name, body string }{{"text", text}, {"html", html.UnescapeString(htmlBody)}} {
		itemsInOrder(t, view.name, view.body,
			"sua conta: aprovado",
			"o coletivo Alfa: recusado", "motivo: sem comprovação",
			"o coletivo Beta: aprovado pelo admin; falta um administrador do coletivo", "motivo: falta o contato",
			"o coletivo Gama: aprovado")
		if n := strings.Count(view.body, "motivo:"); n != 2 {
			t.Errorf("%s: %d reasons, want 2 (one per item that has one)", view.name, n)
		}
	}
}

func TestRenderDecisionReasonKeepsLineBreaks(t *testing.T) {
	for _, sep := range []string{"\n", "\r\n"} {
		t.Run(strings.NewReplacer("\r", "CR", "\n", "LF").Replace(sep), func(t *testing.T) {
			data := DecisionData{
				Name:  "Ana Souza",
				Items: []DecisionItem{{What: "sua conta", Outcome: DecisionRejected, Reason: "primeira linha" + sep + "segunda " + boldName}},
				Link:  linkLogin,
			}
			_, text, htmlBody, err := Render(TemplateDecision, data)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if want := "motivo: primeira linha" + sep + "segunda " + boldName; !strings.Contains(text, want) {
				t.Errorf("text does not carry the reason as it is, want %q:\n%s", want, text)
			}
			if want := "motivo: primeira linha<br>segunda &lt;b&gt;negrito&lt;/b&gt;"; !strings.Contains(htmlBody, want) {
				t.Errorf("html does not break the reason into lines, want %q:\n%s", want, htmlBody)
			}
			if strings.Contains(htmlBody, "\r") {
				t.Errorf("html carries a carriage return from the reason")
			}
		})
	}
}

func TestRenderEscapesMarkupInNamesAndReasons(t *testing.T) {
	cases := []struct {
		name string
		tmpl Template
		data any
	}{
		{"decision", TemplateDecision, DecisionData{
			Name:  boldName,
			Items: []DecisionItem{{What: "o coletivo " + boldName, Outcome: DecisionRejected, Reason: "veja " + boldName}},
			Link:  linkLogin,
		}},
		{"owner join request", TemplateOwnerJoinRequest, OwnerJoinRequestData{
			CollectiveName: boldName, UserName: boldName, Role: "Membro", Link: linkOwnerRequests,
		}},
		{"admin request", TemplateAdminRequest, AdminRequestData{
			Kind: AdminRequestRename, UserName: boldName, OldName: boldName, CollectiveName: boldName, Link: linkAdminCollectives,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, text, htmlBody, err := Render(tc.tmpl, tc.data)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			// Not "<b": the reason's line breaks make <br> legitimate.
			if strings.Contains(htmlBody, "<b>") || strings.Contains(htmlBody, "</b>") {
				t.Errorf("html carries markup from user text:\n%s", htmlBody)
			}
			if !strings.Contains(htmlBody, "&lt;b&gt;negrito&lt;/b&gt;") {
				t.Errorf("html does not show the user text escaped:\n%s", htmlBody)
			}
			if !strings.Contains(text, boldName) {
				t.Errorf("text does not carry the user text literally:\n%s", text)
			}
		})
	}
}

func TestRenderAdminNewSignupListsEachCollective(t *testing.T) {
	data := AdminNewSignupData{
		UserName:        "Ana Souza",
		UserEmail:       "ana@example.com",
		UserPhone:       "+55 85 99999-0000",
		PhoneIsWhatsApp: true,
		Collectives: []SignupCollective{
			{Name: "Maré Alta", Kind: "Coletivo", Role: "Dono", New: true},
			{Name: "Sol Nascente", Kind: "Produtora", Role: "Membro", New: false},
		},
		AdminLink: linkAdmin,
	}
	_, text, htmlBody, err := Render(TemplateAdminNewSignup, data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	// Each collective has a line of its own in the text and a row of its own in the HTML.
	rowOf := func(body, sep, name string) string {
		for _, chunk := range strings.Split(body, sep) {
			if strings.Contains(chunk, name) {
				return chunk
			}
		}
		t.Fatalf("no %q segment mentions %q", sep, name)
		return ""
	}
	for _, view := range []struct{ name, body, sep string }{{"text", text, "\n"}, {"html", htmlBody, "</tr>"}} {
		fresh := rowOf(view.body, view.sep, "Maré Alta")
		for _, want := range []string{"Coletivo", "Dono", "novo"} {
			if !strings.Contains(strings.ToLower(fresh), strings.ToLower(want)) {
				t.Errorf("%s: the new collective's entry lacks %q: %q", view.name, want, fresh)
			}
		}
		known := rowOf(view.body, view.sep, "Sol Nascente")
		for _, want := range []string{"Produtora", "Membro"} {
			if !strings.Contains(known, want) {
				t.Errorf("%s: the existing collective's entry lacks %q: %q", view.name, want, known)
			}
		}
		if strings.Contains(strings.ToLower(known), "novo") {
			t.Errorf("%s: the existing collective is marked as new: %q", view.name, known)
		}
	}
	if !strings.Contains(text, "WhatsApp") {
		t.Errorf("text does not say the phone is a WhatsApp number:\n%s", text)
	}

	data.PhoneIsWhatsApp = false
	_, text, _, err = Render(TemplateAdminNewSignup, data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(text, "WhatsApp") {
		t.Errorf("text mentions WhatsApp for a plain phone number:\n%s", text)
	}

	data.Collectives = nil
	if _, _, _, err := Render(TemplateAdminNewSignup, data); err != nil {
		t.Errorf("Render without collectives: %v", err)
	}
}

func TestRenderRejectsBadInput(t *testing.T) {
	cases := []struct {
		name string
		tmpl Template
		data any
	}{
		{"unknown template", Template("nope"), VerifyEmailData{}},
		{"empty template", Template(""), VerifyEmailData{}},
		{"nil data", TemplateVerifyEmail, nil},
		{"wrong data type", TemplateVerifyEmail, "Ana"},
		{"decision with another template's data", TemplateDecision, VerifyEmailData{Name: "Ana", Link: linkLogin}},
		{"admin request with another template's data", TemplateAdminRequest, OwnerJoinRequestData{CollectiveName: "Maré Alta", UserName: "Ana", Role: "Membro", Link: linkAdmin}},
		{"decision with an unknown outcome", TemplateDecision, DecisionData{
			Name: "Ana Souza", Link: linkLogin,
			Items: []DecisionItem{{What: "sua conta", Outcome: "maybe"}},
		}},
		{"decision with no outcome", TemplateDecision, DecisionData{
			Name: "Ana Souza", Link: linkLogin,
			Items: []DecisionItem{{What: "sua conta"}},
		}},
		{"decision with an unknown outcome after a good one", TemplateDecision, DecisionData{
			Name: "Ana Souza", Link: linkLogin,
			Items: []DecisionItem{{What: "sua conta", Outcome: DecisionApproved}, {What: "o coletivo Alfa", Outcome: "Approved"}},
		}},
		{"admin request with an unknown kind", TemplateAdminRequest, AdminRequestData{
			Kind: "bogus", UserName: "Ana Souza", CollectiveName: "Maré Alta", Link: linkAdmin,
		}},
		{"admin request with no kind", TemplateAdminRequest, AdminRequestData{
			UserName: "Ana Souza", CollectiveName: "Maré Alta", Link: linkAdmin,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, _, err := Render(tc.tmpl, tc.data); err == nil {
				t.Error("Render succeeded, want an error")
			}
		})
	}
}
