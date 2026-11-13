// Package accounts holds the account flows: signup, email confirmation, login,
// password change and recovery. Each flow is a service function that takes the
// domain store and the current time, runs its writes in one Store.WithTx, and
// returns domain.FieldErrors for input the person can fix.
//
// For data access this package uses only internal/domain, never a database
// driver or a store implementation. Helpers and test fixtures carry the prefix
// of their flow (signup…, login…, confirm…, password…) so flows written in
// parallel do not collide.
package accounts
