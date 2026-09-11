#!/usr/bin/env bash
#
# fiken-cli installer. Downloads the latest GitHub release binary for this
# OS/arch, verifies it against checksums.txt, and puts it on your PATH.
#
#   curl -fsSL https://raw.githubusercontent.com/nytafar/fiken-cli/main/install.sh | bash
#   curl -fsSL .../install.sh | bash -s -- --with-mcp        # also fiken-mcp
#   curl -fsSL .../install.sh | bash -s -- --version v2.1.0  # pin a release
#
# The agent skills are a separate, one-line step through `npx skills` (default
# for most people, works without this script):
#
#   npx skills add nytafar/fiken-cli --skill '*' --agent claude-code --global
#
# Pass --skills to have this script run that for you.
#
# Options:
#   --with-mcp          also install the fiken-mcp server binary
#   --version <tag>     release tag to install (default: latest)
#   --bin-dir <dir>     where to put the binaries (default: $FIKEN_BIN_DIR or ~/.local/bin)
#   --skills            also install the agent skills with `npx skills add` (claude-code)
#   --agent <name>      agent target for --skills; '*' for every agent npx skills knows
#   --from-source       build with `go install` from this clone instead of downloading
#   --link-skills       development: symlink skills/ from this clone into ~/.claude/skills
#   -h, --help          this text
#
set -euo pipefail

REPO="nytafar/fiken-cli"
WITH_MCP=0
VERSION=""
BIN_DIR="${FIKEN_BIN_DIR:-$HOME/.local/bin}"
SKILLS=0
SKILL_AGENT="claude-code"
FROM_SOURCE=0
LINK_SKILLS=0
SKILLS_ROOT="${FIKEN_SKILLS_ROOT:-$HOME/.claude/skills}"

usage() {
  cat <<'USAGE'
fiken-cli installer: downloads the latest GitHub release, verifies it, puts it on PATH.

  curl -fsSL https://raw.githubusercontent.com/nytafar/fiken-cli/main/install.sh | bash
  curl -fsSL .../install.sh | bash -s -- --with-mcp          # also fiken-mcp
  npx skills add nytafar/fiken-cli --skill '*' --agent claude-code --global   # the skills

Options:
  --with-mcp          also install the fiken-mcp server binary
  --version <tag>     release tag to install (default: latest)
  --bin-dir <dir>     where to put the binaries (default: $FIKEN_BIN_DIR or ~/.local/bin)
  --skills            also install the agent skills with `npx skills add` (claude-code)
  --agent <name>      agent target for --skills; '*' for every agent npx skills knows
  --from-source       build with `go install` from this clone instead of downloading
  --link-skills       development: symlink skills/ from this clone into ~/.claude/skills
  -h, --help          this text
USAGE
}

while [ $# -gt 0 ]; do
  case "$1" in
    --with-mcp) WITH_MCP=1 ;;
    --version) VERSION="$2"; shift ;;
    --version=*) VERSION="${1#*=}" ;;
    --bin-dir) BIN_DIR="$2"; shift ;;
    --bin-dir=*) BIN_DIR="${1#*=}" ;;
    --skills) SKILLS=1 ;;
    --agent) SKILL_AGENT="$2"; shift ;;
    --agent=*) SKILL_AGENT="${1#*=}" ;;
    --from-source) FROM_SOURCE=1 ;;
    --link-skills) LINK_SKILLS=1 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "error: unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

# The clone root, if this script is running from one (empty when piped).
ROOT=""
if [ -n "${BASH_SOURCE[0]:-}" ] && [ -f "${BASH_SOURCE[0]}" ]; then
  ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  [ -f "$ROOT/go.mod" ] && grep -q '^module fiken-cli' "$ROOT/go.mod" || ROOT=""
fi
need_clone() {
  [ -n "$ROOT" ] || { echo "error: $1 needs a clone of $REPO (run ./install.sh from inside it)" >&2; exit 2; }
}

install_from_source() {
  need_clone "--from-source"
  command -v go >/dev/null 2>&1 || { echo "error: Go toolchain not found (https://go.dev/dl/)" >&2; exit 1; }
  echo "Building fiken-cli from source (go install ./cmd/fiken-cli)..."
  (cd "$ROOT" && go install ./cmd/fiken-cli)
  if [ "$WITH_MCP" = 1 ]; then
    echo "Building fiken-mcp..."
    (cd "$ROOT" && go install ./cmd/fiken-mcp)
  fi
  BIN_DIR="$(go env GOBIN)"; [ -z "$BIN_DIR" ] && BIN_DIR="$(go env GOPATH)/bin"
}

install_from_release() {
  command -v curl >/dev/null 2>&1 || { echo "error: curl is required" >&2; exit 1; }
  command -v tar >/dev/null 2>&1 || { echo "error: tar is required" >&2; exit 1; }

  local os arch
  case "$(uname -s)" in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) echo "error: unsupported OS $(uname -s). Download the archive by hand from https://github.com/$REPO/releases" >&2; exit 1 ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64) arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    *) echo "error: unsupported architecture $(uname -m)" >&2; exit 1 ;;
  esac

  if [ -z "$VERSION" ]; then
    VERSION="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)"
    [ -n "$VERSION" ] || { echo "error: could not resolve the latest release of $REPO" >&2; exit 1; }
  fi
  local ver="${VERSION#v}"
  local asset="fiken-cli_${ver}_${os}_${arch}.tar.gz"
  local base="https://github.com/$REPO/releases/download/v${ver}"

  TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT; local tmp="$TMP"
  echo "Downloading fiken-cli v${ver} (${os}/${arch})..."
  curl -fsSL -o "$tmp/$asset" "$base/$asset"
  curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt"
  (cd "$tmp" && grep " $asset\$" checksums.txt > sum.txt && \
    { command -v sha256sum >/dev/null 2>&1 && sha256sum -c --quiet sum.txt || shasum -a 256 -c --quiet sum.txt; }) \
    || { echo "error: checksum mismatch for $asset" >&2; exit 1; }
  tar -xzf "$tmp/$asset" -C "$tmp"

  mkdir -p "$BIN_DIR"
  install -m 0755 "$tmp/fiken-cli" "$BIN_DIR/fiken-cli"
  echo "Installed $BIN_DIR/fiken-cli"
  if [ "$WITH_MCP" = 1 ]; then
    install -m 0755 "$tmp/fiken-mcp" "$BIN_DIR/fiken-mcp"
    echo "Installed $BIN_DIR/fiken-mcp"
  fi
}

install_skills() {
  if [ "$LINK_SKILLS" = 1 ]; then
    need_clone "--link-skills"
    mkdir -p "$SKILLS_ROOT"
    for skill in "$ROOT"/skills/*/; do
      name="$(basename "$skill")"
      rm -rf "${SKILLS_ROOT:?}/$name"
      ln -s "${skill%/}" "$SKILLS_ROOT/$name"
      echo "Linked agent skill: $SKILLS_ROOT/$name -> ${skill%/}"
    done
    return
  fi
  command -v npx >/dev/null 2>&1 || { echo "error: --skills needs Node (npx). Or copy skills/ from a release archive into $SKILLS_ROOT." >&2; exit 1; }
  local src="$REPO"; [ -n "$ROOT" ] && src="$ROOT"
  npx --yes skills add "$src" --skill '*' --agent "$SKILL_AGENT" --global --yes
}

if [ "$FROM_SOURCE" = 1 ]; then install_from_source; else install_from_release; fi
if [ "$SKILLS" = 1 ] || [ "$LINK_SKILLS" = 1 ]; then install_skills; fi

case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) echo "WARNING: $BIN_DIR is not on \$PATH — add it so the agent can run 'fiken-cli'." ;;
esac

echo ""
echo "Done."
echo "  Verify: fiken-cli --version"
echo "  Auth:   export FIKEN_API_TOKEN=<personal token>   (or: fiken-cli auth login)"
echo "  Try:    fiken-cli doctor"
if [ "$SKILLS" != 1 ] && [ "$LINK_SKILLS" != 1 ]; then
  echo "  Skills: npx skills add $REPO --skill '*' --agent claude-code --global"
fi
