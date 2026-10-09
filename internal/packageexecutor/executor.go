// Package packageexecutor implements the fixed, privileged package operations
// authorized by ADR-0022. It deliberately exposes operations, not commands.
package packageexecutor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	DefaultRequestDir     = "/var/lib/bitacora/package-actions/requests"
	DefaultResultDir      = "/var/lib/bitacora/package-actions/results"
	DefaultCacheStampPath = "/var/lib/apt/periodic/update-success-stamp"
	DefaultMaxCacheAge    = 24 * time.Hour
)

type Operation string

const (
	RefreshPackageCache        Operation = "refresh-package-cache"
	ApplyPendingPackageUpdates Operation = "apply-pending-package-updates"
)

type Status string

const (
	StatusSuccess Status = "success"
	StatusFailed  Status = "failed"
)

// Request is written by the unprivileged agent only after it has validated a
// signed, human-confirmed order. Its fields are identifiers, never arguments.
type Request struct {
	ID        string    `json:"id"`
	Operation Operation `json:"operation"`
	HostID    string    `json:"host_id"`
}

// Result is consumed by the unprivileged agent to report the terminal job.
// Output is data for the job log, never reinterpreted as an instruction.
type Result struct {
	Request
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Status     Status    `json:"status"`
	ExitCode   int       `json:"exit_code"`
	Output     string    `json:"output"`
}

type Allowlist struct {
	RefreshPackageCache        bool `json:"refresh_package_cache"`
	ApplyPendingPackageUpdates bool `json:"apply_pending_package_updates"`
}

func (a Allowlist) Allows(operation Operation) bool {
	switch operation {
	case RefreshPackageCache:
		return a.RefreshPackageCache
	case ApplyPendingPackageUpdates:
		return a.ApplyPendingPackageUpdates
	default:
		return false
	}
}

type Config struct {
	RequestDir     string
	ResultDir      string
	Allowlist      Allowlist
	CacheStampPath string
	MaxCacheAge    time.Duration
	Now            func() time.Time
}

// Runner is intentionally an operation-level interface. The production
// adapter owns the two fixed argv arrays; callers cannot supply an argv.
type Runner func(context.Context, Operation) (output []byte, exitCode int, err error)

// Enqueue atomically writes a request for the systemd path unit. It rejects
// unsafe IDs so no untrusted value becomes a pathname.
func Enqueue(dir string, request Request) error {
	if !validID(request.ID) || !validOperation(request.Operation) || request.HostID == "" {
		return fmt.Errorf("package action: invalid request")
	}
	data, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("package action: marshal request: %w", err)
	}
	return writeAtomic(filepath.Join(dir, request.ID+".json"), data, 0o640)
}

// ProcessAll consumes each staged request once. It never executes an operation
// that is absent from the helper's independently loaded local allowlist.
func ProcessAll(ctx context.Context, cfg Config, run Runner) (int, error) {
	if cfg.RequestDir == "" {
		cfg.RequestDir = DefaultRequestDir
	}
	if cfg.ResultDir == "" {
		cfg.ResultDir = DefaultResultDir
	}
	if cfg.CacheStampPath == "" {
		cfg.CacheStampPath = DefaultCacheStampPath
	}
	if cfg.MaxCacheAge <= 0 {
		cfg.MaxCacheAge = DefaultMaxCacheAge
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	entries, err := os.ReadDir(cfg.RequestDir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("package action: reading requests: %w", err)
	}
	processed := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || !validID(strings.TrimSuffix(entry.Name(), ".json")) {
			continue
		}
		path := filepath.Join(cfg.RequestDir, entry.Name())
		processing := path + ".processing"
		if err := os.Rename(path, processing); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return processed, fmt.Errorf("package action: claiming request: %w", err)
		}
		result := processOne(ctx, processing, cfg, run)
		if err := writeResult(cfg.ResultDir, result); err != nil {
			return processed, err
		}
		if err := os.Remove(processing); err != nil && !os.IsNotExist(err) {
			return processed, fmt.Errorf("package action: removing request: %w", err)
		}
		processed++
	}
	return processed, nil
}

func processOne(ctx context.Context, path string, cfg Config, run Runner) Result {
	started := cfg.Now().UTC()
	request, err := readRequest(path)
	if err != nil {
		return Result{Request: Request{ID: strings.TrimSuffix(filepath.Base(path), ".json.processing")}, StartedAt: started, FinishedAt: cfg.Now().UTC(), Status: StatusFailed, ExitCode: 1, Output: err.Error()}
	}
	result := Result{Request: request, StartedAt: started}
	if !cfg.Allowlist.Allows(request.Operation) {
		result.Status, result.ExitCode, result.Output = StatusFailed, 1, "package operation is not enabled in the local helper allowlist"
		result.FinishedAt = cfg.Now().UTC()
		return result
	}
	if request.Operation == ApplyPendingPackageUpdates {
		if err := cacheFresh(cfg.CacheStampPath, cfg.MaxCacheAge, cfg.Now()); err != nil {
			result.Status, result.ExitCode, result.Output = StatusFailed, 1, err.Error()
			result.FinishedAt = cfg.Now().UTC()
			return result
		}
	}
	output, exitCode, err := run(ctx, request.Operation)
	result.Output, result.ExitCode, result.FinishedAt = string(output), exitCode, cfg.Now().UTC()
	if err != nil || exitCode != 0 {
		result.Status = StatusFailed
		if result.Output == "" && err != nil {
			result.Output = err.Error()
		}
		return result
	}
	result.Status = StatusSuccess
	return result
}

func cacheFresh(path string, maxAge time.Duration, now time.Time) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("package cache freshness is unavailable: %w", err)
	}
	age := now.Sub(info.ModTime())
	if age > maxAge {
		return fmt.Errorf("package cache is stale (%s old; maximum is %s); refresh it before applying updates", age.Round(time.Second), maxAge)
	}
	return nil
}

func readRequest(path string) (Request, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Request{}, fmt.Errorf("package action: reading request: %w", err)
	}
	var request Request
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return Request{}, fmt.Errorf("package action: decoding request: %w", err)
	}
	if !validID(request.ID) || !validOperation(request.Operation) || request.HostID == "" {
		return Request{}, fmt.Errorf("package action: invalid request")
	}
	return request, nil
}

func writeResult(dir string, result Result) error {
	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("package action: marshal result: %w", err)
	}
	if !validID(result.ID) {
		return fmt.Errorf("package action: invalid result request ID")
	}
	return writeAtomic(filepath.Join(dir, result.ID+".json"), data, 0o640)
}

func ReadResult(path string) (Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	var result Result
	if err := json.Unmarshal(data, &result); err != nil {
		return Result{}, err
	}
	return result, nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func validOperation(operation Operation) bool {
	return operation == RefreshPackageCache || operation == ApplyPendingPackageUpdates
}

func validID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}
