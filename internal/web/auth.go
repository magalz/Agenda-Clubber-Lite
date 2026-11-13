package web

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// msgTooMany is the message of every rate-limited answer.
const msgTooMany = "muitas tentativas. espere alguns minutos e tente de novo."

// Gin context keys set by loadSession. They are unexported strings, so only
// this file reads them; handlers use currentUser and sessionToken.
const (
	sessionUserKey  = "web.session_user"
	sessionTokenKey = "web.session_token"
)

// sameOrigin refuses every request that can change state unless it comes from
// this site: the Origin header must equal APP_ORIGIN exactly (a missing one is
// refused too) and Sec-Fetch-Site must not say cross-site. GET, HEAD and
// OPTIONS are left alone; they never change state here.
func (a *App) sameOrigin() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		origin := c.GetHeader("Origin")
		if origin == "" || origin != a.Cfg.Origin || c.GetHeader("Sec-Fetch-Site") == "cross-site" {
			c.String(http.StatusForbidden, "origem não permitida")
			c.Abort()
			return
		}
		c.Next()
	}
}

// loadSession finds the signed-in user from the session cookie. A request
// without a valid session carries on as anonymous (a cookie that no longer
// names one is cleared); only a store failure stops it, with a 500.
func (a *App) loadSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, err := c.Cookie(auth.SessionCookieName)
		if err != nil { // no cookie
			c.Next()
			return
		}
		ctx := c.Request.Context()
		repos := a.Store.Repos()

		s, err := auth.ResolveSession(ctx, repos.Sessions, raw, a.Now())
		if errors.Is(err, auth.ErrNoSession) {
			a.setSessionCookie(c, "", -1)
			c.Next()
			return
		}
		if err != nil {
			a.sessionFailed(c, err)
			return
		}

		user, err := repos.Users.ByID(ctx, s.UserID)
		if err == nil && user.Status == domain.UserStatusActive {
			c.Set(sessionUserKey, user)
			c.Set(sessionTokenKey, raw)
			c.Next()
			return
		}
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			a.sessionFailed(c, err)
			return
		}
		// The user is gone or no longer active: the session ends with it.
		if err := auth.EndSession(ctx, repos.Sessions, raw); err != nil {
			a.Log.Error("end session of an inactive user", "err", err)
		}
		a.setSessionCookie(c, "", -1)
		c.Next()
	}
}

func (a *App) sessionFailed(c *gin.Context, err error) {
	a.Log.Error("load session", "err", err)
	c.String(http.StatusInternalServerError, "erro interno")
	c.Abort()
}

// currentUser returns the user loadSession found, and false for an anonymous
// request (or a route outside loadSession).
func (a *App) currentUser(c *gin.Context) (domain.User, bool) {
	v, ok := c.Get(sessionUserKey)
	if !ok {
		return domain.User{}, false
	}
	u, ok := v.(domain.User)
	return u, ok
}

// sessionToken returns the raw session cookie value of the signed-in user, or
// "" for an anonymous request. Handlers pass it to auth.RevokeOtherSessions.
func (a *App) sessionToken(c *gin.Context) string {
	return c.GetString(sessionTokenKey)
}

// requireAuth sends an anonymous request to the login page. A page request (a
// GET or HEAD that is not HTMX) carries its own address in ?next= so the login
// can return to it; an HTMX request is a fragment, not somewhere to return to.
func (a *App) requireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := a.currentUser(c); ok {
			c.Next()
			return
		}
		to := "/entrar"
		if c.GetHeader("HX-Request") != "true" && (c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead) {
			if next := c.Request.URL.RequestURI(); next != "/" {
				to += "?next=" + url.QueryEscape(next)
			}
		}
		a.denyTo(c, to)
	}
}

// requireFreshPassword keeps a user whose password has expired on the pages
// that change it (and on logout) until they set a new one.
func (a *App) requireFreshPassword() gin.HandlerFunc {
	return func(c *gin.Context) {
		u, ok := a.currentUser(c)
		if !ok || !auth.PasswordExpired(u.PasswordChangedAt, a.Now()) {
			c.Next()
			return
		}
		switch c.Request.URL.Path {
		case "/senha/trocar", "/senha/verificar", "/sair":
			c.Next()
		default:
			a.denyTo(c, "/senha/trocar")
		}
	}
}

// requireAdmin answers 403 unless the signed-in user is the admin. Put it
// after requireAuth, which sends an anonymous visitor to the login page.
func (a *App) requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if u, ok := a.currentUser(c); !ok || !u.IsAdmin {
			c.String(http.StatusForbidden, "acesso negado")
			c.Abort()
			return
		}
		c.Next()
	}
}

// denyTo stops the request and sends it to a page: HTMX gets a 401 with
// HX-Redirect (a 303 would be followed in place and swapped into the page),
// anything else a 303.
func (a *App) denyTo(c *gin.Context, to string) {
	if c.GetHeader("HX-Request") == "true" {
		c.Header("HX-Redirect", to)
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	c.Redirect(http.StatusSeeOther, to)
	c.Abort()
}

// redirect answers a successful action: HTMX gets a 204 with HX-Redirect,
// anything else a 303.
func (a *App) redirect(c *gin.Context, to string) {
	if c.GetHeader("HX-Request") == "true" {
		c.Header("HX-Redirect", to)
		c.Status(http.StatusNoContent)
		c.Writer.WriteHeaderNow()
		return
	}
	c.Redirect(http.StatusSeeOther, to)
}

// signIn starts a session for the user: it ends the one the request came with
// (so a token is never reused across logins), stores a new one and sets the
// cookie. The caller has already verified the person.
func (a *App) signIn(c *gin.Context, userID string) error {
	ctx := c.Request.Context()
	sessions := a.Store.Repos().Sessions
	if old, err := c.Cookie(auth.SessionCookieName); err == nil {
		if err := auth.EndSession(ctx, sessions, old); err != nil {
			return err
		}
	}
	raw, _, err := auth.StartSession(ctx, sessions, userID, c.Request.UserAgent(), a.Now())
	if err != nil {
		return err
	}
	a.setSessionCookie(c, raw, int(auth.SessionMaxLifetime/time.Second))
	return nil
}

// signOut ends the session of the request and clears the cookie.
func (a *App) signOut(c *gin.Context) error {
	if raw, err := c.Cookie(auth.SessionCookieName); err == nil {
		if err := auth.EndSession(c.Request.Context(), a.Store.Repos().Sessions, raw); err != nil {
			return err
		}
	}
	a.setSessionCookie(c, "", -1)
	return nil
}

// setSessionCookie writes the session cookie: the __Host- rules (Secure, Path=/,
// no Domain) hold in every environment, so it is Secure in dev too. A response
// carries at most one Set-Cookie for it, the last one set, so clearing a stale
// cookie and then signing in does not send both.
func (a *App) setSessionCookie(c *gin.Context, value string, maxAge int) {
	h := c.Writer.Header()
	prefix := auth.SessionCookieName + "="
	var others []string
	for _, v := range h.Values("Set-Cookie") {
		if !strings.HasPrefix(v, prefix) {
			others = append(others, v)
		}
	}
	h.Del("Set-Cookie")
	for _, v := range others {
		h.Add("Set-Cookie", v)
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge, // negative: Max-Age=0
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// safeNext returns raw when it is a path on this site, else "/". It guards the
// ?next= redirect after login against open redirects: a scheme, a host, a
// protocol-relative "//host", a backslash (browsers read "/\host" as "//host")
// and control characters (header injection) are all refused.
func safeNext(raw string) string {
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return "/"
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f || r == '\\' {
			return "/"
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return "/"
	}
	return raw
}

// parseForm returns the fields of a POST body. A body over the size limit, a
// malformed escape or text that is not valid UTF-8 is an error, which the
// handler answers with a 400. Query-string parameters are not part of it.
func (a *App) parseForm(c *gin.Context) (url.Values, error) {
	if err := c.Request.ParseForm(); err != nil {
		return nil, err
	}
	form := c.Request.PostForm
	for k, vs := range form {
		if !utf8.ValidString(k) {
			return nil, errors.New("form: a field name is not valid UTF-8")
		}
		for _, v := range vs {
			if !utf8.ValidString(v) {
				return nil, errors.New("form: a field value is not valid UTF-8")
			}
		}
	}
	return form, nil
}

// viewUser is the signed-in user as the templates see it, or nil when the
// request is anonymous. A failed membership lookup is logged and reads as "may
// not create events": the page still renders, without the link.
func (a *App) viewUser(c *gin.Context) *ViewUser {
	u, ok := a.currentUser(c)
	if !ok {
		return nil
	}
	v := &ViewUser{ID: u.ID, Name: u.Name, IsAdmin: u.IsAdmin}
	can, err := a.Store.Repos().Memberships.UserCanCreateEvents(c.Request.Context(), u.ID)
	if err != nil {
		a.Log.Error("check event creation rights", "user", u.ID, "err", err)
	}
	v.CanCreateEvents = err == nil && can
	if days, warn := auth.ExpiryWarning(u.PasswordChangedAt, a.Now()); warn {
		v.PasswordDaysLeft = days
	}
	return v
}

// page renders a page with the signed-in user filled in, unless the handler set
// one.
func (a *App) page(c *gin.Context, status int, name string, data PageData) {
	if data.User == nil {
		data.User = a.viewUser(c)
	}
	a.Render.Page(c, status, name, data)
}

// formPage re-renders a form with a 422, the person's input in data.Form and
// the validation messages in errs.
func (a *App) formPage(c *gin.Context, name string, data PageData, errs domain.FieldErrors) {
	data.Errors = errs
	a.page(c, http.StatusUnprocessableEntity, name, data)
}
