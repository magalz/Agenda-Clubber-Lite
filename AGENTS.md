# Agenda Clubber Lite — project conventions

Read this before changing anything. It is the shared contract between tasks.

## Product in one paragraph

A self-hosted shared calendar for party collectives and producers (Nordeste, Brazil). Access needs login; people sign up, confirm their email, and the admin approves them. Collectives/producers are entities: only approved **owners** create events; any owner of the event's collective (or the admin) edits/deletes it. The calendar shows every event, past ones included. "Analisar Data" lists the events already on a given day.

## Language

- UI text, emails and validation messages: **Brazilian Portuguese** (`lang="pt-BR"`).
- Code, identifiers, comments, commits, docs: **English**.
- Dates shown in `America/Fortaleza` (config `APP_TZ`), format `dd/mm/aaaa`, 24h time.

## Stack

- Go (version in `go.mod`), module `github.com/magalz/agenda-clubber-lite`.
- HTTP: Gin (`gin.New()`, never `gin.Default()`), `html/template` for server-rendered pages.
- Front end: HTMX 2 and Alpine.js **CSP build**, both vendored under `web/static/js/vendor/`; vanilla CSS under `web/static/css/`. No build step, no npm at runtime, no CDNs.
- Database: MongoDB, a single-node replica set (transactions need one), reached through the interfaces in `internal/domain`. Driver `go.mongodb.org/mongo-driver/v2` (pure Go, so the binary stays CGO-free). No ODM, no query builder.
- Passwords: argon2id (`golang.org/x/crypto/argon2`). Email: `github.com/wneessen/go-mail`.
- Add a dependency only when the standard library or existing code cannot do the job, and say why in the commit.

## Layout

```
cmd/agenda/main.go     subcommands: serve (default), migrate, healthcheck, create-admin, seed-dev
internal/config/       Config loaded from env (see below)
internal/domain/       entities and the store interfaces the business code depends on
internal/store/storetest/    contract test suite every store implementation must pass
internal/store/mongostore/   the MongoDB implementation of the domain interfaces
internal/store/mongotest/    mongotest.New(t): an isolated test database on MONGO_TEST_URI
internal/auth/         password policy/hash/history/expiry, sessions, rate limiting
internal/accounts/     account flows (signup, confirmation, login, password reset/change/reminders, profile/email change, unconfirmed signup purge); imports only internal/domain, internal/auth and internal/mail
internal/approvals/    the approval flows as service functions (store, actor, time; one WithTx for each write); imports only internal/domain and internal/mail
internal/catalog/      music styles and IBGE states/cities from CircuitoNE (embedded JSON, one copy, pinned to git blobs, LF through .gitattributes) and event types
internal/events/       the event flows as service functions (input parsing, writes, reads, the month grid); imports only internal/domain and internal/catalog
internal/mail/         SMTP sender, outbox worker, email templates
internal/web/          App (dependencies), router, middleware, render, handlers
web/embed.go           package web: go:embed of static/ and templates/
web/templates/         layouts/, partials/, pages/
web/static/            css/, js/ (app.js + vendor/), fonts/, favicon.svg
deploy/                Containerfile, compose.dev.yml
```

Dependency rule: for data access, business code (accounts, approvals, events, `internal/web`) uses only `internal/domain`, never a database driver or `internal/store/mongostore`. Only `cmd/agenda` imports the backend and wires it in. Switching databases means implementing the `internal/domain` interfaces until `internal/store/storetest` passes.

## Configuration (environment)

| Var | Default | Notes |
|---|---|---|
| `APP_ENV` | `prod` | `dev` enables dev-only routes/commands and relaxes nothing security-related except the `Secure` cookie requirement on plain http localhost |
| `APP_ADDR` | `:8120` | listen address |
| `APP_ORIGIN` | (required) | e.g. `https://agendaclubber.magalz.space`; used for the Origin check and absolute links in emails |
| `APP_TZ` | `America/Fortaleza` | IANA zone; `time/tzdata` is embedded. `seed-dev` reads it too: the sample events' dates and times are in this zone |
| `MONGO_URI` | (required) | secret (it may carry credentials; never printed or logged). `mongodb://` only (the driver refuses `directConnection` with `mongodb+srv://`), a single-node replica set, and it must carry `directConnection=true` (checked at startup) |
| `MONGO_DB` | `agenda` | database name: 1 to 38 letters, digits, `_` or `-` |
| `TRUSTED_PROXY_CIDRS` | empty | comma-separated CIDRs whose `CF-Connecting-IP` header is trusted |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM` | | SMTP2GO in prod (`587`, mandatory STARTTLS); Mailpit in dev (`1025`). `SMTP_PORT` defaults to `587` and `SMTP_FROM` to `IgnisDev <ignisdev@magalz.space>`. With `APP_ENV=prod` the config refuses a missing `SMTP_HOST` or `SMTP_USERNAME` (TLS and login are mandatory there), and the SMTP sender requires `SMTP_PASSWORD` whenever `SMTP_USERNAME` is set. In dev with no `SMTP_HOST` and no `MAIL_DROP_DIR`, `serve` logs one warning, runs no outbox worker and leaves queued emails in the outbox |
| `MAIL_DROP_DIR` | empty | dev only (refused with `APP_ENV=prod`): `serve` writes outgoing email as `.eml` files in this folder, From = `SMTP_FROM`, instead of sending; it wins over `SMTP_HOST` |
| `SEED_PASSWORD` | empty | dev only: the password `seed-dev` gives the sample accounts (it must pass the password policy); the command refuses to run without it or with `APP_ENV=prod` |

Secrets live only in `.env` (gitignored). Never log, print or commit them.

## Security rules (non-negotiable)

- CSP: `default-src 'self'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'`. Therefore:
  - no inline `<script>` with code, no `on*=` attributes, no `style="..."` attributes in templates;
  - Alpine components are registered with `Alpine.data()` in `web/static/js/app.js`; template attributes only reference properties/methods (CSP build has no expression evaluation);
  - HTMX config via `<meta name="htmx-config" content='{"allowEval":false,"includeIndicatorStyles":false,...}'>`;
  - JSON data for scripts goes in `<script type="application/json" id="...">` blocks.
- Mutations are POST only. Every non-GET request must have `Origin` equal to `APP_ORIGIN`; `Sec-Fetch-Site: cross-site` is rejected (403). Request bodies capped at 64 KiB.
- Client IP: `engine.SetTrustedProxies(TRUSTED_PROXY_CIDRS)` and `engine.RemoteIPHeaders = []string{"CF-Connecting-IP"}`. Never set `engine.TrustedPlatform` (Gin then trusts the header from anyone).
- Cookies: `__Host-` prefix, `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/`.
- Tokens (session, email confirmation, password reset) are 32 random bytes; only their SHA-256 is stored.
- Templates rely on `html/template` escaping; never build HTML with string concatenation; never use `template.HTML` on user data.
- External links from user data: only `http`/`https`, rendered with `rel="noopener noreferrer nofollow ugc"` and `target="_blank"`.
- Logs: `log/slog`, structured. Never log passwords, tokens, cookies, request bodies or SMTP credentials.

## Visual identity (ported from CircuitoNE — dark only)

Tokens are defined once in `web/static/css/tokens.css`; every other stylesheet uses them.

```css
--color-bg:#050506; --color-bg-elev:#0b0b0d; --color-surface:#101014;
--color-foreground:#e8e8ea; --foreground:var(--color-foreground); --color-muted:#8a8a92;
--color-accent:#ff2040; --color-accent-text:#ff7285;
--accent:#ff2040; --accent-text:#ff7285; --accent-contrast:#000000;
--color-ok:#35e06f; --color-warn:#ffcc33; --color-line:#23232a; --color-control:#6b6b75;
--font-mono:'JetBrains Mono',ui-monospace,monospace; --font-display:'Space Mono',ui-monospace,monospace;
```

`color-scheme: dark` is set on `html` in `base.css`. Read `tokens.css` for the full token list (type scale, spacing, widths, z-index, motion) and `base.css` for the shared effect classes and keyframes before writing new CSS.

- Square corners everywhere (no border-radius except tiny status dots). 1px borders; 2px for "active" markers.
- Body in JetBrains Mono; headings, wordmark and names in Space Mono. Weights 400 and 700 only.
- UPPERCASE labels/nav/badges/buttons with letter-spacing (.05em buttons, .1em labels); body copy in lowercase-ish tone.
- Neon border/glow, CRT 40px grid behind the page, scanlines; terminal microcopy: `$ ` before field labels, `[erro] …` for errors, `[✕]` close, `[→]` actions, section prompts like `~/`.
- Focus: `outline: 2px solid #fff; outline-offset: 2px`. Targets ≥ 24px. Links underlined. Nothing animates longer than 5 s; `prefers-reduced-motion: reduce` disables animations.
- Wordmark: `◢◤ AGENDA_CLUBBER` + blinking block cursor (4 blinks). Page titles end with `· AGENDA CLUBBER`.
- Footer on every page: `$ feito_por --dev=IgnisDev` and `contato: ignisdev@magalz.space` (mailto link).

## Decisions already made (do not undo)

- Routing: `RedirectTrailingSlash` is off (Gin's redirect would bypass the security headers), so `/x/` is a 404. Register routes without trailing slashes and link to them exactly.
- Static files: `Assets` is an exact-name allowlist; `asset "path"` in templates panics on unknown names. A new file extension needs an entry in the content-type map in `internal/web/assets.go`.
- Times are `time.Time` in UTC. MongoDB keeps millisecond precision, so compare stored and read-back times with `Truncate(time.Millisecond)`.
- Token and session hashes are 32-byte `[]byte`, never a string or hex.
- IDs are opaque strings: nothing outside the store parses or builds them.
- MongoDB filters are built in code from typed values only, never from user-supplied JSON. No filter or update key is ever built from user data, and user strings inside aggregation expressions are quoted with `$literal`. If a later change ever builds a key from data, it must add a guard that rejects keys starting with `$`.
- Backups are `mongodump`, run outside the app; there is no `backup` subcommand.
- CSS class names live in `web/static/css/components.css` (section comments list them and their markup contracts: wordmark, drawer, checklist `sr-only` state, `required` on `.label--required` controls). Use `.field-error` and `.spinner*` (not `.error` / `.spinner-input`).
- The itsaplan commit hook only accepts the message (trailers included) through `-m` arguments, not `-F file`.
- Roles and who approves what are in the README ("Approvals"). The labels: stored role `owner` is shown as Administrador, `member` as Membro, the creator of a collective (`Collective.CreatedBy`) is its Master, and `User.IsAdmin` is the Admin. The stored values never change; `approvals.RoleLabel` maps them to the labels.
- Collectives are serialized through the store: an operation that depends on a collective's state (a join decision, a role change, a rename, a join request, a change of the blocks, an event's creation, edit or deletion) runs in `Store.WithTx`, reads the collective there and writes it back with `Collectives.Update`, which always writes, so two such transactions on one collective conflict and one retries. `PurgeUnconfirmedSignups` also reads each collective inside `WithTx` before deleting pending collectives. The generic `Update` methods (`Collectives.Update`, `Memberships.Update`) are called only inside `WithTx`, after a read in that same transaction.
- The audit log keeps IDs and name snapshots, never an email address. Its references are not checked on write and never cleared or changed when a user or collective is deleted.
- In `/admin` the IDs a POST acts on travel in hidden form fields.
- A collective's page (`/coletivo/<id>`) and its actions (`POST /coletivo/<id>/*`) answer anyone who may not see it (a stranger, a Membro, the Admin, or everyone while the collective is not approved) exactly like a path that does not exist (`answerNotFound`, 404); `collectiveAct` checks `Memberships.Get` before reading the form.
- The catalog lists exist once, in `internal/catalog`. Never copy them to `web/static`: the browser gets the lists from Go routes.
- Events: times are stored in UTC and the day of an event is its start date in `APP_TZ`, so one that ends after midnight stays on the day it starts. A new event cannot start before today; an edit may keep a past date.
- An event edit is versioned: the form carries the version it read, `Events.Update` saves only when it still matches and returns `domain.ErrConflict` otherwise, which `events.Update` turns into `ErrChanged`, answered 409.
- Who may create, edit and delete events is the table in `internal/events/events.go`. `mayCreate` and `mayEdit` (`internal/events/permissions.go`) code it, and the write flows and the views that decide which buttons to show both call them. Two places mirror the create rule and change with it: `Memberships.UserCanCreateEvents` (the store, behind the nav's "Criar Evento" link; its contract is in `internal/store/storetest`) and the block check in `events.FormCollectives` (the form never offers a blocked collective, not even to the admin).
- The audit log records every deletion of an event, and an edit only when the admin made it in a collective where the admin is not an approved Administrador. Creations and an Administrador's own edits are not recorded. The detail of an entry is the event's name and its start date in `APP_TZ`.
- The dependent selects (the cities of a state, the substyles of a style) are `<option>` fragments served by `/opcoes/*`. The Go builders (`cityOptions`, `substyleOptions`) and the `select-options` partial that make them also render the event form's own lists, so the two cannot differ. The form works without JavaScript.
- Every link to an event is built with `eventPath` or `eventURL`, and every link or form action to a collective's page is built with `approvals.CollectivePath(id)` (which escapes the ID with `url.PathEscape`, used both in Go links/redirects and in `coletivo.html` via `collectivePage.Path`): a change of the route changes every link.
- A page the person may not see, or that does not exist (an unknown event, a collective's page), is answered by `answerNotFound`, the same as a path no route serves. Handlers of authed routes get the user from `signedInUser`.
- Focus ring inside a cell: the date picker's day buttons (`.date-picker__day:focus-visible`) keep the 2px white ring but use `outline-offset: -2px`, the one exception to the focus rule in "Visual identity" (at +2px the ring would cross into the neighbouring day). The other `outline-offset: -2px`, on `.calendar__cell--today`, is no focus ring: it is the 2px accent outline that marks today's cell, drawn inside it.
- On an HTMX request (`HX-Request: true`), a missing or expired session (or an expired password) is answered 401 with `HX-Redirect` to the login or `/senha/trocar` (`denyTo`), and a handler that ends in a redirect answers 204 with `HX-Redirect` (`redirect`), never a 303: htmx follows a 303 in place and swaps the destination page into the target. A plain request gets the 303, which the forms that work without JavaScript depend on. Script `fetch` calls outside htmx (`/opcoes/*` in `refill` and `/coletivos/sugestoes` in `collectivePicker`) set `redirect: 'error'`, so an unexpected redirect (such as an expired session) fails the call instead of filling a select or listbox with a page.
- htmx swaps no 4xx answer by default. The event form's day panel (`#analise-resultado`) lets the 422 of `/eventos/dia` in through an `htmx:beforeSwap` handler in `app.js` limited to that target and that status; every other 4xx stays unswapped. The fragment a route answers to an HTMX request (`/calendario`, `/eventos/dia`, `/eventos/:id`, `/opcoes/*`) carries `Cache-Control: no-store`.
- The event form's `[↻] ATUALIZAR LISTAS` is a second submit button (`name="acao" value="atualizar"`) that posts the form back with the cities and substyles filled in and saves nothing: the form works without JavaScript. With the script the button and its hint hide, because the `/opcoes/*` fetches fill the lists. Both handlers check `acao` before they validate or write.
- Who gets which answer when a collective is blocked: the event form offers no blocked collective (not even to the Admin), so a person whose collectives are all blocked gets a 403, whatever they post. Otherwise the 409 with the reason (`ErrBlocked`, `eventsMsgBlocked`) goes only to an Administrador of the blocked collective who is not the Admin: one who posts it anyway, or who opens the edit form of, saves or deletes one of its events. Everyone else the rules refuse (a Membro, a stranger, a post naming a collective the person is not an Administrador of) gets the plain 403, never the reason. The block never stops the Admin (`mayCreate` and `mayEdit` let the Admin through): the Admin edits and deletes any event, and a post naming a blocked collective the Admin is an Administrador of creates the event, though the form offers none.
- A notice that a redirect carries is a code in `?msg=`: the page maps it through its fixed `*Notices` table (`calendarNotices`, `eventNotices`, `adminNotices` and the others), shows the text of a code in the table and nothing for any other, and never echoes the code.
- `config.DefaultTZ` is the one default zone (when `APP_TZ` is unset, and the fallback of `seed-dev`), and `domain.TicketURLProblem` the one ticket-link rule (domain, events and web call it). `internal/web/format.go` builds its date and price text from `events.DateLayout` (`dd/mm/aaaa`) and `events.FormatCents` (`1.234,56`); write new date or price text with them (including `/admin` cards and `/admin/historico` via `fmtDate`/`fmtClock`, and in templates through the `date` and `price` funcs), not with a layout of its own.
- The calendar page (`calendarPage` in `app.js`) sits on markup that works as plain links without the script: the month bar, the filter (its FILTRAR button, which the script hides through `calendar-page--js`), the day links and the chips. The script takes only a plain left click; a modifier key, the middle button or a request that fails follows the link. A month change swaps `GET /calendario` (the month alone, from the same query as `/`) as `innerHTML` into `#calendar-slot`, which holds only the month; the sheet (`dialog#evento-sheet`) stays outside it and the listeners sit on the root (the `x-data="calendarPage"` element, which the script marks `calendar-page--js`), so the HTML a swap brings holds no script attribute and needs no setup. Each chosen month's address is pushed as its own history entry; back and forward load it again without pushing (validating `uf`/`cidade` against the page's selects via `offers`). Month slide transitions are skipped while `#evento-sheet` is open, and boundary months where `dayOf(next)` cannot represent the target (years `< 0100` or `> 9999`) hand off to the plain link instead of stalling. Below 640px (`WIDE`) a day's number loads its list into `#calendar-day`; from 640px it follows its link.
- Two calendar actions can overlap, and the person's last choice must win. Build every month change the person makes in the page (the month bar, Hoje, the filter) from `latest()` (the newest address asked for), never from the filter or the links in the page, which are stale while a month loads; back and forward take the address from the URL, and only today's month is read from the Hoje link. Run each kind of request (the month, a chip's event, a day list) through its own `newestOnly` queue (one out, only the newest waits) and from its own htmx source element (`#calendar-slot`, `#evento-sheet`, the root): htmx keeps one request slot per source element and queues a second request on it with its promise already resolved. A request out longer than `HANG_WAIT` (8 s) hands the page to the plain link. A new kind of request on this page follows the same rules.
- Profile & email change: `/perfil` (`GET`/`POST` via `UpdateProfile`, validated in `accounts` using `domain.TextProblem` and `User.Validate`; `Users.UpdateProfile` is a filtered write without revalidating) and `POST /perfil/email` (rate-limited by `Limits.EmailAction` via `emailForm`). Public `GET /confirmar-email` shows only the confirm button without looking up or consuming the token (so mail scanners do not spend links), and `POST /confirmar-email` (rate-limited by `Limits.Login`) consumes the 24h `TokenPurposeChangeEmail` (`change_email`) token (`ChangeEmailTokenTTL`). Requesting again retires any earlier unused `change_email` token (`Tokens.Create`). The old email stays active until confirmation and receives `TemplateEmailChangeNotice` on request; confirmation (`ConfirmEmailChange`) runs `Tokens.Consume` and `Users.SetEmail` in `WithTx` and sends no second email. An occupied new email at request time queues `TemplateExistingAccount` without creating a token while returning the same success notice (`email-pendente`); requesting the account's own current email fails with a field error (`"este já é o seu e-mail"`), while `Users.SetEmail` ignores the account itself for uniqueness. Sessions are keyed by `UserID` so they survive email change, and self-service profile/email changes are not written to the audit log.
- `/perfil` notice codes (`perfilNotices` in `internal/web/routes_profile.go`): `perfil-salvo`, `email-pendente` and `email-confirmado` render as status notices (`.status--ok`), while `email-invalido` and `email-em-uso` render as error alerts (`.status--error`).
- Daily background jobs (`dailyLoop` in `cmd/agenda/main.go`, `dailyInterval = 24 * time.Hour`, runs immediately on startup alongside `purgeLoop` in `startJobs`, then on each tick; the two steps are independent and idempotent):
  - `SendPasswordReminders` (`internal/accounts/password.go`): sends 1 email (`TemplatePasswordExpiring`) per password cycle while time remains within `auth.WarnBefore` (7 days), skipping accounts whose `PasswordRemindedAt` is already `>= PasswordChangedAt`. `Users.ChangePassword` clears `PasswordRemindedAt`. Each reminder is enqueued and marked (`Users.MarkPasswordReminded`) in `WithTx`; logs `"daily: password reminders sent"` with `"count"` only.
  - `PurgeUnconfirmedSignups` (`internal/accounts/profile.go`): 7-day cutoff on `UserStatusPendingEmail` (`ListByStatusCreatedBefore`); for each account in `WithTx`, deletes pending collectives created by that user (`c.Status == CollectiveStatusPending && c.CreatedBy == userID`) and then calls `Users.Delete` (cascading sessions, tokens and memberships). Writes no audit entry and sends no email; logs `"daily: unconfirmed signups purged"` with `"count"` only.
- Phase 5 cleanups & invariants:
  - `Collectives.SearchApproved` excludes `Locked` collectives;
  - `approvals.Promote` requires an active target account (`UserStatusActive`, else `ErrNotAllowed`);
  - `approvals.CheckReason` normalizes `\r\n`/`\r` to `\n` and delegates to `domain.TextProblem(..., 0, domain.AuditTextMax, true)`;
  - `domain.LengthProblem`, `domain.MsgControl` and `domain.MsgCollectiveKind` are shared across `domain`, `accounts`, `approvals` and `cmd/agenda`;
  - decision email (`internal/mail/templates/decision.tmpl`, `DecisionData.Button`) selects the button label from the destination (`"Entrar"`, `"Ver meus coletivos"`, `"Criar conta"`) and renders the pending-account note only when `Button == "Entrar"`;
  - mobile drawer (`shell` in `web/static/js/app.js` and `web/templates/layouts/app.html`) traps `Tab`/`Shift+Tab` focus inside `#menu-lateral` while open below `1024px` (`DESKTOP`) and sets `:inert="mainInert"` on `<main>` (CSP-safe Alpine property);
  - `caretAfterRewrite` in `app.js` preserves caret position in masked inputs (`timeSpinner`, `moneyInput`) when editing in the middle;
  - `Meus Eventos` (`meus-eventos.html`, as well as `Analisar Data` and the calendar) shows a truncation notice when results hit `domain.ListLimitMax` (500);
  - day back link (`routes_day.go`) on an invalid `?data=` (422) points to the current month keeping the active `?uf=`/`?cidade=` filter (`calendarHref`);
  - substyle option (`routes_options.go` `substyleOptions`) renders `domain.StyleOther` (`"outros"`) with label `"Outros"`.
- SecOps backlog round (`docs/adr/INDEX.md`, `docs/bdr/INDEX.md`):
  - `ACL-49` / `ACL-32`: upgraded `golang.org/x/crypto` to `v0.59.0` (resolving `GO-2026-5932`) and `github.com/wneessen/go-mail` to `v0.8.2` (fixing socket leaks on Hello/STARTTLS/AUTH failure, verified by active-connection cleanup tests);
  - `ACL-38`: `SMTPSender.Send` splits dial/send/close (`DialToSMTPClientWithContext`, `SendWithSMTPClient`, `CloseWithSMTPClient`) so a `QUIT` error after `250 DATA` logs a redacted warning via `SMTPSender.Log` and returns `nil` instead of retrying;
  - `ACL-21` (`ADR-001`): `mongostore` serializes reference-checked writes against concurrent `Users.Delete` / `Collectives.Delete` via atomic `$inc: {rev: 1}` (`mustExist`) inside `inTx`, adding `rev` to `users` alongside `collectives`;
  - `ACL-67`: `domain.TextProblem` rejects Unicode `Cf` format characters and `U+2028`/`U+2029` separators alongside control characters, unified across `signupValidate` and `newAdmin`;
  - `ACL-43` (`BDR-001`): login, `POST /senha/trocar`, and `POST /senha/verificar` share the 5-try / 15-min account lockout on wrong non-empty `current_password` (valid `current_password` resets `FailedLogins`; locked accounts skip Argon2);
  - `ACL-37`: `Outbox.Claim` returns `nil` without claiming when `limit < 1` or `lease <= 0`;
  - `ACL-48` (`BDR-001`): `verify_email.tmpl` uses a nameless `"Olá!"` greeting (omitting untrusted signup `Name` from `VerifyEmailData`), while `existing_account.tmpl` keeps the stored account name;
  - `ACL-41` (`ADR-002`): `Limits` isolates IP (`Signup`, `EmailAction`) and email-hash (`SignupEmail`, `EmailActionEmail`) rate limiters, and `POST /cadastro` runs `accounts.ValidateSignup` before charging `SignupEmail`;
  - `ACL-39` (`BDR-001`): PO accepted the low-value two-step collective-name signup enumeration risk (closed community, 5/h IP limit, 7-day unconfirmed-signup purge); no code change.

## Testing and checks

- Test first where behavior changes. Tests live next to the code (`*_test.go`), use the standard `testing` package and `net/http/httptest`; no assertion libraries.
- Database tests use `mongotest.New(t)`, which needs `MONGO_TEST_URI`: a single-node replica set URI with `directConnection=true`. CI starts one; locally, run the pinned image from `deploy/compose.dev.yml` with `--replSet rs0`, initiate it, and point the variable at it.
- Before every commit: `gofmt -l .` prints nothing, `go vet ./...` and `go test -race -timeout 30m ./...` pass. (`-race` needs CGO; where no C compiler is available run `go test ./...` and say so.) The database packages can run past Go's default 10-minute test timeout under `-race`, hence `-timeout 30m`; on Windows add `-p 4` (more parallel packages exhaust local ports).

## Git and itsaplan

- Linked to itsaplan project `ignis-dev.ACL`. Every commit ends with one trailer block (no blank line inside it): `Refs: ACL-N` for its card, then `Co-Authored-By: <the model that wrote the commit> <noreply@anthropic.com>`. Check with `git log -1 --format='%(trailers:key=Refs,valueonly)'`.
- Task branches `<type>/ACL-N-<slug>` cut from the phase branch `phase/ACL-<phase-card>-<slug>`; merged `--no-ff`. One PR per phase into `main`.
- Conventional commit subjects (`feat:`, `fix:`, `chore:`, `docs:`, `test:`, `refactor:`, `ci:`, `build:`), imperative, English.
- Stage files by name (`git add <paths>`), never `git add -A` / `git add .`: the repository is public and local tool state can appear in the working tree.
