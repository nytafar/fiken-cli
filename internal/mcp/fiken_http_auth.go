// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated. Makes the streamable-HTTP MCP server
// use FIKEN'S OAuth2 as its own authorization layer:
//
//   - Each HTTP request's `Authorization: Bearer <token>` is the caller's Fiken
//     OAuth access token (or personal API token). It is threaded into the tool
//     context and forwarded as the bearer on the downstream Fiken API call, so
//     the MCP server acts on behalf of whoever connected — no shared server
//     credential, fully multi-tenant.
//   - The server advertises Fiken as the OAuth authorization server via RFC 9728
//     (protected-resource) and RFC 8414 (authorization-server) metadata, so an
//     MCP client can discover the authorize/token endpoints and run the flow.
//   - With --require-auth, an unauthenticated MCP request gets 401 +
//     WWW-Authenticate pointing at the protected-resource metadata, which is the
//     standard trigger for MCP-client OAuth.
//
// Caveat: Fiken has no dynamic client registration (RFC 7591). MCP clients that
// require DCR must instead be configured with a pre-registered FIKEN_CLIENT_ID
// (the authorization-server metadata omits a registration_endpoint to signal
// this). Token passthrough works regardless of how the client obtained the token.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/server"
)

type fikenCtxKey int

const fikenTokenCtxKey fikenCtxKey = 0

const (
	fikenIssuer       = "https://fiken.no"
	fikenAuthorizeURL = "https://fiken.no/oauth/authorize"
	fikenTokenURL     = "https://fiken.no/oauth/token"
)

// fikenBearerContext extracts a Bearer token from the Authorization header and
// stashes it in the context for newMCPClient to use as the per-request bearer.
func fikenBearerContext(ctx context.Context, r *http.Request) context.Context {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ctx
	}
	if tok := strings.TrimSpace(strings.TrimPrefix(h, "Bearer ")); tok != "" && tok != h {
		return context.WithValue(ctx, fikenTokenCtxKey, tok)
	}
	return ctx
}

// fikenTokenFromContext returns the per-request Fiken token, if any.
func fikenTokenFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(fikenTokenCtxKey).(string); ok {
		return v
	}
	return ""
}

// publicBaseURL reconstructs the externally-visible base URL for metadata, honoring
// a FIKEN_MCP_PUBLIC_URL override and reverse-proxy forwarding headers.
func publicBaseURL(r *http.Request) string {
	if u := strings.TrimRight(os.Getenv("FIKEN_MCP_PUBLIC_URL"), "/"); u != "" {
		return u
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = h
	}
	return scheme + "://" + host
}

func writeJSONMeta(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_ = json.NewEncoder(w).Encode(v)
}

// ServeStreamableHTTPWithFikenOAuth serves the MCP server over streamable HTTP
// with Fiken OAuth2 as the authorization layer (see file header). When
// requireAuth is true, MCP requests without a Bearer token are rejected with
// 401 + WWW-Authenticate so the client begins the OAuth flow; otherwise the
// server falls back to its locally-configured token (single-user convenience).
func ServeStreamableHTTPWithFikenOAuth(s *server.MCPServer, addr string, requireAuth bool) error {
	httpSrv := server.NewStreamableHTTPServer(s, server.WithHTTPContextFunc(fikenBearerContext))

	mux := http.NewServeMux()

	// RFC 9728: protected-resource metadata — points clients at Fiken as the
	// authorization server for this MCP resource.
	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, r *http.Request) {
		writeJSONMeta(w, map[string]any{
			"resource":                 publicBaseURL(r),
			"authorization_servers":    []string{fikenIssuer},
			"bearer_methods_supported": []string{"header"},
			"resource_documentation":   "https://api.fiken.no/api/v2/docs/",
		})
	})

	// RFC 8414: authorization-server metadata for Fiken (synthesized — Fiken
	// does not publish its own). No registration_endpoint: Fiken has no DCR, so
	// clients use a pre-registered FIKEN_CLIENT_ID.
	asMeta := map[string]any{
		"issuer":                                fikenIssuer,
		"authorization_endpoint":                fikenAuthorizeURL,
		"token_endpoint":                        fikenTokenURL,
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
		"code_challenge_methods_supported":      []string{"S256"},
	}
	asHandler := func(w http.ResponseWriter, r *http.Request) { writeJSONMeta(w, asMeta) }
	mux.HandleFunc("/.well-known/oauth-authorization-server", asHandler)
	// Some clients probe the AS metadata under the resource path suffix.
	mux.HandleFunc("/.well-known/openid-configuration", asHandler)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if requireAuth && fikenTokenFromContext(fikenBearerContext(r.Context(), r)) == "" {
			w.Header().Set("WWW-Authenticate",
				fmt.Sprintf(`Bearer resource_metadata=%q`, publicBaseURL(r)+"/.well-known/oauth-protected-resource"))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized","error_description":"present a Fiken OAuth2 Bearer token (see /.well-known/oauth-protected-resource)"}`))
			return
		}
		httpSrv.ServeHTTP(w, r)
	})

	fmt.Fprintf(os.Stderr, "fiken-mcp: streamable HTTP at %s (Fiken OAuth2 layer; require-auth=%v)\n", addr, requireAuth)
	fmt.Fprintf(os.Stderr, "  discovery: <base>/.well-known/oauth-protected-resource\n")
	return http.ListenAndServe(addr, mux)
}
