# Agenda Clubber Lite

A lightweight, self-hosted shared calendar where collectives and producers schedule events and check date clashes before announcing them.

- Backend: Go + Gin; data access through database-agnostic interfaces (`internal/domain`) with a MongoDB implementation (single-node replica set)
- Frontend: server-rendered `html/template`, HTMX, Alpine.js (CSP build), vanilla CSS
- Visual identity: ported from CircuitoNE
- Runs under Podman; production at `agenda.magalz.space`

UI text is in Brazilian Portuguese; code, identifiers and commits are in English.

Deployment docs will be added as the project takes shape. Conventions, layout and decisions are in [AGENTS.md](AGENTS.md); every setting is in [.env.example](.env.example).

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

With `APP_ENV=dev`, `APP_ORIGIN` and `MONGO_URI` are the only required settings (`prod` also needs `SMTP_HOST`, `SMTP_USERNAME` and `SMTP_PASSWORD`); `.env.example` documents the rest. The server creates the schema when it starts.

## Commands

`agenda` runs `serve` with no argument. Natively, run them as `go run ./cmd/agenda <command>` (or a built binary); in the dev stack, through `exec` as above.

| Command | What it does |
|---|---|
| `migrate` | Creates or updates the database schema; safe to run again. It reads only the MongoDB settings. |
| `create-admin --name ... --email ... --phone ...` | Creates an active admin whose email counts as confirmed. This is how the first admin exists: people who sign up wait for an admin to approve them. |
| `seed-dev` | Fills a dev database with sample accounts and collectives. |
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

`seed-dev` runs only with `APP_ENV=dev` and refuses to run without `SEED_PASSWORD`, which must pass the password policy; every sample account gets that password. Running it again changes nothing that already exists. Set `SEED_PASSWORD` in `.env` (the dev stack passes it on to the container) or in the environment, then run `seed-dev`.

The sample accounts, all `@agenda.test`:

| Email | What it is |
|---|---|
| `admin@agenda.test` | the admin |
| `dono@agenda.test` | the approved Administrador (Master) of the sample collective and producer |
| `membro@agenda.test` | an approved member of the sample collective |
| `pendente-email@agenda.test` | has not confirmed its email (the first run prints its confirmation link) |
| `pendente-aprovacao@agenda.test` | confirmed, waiting for the admin's approval |
| `senha-vencida@agenda.test` | its password has expired |
| `senha-quase@agenda.test` | its password is about to expire |

### Email in development

The dev stack sends through Mailpit. Natively, with neither `SMTP_HOST` nor `MAIL_DROP_DIR` set, the server sends nothing and the emails wait in the outbox. Set `MAIL_DROP_DIR` to a folder to have every outgoing email written there as an `.eml` file instead; it wins over `SMTP_HOST` and is refused with `APP_ENV=prod`.

## Checks

Run these before every commit:

```sh
gofmt -l .               # prints nothing
go vet ./...
go test -race ./...
```

Database tests need `MONGO_TEST_URI`, a single-node replica set URI with `directConnection=true`; without it they are skipped, and the packages still report `ok`. `-race` needs a C compiler; without one, run `go test ./...`.

---

Made by IgnisDev · ignisdev@magalz.space
