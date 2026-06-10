//go:build unix

// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL). Cross-process single-flight via flock(2). The kernel
// releases the lock automatically when the fd closes — including on process
// death — so a crashed invocation self-heals with no stale lock file.
package client

import (
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

var (
	fikenFlockMu   sync.Mutex
	fikenFlockFile *os.File
)

func fikenLockPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "fiken-cli-single-flight.lock")
	}
	dir := filepath.Join(home, ".cache", "fiken-cli")
	_ = os.MkdirAll(dir, 0o700)
	return filepath.Join(dir, "single-flight.lock")
}

// flockAcquire takes an exclusive advisory lock on the shared lock file. The
// in-process gate in fiken_transport.go guarantees only one goroutine reaches
// here at a time, so a single lazily-opened fd is safe.
func flockAcquire() error {
	fikenFlockMu.Lock()
	defer fikenFlockMu.Unlock()
	if fikenFlockFile == nil {
		f, err := os.OpenFile(fikenLockPath(), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return err
		}
		fikenFlockFile = f
	}
	return syscall.Flock(int(fikenFlockFile.Fd()), syscall.LOCK_EX)
}

func flockRelease() {
	fikenFlockMu.Lock()
	defer fikenFlockMu.Unlock()
	if fikenFlockFile != nil {
		_ = syscall.Flock(int(fikenFlockFile.Fd()), syscall.LOCK_UN)
	}
}
