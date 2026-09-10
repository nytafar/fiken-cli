> Machine-readable records for the four patches below: `oauth-token-endpoint-http-basic.json`,
> `auth-setup-registration-path.json`, `auth-login-redirect-uri.json`,
> `auth-login-callback-timeout.json` in this directory. Those are what the regen/validate
> tooling reads (it only loads `*.json`); this file is the human explanation. Keep both in sync.

# OAuth token endpoint uses HTTP Basic client authentication

**Files:** `internal/cli/auth.go` (`runOAuthLogin`), `internal/client/client.go` (`refreshAccessToken`)

**Why:** Fiken's token endpoint at `https://fiken.no/oauth/token` is protected with
HTTP Basic authentication — the client id and secret are the request credentials, not
form fields. The generated flow posted `client_id`/`client_secret` in the body with no
`Authorization` header, which Fiken rejects, so `auth login` could never complete and an
expired access token could never refresh. Fiken also requires `state` on the
authorization-code exchange; the generated body omitted it.

Verified end to end against a mock token endpoint: the exchange and the refresh both send
`Authorization: Basic base64(client_id:client_secret)` and carry no credentials in the body.

**On reprint, re-apply:**

- Set the Basic auth header from client id + secret on both token requests.
- Drop `client_id`/`client_secret` from both request bodies when a secret is present.
  Sending credentials in both places is what many OAuth servers reject as
  `invalid_request`. A public client (no secret) still sends `client_id` in the body,
  since it has nothing to authenticate with.
- Include `state` in the authorization-code token request.

Source: Fiken API v2 description, "Token Endpoint" and "Refresh Tokens"
(`https://api.fiken.no/api/v2/docs/swagger.yaml`).

---

# `auth setup` prints the real registration path

**File:** `internal/cli/auth.go` (`newAuthSetupCmd`)

**Why:** the generated text pointed at bare `https://fiken.no/`. Fiken hides OAuth app
registration behind an account-level developer flag, so that link leaves the user hunting.
The command now prints the menu path from Fiken's own docs (Rediger konto -> Profil ->
Andre innstillinger, then the API tab under Brukerinnstillinger), the redirect URI the
login flow actually listens on, and the paid-module and 5-user development limits.

**On reprint, re-apply:** keep the concrete steps; the `--launch` URL stays `https://fiken.no/`.

---

# `auth login --redirect-uri` for off-host browsers

**File:** `internal/cli/auth.go` (`newAuthLoginCmd`, `runOAuthLogin`)

**Why:** the generated flow derives the redirect URI from its own loopback listener, so it
only works when the browser that authorizes runs on the same machine as the CLI. On a
headless server reached over SSH there is no such browser, and Fiken cannot redirect to
the server's `localhost`. The new `--redirect-uri` flag (env `FIKEN_REDIRECT_URI`)
advertises a reachable public callback URL while the listener stays on loopback; an SSH
tunnel or a Tailscale serve proxy bridges the two. The URL is used in both the authorize
request and the token exchange, which Fiken requires to match.

Verified end to end through `tailscale serve --https=8443 8085` against a mock
authorization server: the callback arrived and the token exchange carried the proxy URL.

**On reprint, re-apply:** the flag, its use in place of the derived loopback URL in both
the live path and the `cliutil.IsVerifyEnv()` short-circuit, and the loopback default when
the flag is empty.

---

# `auth login` callback wait is 10 minutes

**File:** `internal/cli/auth.go` (`runOAuthLogin`)

**Why:** the generated flow waits 2 minutes for the callback. That assumes a browser on the
same machine. On a headless host the URL has to be carried to another device and approved
there, and 2 minutes does not survive that round trip. Widened to 10.

**On reprint, re-apply:** the timeout value and its error string.
