---
id: ADR-002
title: "Separate IP and Email-Hash Rate Limiters with Signup Pre-Validation"
status: accepted
date: 2026-11-26
superseded_by: null
issues: [ACL-41]
scope: [internal/web, internal/accounts]
---

# ADR-002: Separate IP and Email-Hash Rate Limiters with Signup Pre-Validation

## Context
Previously, `Limits.Signup` and `Limits.EmailAction` were each a single `auth.Limiter` shared by both per-IP keys (`ip:<addr>`) and per-email keys (`email:<sha256>`). Because `auth.Limiter` enforces a bounded total key capacity, a flood of distinct keys of one kind (for example, spoofed or disposable addresses on signup, or distributed IP probes) could fill the shared cap and evict or block keys of the other kind. In addition, `POST /cadastro` charged the per-email budget before validating signup fields, allowing malformed or empty submissions to burn a victim's per-email signup quota.

## Options Considered
1. **Increase the shared limiter cap:** Delays exhaustion under flood, but IP and email-hash namespaces still compete for the same key table and malformed signups still spend per-email quota.
2. **Dedicated IP and email-hash limiters plus pre-validation:** Split `Limits` into separate `auth.Limiter` instances for IP (`Signup`, `EmailAction`) and email-hash (`SignupEmail`, `EmailActionEmail`), and validate signup fields via `accounts.ValidateSignup` before charging `SignupEmail` on `POST /cadastro`.

## Decision
Adopt dedicated `auth.Limiter` instances in `internal/web/app.go` (`Signup` / `SignupEmail` and `EmailAction` / `EmailActionEmail`) and pre-validate signup inputs with `accounts.ValidateSignup` before consuming `SignupEmail` on `POST /cadastro`.

- `emailForm` charges the IP limiter first, then validates the email format before charging `EmailActionEmail` (blank, missing, or invalid emails spend no email budget).
- `POST /cadastro` charges `Limits.Signup` (IP) first, runs `accounts.ValidateSignup` (returning 422 with field errors without charging `SignupEmail` on invalid input), and only charges `Limits.SignupEmail` once the payload is structurally valid.
- Each limiter owns an isolated key table, so distinct-key floods on one dimension cannot displace or exhaust the other.

## Consequences
- **Positive:** Per-IP floods cannot evict or starve per-email-hash budgets and vice versa; invalid `POST /cadastro` payloads can no longer lock out a target email address.
- **Negative / Risks:** Slightly higher memory ceiling (four bounded `auth.Limiter` maps instead of two), which remains strictly capped and negligible at this deployment's scale.

## Verification & Links
- **Code / Contracts:** `internal/web/app.go`, `internal/web/routes_signup.go`, `internal/web/routes_confirm.go`, `internal/web/routes_profile.go`, `internal/web/routes_reset.go`, `internal/accounts/signup.go`
- **Tests:** `internal/accounts/signup_test.go` (`TestValidateSignup`), `internal/web/auth_test.go` (`TestNewAppLimits`), `internal/web/routes_signup_test.go` (`TestEmailFormSpendsTheAddressThenTheEmail`, `TestSignupRateLimitPerEmail`, `TestSignupInvalidPostsDoNotSpendEmailBudget`, `TestSignupIPAndEmailLimitersAreIsolated`), `internal/web/routes_profile_test.go` (`TestProfileEmailChangeLimitsPerEmail`), `internal/web/routes_confirm_test.go`, `internal/web/routes_reset_test.go`
- **Tracker:** `ACL-41` (commit `ce100a4`)
