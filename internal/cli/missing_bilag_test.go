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
		name string
		doc  string
		want bool
	}{
		{"absent attachments field", `{"purchaseId":1,"date":"2026-01-01"}`, false},
		{"empty attachments array", `{"purchaseId":1,"attachments":[]}`, false},
		{"null attachments", `{"purchaseId":1,"attachments":null}`, false},
		{"one attachment present", `{"purchaseId":1,"attachments":[{"identifier":"a"}]}`, true},
		{"several attachments", `{"attachments":[{"identifier":"a"},{"identifier":"b"}]}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasAttachments(mustObj(t, tt.doc)); got != tt.want {
				t.Errorf("hasAttachments(%s) = %v, want %v", tt.doc, got, tt.want)
			}
		})
	}
}
