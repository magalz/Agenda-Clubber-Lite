package mail

// Template names one email.
type Template string

const (
	TemplateVerifyEmail     Template = "verify_email"
	TemplateExistingAccount Template = "existing_account"
	TemplateAdminNewSignup  Template = "admin_new_signup"
	TemplatePasswordReset   Template = "password_reset"
)

// VerifyEmailData is the data of TemplateVerifyEmail.
type VerifyEmailData struct {
	Name string
	Link string
}

// ExistingAccountData is the data of TemplateExistingAccount.
type ExistingAccountData struct {
	Name      string
	LoginLink string
	ResetLink string
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

// PasswordResetData is the data of TemplatePasswordReset.
type PasswordResetData struct {
	Name string
	Link string
}

// Render renders t with data.
func Render(t Template, data any) (subject, text, html string, err error) {
	return "", "", "", errNotImplemented
}
