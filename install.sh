#!/usr/bin/env bash
#
# Standalone installer for fiken-cli + the `fiken` agent skill.
# No Printing Press required — just the Go toolchain.
#
#   ./install.sh                  # install fiken-cli + the agent skill
#   FIKEN_WITH_MCP=1 ./install.sh # also install the fiken-mcp server
#   FIKEN_SKILL_ONLY=1 ./install.sh   # only (re)install the agent skill
#   FIKEN_SKILL_DIR=/path ./install.sh   # install the skill elsewhere
#
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

WITH_MCP="${FIKEN_WITH_MCP:-0}"
SKILL_ONLY="${FIKEN_SKILL_ONLY:-0}"

if [ "$SKILL_ONLY" != "1" ]; then
  command -v go >/dev/null 2>&1 || { echo "error: Go toolchain not found (https://go.dev/dl/)"; exit 1; }
  echo "Installing fiken-cli (go install ./cmd/fiken-cli)..."
  go install ./cmd/fiken-cli
  if [ "$WITH_MCP" = "1" ]; then
    echo "Installing fiken-mcp..."
    go install ./cmd/fiken-mcp
  fi
  BIN_DIR="$(go env GOBIN)"; [ -z "$BIN_DIR" ] && BIN_DIR="$(go env GOPATH)/bin"
  echo "Installed binary to: $BIN_DIR"
  case ":$PATH:" in
    *":$BIN_DIR:"*) ;;
    *) echo "WARNING: $BIN_DIR is not on \$PATH — add it so the agent can run 'fiken-cli'." ;;
  esac
fi

# Install the agent skill (Claude Code + any agent that reads ~/.claude/skills).
SKILLS_ROOT="${FIKEN_SKILLS_ROOT:-$HOME/.claude/skills}"
for skill in "$ROOT"/skills/*/; do
  name="$(basename "$skill")"
  mkdir -p "$SKILLS_ROOT/$name"
  cp -R "$skill". "$SKILLS_ROOT/$name/"
  echo "Installed agent skill to: $SKILLS_ROOT/$name/"
done

echo ""
echo "Done."
echo "  Verify: fiken-cli --version"
echo "  Auth:   export FIKEN_API_TOKEN=<personal token>   (or: fiken-cli auth login)"
echo "  Try:    fiken-cli doctor"
