package accounts

import (
	"context"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// SignupTokenTTL is how long the email confirmation link works.
const SignupTokenTTL = 24 * time.Hour

// SignupInput is the signup form as the person filled it in.
type SignupInput struct {
	Name            string
	Email           string
	Phone           string // as typed; only its digits are kept
	PhoneIsWhatsApp bool
	Password        string
	PasswordConfirm string
	CollectiveKind  domain.CollectiveKind // coletivo or produtora
	CollectiveID    string                // an approved collective picked from the suggestions; empty when a name was typed
	CollectiveName  string                // the typed name, used when CollectiveID is empty
	Role            domain.Role
}

// Signup is not written yet.
func Signup(ctx context.Context, store domain.Store, origin string, now time.Time, in SignupInput) error {
	return nil
}
