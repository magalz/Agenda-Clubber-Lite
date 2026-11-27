# Agenda Clubber Lite

A lightweight, self-hosted shared calendar where collectives and producers schedule events and check date clashes before announcing them.

- Backend: Go + Gin; data access through database-agnostic interfaces (`internal/domain`) with a MongoDB implementation (single-node replica set)
- Frontend: server-rendered `html/template`, HTMX, Alpine.js (CSP build), vanilla CSS
- Visual identity: ported from CircuitoNE
- Runs under Podman; production at `agendaclubber.magalz.space`

UI text is in Brazilian Portuguese; code, identifiers and commits are in English.

See [Deploy](#deploy) for the production target and [Environment variables](#environment-variables) for configuration. Conventions, layout and decisions are in [AGENTS.md](AGENTS.md); every setting is in [.env.example](.env.example).

## Run it locally

### With the dev stack (Podman)

The stack in `deploy/compose.dev.yml` runs the app, a single-node MongoDB replica set and Mailpit, which catches every email the app sends (UI at <http://127.0.0.1:8025>).

```sh
cp .env.example .env     # once
podman compose -f deploy/compose.dev.yml up -d --build
```

The app answers on <http://localhost:8120>. The compose file sets `MONGO_URI` and the other variables that must point at its own services, so `.env` needs no database settings there. Stop it with `down`; add `-v` to drop the database volume too.

The commands below run inside the app container, which has no shell, so they go through `exec`:

```sh
podman compose -f deploy/compose.dev.yml exec app /app/agenda <command> ...
```

Pass `-T` to `exec` when the command reads stdin (`create-admin`), so stdin is not a terminal.

### Natively

You need Go (the version in `go.mod`) and a MongoDB run as a single-node replica set, because the app uses transactions: start `mongod --replSet rs0` and run `rs.initiate()` once. The app reads its settings from the environment only (it does not read `.env`), and `MONGO_URI` must be a `mongodb://` URI with `directConnection=true`:

```sh
export APP_ENV=dev
export APP_ORIGIN=http://localhost:8120
export MONGO_URI='mongodb://localhost:27017/?directConnection=true'
go run ./cmd/agenda          # same as: go run ./cmd/agenda serve
```

With `APP_ENV=dev`, `APP_ORIGIN` and `MONGO_URI` are the only required settings (`prod` also needs `SMTP_HOST`, `SMTP_USERNAME` and `SMTP_PASSWORD`); [Environment variables](#environment-variables) and [.env.example](.env.example) document the rest. The server creates the schema when it starts.

## Environment variables

The binary reads settings from the process environment only (it does not load `.env` files). Every variable in [.env.example](.env.example):

| Variable | Default | Required / Scope | Behavior |
|---|---|---|---|
| `APP_ENV` | `prod` | Optional | Must be `dev` or `prod`. `dev` enables dev-only routes/commands and plain-HTTP cookies on localhost; `prod` enforces `Secure` cookies and authenticated TLS SMTP |
| `APP_ORIGIN` | — | **Required** (`serve`) | Absolute `http://` or `https://` origin (lowercased, default port stripped, no path/query/trailing slash); used for `Origin` checks and absolute email links. `seed-dev` falls back to `http://localhost:8120` |
| `APP_ADDR` | `:8120` | Optional | Listen address (`host:port`) for `serve`; `healthcheck` reads only `APP_ADDR` and probes `127.0.0.1:<port>/healthz` (refusing port `0`) |
| `APP_TZ` | `America/Fortaleza` | Optional | IANA time zone (`time/tzdata` is embedded). `serve` and `seed-dev` reject unknown zones; controls displayed dates/times and sample event dates |
| `TRUSTED_PROXY_CIDRS` | empty | Optional | Comma-separated CIDRs allowed to supply `CF-Connecting-IP` (the `cloudflared` peer IP as seen by the app). Empty trusts no proxy |
| `MONGO_URI` | — | **Required, secret** | `mongodb://` URI (never `mongodb+srv://`) for a single-node replica set, with `directConnection=true` set once; redacted from logs and errors. Used by `serve`, `migrate`, `create-admin` and `seed-dev` |
| `MONGO_DB` | `agenda` | Optional | Database name (1 to 38 chars: letters, digits, `_`, `-`). Used by `serve`, `migrate`, `create-admin` and `seed-dev` |
| `SMTP_HOST` | empty (`.env.example`: `localhost`) | **Required in `prod`** | Outgoing mail relay host. In `dev` with neither `SMTP_HOST` nor `MAIL_DROP_DIR`, `serve` logs one warning, runs no outbox worker and leaves emails in the outbox |
| `SMTP_PORT` | `587` (`.env.example`: `1025`) | Optional | SMTP port (`1..65535`). Port `587` uses mandatory STARTTLS when `SMTP_USERNAME` is set; port `465` uses implicit TLS |
| `SMTP_USERNAME` | empty | **Required in `prod`** | SMTP login (`AUTH PLAIN` with mandatory TLS). Empty in `dev` sends over plain SMTP with no login (e.g. Mailpit) |
| `SMTP_PASSWORD` | empty | **Secret** (required when `SMTP_USERNAME` is set) | Read verbatim (spaces preserved) and redacted from logs; required by the SMTP sender whenever `SMTP_USERNAME` is non-empty |
| `SMTP_FROM` | `IgnisDev <ignisdev@magalz.space>` | Optional | Sender `From` header used by both SMTP and `MAIL_DROP_DIR` `.eml` output |
| `MAIL_DROP_DIR` | empty | **Dev only** | Refused with `APP_ENV=prod`. Writes outgoing emails as `.eml` files in this folder instead of sending via SMTP; wins over `SMTP_HOST` |
| `SEED_PASSWORD` | empty | **Dev only, secret** | Refused with `APP_ENV=prod` or when empty. Password assigned to all `@agenda.test` sample accounts created by `seed-dev`; must satisfy the password policy |

## Commands

`agenda` runs `serve` with no argument. Natively, run them as `go run ./cmd/agenda <command>` (or a built binary); in the dev stack, through `exec` as above.

| Command | What it does |
|---|---|
| `migrate` | Creates or updates the database schema; safe to run again. It reads only the MongoDB settings. |
| `create-admin --name ... --email ... --phone ...` | Creates an active admin whose email counts as confirmed. This is how the first admin exists: people who sign up wait for an admin to approve them. |
| `seed-dev` | Fills a dev database with sample accounts, collectives and events. |
| `healthcheck` | Probes the running server's `/healthz`; the container health check uses it. |

### The first admin

`create-admin` reads the password as the first line of stdin, so it never appears in the command line or the shell history. The password must meet the policy and differ from the email; the name may not contain line breaks or other control characters, and only the digits of the phone are kept.

```bash
read -rs ADMIN_PASSWORD
printf '%s\n' "$ADMIN_PASSWORD" | go run ./cmd/agenda create-admin \
  --name 'Ana Admin' --email ana@example.com --phone '(85) 99999-0001'
```

In the dev stack, replace `go run ./cmd/agenda` with `podman compose -f deploy/compose.dev.yml exec -T app /app/agenda`.

It prints `admin created: <email>`, or says why it could not (the password is never printed).

### Sample data

`seed-dev` runs only with `APP_ENV=dev` and refuses to run without `SEED_PASSWORD`, which must pass the password policy; every sample account gets that password. Running it again changes nothing that already exists. It also reads `APP_TZ` (the zone of the sample events' dates, `America/Fortaleza` by default) and refuses an unknown zone. Set `SEED_PASSWORD` in `.env` (the dev stack passes it on to the container) or in the environment, then run `seed-dev`.

The sample accounts, all `@agenda.test`:

| Email | What it is |
|---|---|
| `admin@agenda.test` | the admin |
| `dono@agenda.test` | "Administrador Dev": the approved Administrador and Master of Coletivo Alfa and Produtora Beta |
| `membro@agenda.test` | an approved Membro of Coletivo Alfa |
| `pendente-email@agenda.test` | has not confirmed its email (the first run prints its confirmation link) |
| `pendente-aprovacao@agenda.test` | confirmed, waiting for the admin's approval; also asked to join Coletivo Alfa (both approvals open) |
| `senha-vencida@agenda.test` | its password has expired |
| `senha-quase@agenda.test` | its password is about to expire |
| `novo-coletivo@agenda.test` | confirmed, waiting for the admin's approval; signed up with the new Coletivo Gama, which waits for the admin too |
| `pedido@agenda.test` | active; asked to join Produtora Beta as Membro (both approvals open) |
| `delta@agenda.test` | active; the only Administrador (and Master) of Coletivo Delta, whose events the admin blocked, so its Criar Evento link is disabled |

Together they leave something to decide in every flow of the [approvals](#approvals): accounts, a new collective, join requests, and a collective with blocked events.

The sample events are made by the Administrador of their collective (`dono`, and `delta` for Coletivo Delta). An event is found by its collective and its name, so a run adds only the ones that are missing. Their dates come from the day of the first run, in `APP_TZ`, and a later run leaves them where they were: use a fresh database to get new dates. Close to the first or the last day of a month, the events that are "earlier this month" or "later this month" fall in the neighbouring month.

| Event | Collective | When | What it shows |
|---|---|---|---|
| Warm Up Techno | Coletivo Alfa | 8 days before, 22:00 | a past event, with a venue and a description |
| Sarau na Praça | Produtora Beta | 3 days before, 17:00 | type Outros, named "Sarau"; Sobral/CE |
| Noite House Recife | Coletivo Alfa | 3 days after, 21:00 | Recife/PE; one of the three events of that day, so [Analisar Data](#events-and-calendar) lists two events in Fortaleza and one in Recife |
| Rave Litoral | Coletivo Alfa | 3 days after, 22:00 | Fortaleza/CE; the same day |
| Pista Drum and Bass | Produtora Beta | 3 days after, 23:00 | Fortaleza/CE; the same day |
| Noite Ambient no Terraço | Coletivo Delta | 5 days after, 20:00 | an event of a collective whose events are blocked |
| Oficina de Discotecagem | Produtora Beta | 6 days after, 14:00 | a Free workshop |
| After Hours Subsolo | Coletivo Alfa | 8 days after, 23:30 | starts late and ends at 06:00 the next day |
| Baile do Brega Funk | Coletivo Alfa | 10 days after, 21:00 | Olinda/PE |
| Baile de Forró Eletrônico | Coletivo Alfa | the 6th of next month, 21:00 | style Outros, named "Forró eletrônico"; Caruaru/PE |
| Festival Nordeste Eletrônico | Produtora Beta | the 12th of next month, 16:00 | an https ticket link, a venue and artists on several lines |

### Email in development

The dev stack sends through Mailpit. Natively, with neither `SMTP_HOST` nor `MAIL_DROP_DIR` set, the server sends nothing and the emails wait in the outbox. Set `MAIL_DROP_DIR` to a folder to have every outgoing email written there as an `.eml` file instead; it wins over `SMTP_HOST` and is refused with `APP_ENV=prod`.

## Accounts, profile and background jobs

- **Sign-up and passwords**: new accounts confirm their email (`/confirmar`, 24h link) and wait for the Admin before signing in (`/entrar`). Passwords expire every 90 days; during the last 7 days an expiry banner links to `/senha/trocar`.
- **Editar Perfil (`/perfil`)**: signed-in users can update their name, phone and WhatsApp flag, jump to `/senha/trocar`, or request an email change (`/perfil/email`). A 24h confirmation link goes to the new address while the old address stays active and receives a notice; `/confirmar-email` shows only a button on `GET` (so mail scanners do not consume tokens) and applies the change on `POST`. Requesting an address another account already uses sends the existing-account notice there without creating a token or revealing who owns it. Active sessions survive an email change.
- **Background jobs (`serve`)**:
  - every hour: removes expired sessions and used or expired tokens;
  - every 24 hours (running once at startup): sends one password-expiry reminder per password cycle during the 7-day warning window (`SendPasswordReminders`), and purges unconfirmed sign-ups (`pending_email`) older than 7 days together with any pending collectives they created (`PurgeUnconfirmedSignups`).

## Approvals

People sign up, confirm their email and wait: nobody gets in or creates events on their own say-so. The roles, as the pages name them:

| Role | Who | What it can do |
|---|---|---|
| Admin | the site administrator (`create-admin`) | approves, rejects and blocks accounts; approves or rejects collectives; can block a collective's events ("bloquear eventos") or freeze its members and name ("congelar membros e nome") |
| Master | the person who created a collective | an Administrador of it who is never demoted |
| Administrador | an approved member with the stored role `owner` | creates events for the collective; decides join requests; promotes and demotes members (never the Master, never leaving the collective without an Administrador); renames it |
| Membro | an approved member with the stored role `member` | belongs to the collective; creates no events |

The rules:

- A new collective needs only the Admin's approval.
- Joining an existing collective needs two approvals, in any order: the Admin's and one Administrador's of that collective.
- A rename takes effect at once; the Admin gets an email about it.
- Every decision is in the history, and a decision about a person's request or account emails the person. Steps that are no decision about a request are only recorded: an Administrador's half of a join approval, a block or unblock of an account, a block of a collective's events, a freeze of its members and name, a promotion or a demotion.

The pages:

- `/admin`, for the Admin only, with `Fila` (what waits for a decision), `Contas`, `Coletivos` and `Histórico` (`/admin/historico`);
- `/coletivos`, "Meus Coletivos": a person's collectives and requests, and where to ask to join one or to create one;
- `/coletivo/<id>`, a collective's page for its Administradores: join requests, members and roles, the name. Anyone else (the Admin included) gets the same 404 as for a path that does not exist on both `GET` and `POST`, as does everyone while the collective is not approved.

The rules live in `internal/approvals`; [AGENTS.md](AGENTS.md) records the decisions behind them.

## Events and calendar

The calendar (`/`) shows every event of every collective, past ones included, a month at a time (`?mes=AAAA-MM`). A day's cell lists its first three events and counts the rest; the day's number opens the list of that day, and an event opens its page. Dates are in `APP_TZ`, `dd/mm/aaaa`, 24h. The pages need a signed-in person, and every one of them sees every event.

Who does what:

- **Create**: an approved Administrador of an approved collective, from "Criar Evento". The form offers only the collectives the person may create in. The Admin creates events only as an Administrador of the collective.
- **Edit and delete**: any approved Administrador of the event's collective, or the Admin. A Membro only reads.
- **The event block**: while the Admin blocks a collective's events ("bloquear eventos"), its Administradores can neither create, edit nor delete them. The form never offers a blocked collective, not even to the Admin, so an Administrador whose collectives are all blocked gets a 403, whatever they post (one who still has a collective that is not blocked and posts a blocked one they are an Administrador of anyway gets a 409 with the reason, unless they are the Admin, whom the block never stops). An Administrador who edits or deletes an event of a blocked collective gets a 409 with the reason, and the event's page tells them why it is read-only. The Admin can still edit and delete them. The events already there stay on the calendar.
- **Editing twice**: an edit carries the version it read. If someone else saved first, nothing is saved: the form comes back (409) with what the person typed and a link to the event as it is now, and saving again is a deliberate choice.

The form asks for the name, the type, the date and start time, the duration (under 24 hours), the price or Free, the style, the state and the city, and, if the organizer wants, a ticket link (http or https), the substyle, the venue, the artists (one per line) and a description. A type or a style of Outros asks for a name. The cities depend on the state and the substyles on the style; the form works without JavaScript, with a button that refreshes the lists. A new event cannot start before today. An event that ends after midnight belongs to the day it starts.

The pages:

- **Meus Eventos** (`/meus-eventos`): the events of every collective where the person has an approved membership, a Membro included. "Próximos" are those that have not ended, "Anteriores" the ones that have. A row the person cannot edit says why: "só leitura (Membro)", "eventos bloqueados pelo admin", or just "só leitura" (an Administrador of a collective that is not approved). Lists cap at 500 events and show a warning when truncated.
- **Analisar Data** (`/eventos/dia?data=dd/mm/aaaa`, or `AAAA-MM-DD`): the events already on a day, from every collective and by start time, so a collective sees the clashes before it picks a date. It takes the same state and city filter, and `?destaque_uf=` and `?destaque_cidade=` put the events of one city first, with the badge "mesma cidade". A date that does not parse gets a message (422), no list and a back link to the current month that keeps the active filter; a request from HTMX gets only the list.
- **The city filter**: the calendar and the day list can be restricted to a state and, within it, a city (`?uf=` and `?cidade=`). The selects offer only the places that have events, and every link of the calendar keeps the filter. A state or city that is not in the catalog means "todos".

The history (`Histórico`, `/admin/historico`) records every deletion of an event ("evento excluído") and an edit made by the Admin in a collective where the Admin is not an approved Administrador ("evento editado pelo admin"), with the event's name and start date, who did it and the collective. Creating an event, and an Administrador's own edit, are not recorded.

The flows live in `internal/events`, and the styles and cities in `internal/catalog`.

## Deploy

Production target (the full operational deploy runbook lands with Phase 6):

- **Host and containers**: an OCI ARM64 VM running a Podman Compose stack under `magalz-infra` with the app container ([deploy/Containerfile](deploy/Containerfile), pure Go / CGO-free cross-compiled for `arm64`) and an authenticated single-node replica set MongoDB on an internal network only (no public database port).
- **Ingress**: published at `https://agendaclubber.magalz.space` via Cloudflare Tunnel, with the `cloudflared` peer IP/CIDR configured in `TRUSTED_PROXY_CIDRS` so `CF-Connecting-IP` is trusted for rate limiting and request logs, and Cloudflare access/login rules in front.
- **Email**: SMTP2GO on port `587` with mandatory STARTTLS and authentication (`SMTP_HOST`, `SMTP_USERNAME`, `SMTP_PASSWORD`), sending as `SMTP_FROM=IgnisDev <ignisdev@magalz.space>`.
- **Healthcheck and bootstrap**: container health is probed against `/healthz` via `/app/agenda healthcheck` (configured in the compose stack or via `--format docker`, since OCI images ignore `Containerfile` `HEALTHCHECK`), and the initial admin account is bootstrapped with `/app/agenda create-admin`.

## Checks

Run these before every commit:

```sh
gofmt -l .               # prints nothing
go vet ./...
go test -race -timeout 30m ./...
```

Database tests need `MONGO_TEST_URI`, a single-node replica set URI with `directConnection=true`; without it they are skipped, and the packages still report `ok`. `-race` needs a C compiler; without one, run `go test ./...`. The database packages can run past Go's default 10-minute test timeout under `-race`, hence `-timeout 30m`; on Windows add `-p 4`.

---

Made by IgnisDev · ignisdev@magalz.space
