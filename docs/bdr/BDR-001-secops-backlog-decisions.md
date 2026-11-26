---
id: BDR-001
title: "SecOps Backlog Product & Domain Security Decisions"
status: accepted
date: 2026-11-26
superseded_by: null
issues: [ACL-39, ACL-43, ACL-48]
scope: [internal/accounts, internal/web, internal/mail]
---

# BDR-001: SecOps Backlog Product & Domain Security Decisions

## Business Context
During the SecOps backlog triage for Agenda Clubber Lite (a closed, admin-approved community calendar for party collectives and producers), three findings required explicit Product Owner trade-offs between abuse mitigation and user experience:

1. **ACL-39 — Two-step collective-name signup enumeration:** Creating a new collective during signup validates uniqueness of the collective name, which can reveal whether a collective name already exists across two steps.
2. **ACL-43 — Authenticated password-check brute force:** Authenticated password change (`POST /senha/trocar`) and password history check (`POST /senha/verificar`) previously did not share the login lockout counter on wrong `current_password`.
3. **ACL-48 — Untrusted signup name in confirmation emails:** Confirmation emails sent to unconfirmed addresses (`verify_email.tmpl`) previously interpolated the user-submitted `Name`, allowing an attacker to send emails containing phishing text or URLs to third-party addresses.

## Agreed Rule
1. **ACL-39 (Risk Accepted — No Code Change):** The Product Owner accepts the low-value two-step collective-name enumeration risk on signup. Agenda Clubber Lite is a closed community calendar, signup is already constrained by the 5/hour IP rate limit, and unconfirmed signups (and their pending collectives) are automatically purged after 7 days (`PurgeUnconfirmedSignups`, Phase 5). No code change is made.
2. **ACL-43 (Shared Account Lockout):** Login, `POST /senha/trocar`, and `POST /senha/verificar` share the same 5-attempt / 15-minute account lockout (`FailedLogins` / `LockedUntil`) whenever a non-empty `current_password` is wrong. A valid `current_password` resets `FailedLogins` even if the new password is later rejected (e.g. password history reuse). While locked, Argon2 verification is skipped. The Product Owner explicitly accepts the self-lockout risk on repeated typos.
3. **ACL-48 (Nameless Greeting for Unconfirmed Addresses):** `verify_email.tmpl` (used for signup confirmation and confirmation resend) uses a fixed nameless greeting (`"Olá!"`) and omits the submitted `Name` from `VerifyEmailData`, preventing attacker-supplied text or URLs from appearing in emails sent to unconfirmed recipients. `existing_account.tmpl` continues to use the already-stored account name.

## Exceptions & Edge Cases
- **ACL-43 empty or overlong `current_password`:** Empty `current_password` or field-validation errors on `POST /senha/trocar` and empty/overlong inputs on `POST /senha/verificar` record no failed-login attempts and do not trigger Argon2.
- **ACL-48 verified accounts:** Only unconfirmed-address verification emails (`verify_email.tmpl`) omit the name; emails sent to already-registered accounts (`existing_account.tmpl`, decision emails, password reminders) retain the stored account name.

## Executable Proof (BDD)
- **ACL-39:** No code change; existing mitigations covered by `internal/web/routes_signup_test.go` (5/h IP rate limit) and `internal/accounts/profile_test.go` (`PurgeUnconfirmedSignups`).
- **ACL-43:**
  - `internal/accounts/password_test.go`: `TestChangePasswordLocksAfterRepeatedWrongCurrentPasswords`, `TestChangePasswordLockedSkipsArgon2`, `TestChangePasswordValidCurrentResetsFailedLoginsEvenOnHistoryRefusal`, `TestChangePasswordFieldErrorsRecordNoFailures`, `TestCheckPasswordHistoryLocksAndSkipsArgon2WhenLocked`, `TestCheckPasswordHistoryValidCurrentResetsFailedLogins`, `TestCheckPasswordHistorySkipsStoreAndArgon2OnEmptyOrOverlong`
  - `internal/web/routes_password_test.go`: `TestPasswordChangeLocksAfterFiveWrongCurrentPasswords`, `TestPasswordVerifyLocksAndResetsFailures`
- **ACL-48:**
  - `internal/accounts/signup_test.go`: `TestSignupVerifyEmailOmitsSubmittedName`
  - `internal/accounts/confirm_test.go`: `TestResendConfirmationOmitsSubmittedName`
  - `internal/mail/render_test.go`: `TestRenderEscapesUserText`, `TestRenderRejectsBadInput`
- **Tracker:** `ACL-39` (accepted, no code commit), `ACL-43` (commit `3ac0385`), `ACL-48` (commit `6db5ca5`)
