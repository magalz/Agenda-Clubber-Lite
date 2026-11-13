package main

import (
	"context"
	"fmt"
	"io"
)

// seedDev fills a development database with sample accounts and collectives.
// It runs only with APP_ENV=dev and SEED_PASSWORD set, and is safe to run again.
func seedDev(ctx context.Context, getenv func(string) string, stdout, stderr io.Writer) int {
	fmt.Fprintln(stderr, "agenda: seed-dev: not implemented yet")
	return 1
}
