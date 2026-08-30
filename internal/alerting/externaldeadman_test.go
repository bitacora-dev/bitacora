package alerting

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestExternalDeadman_PingSucceedsOn2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	e := &ExternalDeadman{URL: server.URL}
	if err := e.Ping(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExternalDeadman_PingFailsOnNon2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	e := &ExternalDeadman{URL: server.URL}
	if err := e.Ping(context.Background()); err == nil {
		t.Fatal("expected an error for a 500 response")
	}
}

func TestExternalDeadman_PingFailsWhenUnreachable(t *testing.T) {
	e := &ExternalDeadman{URL: "http://127.0.0.1:1"} // nothing listens here
	if err := e.Ping(context.Background()); err == nil {
		t.Fatal("expected an error when the endpoint is unreachable")
	}
}

func TestExternalDeadman_RunKeepsGoingAfterAFailure(t *testing.T) {
	var successes int32
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		atomic.AddInt32(&successes, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	e := &ExternalDeadman{URL: server.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	var errCount int32
	e.Run(ctx, 30*time.Millisecond, func(err error) {
		atomic.AddInt32(&errCount, 1)
	})

	if atomic.LoadInt32(&errCount) == 0 {
		t.Fatal("expected at least the first failure to be reported via onError")
	}
	if atomic.LoadInt32(&successes) == 0 {
		t.Fatal("expected Run to keep pinging after a failure, not stop")
	}
}
