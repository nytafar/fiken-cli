// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — tests for the create-result id (issue #26), driven
// through the real command tree against a fake Fiken that answers a create the
// way the API does: 201, no body, a Location header.
package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// createServer is a fake Fiken for the write commands. `existing` is what a
// GET on the purchases list answers, so the duplicate guard (issue #25) can be
// driven from the same server.
type createServer struct {
	baseURL  string
	existing string

	mu     sync.Mutex
	posts  []string
	bodies []string
	gets   []string
}

func (s *createServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/companies/agensia/purchases":
		body, _ := io.ReadAll(r.Body)
		s.posts = append(s.posts, r.URL.Path)
		s.bodies = append(s.bodies, string(body))
		w.Header().Set("Location", s.baseURL+"/companies/agensia/purchases/900123")
		w.WriteHeader(http.StatusCreated)
	case r.Method == http.MethodPost && r.URL.Path == "/companies/agensia/purchases/900123/payments":
		s.posts = append(s.posts, r.URL.Path)
		w.Header().Set("Location", s.baseURL+"/companies/agensia/purchases/900123/payments/77")
		w.WriteHeader(http.StatusCreated)
	case r.Method == http.MethodPost && r.URL.Path == "/companies/agensia/invoices/drafts":
		s.posts = append(s.posts, r.URL.Path)
		w.Header().Set("Location", s.baseURL+"/companies/agensia/invoices/drafts/3f6c1a52-0000-4000-8000-000000000001")
		w.WriteHeader(http.StatusCreated)
	case r.Method == http.MethodPost && r.URL.Path == "/companies/agensia/contacts":
		// A create that answers without a Location: nothing may be invented.
		s.posts = append(s.posts, r.URL.Path)
		w.WriteHeader(http.StatusCreated)
	case r.Method == http.MethodGet && r.URL.Path == "/companies/agensia/purchases":
		s.gets = append(s.gets, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		body := s.existing
		if body == "" || r.URL.Query().Get("page") != "0" && r.URL.Query().Get("page") != "" {
			body = "[]"
		}
		_, _ = w.Write([]byte(body))
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (s *createServer) postCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.posts)
}

// runCreateCLI drives the command tree against a createServer. stdin, when
// non-empty, replaces os.Stdin for the call: the generated create commands
// read the --stdin body from the process stdin, not from the cobra command.
func runCreateCLI(t *testing.T, srvState *createServer, stdin string, args ...string) (string, string, error) {
	t.Helper()
	srv := httptest.NewServer(srvState)
	t.Cleanup(srv.Close)
	srvState.baseURL = srv.URL

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("FIKEN_CONFIG", filepath.Join(home, "config.toml"))
	t.Setenv("FIKEN_BASE_URL", srv.URL)
	t.Setenv("FIKEN_API_TOKEN", "test-token")
	t.Setenv("FIKEN_NO_LOCK", "1")

	if stdin != "" {
		path := filepath.Join(home, "stdin.json")
		if err := os.WriteFile(path, []byte(stdin), 0o600); err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		old := os.Stdin
		os.Stdin = f
		t.Cleanup(func() { os.Stdin = old; f.Close() })
	}

	var out, errOut bytes.Buffer
	root := RootCmd()
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errOut.String(), err
}

const purchaseBody = `{"date":"2026-03-02","kind":"supplier","currency":"NOK","supplierId":501,"identifier":"INV-1001","lines":[{"description":"Hosting","netPrice":10000,"vat":2500,"account":"6540","vatType":"HIGH"}]}`

func decodeEnvelope(t *testing.T, out string) map[string]any {
	t.Helper()
	var env map[string]any
	dec := json.NewDecoder(strings.NewReader(out))
	dec.UseNumber()
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("stdout is not a JSON envelope: %v\n%s", err, out)
	}
	return env
}

func TestPurchasesCreate_EnvelopeCarriesIDAndLocation(t *testing.T) {
	srv := &createServer{}
	out, _, err := runCreateCLI(t, srv, purchaseBody, "purchases", "create", "agensia", "--stdin", "--agent")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	env := decodeEnvelope(t, out)
	// A number, not a string: `jq .id` must print the bare purchaseId.
	if id, ok := env["id"].(json.Number); !ok || id.String() != "900123" {
		t.Fatalf("id = %#v, want the number 900123", env["id"])
	}
	if loc, _ := env["location"].(string); loc != srv.baseURL+"/companies/agensia/purchases/900123" {
		t.Fatalf("location = %q", env["location"])
	}
	if env["success"] != true || env["resource"] != "purchases" {
		t.Fatalf("the rest of the envelope must survive: %v", env)
	}
	if srv.postCount() != 1 {
		t.Fatalf("posts = %d, want 1", srv.postCount())
	}
}

func TestCreate_NestedAndUUIDLocations(t *testing.T) {
	t.Run("a payment reports the payment id, not the purchase id", func(t *testing.T) {
		out, _, err := runCreateCLI(t, &createServer{}, `{"date":"2026-03-02","account":"1920:10001","amount":12500}`,
			"purchases", "payments", "create-purchase", "agensia", "900123", "--stdin", "--agent")
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if id := decodeEnvelope(t, out)["id"]; id != json.Number("77") {
			t.Fatalf("id = %#v, want 77", id)
		}
	})
	t.Run("a draft's UUID stays a string", func(t *testing.T) {
		out, _, err := runCreateCLI(t, &createServer{}, `{"type":"invoice","daysUntilDueDate":14,"customerId":1}`,
			"invoices", "create-draft", "agensia", "--stdin", "--agent")
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if id := decodeEnvelope(t, out)["id"]; id != "3f6c1a52-0000-4000-8000-000000000001" {
			t.Fatalf("id = %#v, want the draft UUID as a string", id)
		}
	})
	t.Run("no Location, no id", func(t *testing.T) {
		out, _, err := runCreateCLI(t, &createServer{}, `{"name":"Leverandør AS"}`,
			"contacts", "create", "agensia", "--stdin", "--agent")
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		env := decodeEnvelope(t, out)
		if _, has := env["id"]; has {
			t.Fatalf("an id was invented for a response with no Location: %v", env)
		}
	})
}

func TestPurchasesCreate_DryRunCarriesNoID(t *testing.T) {
	srv := &createServer{}
	out, _, err := runCreateCLI(t, srv, purchaseBody, "purchases", "create", "agensia", "--stdin", "--agent", "--dry-run")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(out, `"id"`) || srv.postCount() != 0 {
		t.Fatalf("a dry run must post nothing and report no id; posts=%d out=%s", srv.postCount(), out)
	}
}

func TestWithCreateResult(t *testing.T) {
	const loc = "https://api.example/companies/agensia/purchases/42"
	tests := []struct {
		name, in string
		wantID   bool
	}{
		{"success envelope", `{"action":"post","resource":"purchases","status":201,"success":true}`, true},
		{"failed envelope", `{"action":"post","resource":"purchases","status":201,"success":false}`, false},
		{"not the envelope", `{"purchaseId":42,"success":true}`, false},
		{"not JSON", "id,name\n1,x\n", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(withCreateResult([]byte(tt.in), loc))
			if has := strings.Contains(got, `"location"`); has != tt.wantID {
				t.Fatalf("location present = %v, want %v: %s", has, tt.wantID, got)
			}
			if !tt.wantID && got != tt.in {
				t.Fatalf("output was rewritten: %q -> %q", tt.in, got)
			}
		})
	}
	if got := string(withCreateResult([]byte(`{"action":"post","success":true}`), "")); strings.Contains(got, "id") {
		t.Fatalf("no Location must leave the envelope alone: %s", got)
	}
}
