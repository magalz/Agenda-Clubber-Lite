package web

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/accounts"
	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// signupSuggestionLimit is how many collectives the suggestions list shows.
const signupSuggestionLimit = 8

// signupPage is the Data of the signup page.
type signupPage struct {
	Checklist []passwordRule
	Notice    string // a status message above the form; empty for none
}

// passwordRule is one line of a password checklist: the rule's key (the
// data-rule attribute the page script ticks) and its label.
type passwordRule struct{ Rule, Label string }

// passwordChecklist lists the rules a password page shows, in the order of
// auth.Rules, plus the history rule at the end where a new password is checked
// against the old ones.
func passwordChecklist(withHistory bool) []passwordRule {
	rules := auth.Rules()
	if withHistory {
		rules = append(rules, auth.RuleHistory)
	}
	list := make([]passwordRule, len(rules))
	for i, r := range rules {
		list[i] = passwordRule{Rule: string(r), Label: auth.RuleLabels[r]}
	}
	return list
}

// signupRoutes registers the signup pages and the collective suggestions (/cadastro, /cadastro/enviado, /coletivos/sugestoes).
// public needs no session; authed requires one (see Router).
func (a *App) signupRoutes(public, authed *gin.RouterGroup) {
	public.GET("/cadastro", a.signupForm)
	public.POST("/cadastro", a.signupSubmit)
	public.GET("/cadastro/enviado", a.signupSent)
	public.GET("/coletivos/sugestoes", a.signupSuggest)
}

// signupData is the data of the signup page for what the person typed so far.
func signupData(form url.Values, notice string) PageData {
	return PageData{
		Title: "Criar conta",
		Form:  form,
		Data:  signupPage{Checklist: passwordChecklist(false), Notice: notice},
	}
}

// signupEmailKey is the limiter key that counts the posts naming an e-mail. It
// holds the SHA-256 of the lowercased address, not the address: the body may
// carry 64 KiB there, and a limiter keeps every key for its whole window.
func signupEmailKey(email string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return "signup-email:" + hex.EncodeToString(sum[:])
}

// emailForm is the stub of the helper the next commit writes.
func (a *App) emailForm(c *gin.Context, lim *auth.Limiter, prefix string, now time.Time) (url.Values, bool, error) {
	return nil, false, nil
}

// signupKept is the form without its passwords: a password is never written
// back into a page.
func signupKept(form url.Values) url.Values {
	kept := maps.Clone(form)
	delete(kept, "password")
	delete(kept, "password_confirm")
	return kept
}

func (a *App) signupForm(c *gin.Context) {
	a.page(c, http.StatusOK, "auth/cadastro", signupData(nil, ""))
}

func (a *App) signupSent(c *gin.Context) {
	a.page(c, http.StatusOK, "auth/cadastro-enviado", PageData{Title: "Confira seu e-mail"})
}

// signupSubmit registers the account. Every post counts against the client
// address, and, when it names an e-mail, against that e-mail too (from any
// address), so one person cannot be mailed in bulk. A refused address does not
// spend the e-mail's budget. The same page follows whether or not the e-mail
// already had an account.
func (a *App) signupSubmit(c *gin.Context) {
	now := a.Now()
	allowed := a.Limits.Signup.Allow("signup:"+c.ClientIP(), now)
	form, err := a.parseForm(c)
	if err != nil {
		c.String(http.StatusBadRequest, "requisição inválida")
		return
	}
	if email := form.Get("email"); allowed && strings.TrimSpace(email) != "" {
		allowed = a.Limits.Signup.Allow(signupEmailKey(email), now)
	}
	kept := signupKept(form)
	if !allowed {
		a.page(c, http.StatusTooManyRequests, "auth/cadastro", signupData(kept, msgTooMany))
		return
	}

	in := accounts.SignupInput{
		Name:            form.Get("name"),
		Email:           form.Get("email"),
		Phone:           form.Get("phone"),
		PhoneIsWhatsApp: form.Get("phone_whatsapp") == "1",
		Password:        form.Get("password"),
		PasswordConfirm: form.Get("password_confirm"),
		CollectiveKind:  domain.CollectiveKind(form.Get("collective_kind")),
		CollectiveID:    form.Get("collective_id"),
		CollectiveName:  form.Get("collective_name"),
		Role:            domain.Role(form.Get("role")),
	}
	var fieldErrs domain.FieldErrors
	switch err := accounts.Signup(c.Request.Context(), a.Store, a.Cfg.Origin, now, in); {
	case err == nil:
		a.redirect(c, "/cadastro/enviado")
	case errors.As(err, &fieldErrs):
		a.formPage(c, "auth/cadastro", signupData(kept, ""), fieldErrs)
	default:
		a.Log.Error("signup", "err", err)
		c.String(http.StatusInternalServerError, "erro interno")
	}
}

// signupSuggest lists the approved collectives whose name contains the query,
// for the signup page's autocomplete. The query is never logged. It answers
// with the empty partial for a query too short or too long to be a name.
func (a *App) signupSuggest(c *gin.Context) {
	if !a.Limits.Suggestions.Allow("suggestions:"+c.ClientIP(), a.Now()) {
		c.Status(http.StatusTooManyRequests)
		c.Writer.WriteHeaderNow()
		return
	}
	kind := domain.CollectiveKind(c.Query("tipo"))
	q := c.Query("q")
	if (kind != "" && !kind.Valid()) || !utf8.ValidString(q) {
		c.String(http.StatusBadRequest, "requisição inválida")
		return
	}
	var found []domain.Collective
	if q = strings.TrimSpace(q); utf8.RuneCountInString(q) >= 2 && utf8.RuneCountInString(q) <= domain.CollectiveNameMax {
		var err error
		if found, err = a.Store.Repos().Collectives.SearchApproved(c.Request.Context(), q, kind, signupSuggestionLimit); err != nil {
			a.Log.Error("search collectives", "err", err)
			c.String(http.StatusInternalServerError, "erro interno")
			return
		}
	}
	a.Render.Partial(c, http.StatusOK, "signup-suggestions", found)
}
