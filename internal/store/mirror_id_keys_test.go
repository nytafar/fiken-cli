// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// Pins the primary-key field for the two resources whose printed override was
// a display field (issue #16): accounts key on `code` and inbox documents on
// `documentId`. A reprint that restores `name` fails here.

package store

import (
	"encoding/json"
	"testing"
)

func TestExtractResourceID_AccountsKeyOnCode(t *testing.T) {
	obj, err := DecodeJSONObject(json.RawMessage(`{"code":"1500:10001","name":"Some Customer"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := ExtractResourceID("accounts", obj); got != "1500:10001" {
		t.Fatalf("accounts id = %q, want the account code", got)
	}
}

func TestExtractResourceID_InboxKeyOnDocumentID(t *testing.T) {
	obj, err := DecodeJSONObject(json.RawMessage(`{"documentId":4711,"name":"faktura.pdf","status":"Unused"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := ExtractResourceID("inbox", obj); got != "4711" {
		t.Fatalf("inbox id = %q, want documentId", got)
	}
}

func TestResourceIDFieldOverrides_NoDisplayNameKeys(t *testing.T) {
	for resource, field := range resourceIDFieldOverrides {
		if field == "name" {
			t.Errorf("resource %q keys on the display field %q", resource, field)
		}
	}
}
