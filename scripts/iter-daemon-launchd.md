# Iter daemon launchd verification

The Mac app installs `dev.iter.IterDaemon` as a per-user LaunchAgent at:

```text
~/Library/LaunchAgents/dev.iter.IterDaemon.plist
```

The plist starts `/usr/local/bin/iter-daemon` at login and keeps it alive. It
binds the local IPC socket at:

```text
~/Library/Application Support/Iter/daemon.sock
```

Safe local verification:

```sh
scripts/install-iter-daemon.sh
launchctl print "gui/${UID}/dev.iter.IterDaemon"
test -S "${HOME}/Library/Application Support/Iter/daemon.sock"
scripts/uninstall-iter-daemon.sh
test ! -e "${HOME}/Library/Application Support/Iter/daemon.sock"
```

Manual release verification after reboot:

```sh
launchctl print "gui/${UID}/dev.iter.IterDaemon"
test -S "${HOME}/Library/Application Support/Iter/daemon.sock"
```

The uninstall script bootouts only `dev.iter.IterDaemon`, removes only its plist,
and refuses to remove the socket path if it is not a Unix socket.
