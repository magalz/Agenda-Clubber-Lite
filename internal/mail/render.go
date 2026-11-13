package mail

import (
	"bytes"
	"embed"
	"fmt"
	htemplate "html/template"
	"reflect"
	"strings"
	ttemplate "text/template"
)

// Template names one email.
type Template string

const (
	TemplateVerifyEmail     Template = "verify_email"
	TemplateExistingAccount Template = "existing_account"
	TemplateAdminNewSignup  Template = "admin_new_signup"
	TemplatePasswordReset   Template = "password_reset"

	TemplateDecision         Template = "decision"
	TemplateOwnerJoinRequest Template = "owner_join_request"
	TemplateAdminRequest     Template = "admin_request"
)

// VerifyEmailData is the data of TemplateVerifyEmail. The link confirms the
// address and is good for 24 hours.
type VerifyEmailData struct {
	Name string
	Link string
}

// ExistingAccountData is the data of TemplateExistingAccount: the notice a
// person gets when they sign up with an address that already has an account.
// ForgotPasswordLink is the forgot-password page ({APP_ORIGIN}/senha/esqueci),
// never a reset-token link: signup does not issue reset tokens, so the email
// states no expiry.
type ExistingAccountData struct {
	Name               string
	LoginLink          string
	ForgotPasswordLink string
}

// SignupCollective is one collective or producer in an admin notice. Kind and
// Role are already Portuguese labels.
type SignupCollective struct {
	Name string
	Kind string
	Role string
	New  bool
}

// AdminNewSignupData is the data of TemplateAdminNewSignup.
type AdminNewSignupData struct {
	UserName        string
	UserEmail       string
	UserPhone       string
	PhoneIsWhatsApp bool
	Collectives     []SignupCollective
	AdminLink       string
}

// PasswordResetData is the data of TemplatePasswordReset. The link is good
// for 1 hour.
type PasswordResetData struct {
	Name string
	Link string
}

// Outcomes of a DecisionItem. Render refuses any other value.
const (
	DecisionApproved = "approved"
	DecisionRejected = "rejected"
	// DecisionWaiting is a join request the site admin approved that still needs
	// an Administrador of the collective.
	DecisionWaiting = "waiting"
)

// DecisionItem is one thing decided in a DecisionData email. What is built by
// the caller and printed before the outcome, for example "sua conta", "o
// coletivo Alfa" or "seu pedido para entrar em Beta como Membro". Reason is the
// free text of whoever decided; it is shown only when it is not empty.
type DecisionItem struct {
	What    string
	Outcome string // DecisionApproved, DecisionRejected or DecisionWaiting
	Reason  string
}

// DecisionData is the data of TemplateDecision: one email per decision call,
// listing everything that was decided in it. Link is where the person goes next
// (the login page or their collectives).
type DecisionData struct {
	Name  string
	Items []DecisionItem
	Link  string
}

// OwnerJoinRequestData is the data of TemplateOwnerJoinRequest: the notice the
// Administradores of a collective get when a join request becomes visible to
// them. Role is a label ("Membro", "Administrador"), not a stored role.
type OwnerJoinRequestData struct {
	CollectiveName string
	UserName       string
	Role           string
	Link           string
}

// Kinds of AdminRequestData. Render refuses any other value.
const (
	AdminRequestJoin          = "join"
	AdminRequestNewCollective = "new_collective"
	AdminRequestRename        = "rename"
)

// AdminRequestData is the data of TemplateAdminRequest: the notice the site
// admins get about a request. Kind picks the wording and which fields are used:
// join uses UserName, CollectiveName and Role; new_collective uses UserName and
// CollectiveName; rename uses UserName, OldName and CollectiveName (the new name).
type AdminRequestData struct {
	Kind           string // AdminRequestJoin, AdminRequestNewCollective or AdminRequestRename
	UserName       string
	CollectiveName string
	OldName        string
	Role           string
	Link           string
}

//go:embed templates/*.tmpl
var templateFS embed.FS

// email is one parsed template: a plain-text set and an HTML set built from
// the same files, and the type of data it takes.
type email struct {
	data reflect.Type
	text *ttemplate.Template
	html *htemplate.Template
}

// templateFuncs is shared by both sets, so an email's text and HTML parts are
// written from one source.
var templateFuncs = map[string]any{"btn": btn, "fail": fail, "lines": lines}

// button is what the btn function hands to the html_button partial.
type button struct {
	Label string
	Href  string
}

// btn makes a button for the html_button partial. It returns a plain struct, so
// html/template escapes the label and the link like any other value.
func btn(label, href string) button { return button{Label: label, Href: href} }

// fail makes a template stop on a value it has no wording for (an unknown
// outcome or kind), so Render returns an error instead of a blank or a default.
func fail(what, got string) (string, error) {
	return "", fmt.Errorf("unknown %s %q", what, got)
}

// lines splits user text into its lines, so the HTML part can join them with
// <br>: the text stays escaped and no template.HTML is needed.
func lines(s string) []string {
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

var emails = map[Template]email{
	TemplateVerifyEmail:     compile(TemplateVerifyEmail, VerifyEmailData{}),
	TemplateExistingAccount: compile(TemplateExistingAccount, ExistingAccountData{}),
	TemplateAdminNewSignup:  compile(TemplateAdminNewSignup, AdminNewSignupData{}),
	TemplatePasswordReset:   compile(TemplatePasswordReset, PasswordResetData{}),

	TemplateDecision:         compile(TemplateDecision, DecisionData{}),
	TemplateOwnerJoinRequest: compile(TemplateOwnerJoinRequest, OwnerJoinRequestData{}),
	TemplateAdminRequest:     compile(TemplateAdminRequest, AdminRequestData{}),
}

// compile parses the shared layout and the template's own file. A parse error
// is a bug in the embedded files, so it panics at startup.
func compile(t Template, data any) email {
	files := []string{"templates/_layout.tmpl", "templates/" + string(t) + ".tmpl"}
	return email{
		data: reflect.TypeOf(data),
		text: ttemplate.Must(ttemplate.New(string(t)).Funcs(templateFuncs).ParseFS(templateFS, files...)),
		html: htemplate.Must(htemplate.New(string(t)).Funcs(templateFuncs).ParseFS(templateFS, files...)),
	}
}

// Render renders t with data, which must be the data type of t. The subject is
// plain text on one line; the HTML part escapes every value, the text part
// prints it as it is.
func Render(t Template, data any) (subject, text, html string, err error) {
	e, ok := emails[t]
	if !ok {
		return "", "", "", fmt.Errorf("mail: unknown template %q", string(t))
	}
	if got := reflect.TypeOf(data); got != e.data {
		return "", "", "", fmt.Errorf("mail: template %q takes %s, got %v", string(t), e.data, got)
	}
	var s, tx, h bytes.Buffer
	if err := e.text.ExecuteTemplate(&s, "subject", data); err != nil {
		return "", "", "", fmt.Errorf("mail: render %s subject: %w", t, err)
	}
	if err := e.text.ExecuteTemplate(&tx, "text", data); err != nil {
		return "", "", "", fmt.Errorf("mail: render %s text: %w", t, err)
	}
	if err := e.html.ExecuteTemplate(&h, "html", data); err != nil {
		return "", "", "", fmt.Errorf("mail: render %s html: %w", t, err)
	}
	// A subject is one line: user text in it (a name) may carry line breaks.
	return strings.Join(strings.Fields(s.String()), " "), tx.String(), h.String(), nil
}
