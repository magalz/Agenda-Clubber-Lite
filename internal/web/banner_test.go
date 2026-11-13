package web

import (
	"io/fs"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
	webfs "github.com/magalz/agenda-clubber-lite/web"
)

const (
	bannerLink   = "[→] trocar agora"
	bannerTarget = "/senha/trocar"
)

// bannerMarkers are what the banner leaves in a page, whatever the days left.
var bannerMarkers = []string{`role="status"`, "expiry-banner", "sua senha vence", bannerLink, bannerTarget}

func wantNoBanner(t *testing.T, body string) {
	t.Helper()
	for _, m := range bannerMarkers {
		if strings.Contains(body, m) {
			t.Errorf("the page shows the expiry banner (%q) when it should not", m)
		}
	}
}

func TestExpiryBannerInTheAppLayout(t *testing.T) {
	app := newTestApp(t)

	tests := []struct {
		name string
		days int
		want string
	}{
		{"a single day left", 1, "sua senha vence em 1 dia."},
		{"two days left", 2, "sua senha vence em 2 dias."},
		{"three days left", 3, "sua senha vence em 3 dias."},
		{"seven days left", 7, "sua senha vence em 7 dias."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := renderHome(t, app, "calendario", &ViewUser{ID: "u1", Name: "Ana", PasswordDaysLeft: tt.days})

			if n := strings.Count(body, "sua senha vence"); n != 1 {
				t.Fatalf("%d banners, want 1, in:\n%s", n, body)
			}
			wantAll(t, "banner", body, tt.want)
			if tt.days == 1 && strings.Contains(body, "1 dias") {
				t.Error(`the singular reads "1 dias"`)
			}
			wantAll(t, "banner", tagAttrs(t, body, `<div ([^>]*class="expiry-banner"[^>]*)>`), `role="status"`)
			link := linkAttrs(t, body, bannerLink)
			wantAll(t, "banner link", link, `class="expiry-banner__link"`, `href="`+bannerTarget+`"`)

			// The banner opens the main area, ahead of the page content and the footer.
			inOrder(t, body,
				`<main id="conteudo" class="shell__main">`,
				`role="status"`,
				tt.want,
				bannerLink,
				`class="section-heading"`,
				"feito_por --dev=IgnisDev",
			)
			if v := cspViolations(body); len(v) > 0 {
				t.Errorf("%s in:\n%s", strings.Join(v, "; "), body)
			}
		})
	}

	// A value that is not a day count to warn about, and a missing user (the layout
	// must guard it, never read a field of it), show nothing.
	hidden := []struct {
		name string
		user *ViewUser
	}{
		{"no warning", &ViewUser{ID: "u1", Name: "Ana"}},
		{"a negative value", &ViewUser{ID: "u1", Name: "Ana", PasswordDaysLeft: -2}},
		{"logged out", nil},
	}
	for _, tt := range hidden {
		t.Run(tt.name, func(t *testing.T) {
			body := renderHome(t, app, "calendario", tt.user)
			wantNoBanner(t, body)
			if v := cspViolations(body); len(v) > 0 {
				t.Errorf("%s in:\n%s", strings.Join(v, "; "), body)
			}
		})
	}
}

// The banner is only as good as its stylesheet: every class the layout gives it
// has a rule, drawn with the warning token.
func TestExpiryBannerIsStyled(t *testing.T) {
	css, err := fs.ReadFile(webfs.Static, "static/css/components.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, class := range []string{".expiry-banner__text", ".expiry-banner__link"} {
		if !strings.Contains(string(css), class) {
			t.Errorf("components.css has no rule for %q", class)
		}
	}
	// The token is used elsewhere in the file, so look inside the banner's own rule.
	_, rest, found := strings.Cut(string(css), ".expiry-banner {")
	if !found {
		t.Fatal("components.css has no rule for .expiry-banner")
	}
	rule, _, _ := strings.Cut(rest, "}")
	wantAll(t, ".expiry-banner rule", rule, "border: 1px solid var(--color-warn)", "color: var(--color-warn)")
}

// The whole path: the clock, the password's age, the session, viewUser and the
// layout.
func TestExpiryBannerOnTheHomePage(t *testing.T) {
	app, store := newStoreApp(t)
	r := app.Router()
	ageing := func(days int) func(*domain.User) {
		return func(u *domain.User) { u.PasswordChangedAt = webTestNow.Add(-time.Duration(days) * 24 * time.Hour) }
	}
	soon := insertWebUser(t, store, "vencendo@agenda.test", ageing(87))
	recent := insertWebUser(t, store, "recente@agenda.test", ageing(30))
	edge := insertWebUser(t, store, "limite@agenda.test", ageing(83)) // seven days left: the first day of the warning
	early := insertWebUser(t, store, "cedo@agenda.test", ageing(82))  // eight days left: one day too early

	get := func(u domain.User) string {
		t.Helper()
		rec := do(r, http.MethodGet, "/", nil, withCookie(loginAs(t, app, u)))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200, body %q", u.Email, rec.Code, rec.Body)
		}
		body := rec.Body.String()
		if v := cspViolations(body); len(v) > 0 {
			t.Errorf("%s: %s in:\n%s", u.Email, strings.Join(v, "; "), body)
		}
		return body
	}

	body := get(soon)
	wantAll(t, "87 days old", body, "sua senha vence em 3 dias.", `role="status"`)
	wantAll(t, "banner link", linkAttrs(t, body, bannerLink), `href="`+bannerTarget+`"`)

	wantAll(t, "83 days old", get(edge), "sua senha vence em 7 dias.")
	wantNoBanner(t, get(early))
	wantNoBanner(t, get(recent))
}
