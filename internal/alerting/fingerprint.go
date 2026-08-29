package alerting

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
)

// Fingerprint computes ADR-0009's dedup key: "fingerprint de la regla más
// las etiquetas." The same rule with the same labels always produces the
// same fingerprint, regardless of map iteration order.
func Fingerprint(ruleID string, labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	h.Write([]byte(ruleID))
	for _, k := range keys {
		h.Write([]byte{0x1f})
		h.Write([]byte(k))
		h.Write([]byte{'='})
		h.Write([]byte(labels[k]))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
