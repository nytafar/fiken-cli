//go:build !unix

// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL). Non-unix fallback: in-process single-flight only (the
// gate in fiken_transport.go still serializes this process). Cross-process
// locking degrades to a no-op here; the primary target is unix (macOS/Linux).
package client

func flockAcquire() error { return nil }
func flockRelease()       {}
