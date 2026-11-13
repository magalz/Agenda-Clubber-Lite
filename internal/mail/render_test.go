package mail

import (
	"strings"
	"testing"
)

const (
	linkConfirm = "https://agenda.example/confirmar/abc123_-xyz"
	linkLogin   = "https://agenda.example/entrar"
	linkReset   = "https://agenda.example/redefinir-senha/def456_-uvw"
	linkAdmin   = "https://agenda.example/admin/aprovacoes"
	xssName     = `<script>alert(1)</script>`
)

// renderCase is one template with data that carries no markup.
type renderCase struct {
	name    string
	tmpl    Template
	data    any
	subject string
	links   []string // each must be a plain-text link and a clickable one
	phrases []string // each must appear in the text and in the HTML
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
			data:    ExistingAccountData{Name: "Ana Souza", LoginLink: linkLogin, ResetLink: linkReset},
			subject: "Você já tem uma conta · Agenda Clubber",
			links:   []string{linkLogin, linkReset},
			phrases: []string{"Ana Souza"},
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
			for _, phrase := range tc.phrases {
				if !strings.Contains(text, phrase) {
					t.Errorf("text lacks %q:\n%s", phrase, text)
				}
				if !strings.Contains(htmlBody, phrase) {
					t.Errorf("html lacks %q", phrase)
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
		name string
		tmpl Template
		data any
	}{
		{"verify email", TemplateVerifyEmail, VerifyEmailData{Name: xssName, Link: linkConfirm}},
		{"existing account", TemplateExistingAccount, ExistingAccountData{Name: xssName, LoginLink: linkLogin, ResetLink: linkReset}},
		{"password reset", TemplatePasswordReset, PasswordResetData{Name: xssName, Link: linkReset}},
		{"admin new signup", TemplateAdminNewSignup, AdminNewSignupData{
			UserName:    xssName,
			UserEmail:   xssName,
			UserPhone:   xssName,
			Collectives: []SignupCollective{{Name: xssName, Kind: xssName, Role: xssName, New: true}},
			AdminLink:   linkAdmin,
		}},
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
			if tc.tmpl == TemplateAdminNewSignup && !strings.Contains(subject, xssName) {
				t.Errorf("subject = %q, want the user text literally (a subject is plain text)", subject)
			}
		})
	}
}

func TestRenderSubjectStaysOnOneLine(t *testing.T) {
	data := AdminNewSignupData{UserName: "Ana\r\nBcc: evil@example.net", AdminLink: linkAdmin}
	subject, _, _, err := Render(TemplateAdminNewSignup, data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.ContainsAny(subject, "\r\n") {
		t.Errorf("subject spans more than one line: %q", subject)
	}
	if !strings.HasPrefix(subject, "Novo cadastro para aprovar: Ana") {
		t.Errorf("subject = %q", subject)
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, _, err := Render(tc.tmpl, tc.data); err == nil {
				t.Error("Render succeeded, want an error")
			}
		})
	}
}
