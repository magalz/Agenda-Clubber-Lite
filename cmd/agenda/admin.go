package main

import (
	"context"
	"fmt"
	"io"
)

// createAdmin creates an active admin account from --name, --email and --phone,
// with the password read from stdin. It reads only the MongoDB settings.
func createAdmin(ctx context.Context, args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	fmt.Fprintln(stderr, "agenda: create-admin: not implemented yet")
	return 1
}
