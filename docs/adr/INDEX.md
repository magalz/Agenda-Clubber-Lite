# Architecture Decision Index (`docs/adr/INDEX.md`)

> Valid statuses: `proposed` | `accepted` | `superseded` | `refuted` | `deprecated` | `stale`

| ID | File | Short Description | Status | Superseded By | Issues / Scope |
| :--- | :--- | :--- | :--- | :--- | :--- |
| ADR-001 | [ADR-001-mongostore-reference-write-locking.md](./ADR-001-mongostore-reference-write-locking.md) | Serialize MongoDB reference-checked writes against concurrent deletes via atomic `$inc: {rev: 1}` in `inTx` | `accepted` | — | `ACL-21`, `internal/store/mongostore/` |
| ADR-002 | [ADR-002-separated-signup-email-rate-limits.md](./ADR-002-separated-signup-email-rate-limits.md) | Isolate IP and email-hash rate limiters and pre-validate `POST /cadastro` before charging `SignupEmail` | `accepted` | — | `ACL-41`, `internal/web/`, `internal/accounts/` |
