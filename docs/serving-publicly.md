# Serving the Fiken MCP publicly (claude.ai + remote agents)

How to expose `fiken-mcp` over public HTTPS so a remote MCP client — **claude.ai
custom connectors**, Claude Desktop remote connectors, hosted agents — can use
it, with **Fiken OAuth2 as the authorization layer**.

> **Status:** the streamable-HTTP transport + Fiken-OAuth *passthrough* and
> discovery metadata are built today (`fiken-mcp --transport http`). The
> **OAuth broker** needed for claude.ai's *one-click* connector is **not built
> yet** — this doc specifies it so it can be added when you deploy. Recommended
> posture: **serve read-only publicly; keep writes local.**

---

## 1. The transport (built)

```bash
fiken-mcp --transport http --addr :7777
```

Serves MCP over streamable HTTP. Put it behind TLS and a public hostname — e.g.
a Cloudflare Tunnel, a reverse proxy (Caddy/nginx) with a real cert, or a small
VM. Then tell the server its public identity so discovery metadata is correct:

```bash
export FIKEN_MCP_PUBLIC_URL=https://fiken-mcp.example.com
fiken-mcp --transport http --addr :7777 --require-auth
```

`--require-auth` makes an unauthenticated MCP request return `401` +
`WWW-Authenticate`, which is what kicks off a client's OAuth flow.

## 2. The OAuth situation

claude.ai's connector auth follows the MCP authorization spec: **OAuth 2.1 +
PKCE**, **metadata discovery** (RFC 9728 protected-resource → RFC 8414
authorization-server), and **Dynamic Client Registration** (RFC 7591).

Fiken's OAuth (`https://fiken.no/oauth/authorize` + `/oauth/token`) is a
classic pre-registered-app, authorization-code flow. It **does not** publish
RFC 8414 metadata and **does not** support DCR. So pointing claude.ai straight
at Fiken (what the built-in metadata does today) stalls: claude.ai fetches
`https://fiken.no/.well-known/oauth-authorization-server` (404) and has no way
to register a client.

There are two ways to bridge this.

### Path A — OAuth broker in the MCP server (recommended for claude.ai; TO BUILD)

Make `fiken-mcp` its own DCR-capable authorization server that brokers to Fiken
on the backend. claude.ai then talks only to your server; your server talks to
Fiken using your one pre-registered app.

What to implement (all on the MCP server, behind `FIKEN_MCP_PUBLIC_URL`):

| Endpoint | Behavior |
|---|---|
| `/.well-known/oauth-protected-resource` | `authorization_servers: ["<PUBLIC_URL>"]` — point at **this server**, not fiken.no |
| `/.well-known/oauth-authorization-server` | `issuer`, `authorization_endpoint=<PUBLIC_URL>/authorize`, `token_endpoint=<PUBLIC_URL>/token`, **`registration_endpoint=<PUBLIC_URL>/register`**, `code_challenge_methods_supported: ["S256"]` |
| `POST /register` | RFC 7591 accept-all DCR: store the client's `redirect_uris`, return a generated `client_id` (public client; no secret needed with PKCE) |
| `GET /authorize` | validate the registered client + PKCE `code_challenge`; 302 to `https://fiken.no/oauth/authorize` with **your** `FIKEN_CLIENT_ID` and `redirect_uri=<PUBLIC_URL>/callback`; keep a short-lived state→{client, claude-redirect, challenge} map |
| `GET /callback` | receive Fiken's `?code`; exchange it at `https://fiken.no/oauth/token` (your `FIKEN_CLIENT_ID`/`SECRET`); mint your own opaque token bound to the Fiken access+refresh token; 302 back to the client's redirect with your `?code` |
| `POST /token` | verify PKCE `code_verifier`; return your access token (and refresh). Map your token → the stored Fiken token. |
| MCP requests | look up the bearer → the Fiken token; forward it to the Fiken API (the existing `newMCPClient(ctx)` passthrough). |

Token store: in-memory is fine for a single-instance server; use a small
encrypted file/KV for restarts. **Only your server's `/callback` is registered
in your Fiken OAuth app** — claude.ai never touches Fiken directly, which is the
whole point of the broker.

This is the standard "MCP server fronting a non-DCR provider" pattern
(equivalent to Cloudflare's `@cloudflare/workers-oauth-provider`). It's a
focused build (~one handler file + a token store); ask and it gets added.

### Path B — manual client_id (lighter; may work depending on claude.ai's UI)

If claude.ai's *advanced* connector settings let you paste an OAuth
`client_id`/`client_secret`, you can skip DCR:

1. In your Fiken OAuth app, add **claude.ai's callback URL** (the one claude.ai
   shows when you add the connector) to the allowed redirect URIs.
2. Adjust the server's `/.well-known/oauth-authorization-server` to be served at
   the URL claude.ai will look for, listing Fiken's `authorize`/`token`
   endpoints (the metadata is already synthesized — see
   `internal/mcp/fiken_http_auth.go`).
3. In claude.ai, add the custom connector with your `FIKEN_MCP_PUBLIC_URL` and
   the pasted `client_id`/`secret`.

This avoids the broker but is brittle: it depends on claude.ai supporting a
pre-registered client for an arbitrary AS, and on Fiken accepting claude.ai's
redirect URI. Treat it as best-effort.

## 3. Security: serve read-only publicly (recommended)

This server reaches your **accounting books and a write layer**
(`commit`/`reconcile`/`reverse`). A public endpoint is a real target even behind
OAuth (token leakage, abuse). Recommended posture:

- **Public = read-only.** Expose the detectors, `search`, `sql`, `context`, and
  BI tools; **drop the write tools** from the public surface. The high-value
  reconciliation/error-hunting/BI use cases are all reads.
- **Writes stay local** over stdio (`fiken-mcp` default), or a separate
  loopback-only instance.

Mechanism (TO BUILD — small): a `--read-only` flag that, when set, skips
registering the write tools (`commit`, `reconcile`, `reverse`, the
write-capable `execute` paths) and rejects non-GET methods in the code-orch
`execute` handler. Pair it with `--require-auth` for the public deployment:

```bash
fiken-mcp --transport http --addr :7777 --require-auth --read-only   # public, read-only
fiken-mcp                                                            # local stdio, full write layer
```

Also: rate-limit at the proxy, log access, scope the Fiken OAuth app's scopes to
read where possible, and rotate `FIKEN_CLIENT_SECRET` if it ever leaks.

## 4. Adding it in claude.ai (once Path A is built + deployed)

1. Deploy `fiken-mcp --transport http --require-auth --read-only` behind HTTPS
   with `FIKEN_MCP_PUBLIC_URL` set.
2. claude.ai → Settings → Connectors → **Add custom connector** → enter the
   public URL.
3. claude.ai discovers the metadata, runs DCR + the OAuth flow; you authorize in
   Fiken once; claude.ai stores the token.
4. The `fiken` tools appear in claude.ai and run against your Fiken data.

## Checklist

- [ ] Public HTTPS (tunnel/proxy with a valid cert)
- [ ] `FIKEN_MCP_PUBLIC_URL` set to the external URL
- [ ] `--require-auth` on the public instance
- [ ] OAuth broker (Path A) built + deployed, **or** manual client_id (Path B) configured
- [ ] `/callback` (broker) registered in the Fiken OAuth app
- [ ] `--read-only` on the public instance; writes kept local
- [ ] proxy rate-limiting + access logging
