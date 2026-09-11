#!/usr/bin/env bash
#
# Standalone installer for fiken-cli + the agent skills under skills/.
# No Printing Press required — just the Go toolchain.
#
#   ./install.sh                  # install fiken-cli + the agent skills
#   FIKEN_WITH_MCP=1 ./install.sh # also install the fiken-mcp server
#   FIKEN_SKILL_ONLY=1 ./install.sh   # only (re)install the agent skills
#   FIKEN_SKILL_AGENTS='*' ./install.sh   # install to every agent npx skills knows
#   FIKEN_SKILL_LINK=1 ./install.sh   # symlink the skills to this clone (development)
#   FIKEN_SKILLS_ROOT=/path ./install.sh  # target for the link/copy fallbacks
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

# Install the agent skills under skills/. `npx skills` owns the placement: it
# discovers every skill in this tree, symlinks them into each agent's directory
# (so edits in this clone are live), and can update or remove them later.
SKILL_AGENTS="${FIKEN_SKILL_AGENTS:-claude-code}"
SKILLS_ROOT="${FIKEN_SKILLS_ROOT:-$HOME/.claude/skills}"
if [ "${FIKEN_SKILL_LINK:-0}" = "1" ]; then
  # Development: point the agent at this clone so edits are live. `npx skills`
  # copies into Claude Code's directory, which would need a reinstall per edit.
  for skill in "$ROOT"/skills/*/; do
    name="$(basename "$skill")"
    rm -rf "${SKILLS_ROOT:?}/$name"
    mkdir -p "$SKILLS_ROOT"
    ln -s "${skill%/}" "$SKILLS_ROOT/$name"
    echo "Linked agent skill: $SKILLS_ROOT/$name -> ${skill%/}"
  done
elif command -v npx >/dev/null 2>&1; then
  npx --yes skills add "$ROOT" --skill '*' --agent "$SKILL_AGENTS" --global --yes
else
  # No Node: copy into Claude Code's skills directory so the install still works.
  echo "npx not found — copying skills to $SKILLS_ROOT (install Node for 'skills update')."
  for skill in "$ROOT"/skills/*/; do
    name="$(basename "$skill")"
    mkdir -p "$SKILLS_ROOT/$name"
    cp -R "$skill". "$SKILLS_ROOT/$name/"
    echo "Installed agent skill to: $SKILLS_ROOT/$name/"
  done
fi

echo ""
echo "Done."
echo "  Verify: fiken-cli --version"
echo "  Auth:   export FIKEN_API_TOKEN=<personal token>   (or: fiken-cli auth login)"
echo "  Try:    fiken-cli doctor"
