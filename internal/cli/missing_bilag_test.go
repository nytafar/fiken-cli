// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — table-driven tests for the missing-bilag attachment
// predicate (the load-bearing pure logic of the command).
package cli

import (
	"encoding/json"
	"testing"
)

func mustObj(t *testing.T, s string) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("bad fixture %q: %v", s, err)
	}
	return m
}

func TestHasAttachments(t *testing.T) {
	tests := []struct {
		name         string
		resourceType string
		doc          string
		want         bool
	}{
		{"purchase without attachment field", "purchases", `{"purchaseId":1,"date":"2026-01-01"}`, false},
		{"purchase empty array", "purchases", `{"purchaseId":1,"purchaseAttachments":[]}`, false},
		{"purchase null array", "purchases", `{"purchaseId":1,"purchaseAttachments":null}`, false},
		{"purchase with one attachment", "purchases", `{"purchaseId":1,"purchaseAttachments":[{"identifier":"a"}]}`, true},
		{"purchase with several attachments", "purchases", `{"purchaseAttachments":[{"identifier":"a"},{"identifier":"b"}]}`, true},
		// The bug this table guards: purchases carry purchaseAttachments, so a
		// bare "attachments" key on a purchase must not count as a bilag, and a
		// documented purchase must not be reported as missing one.
		{"purchase bare attachments key does not count", "purchases", `{"purchaseId":1,"attachments":[{"identifier":"a"}]}`, false},
		{"sale with saleAttachments", "sales", `{"saleId":1,"saleAttachments":[{"uuid":"u"}]}`, true},
		{"sale empty saleAttachments", "sales", `{"saleId":1,"saleAttachments":[]}`, false},
		{"sale bare attachments key does not count", "sales", `{"saleId":1,"attachments":[{"uuid":"u"}]}`, false},
		{"journal entry with attachments", "journal_entries", `{"journalEntryId":1,"attachments":[{"uuid":"u"}]}`, true},
		{"journal entry empty attachments", "journal_entries", `{"journalEntryId":1,"attachments":[]}`, false},
		{"journal entry absent attachments", "journal_entries", `{"journalEntryId":1}`, false},
		{"unknown type falls back to attachments", "invoices", `{"attachments":[{"uuid":"u"}]}`, true},
		{"unknown type without attachments", "invoices", `{"saleAttachments":[{"uuid":"u"}]}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasAttachments(tt.resourceType, mustObj(t, tt.doc)); got != tt.want {
				t.Errorf("hasAttachments(%q, %s) = %v, want %v", tt.resourceType, tt.doc, got, tt.want)
			}
		})
	}
}
