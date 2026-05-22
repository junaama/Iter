#!/usr/bin/env bash
set -euo pipefail

LABEL="dev.iter.IterDaemon"
PLIST_PATH="${HOME}/Library/LaunchAgents/${LABEL}.plist"
APP_SUPPORT_DIR="${HOME}/Library/Application Support/Iter"
SOCKET_PATH="${APP_SUPPORT_DIR}/daemon.sock"
BINARY_PATH="${ITER_DAEMON_BINARY:-/usr/local/bin/iter-daemon}"
LOG_DIR="${HOME}/Library/Logs/Iter"

mkdir -p "$(dirname "${PLIST_PATH}")" "${APP_SUPPORT_DIR}" "${LOG_DIR}"

tmp_plist="$(mktemp "${TMPDIR:-/tmp}/iter-daemon.XXXXXX.plist")"
trap 'rm -f "${tmp_plist}"' EXIT

cat >"${tmp_plist}" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>${LABEL}</string>
  <key>ProgramArguments</key>
  <array>
    <string>${BINARY_PATH}</string>
    <string>-socket</string>
    <string>${SOCKET_PATH}</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>${LOG_DIR}/iter-daemon.log</string>
  <key>StandardErrorPath</key>
  <string>${LOG_DIR}/iter-daemon.err.log</string>
  <key>WorkingDirectory</key>
  <string>${APP_SUPPORT_DIR}</string>
</dict>
</plist>
PLIST

plutil -lint "${tmp_plist}" >/dev/null
install -m 0644 "${tmp_plist}" "${PLIST_PATH}"

launchctl bootout "gui/${UID}/${LABEL}" >/dev/null 2>&1 || true
launchctl bootstrap "gui/${UID}" "${PLIST_PATH}"
launchctl enable "gui/${UID}/${LABEL}"
launchctl kickstart -k "gui/${UID}/${LABEL}" >/dev/null 2>&1 || true

echo "installed ${LABEL} at ${PLIST_PATH}"
