# internal/collector/users

System user inventory collector (ADR-0015): which accounts exist, and —
best-effort, from Samba's own static config — which shares each can read
or write.

- `parsePasswd` reads `/etc/passwd`, keeping accounts at or above
  `UID_MIN` (read from `/etc/login.defs`, defaulting to 1000) and
  excluding the conventional `nobody` UID (65534), which `UID_MIN` alone
  doesn't exclude.
- `parseSambaPermissions` reads `smb.conf`'s per-share `valid users` (read
  access) and `write list` (write access) directives into a
  `username -> shares` map.

NFS has no per-user permission model to cross-reference — it's host-based
(client IP/subnet), not user-based — so the read/write attrs only ever
come from Samba.

## What's NOT here

- **Full Samba permission resolution.** `valid users`/`write list` cover
  the common case; this doesn't resolve group membership (`@groupname`),
  `invalid users`, `admin users`, or `force user`. A user granted access
  only via a Unix group they belong to won't show up here.
- Password/shadow data, obviously — only the account name and UID.
