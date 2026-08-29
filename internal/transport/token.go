// Package transport implements Bitácora's agent-to-hub wire protocol
// (ADR-0008): the POST /v1/ingest endpoint over HTTP/2 with a Protobuf
// body, Argon2id-hashed per-agent tokens bound to a host_id, and batch
// idempotency by ULID.
package transport

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters. These match the RFC 9106 "second recommended"
// interactive profile — the hub verifies a token on every request, so it
// can't afford the higher memory profile meant for offline password
// storage without a real latency cost per ingest call.
const (
	argon2Time    = 2
	argon2Memory  = 19 * 1024 // KiB
	argon2Threads = 1
	argon2KeyLen  = 32
	saltLen       = 16
)

// HashToken returns an encoded Argon2id hash of token, in the standard
// $argon2id$v=...$m=...,t=...,p=...$salt$hash form — the same shape
// libraries like passlib and PHP's password_hash produce, so it's
// recognizable and future-portable.
func HashToken(token string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generating salt: %w", err)
	}
	hash := argon2.IDKey([]byte(token), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argon2Memory, argon2Time, argon2Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

// VerifyToken reports whether token matches encodedHash, in constant time
// with respect to the hash comparison (the Argon2id computation itself is
// not secret-independent-time, but that's true of any KDF-based check).
func VerifyToken(token, encodedHash string) (bool, error) {
	params, salt, wantHash, err := decodeHash(encodedHash)
	if err != nil {
		return false, err
	}

	gotHash := argon2.IDKey([]byte(token), salt, params.time, params.memory, params.threads, uint32(len(wantHash)))
	return subtle.ConstantTimeCompare(gotHash, wantHash) == 1, nil
}

type argon2Params struct {
	memory  uint32
	time    uint32
	threads uint8
}

func decodeHash(encoded string) (params argon2Params, salt, hash []byte, err error) {
	parts := strings.Split(encoded, "$")
	// parts[0] is "" (leading $), [1]="argon2id", [2]="v=..", [3]="m=..,t=..,p=..", [4]=salt, [5]=hash
	if len(parts) != 6 || parts[1] != "argon2id" {
		return params, nil, nil, fmt.Errorf("unrecognized hash format")
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return params, nil, nil, fmt.Errorf("parsing version: %w", err)
	}
	if version != argon2.Version {
		return params, nil, nil, fmt.Errorf("unsupported argon2 version %d", version)
	}

	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &params.memory, &params.time, &params.threads); err != nil {
		return params, nil, nil, fmt.Errorf("parsing params: %w", err)
	}

	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return params, nil, nil, fmt.Errorf("decoding salt: %w", err)
	}
	hash, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return params, nil, nil, fmt.Errorf("decoding hash: %w", err)
	}

	return params, salt, hash, nil
}
