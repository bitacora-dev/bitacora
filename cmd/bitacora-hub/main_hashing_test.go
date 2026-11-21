package main

import (
	"os"
	"testing"

	"github.com/bitacora-dev/bitacora/internal/hubauth"
)

// TestMain lowers the Argon2id cost for this package's tests only: they
// create many credentials and the ADR-0023 64 MiB cost made the -race run
// exceed the default 10-minute timeout. Production parameters are untouched.
func TestMain(m *testing.M) {
	hubauth.UseCheapHashingForTests()
	os.Exit(m.Run())
}
