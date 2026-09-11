# Configuration, auth and the write guard

## Authentication

Default auth is a personal API token (Settings -> API -> Personlige API-nokler in Fiken), set as FIKEN_API_TOKEN and sent as a Bearer token — the ToS-clean path for your own companies, with no expiry. For acting on behalf of other companies, OAuth2 authorization-code login is wired for FIKEN_CLIENT_ID/FIKEN_CLIENT_SECRET via 'fiken-cli auth login' (opens a browser). Fiken allows only one concurrent request per token, so all calls are serialized through a single-flight lock and a serial, resumable sync.

```bash
export FIKEN_API_TOKEN=<token>   # personal API token (Fiken -> Settings -> API), no expiry
# ...or OAuth2 (for acting on behalf of other companies):
fiken-cli auth login             # uses FIKEN_CLIENT_ID / FIKEN_CLIENT_SECRET
fiken-cli doctor                 # verify auth + connectivity
```

### OAuth2 on a headless host

`auth login` listens on `127.0.0.1:8085` and, by default, tells Fiken to redirect there.
On a headless host the browser that authorizes is somewhere else, so point the callback at
a URL both sides can reach and register that same URL with the OAuth app:

```bash
# e.g. behind a Tailscale serve proxy in front of the loopback listener
tailscale serve --bg --https=8443 8085
fiken-cli auth login --redirect-uri https://<host>.<tailnet>.ts.net:8443/callback
tailscale serve --https=8443 off     # afterwards

# …or just tunnel the port and keep the localhost default
ssh -L 8085:localhost:8085 <host>
```

## Write guard: test companies and live mode

Every mutating request is refused unless the mirrored company record has `testCompany: true`. A refusal exits **8** with a JSON error naming the company slug and a reason code — `not_test_company`, `company_unknown` (the slug is not in the local mirror yet), or `guard_unconfigured` (the fail-closed default when a code path never wired the guard). Nothing is sent to Fiken.

Writing to real books requires live mode, reached only by `FIKEN_MODE` set to `live` in an untracked env file (`.env.local` or `.env` in the working directory, or `~/.config/fiken-cli/env`) or in the process environment. **There is deliberately no CLI flag**: `--agent` never changes the write mode — only `FIKEN_MODE` in the process environment or an untracked env file does — and the MCP server is always in test mode. Every refusal is appended to the audit log.

## Health Check

```bash
fiken-cli doctor
```

Verifies configuration, credentials, and connectivity to the API.

## Configuration

Config file: `~/.config/fiken-cli/config.toml`

Static request headers can be configured under `headers`; per-command header overrides take precedence.

Environment variables:

| Name | Kind | Required | Description |
| --- | --- | --- | --- |
| `FIKEN_API_TOKEN` | per_call | No | Set to your API credential. |
| `FIKEN_CLIENT_ID` | auth_flow_input | No | OAuth2 client ID, used only by 'auth login' when acting on behalf of other companies. Not needed if you use a personal API token. |
| `FIKEN_CLIENT_SECRET` | auth_flow_input | No | Set during initial auth setup. |

### agentcookie (optional)

If you use agentcookie to sync secrets across machines, this CLI auto-adopts agentcookie-managed credentials with no extra setup. When the daemon writes to this CLI's config, `fiken-cli doctor` reports `agentcookie: detected` and `auth-status` labels the source as `agentcookie`. Skip this section if you don't use agentcookie - the CLI works the same as any other.

## Troubleshooting
**Authentication errors (exit code 4)**
- Run `fiken-cli doctor` to check credentials
- Verify the environment variable is set: `echo $FIKEN_API_TOKEN`
**Not found errors (exit code 3)**
- Check the resource ID is correct
- Run the `list` command to see available items

### API-specific
- **HTTP 429 or a sudden ban warning** — Fiken bans on concurrent requests per token — keep --concurrency 1 (the default) and don't run two invocations at once against the same token; the built-in single-flight lock enforces this across processes.
- **A posting won't match the bank line in Fiken** — Date-align: commit dates the posting to the expected bank-statement date, not the invoice date. Re-run with the bank-line date so it rendezvous with the waiting line.
- **401 Unauthorized** — Set FIKEN_API_TOKEN to a personal API token from Fiken (Settings -> API), or run 'fiken-cli auth login' for OAuth2; confirm with 'fiken-cli doctor'.
