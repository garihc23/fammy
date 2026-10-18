#!/usr/bin/env bash
# Start Famyy's brain on this laptop: it listens for the device on port 8787.
# Keys and settings live in brain/.env (git-ignored); see brain/.env.example.
#   ./start-brain.sh            start it (Ctrl-C stops it cleanly)
# Before restarting a running brain, wait until nothing is in flight:
#   tools/brain-idle.sh 60 && <stop it, then start it again>
set -euo pipefail
cd "$(dirname "$0")/brain"
if [[ ! -f .env ]]; then
  echo "brain/.env is missing: copy brain/.env.example to brain/.env and fill in your keys" >&2
  exit 1
fi
command -v go >/dev/null || { echo "Go is not installed (https://go.dev/dl/)" >&2; exit 1; }
command -v claude >/dev/null || { echo "Claude Code (claude) is not installed or not on PATH" >&2; exit 1; }
grep -q '^CARTESIA_API_KEY=.' .env || echo "note: no CARTESIA_API_KEY in brain/.env, so Famyy can't hear or speak (text only)" >&2
exec go run ./cmd/femi-brain "$@"
