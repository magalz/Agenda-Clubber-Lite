-- Initial schema.
--
-- Timestamps are TEXT in UTC RFC 3339 with second precision
-- (2026-11-12T16:00:00Z), written by Go. Booleans are INTEGER 0/1. A CHECK
-- passes when its expression is NULL, so a nullable column that must satisfy a
-- rule when present says "IS NULL OR ...". Rows that belong to a user or a
-- collective go with it (ON DELETE CASCADE); references that only record who
-- did something are kept and set to NULL.

CREATE TABLE users (
    id                  INTEGER PRIMARY KEY,
    name                TEXT    NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    email               TEXT    NOT NULL UNIQUE COLLATE NOCASE CHECK (length(email) BETWEEN 3 AND 254),
    phone               TEXT    NOT NULL CHECK (phone NOT GLOB '*[^0-9]*' AND length(phone) BETWEEN 10 AND 11),
    phone_is_whatsapp   INTEGER NOT NULL DEFAULT 0 CHECK (phone_is_whatsapp IN (0, 1)),
    password_hash       TEXT    NOT NULL,
    password_changed_at TEXT    NOT NULL,
    status              TEXT    NOT NULL CHECK (status IN ('pending_email', 'pending_approval', 'active', 'disabled')),
    is_admin            INTEGER NOT NULL DEFAULT 0 CHECK (is_admin IN (0, 1)),
    failed_logins       INTEGER NOT NULL DEFAULT 0 CHECK (failed_logins >= 0),
    locked_until        TEXT,
    email_verified_at   TEXT,
    approved_at         TEXT,
    approved_by         INTEGER REFERENCES users (id) ON DELETE SET NULL,
    created_at          TEXT    NOT NULL,
    updated_at          TEXT    NOT NULL
);
CREATE INDEX idx_users_status ON users (status);

CREATE TABLE password_history (
    id            INTEGER PRIMARY KEY,
    user_id       INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    password_hash TEXT    NOT NULL,
    created_at    TEXT    NOT NULL
);
CREATE INDEX idx_password_history_user_created ON password_history (user_id, created_at);

-- token_hash is the SHA-256 of the cookie value; the cookie itself is never stored.
CREATE TABLE sessions (
    token_hash   BLOB    PRIMARY KEY CHECK (typeof(token_hash) = 'blob' AND length(token_hash) = 32),
    user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at   TEXT    NOT NULL,
    last_seen_at TEXT    NOT NULL,
    expires_at   TEXT    NOT NULL,
    user_agent   TEXT    CHECK (user_agent IS NULL OR length(user_agent) <= 300)
);
CREATE INDEX idx_sessions_user    ON sessions (user_id);
CREATE INDEX idx_sessions_expires ON sessions (expires_at);

-- One-time tokens mailed to a user. token_hash is the SHA-256 of the token.
CREATE TABLE tokens (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    purpose    TEXT    NOT NULL CHECK (purpose IN ('verify_email', 'reset_password', 'change_email')),
    token_hash BLOB    NOT NULL UNIQUE CHECK (typeof(token_hash) = 'blob' AND length(token_hash) = 32),
    new_email  TEXT,
    expires_at TEXT    NOT NULL,
    used_at    TEXT,
    created_at TEXT    NOT NULL,
    CHECK ((purpose = 'change_email') = (new_email IS NOT NULL))
);
CREATE INDEX idx_tokens_user_purpose ON tokens (user_id, purpose);

CREATE TABLE collectives (
    id          INTEGER PRIMARY KEY,
    name        TEXT    NOT NULL CHECK (length(name) BETWEEN 2 AND 80),
    name_key    TEXT    NOT NULL UNIQUE,
    kind        TEXT    NOT NULL CHECK (kind IN ('coletivo', 'produtora')),
    status      TEXT    NOT NULL CHECK (status IN ('pending', 'approved', 'rejected')),
    created_by  INTEGER REFERENCES users (id) ON DELETE SET NULL,
    approved_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    approved_at TEXT,
    created_at  TEXT    NOT NULL,
    updated_at  TEXT    NOT NULL
);
CREATE INDEX idx_collectives_status_kind ON collectives (status, kind);

CREATE TABLE memberships (
    user_id           INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    collective_id     INTEGER NOT NULL REFERENCES collectives (id) ON DELETE CASCADE,
    role              TEXT    NOT NULL CHECK (role IN ('owner', 'member')),
    status            TEXT    NOT NULL CHECK (status IN ('pending', 'approved', 'rejected')),
    admin_approved_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    admin_approved_at TEXT,
    owner_approved_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    owner_approved_at TEXT,
    requested_at      TEXT    NOT NULL,
    decided_at        TEXT,
    PRIMARY KEY (user_id, collective_id)
);
CREATE INDEX idx_memberships_collective_status ON memberships (collective_id, status);

-- starts_on is a local calendar date (YYYY-MM-DD) and start_time a local
-- wall-clock time (HH:MM), both in the app's time zone.
CREATE TABLE events (
    id            INTEGER PRIMARY KEY,
    collective_id INTEGER NOT NULL REFERENCES collectives (id) ON DELETE CASCADE,
    created_by    INTEGER REFERENCES users (id) ON DELETE SET NULL,
    name          TEXT    NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    starts_on     TEXT    NOT NULL CHECK (starts_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    start_time    TEXT    NOT NULL CHECK (start_time GLOB '[0-2][0-9]:[0-5][0-9]' AND start_time < '24:00'),
    duration_min  INTEGER NOT NULL CHECK (duration_min BETWEEN 15 AND 1439),
    type          TEXT    NOT NULL CHECK (type IN ('festa', 'rave', 'festival', 'workshop', 'feira', 'outros')),
    is_free       INTEGER NOT NULL CHECK (is_free IN (0, 1)),
    price_cents   INTEGER,
    ticket_url    TEXT    CHECK (ticket_url IS NULL OR (length(ticket_url) <= 500 AND (ticket_url GLOB 'http://*' OR ticket_url GLOB 'https://*'))),
    style         TEXT    NOT NULL CHECK (length(style) BETWEEN 1 AND 60),
    substyle      TEXT    CHECK (substyle IS NULL OR length(substyle) <= 60),
    style_other   TEXT    CHECK (style_other IS NULL OR length(style_other) BETWEEN 1 AND 60),
    artists       TEXT    CHECK (artists IS NULL OR length(artists) <= 2000),
    description   TEXT    CHECK (description IS NULL OR length(description) <= 4000),
    created_at    TEXT    NOT NULL,
    updated_at    TEXT    NOT NULL,
    CHECK ((is_free = 1 AND price_cents IS NULL) OR (is_free = 0 AND price_cents IS NOT NULL AND price_cents > 0)),
    CHECK ((style = 'outros') = (style_other IS NOT NULL)),
    CHECK (style <> 'outros' OR substyle IS NULL)
);
CREATE INDEX idx_events_starts_on     ON events (starts_on);
CREATE INDEX idx_events_collective    ON events (collective_id);
CREATE INDEX idx_events_created_by    ON events (created_by);

CREATE TABLE email_outbox (
    id              INTEGER PRIMARY KEY,
    to_addr         TEXT    NOT NULL,
    subject         TEXT    NOT NULL,
    body_text       TEXT    NOT NULL,
    body_html       TEXT,
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TEXT    NOT NULL,
    sent_at         TEXT,
    last_error      TEXT,
    created_at      TEXT    NOT NULL
);
CREATE INDEX idx_email_outbox_pending ON email_outbox (sent_at, next_attempt_at);

CREATE TABLE audit_log (
    id          INTEGER PRIMARY KEY,
    actor_id    INTEGER REFERENCES users (id) ON DELETE SET NULL,
    action      TEXT    NOT NULL,
    target_type TEXT    NOT NULL,
    target_id   INTEGER,
    detail      TEXT,
    created_at  TEXT    NOT NULL
);
CREATE INDEX idx_audit_log_created ON audit_log (created_at);
