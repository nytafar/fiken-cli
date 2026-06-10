// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// Fiken allows only ONE concurrent API request per token; concurrent requests
// risk a ban (build-spec §4). The generated AdaptiveLimiter paces requests
// within a single process, but two overlapping invocations (e.g. a background
// sync during an interactive `commit`) would each think they are alone. This
// file closes that gap at the single chokepoint: every generated and
// hand-written request flows through one newHTTPClient() whose Transport is
// nil, so it uses http.DefaultTransport. We wrap http.DefaultTransport from
// init() with a RoundTripper that, for Fiken hosts only:
//
//   - stamps an X-Request-ID header, and
//   - holds a cross-process single-flight lock (in-process semaphore + a
//     file lock) for the full request+response-body lifecycle.
//
// The lock is released on response Body.Close() (not RoundTrip return — the
// body streams after headers arrive) and by a lease timer that self-heals a
// forgotten body. The OS releases the file lock when the process dies, so a
// crash self-heals too. FIKEN_NO_LOCK=1 disables locking for unit tests.
//
// Guard: fiken_transport_test.go asserts newHTTPClient(...).Transport == nil.
// If a future generator bump gives the client an explicit Transport, that test
// fails loudly rather than silently bypassing this lock.
package client

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

// fikenLockHosts are the rate-limited hosts the single-flight lock guards: the
// API host and the OAuth authorize/token host. Any other host (update checks,
// unrelated HTTP) passes straight through so it cannot starve real Fiken work.
var fikenLockHosts = map[string]bool{
	"api.fiken.no": true,
	"fiken.no":     true,
}

// fikenLeaseMax bounds how long the lock is held for a single request. A
// forgotten/never-closed body would otherwise pin the lock forever; the lease
// timer force-releases after this. Generous enough for a slow paginated page
// plus body read, far below any human patience threshold.
const fikenLeaseMax = 90 * time.Second

// fikenInflight is the in-process single-flight gate (capacity 1). Combined
// with the cross-process file lock it guarantees at most one in-flight Fiken
// request per machine for this CLI, which is what Fiken's per-token rule wants.
var fikenInflight = make(chan struct{}, 1)

func init() {
	base := http.DefaultTransport
	if base == nil {
		base = &http.Transport{}
	}
	http.DefaultTransport = &fikenRoundTripper{base: base}
}

type fikenRoundTripper struct{ base http.RoundTripper }

func fikenNewRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fall back to a timestamp-derived value; uniqueness is best-effort for
		// tracing, never a correctness dependency.
		return "req-" + hex.EncodeToString([]byte(time.Now().UTC().Format("150405.000000")))
	}
	return hex.EncodeToString(b[:])
}

func (rt *fikenRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if !fikenLockHosts[req.URL.Hostname()] {
		return rt.base.RoundTrip(req)
	}

	// RoundTrip must not mutate the input request; clone before adding headers.
	r2 := req.Clone(req.Context())
	if r2.Header.Get("X-Request-ID") == "" {
		r2.Header.Set("X-Request-ID", fikenNewRequestID())
	}

	lockEnabled := os.Getenv("FIKEN_NO_LOCK") != "1"
	var release func()
	if lockEnabled {
		rel, err := acquireFikenLock(req)
		if err != nil {
			return nil, err
		}
		release = rel
	} else {
		release = func() {}
	}

	// Lease safety net: force-release if the caller never closes the body.
	timer := time.AfterFunc(fikenLeaseMax, release)
	stopAndRelease := func() {
		timer.Stop()
		release()
	}

	resp, err := rt.base.RoundTrip(r2)
	if err != nil {
		stopAndRelease()
		return nil, err
	}
	// Hold the lock until the body is fully consumed and closed.
	resp.Body = &fikenLockBody{ReadCloser: resp.Body, release: stopAndRelease}
	return resp, nil
}

// acquireFikenLock takes the in-process gate then the cross-process file lock,
// honoring request-context cancellation while waiting on the in-process gate.
// The returned release is idempotent (lease timer and Body.Close may both fire).
func acquireFikenLock(req *http.Request) (func(), error) {
	select {
	case fikenInflight <- struct{}{}:
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
	if err := flockAcquire(); err != nil {
		<-fikenInflight
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			flockRelease()
			<-fikenInflight
		})
	}, nil
}

// fikenLockBody releases the single-flight lock when the response body is
// closed, so the lock spans the whole request+response lifecycle.
type fikenLockBody struct {
	io.ReadCloser
	release func()
	once    sync.Once
}

func (b *fikenLockBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.release)
	return err
}
