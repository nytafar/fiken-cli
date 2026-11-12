// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// Which failures may be answered by sending the request again. The generated
// client retried every verb three times after a transport error or a 5xx.
// For a read that is harmless. For a write it is not: Fiken has no idempotency
// key, so a create whose answer was lost on the way back (a timeout, a dropped
// connection, a 502 from a proxy in front of a server that had already booked
// it) was sent again and booked twice, inside one call, where no idempotency
// map or duplicate lookup of ours could see it.
//
// A write is therefore sent once. The caller gets the error and decides: the
// duplicate guard on `purchases create` and the recovery pass in `commit` both
// look in Fiken for what the lost request may have made before posting again.
// The two retries that remain for writes are the ones where the server said it
// did nothing: 429 (rate limited) and the single 401 that follows a token
// refresh.
package client

import "net/http"

// retryAfterAmbiguousFailure reports whether a request may be sent again
// after a failure that leaves open whether the server acted on it: a transport
// error, or a 5xx answer.
func retryAfterAmbiguousFailure(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}
