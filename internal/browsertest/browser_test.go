//go:build browser

package browsertest

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"

	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/config"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/store/mongotest"
	"github.com/magalz/agenda-clubber-lite/internal/web"
)

func TestBrowserCalendarAndShell(t *testing.T) {
	store := mongotest.New(t)
	loc, err := time.LoadLocation(config.DefaultTZ)
	if err != nil {
		t.Fatalf("load tz: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	eventName := "Noite Subgrave Browser"
	seedFixture(t, store, now, loc, eventName)

	cfg := config.Config{
		Env:      "dev",
		Addr:     ":0",
		Location: loc,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app := web.NewApp(cfg, logger, store)
	app.Now = func() time.Time { return now }

	srv := httptest.NewServer(nil)
	app.Cfg.Origin = srv.URL
	srv.Config.Handler = app.Router()
	t.Cleanup(srv.Close)

	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	t.Cleanup(allocCancel)

	var (
		mu       sync.Mutex
		jsErrors []string
	)
	browserCtx, browserCancel := chromedp.NewContext(allocCtx, chromedp.WithListener(func(ev any) {
		switch e := ev.(type) {
		case *runtime.EventExceptionThrown:
			mu.Lock()
			jsErrors = append(jsErrors, fmt.Sprintf("exception: %s", e.ExceptionDetails.Text))
			mu.Unlock()
		case *cdplog.EventEntryAdded:
			if e.Entry.Level == cdplog.EntryLevel("error") {
				mu.Lock()
				jsErrors = append(jsErrors, fmt.Sprintf("console[%s]: %s", e.Entry.Source, e.Entry.Text))
				mu.Unlock()
			}
		}
	}))
	t.Cleanup(browserCancel)

	ctx, timeoutCancel := context.WithTimeout(browserCtx, 60*time.Second)
	t.Cleanup(timeoutCancel)

	err = chromedp.Do(ctx,
		chromedp.Func(func(ctx context.Context, _ *chromedp.Target) error {
			_, err := chromedp.Call(ctx, cdplog.Enable, cdp.Empty{})
			return err
		}),
		chromedp.EmulateViewport(1280, 800),
		chromedp.Navigate(srv.URL+"/entrar"),
		chromedp.WaitVisible(`#email`),
		chromedp.SendKeys(`#email`, "admin@example.com"),
		chromedp.SendKeys(`#senha`, "S3nha!Forte#Browser"),
		chromedp.Click(`button[type="submit"]`),
		chromedp.WaitVisible(`[x-data="calendarPage"].calendar-page--js`),
	)
	if err != nil {
		t.Fatalf("login and load calendar: %v", err)
	}

	desktopOverflow, err := chromedp.Run(ctx, chromedp.Evaluate[bool](`document.documentElement.scrollWidth > document.documentElement.clientWidth`))
	if err != nil {
		t.Fatalf("check desktop overflow: %v", err)
	}
	if desktopOverflow {
		t.Error("horizontal overflow detected at 1280x800")
	}

	err = chromedp.Do(ctx,
		chromedp.WaitVisible(`a.calendar__chip`),
		chromedp.Click(`a.calendar__chip`),
		chromedp.WaitVisible(`dialog#evento-sheet[open] .sheet__body h2`),
	)
	if err != nil {
		t.Fatalf("open event sheet: %v", err)
	}

	sheetText, err := chromedp.Run(ctx, chromedp.Text(`dialog#evento-sheet .sheet__body`))
	if err != nil {
		t.Fatalf("read event sheet: %v", err)
	}
	if !strings.Contains(sheetText, eventName) {
		t.Errorf("expected sheet to contain %q, got %q", eventName, sheetText)
	}

	if err := chromedp.Do(ctx, chromedp.KeyEvent(kb.Escape)); err != nil {
		t.Fatalf("escape event sheet: %v", err)
	}
	sheetClosed, err := chromedp.Run(ctx, chromedp.Poll[bool](`!document.getElementById('evento-sheet').open`, chromedp.WithPollingInterval(50*time.Millisecond)))
	if err != nil || !sheetClosed {
		t.Errorf("expected dialog#evento-sheet to close on Escape: closed=%v err=%v", sheetClosed, err)
	}

	if err := chromedp.Do(ctx, chromedp.EmulateViewport(375, 812, chromedp.EmulateMobile)); err != nil {
		t.Fatalf("emulate mobile viewport: %v", err)
	}
	mobileOverflow, err := chromedp.Run(ctx, chromedp.Evaluate[bool](`document.documentElement.scrollWidth > document.documentElement.clientWidth`))
	if err != nil {
		t.Fatalf("check mobile overflow: %v", err)
	}
	if mobileOverflow {
		t.Error("horizontal overflow detected at 375x812")
	}

	err = chromedp.Do(ctx,
		chromedp.WaitVisible(`button[aria-controls="menu-lateral"]`),
		chromedp.Click(`button[aria-controls="menu-lateral"]`),
	)
	if err != nil {
		t.Fatalf("open mobile drawer: %v", err)
	}
	mainInertOpen, err := chromedp.Run(ctx, chromedp.Poll[bool](`document.getElementById('conteudo').inert === true`, chromedp.WithPollingInterval(50*time.Millisecond)))
	if err != nil || !mainInertOpen {
		t.Errorf("expected main.inert === true when mobile drawer open: inert=%v err=%v", mainInertOpen, err)
	}

	if err := chromedp.Do(ctx, chromedp.KeyEvent(kb.Escape)); err != nil {
		t.Fatalf("escape mobile drawer: %v", err)
	}
	mainInertClosed, err := chromedp.Run(ctx, chromedp.Poll[bool](`document.getElementById('conteudo').inert === false`, chromedp.WithPollingInterval(50*time.Millisecond)))
	if err != nil || !mainInertClosed {
		t.Errorf("expected main.inert === false when mobile drawer closed: inert=%v err=%v", mainInertClosed, err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(jsErrors) > 0 {
		t.Errorf("browser JS/CSP errors:\n%s", strings.Join(jsErrors, "\n"))
	}
}

func seedFixture(t *testing.T, store domain.Store, now time.Time, loc *time.Location, eventName string) {
	t.Helper()
	ctx := context.Background()
	r := store.Repos()

	hash, err := auth.HashPassword("S3nha!Forte#Browser")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	user := domain.User{
		Name:              "Admin Browser",
		Email:             "admin@example.com",
		Phone:             "85999990000",
		PhoneIsWhatsApp:   true,
		PasswordHash:      hash,
		PasswordChangedAt: now,
		Status:            domain.UserStatusActive,
		IsAdmin:           true,
		EmailVerifiedAt:   &now,
		ApprovedAt:        &now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := r.Users.Create(ctx, &user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	collective := domain.Collective{
		Name:       "Coletivo Browser",
		NameKey:    domain.NameKey("Coletivo Browser"),
		Kind:       domain.CollectiveKindColetivo,
		Status:     domain.CollectiveStatusApproved,
		CreatedBy:  user.ID,
		ApprovedBy: user.ID,
		ApprovedAt: &now,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := r.Collectives.Create(ctx, &collective); err != nil {
		t.Fatalf("create collective: %v", err)
	}

	membership := domain.Membership{
		UserID:          user.ID,
		CollectiveID:    collective.ID,
		Role:            domain.RoleOwner,
		Status:          domain.MembershipStatusApproved,
		AdminApprovedBy: user.ID,
		AdminApprovedAt: &now,
		OwnerApprovedBy: user.ID,
		OwnerApprovedAt: &now,
		RequestedAt:     now,
		DecidedAt:       &now,
	}
	if err := r.Memberships.Create(ctx, membership); err != nil {
		t.Fatalf("create membership: %v", err)
	}

	localNow := now.In(loc)
	startsAt := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 22, 0, 0, 0, loc).UTC()
	event := domain.Event{
		CollectiveID: collective.ID,
		Name:         eventName,
		Type:         domain.EventTypeFesta,
		StartsAt:     startsAt,
		DurationMin:  240,
		Free:         true,
		Style:        "Techno",
		State:        "CE",
		City:         "Fortaleza",
		Venue:        "Galpão Central",
		CreatedBy:    user.ID,
		UpdatedBy:    user.ID,
		CreatedAt:    now,
		UpdatedAt:    now,
		Version:      1,
	}
	if err := r.Events.Create(ctx, &event); err != nil {
		t.Fatalf("create event: %v", err)
	}
}
