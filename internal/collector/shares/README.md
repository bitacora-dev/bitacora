# internal/collector/shares

Share inventory collector (ADR-0015): reads SMB (`smb.conf`) and NFS
(`/etc/exports`) static configuration and combines both into one
`Inventory` of kind `share`. Never queries the running service for live
connection counts — that would need `exec` (ADR-0012), and answers a
different question ("who's connected right now") than this one ("what
shares exist").

- `parseSambaConf` reads `smb.conf`'s `[section]`/`key = value` format,
  skipping Samba's own meta-sections (`[global]`, `[homes]`, `[printers]`,
  `[print$]`). Per share: `path`, `mode` (public/private, from
  `public`/`guest ok`), `writable` (from `read only`/`writeable`).
- `parseExports` reads `/etc/exports`: one export per line, path first,
  then `client(options)` entries. `mode` is public if any client is `*`;
  `writable` if any options list contains `rw`.

`Requires()` returns nil deliberately — see the doc comment on it in
`shares.go`. The Registry's capability check is AND-only ("every listed
capability must be present"), but this collector needs "at least one of
SMB/NFS", which that can't express; it self-gates by trying both files and
emitting whatever combination actually parses, including an empty (not
missing) snapshot when neither exists.

## What's NOT here

- Live connection state (`smbstatus`, active NFS mounts) — explicitly the
  non-goal per ADR-0015's own alternatives-considered.
- Group-based Samba permissions, `force user`, `admin users` — see
  `internal/collector/users`' README for the same caveat on the
  permission-resolution side.
