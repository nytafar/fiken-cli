# Install

Two commands: one for the binary, one for the agent skills.

```bash
curl -fsSL https://raw.githubusercontent.com/nytafar/fiken-cli/main/install.sh | bash
npx skills add nytafar/fiken-cli --skill '*' --agent claude-code --global
```

The first line downloads the latest [GitHub release](https://github.com/nytafar/fiken-cli/releases)
for your OS and architecture (Linux and macOS, amd64 and arm64), verifies it against the
release's `checksums.txt`, and installs `fiken-cli` into `~/.local/bin`. The second installs
both skills, `fiken` (the CLI) and `fiken-superforing` (browser bank matching), through
[`npx skills`](https://skills.sh/), which also handles `skills update` and `skills remove`.

Verify:

```bash
fiken-cli --version
fiken-cli doctor      # after setting FIKEN_API_TOKEN, see configuration.md
```

If `fiken-cli` is not found, add `~/.local/bin` to `$PATH`.

## Installer options

```bash
curl -fsSL https://raw.githubusercontent.com/nytafar/fiken-cli/main/install.sh | bash -s -- [options]
```

| Option | Effect |
| --- | --- |
| `--with-mcp` | Also install the `fiken-mcp` server binary ([mcp.md](mcp.md)). |
| `--version <tag>` | Install a specific release, e.g. `--version v2.1.0`. Default is latest. |
| `--bin-dir <dir>` | Install location. Default `$FIKEN_BIN_DIR` or `~/.local/bin`. |
| `--skills` | Run the `npx skills add` step for you. `--agent '*'` targets every agent `npx skills` knows. |
| `--from-source` | Build with `go install` from a clone instead of downloading. |
| `--link-skills` | Development: symlink `skills/` from the clone into `~/.claude/skills` so edits are live. |

Windows: download the `.zip` for your architecture from the releases page and put `fiken-cli.exe`
on `PATH`; the skills step is the same.

## Other agents (Hermes, OpenClaw, ...)

`npx skills add nytafar/fiken-cli -l` lists what the repo offers. Swap `--agent claude-code`
for `--agent '*'` to install everywhere, or name the agent. Without Node, unpack the release
archive: it carries `skills/` next to the binary, and each folder can be copied into the
agent's skills directory by hand. Restart the agent session or gateway if a skill is not
visible immediately.

## From source

```bash
git clone https://github.com/nytafar/fiken-cli && cd fiken-cli
./install.sh --from-source            # go install ./cmd/fiken-cli -> $(go env GOPATH)/bin
./install.sh --from-source --with-mcp # also fiken-mcp
make install                          # the same, without the script
```

`make check` is the gate for changes to this tree: `go vet ./...`, `go test ./...`, and
`scripts/check-boundaries.sh`, which enforces the generated-vs-hand-authored marker on every
`.go` file, the `vatType` prefix-match lint, the `.printing-press-patches/` ledger, and the
claims the docs make about `drift` and `rollup`. Run it before every commit.

Working on the skills: `./install.sh --link-skills` symlinks them from the clone. The `skills`
CLI copies into Claude Code's directory, so a plain install needs a rerun per edit.
`FIKEN_SKILLS_ROOT` moves the link target.
