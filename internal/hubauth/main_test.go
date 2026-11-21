package hubauth

import (
	"os"
	"testing"
)

// TestMain keeps the ADR-0023 Argon2id cost out of the -race suite. Production
// parameters are pinned by TestProductionArgon2CostMatchesADR0023.
func TestMain(m *testing.M) {
	UseCheapHashingForTests()
	os.Exit(m.Run())
}
