#!/usr/bin/env bash
# Wait until the brain has had nothing in flight (no recording, no Claude turn, no Fo errand,
# no reply being spoken) for QUIET seconds, so a restart loses nothing.
#   tools/brain-idle.sh [QUIET=60]  && <restart the brain>
# A brain that isn't running counts as idle.
quiet=${1:-60}
calm=$(date +%s)
while :; do
  status=$(curl -s --max-time 2 http://127.0.0.1:8788/status || true)
  if [[ "$status" == *'"busy":true'* ]]; then
    calm=$(date +%s)
  fi
  (( $(date +%s) - calm >= quiet )) && break
  sleep 2
done
echo "brain idle for ${quiet}s"
