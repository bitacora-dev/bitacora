package hubauth

import (
	"sync/atomic"
	"testing"
)

// argon2Cost carries the ADR-0023 Argon2id parameters.
type argon2Cost struct {
	time      uint32
	memoryKiB uint32
	threads   uint8
}

// productionArgon2Cost is the ADR-0023 decision: 3 iterations, 64 MiB,
// parallelism 4. Changing it requires amending that ADR.
var productionArgon2Cost = argon2Cost{time: 3, memoryKiB: 64 * 1024, threads: 4}

var argon2CostOverride atomic.Pointer[argon2Cost]

func currentArgon2Cost() argon2Cost {
	if cost := argon2CostOverride.Load(); cost != nil {
		return *cost
	}
	return productionArgon2Cost
}

// UseCheapHashingForTests lowers the Argon2id cost so test suites that create
// many credentials finish under -race. It panics outside a test binary, so a
// production hub can never run with the weakened parameters, and hashes made
// with it are only ever verified inside the same test process.
func UseCheapHashingForTests() {
	if !testing.Testing() {
		panic("hubauth: UseCheapHashingForTests called outside a test binary")
	}
	argon2CostOverride.Store(&argon2Cost{time: 1, memoryKiB: 64, threads: 1})
}
