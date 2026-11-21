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
| `APP_ORIGIN` | (required) | e.g. `https://agenda.magalz.space`; used for the Origin check and absolute links in emails |
| `APP_TZ` | `America/Fortaleza` | IANA zone; `time/tzdata` is embedded. `seed-dev` reads it too: the sample events' dates and times are in this zone |
| `MONGO_URI` | (required) | secret (it may carry credentials; never printed or logged). `mongodb://` only (the driver refuses `directConnection` with `mongodb+srv://`), a single-node replica set, and it must carry `directConnection=true` (checked at startup) |
| `MONGO_DB` | `agenda` | database name: 1 to 38 letters, digits, `_` or `-` |
| `TRUSTED_PROXY_CIDRS` | empty | comma-separated CIDRs whose `CF-Connecting-IP` header is trusted |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM` | | SMTP2GO in prod (587, STARTTLS mandatory); Mailpit in dev. With `APP_ENV=prod` the config refuses a missing `SMTP_HOST` or `SMTP_USERNAME` (TLS and login are mandatory there). In dev with no `SMTP_HOST` and no `MAIL_DROP_DIR`, `serve` logs one warning, runs no outbox worker and leaves queued emails in the outbox |
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
- Collectives are serialized through the store: an operation that depends on a collective's state (a join decision, a role change, a rename, a join request, a change of the blocks, an event's creation, edit or deletion) runs in `Store.WithTx`, reads the collective there and writes it back with `Collectives.Update`, which always writes, so two such transactions on one collective conflict and one retries. The generic `Update` methods (`Collectives.Update`, `Memberships.Update`) are called only inside `WithTx`, after a read in that same transaction.
- The audit log keeps IDs and name snapshots, never an email address. Its references are not checked on write and never cleared or changed when a user or collective is deleted.
- In `/admin` the IDs a POST acts on travel in hidden form fields.
- A collective's page (`/coletivo/<id>`) answers anyone who may not see it (a stranger, a Membro, the Admin, or everyone while the collective is not approved) exactly like a path that does not exist.
- The catalog lists exist once, in `internal/catalog`. Never copy them to `web/static`: the browser gets the lists from Go routes.
- Events: times are stored in UTC and the day of an event is its start date in `APP_TZ`, so one that ends after midnight stays on the day it starts. A new event cannot start before today; an edit may keep a past date.
- An event edit is versioned: the form carries the version it read, `Events.Update` saves only when it still matches and returns `domain.ErrConflict` otherwise, which `events.Update` turns into `ErrChanged`, answered 409.
- Who may create, edit and delete events is the table in `internal/events/events.go`. `mayCreate` and `mayEdit` (`internal/events/permissions.go`) code it, and the write flows and the views that decide which buttons to show both call them. Two places mirror the create rule and change with it: `Memberships.UserCanCreateEvents` (the store, behind the nav's "Criar Evento" link; its contract is in `internal/store/storetest`) and the block check in `events.FormCollectives` (the form never offers a blocked collective, not even to the admin).
- The audit log records every deletion of an event, and an edit only when the admin made it in a collective where the admin is not an approved Administrador. Creations and an Administrador's own edits are not recorded. The detail of an entry is the event's name and its start date in `APP_TZ`.
- The dependent selects (the cities of a state, the substyles of a style) are `<option>` fragments served by `/opcoes/*`. The Go builders (`cityOptions`, `substyleOptions`) and the `select-options` partial that make them also render the event form's own lists, so the two cannot differ. The form works without JavaScript.
- Every link to an event is built with `eventPath` or `eventURL`: the ID is escaped there, and a change of the route changes every link.
- A page the person may not see, or that does not exist (an unknown event, a collective's page), is answered by `answerNotFound`, the same as a path no route serves. Handlers of authed routes get the user from `signedInUser`.
- Focus ring inside a cell: the date picker's day buttons (`.date-picker__day:focus-visible`) keep the 2px white ring but use `outline-offset: -2px`, the one exception to the focus rule in "Visual identity" (at +2px the ring would cross into the neighbouring day). The other `outline-offset: -2px`, on `.calendar__cell--today`, is no focus ring: it is the 2px accent outline that marks today's cell, drawn inside it.
- On an HTMX request (`HX-Request: true`), a missing or expired session (or an expired password) is answered 401 with `HX-Redirect` to the login or `/senha/trocar` (`denyTo`), and a handler that ends in a redirect answers 204 with `HX-Redirect` (`redirect`), never a 303: htmx follows a 303 in place and swaps the destination page into the target. A plain request gets the 303, which the forms that work without JavaScript depend on. A script `fetch` of an authed route outside htmx (`/opcoes/*`) sets `redirect: 'error'`, so an expired session fails the call instead of filling a select with a page.
- htmx swaps no 4xx answer by default. The event form's day panel (`#analise-resultado`) lets the 422 of `/eventos/dia` in through an `htmx:beforeSwap` handler in `app.js` limited to that target and that status; every other 4xx stays unswapped. The fragment a route answers to an HTMX request (`/calendario`, `/eventos/dia`, `/eventos/:id`, `/opcoes/*`) carries `Cache-Control: no-store`.
- The event form's `[↻] ATUALIZAR LISTAS` is a second submit button (`name="acao" value="atualizar"`) that posts the form back with the cities and substyles filled in and saves nothing: the form works without JavaScript. With the script the button and its hint hide, because the `/opcoes/*` fetches fill the lists. Both handlers check `acao` before they validate or write.
- Who gets which answer when a collective is blocked: the event form offers no blocked collective (not even to the Admin), so a person whose collectives are all blocked gets a 403, whatever they post; one who still has a collective that is not blocked and posts a blocked one anyway, and an Administrador who opens the edit form of, saves or deletes an event of a blocked collective, get a 409 with the reason (`ErrBlocked`, `eventsMsgBlocked`); a Membro or a stranger gets the plain 403 they get for any event they may not edit, never the reason. The block never stops the Admin from editing or deleting (`mayEdit` lets the Admin through).
- A notice that a redirect carries is a code in `?msg=`: the page maps it through its fixed `*Notices` table (`calendarNotices`, `eventNotices`, `adminNotices` and the others), shows the text of a code in the table and nothing for any other, and never echoes the code.
- `config.DefaultTZ` is the one default zone (when `APP_TZ` is unset, and the fallback of `seed-dev`), and `domain.TicketURLProblem` the one ticket-link rule (domain, events and web call it). `internal/web/format.go` builds its date and price text from `events.DateLayout` (`dd/mm/aaaa`) and `events.FormatCents` (`1.234,56`); write new date or price text with them (in templates, through `fmtDate` and `fmtPrice`), not with a layout of its own.

## Testing and checks

- Test first where behavior changes. Tests live next to the code (`*_test.go`), use the standard `testing` package and `net/http/httptest`; no assertion libraries.
- Database tests use `mongotest.New(t)`, which needs `MONGO_TEST_URI`: a single-node replica set URI with `directConnection=true`. CI starts one; locally, run the pinned image from `deploy/compose.dev.yml` with `--replSet rs0`, initiate it, and point the variable at it.
- Before every commit: `gofmt -l .` prints nothing, `go vet ./...` and `go test -race -timeout 30m ./...` pass. (`-race` needs CGO; where no C compiler is available run `go test ./...` and say so.) The database packages can run past Go's default 10-minute test timeout under `-race`, hence `-timeout 30m`; on Windows add `-p 4` (more parallel packages exhaust local ports).

## Git and itsaplan

- Linked to itsaplan project `ignis-dev.ACL`. Every commit ends with one trailer block (no blank line inside it): `Refs: ACL-N` for its card, then `Co-Authored-By: <the model that wrote the commit> <noreply@anthropic.com>`. Check with `git log -1 --format='%(trailers:key=Refs,valueonly)'`.
- Task branches `<type>/ACL-N-<slug>` cut from the phase branch `phase/ACL-<phase-card>-<slug>`; merged `--no-ff`. One PR per phase into `main`.
- Conventional commit subjects (`feat:`, `fix:`, `chore:`, `docs:`, `test:`, `refactor:`, `ci:`, `build:`), imperative, English.
- Stage files by name (`git add <paths>`), never `git add -A` / `git add .`: the repository is public and local tool state can appear in the working tree.
