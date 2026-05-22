#!/usr/bin/env bash
set -euo pipefail

LABEL="dev.iter.IterDaemon"
PLIST_PATH="${HOME}/Library/LaunchAgents/${LABEL}.plist"
SOCKET_PATH="${HOME}/Library/Application Support/Iter/daemon.sock"

launchctl bootout "gui/${UID}/${LABEL}" >/dev/null 2>&1 || true
rm -f "${PLIST_PATH}"

if [[ -S "${SOCKET_PATH}" || ! -e "${SOCKET_PATH}" ]]; then
  rm -f "${SOCKET_PATH}"
else
  echo "refusing to remove non-socket at ${SOCKET_PATH}" >&2
  exit 1
fi

echo "uninstalled ${LABEL}"
