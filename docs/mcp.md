# MCP server

`fiken-mcp` exposes the same operations to MCP clients. Install it with `--with-mcp` (see [install.md](install.md)). It always runs the write guard in test mode; see [configuration.md](configuration.md).

## Claude Code

```bash
claude mcp add fiken-mcp -- fiken-mcp
claude mcp list   # verify
```

## Claude Desktop

Point Claude Desktop at the binary:

Add to `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "fiken": {
      "command": "fiken-mcp",
      "env": { "FIKEN_API_TOKEN": "<your-token>" }
    }
  }
}
```

For remote/HTTP use (and claude.ai), see below.


## MCP over HTTP (Fiken OAuth as the auth layer)

The bundled `fiken-mcp` server speaks stdio by default; it also serves **streamable HTTP** for remote/hosted agents, with **Fiken's own OAuth2 as the authorization layer**:

```bash
fiken-mcp --transport http --addr :7777                 # passthrough: use a Bearer token if present, else the local config token
fiken-mcp --transport http --addr :7777 --require-auth   # require a per-request Fiken token (multi-tenant)
# env equivalents: FIKEN_MCP_TRANSPORT=http  FIKEN_MCP_REQUIRE_AUTH=1
```

How the auth works:

- **Per-request token, forwarded to Fiken.** Each HTTP request's `Authorization: Bearer <token>` is the *caller's* Fiken OAuth access token (or personal API token). The server uses it as the bearer on the downstream Fiken API call — it acts on behalf of whoever connected, with no shared server credential. (`stdio` is unchanged: it uses the local config token.)
- **Fiken is advertised as the OAuth authorization server**, so MCP clients can discover the flow:
  - `GET /.well-known/oauth-protected-resource` → `authorization_servers: ["https://fiken.no"]` (RFC 9728)
  - `GET /.well-known/oauth-authorization-server` → Fiken's `authorize`/`token` endpoints, PKCE S256 (RFC 8414)
  - An MCP request with `--require-auth` and no token returns `401` + `WWW-Authenticate: Bearer resource_metadata=…`, the standard trigger for client-side OAuth.
- **Caveat — no dynamic client registration.** Fiken doesn't support RFC 7591, so the AS metadata omits a `registration_endpoint`. MCP clients that *require* DCR must be configured with a pre-registered `FIKEN_CLIENT_ID`/`FIKEN_CLIENT_SECRET` (a Fiken OAuth app, redirect URI on `localhost`). Plain token passthrough works regardless of how the client obtained the token.
- Behind a proxy/TLS, set `FIKEN_MCP_PUBLIC_URL=https://your-host` so the discovery metadata advertises the correct external URL.

**Serving it publicly for claude.ai / remote agents:** see [`serving-publicly.md`](serving-publicly.md) — claude.ai's one-click connector needs the server to act as a small DCR-capable OAuth broker in front of Fiken (Fiken has no dynamic client registration), plus a recommended **read-only** public posture for an accounting + write server.
