---
id: ADR-001
title: "MongoDB Store Reference Integrity via Atomic rev Write-Locking"
status: accepted
date: 2026-11-26
superseded_by: null
issues: [ACL-21]
scope: [internal/store/mongostore, internal/store/storetest, internal/domain]
---

# ADR-001: MongoDB Store Reference Integrity via Atomic `rev` Write-Locking

## Context
MongoDB has no foreign-key constraints and uses snapshot isolation inside multi-document transactions. Previously, reference-checked writes (`Sessions.Create`, `Tokens.Create`, `Memberships.Create`, `Events.Create`, `Collectives.Create`, and related updates) verified parent existence with a read (`FindOne`) outside or inside a transaction, which does not acquire a write lock on the parent document. Consequently, a concurrent `Users.Delete` or `Collectives.Delete` could commit in parallel with a child `Create`, leaving orphan sessions, tokens, memberships, or events.

## Options Considered
1. **Periodic background orphan-sweep job:** Eventually cleans up orphaned child records, but allows transient dangling sessions/tokens/events to exist after deletion and adds operational complexity and sweep queries.
2. **Atomic `$inc: {rev: 1}` write-lock inside `inTx` (`mustExist`):** Replace read-only parent existence checks with an atomic `UpdateOne` (`$inc: {rev: 1}`) inside a transaction (`inTx`), adding a `rev` integer field to the `users` schema alongside `collectives`.

## Decision
Adopt atomic `$inc: {rev: 1}` write-locking in `internal/store/mongostore`:

- Add `rev` to the `users` collection schema (`internal/store/mongostore/schema.go`) and `userDoc` (`internal/store/mongostore/docs.go`), matching the `rev` pattern already used on `collectives`.
- Implement `mustExist` in `internal/store/mongostore/store.go` via `UpdateOne` with `$inc: {rev: 1}`, returning `domain.ErrNotFound` when `MatchedCount == 0`.
- Wrap reference-checked writes (`Sessions.Create`, `Tokens.Create`, `Memberships.Create`, `Events.Create`, `Collectives.Create`, and reference-dependent updates) in `inTx` so they either join the caller's `WithTx` or open their own transaction.
- When a child write races with `Users.Delete` or `Collectives.Delete`, MongoDB's WiredTiger write-conflict detection serializes the two transactions: either the child commits first and is removed by the delete cascade, or the delete commits first and the retried child transaction sees `MatchedCount == 0` (`domain.ErrNotFound`).

## Consequences
- **Positive:** Eliminates orphan sessions, tokens, memberships, and events under concurrent deletes with zero background sweep jobs; preserves the existing `internal/domain` contract and MongoDB single-node replica set architecture.
- **Negative / Risks:** Reference-checked writes perform a parent `UpdateOne` inside a transaction rather than a read-only `FindOne`, introducing a small write-conflict retry cost when many writes target the same user or collective concurrently. At this application's scale, contention is negligible.

## Verification & Links
- **Code / Contracts:** `internal/domain/repo.go`, `internal/store/mongostore/store.go`, `internal/store/mongostore/schema.go`, `internal/store/mongostore/docs.go`, `internal/store/mongostore/users.go`, `internal/store/mongostore/collectives.go`, `internal/store/mongostore/sessions.go`, `internal/store/mongostore/memberships.go`, `internal/store/mongostore/events.go`
- **Tests:** `internal/store/storetest/tx.go` (`RunTx` subtests: `"concurrent Users.Delete vs Sessions.Create, Tokens.Create and Memberships.Create leaves no orphans"` and `"concurrent Collectives.Delete vs Memberships.Create and Events.Create leaves no orphans"`)
- **Tracker:** `ACL-21` (commit `40cdbe7`)
