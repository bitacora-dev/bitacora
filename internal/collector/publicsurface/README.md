# publicsurface collector

Collects read-only signals for VPS hosts whose management surface is exposed to
the public internet.

Required capability:

- `public.exposed`

Metrics:

- `bitacora_public_ssh_failed_logins_total`
- `bitacora_public_fail2ban_jails_total`
- `bitacora_public_fail2ban_banned_total`
- `bitacora_public_firewall_rules_total`
- `bitacora_public_ovh_traffic_used_ratio`

Inputs are local files only:

- auth log: `/var/log/auth.log` or `/var/log/secure`
- fail2ban snapshot: `/var/lib/bitacora/public-surface/fail2ban.json`
- firewall rules snapshot: `/var/lib/bitacora/public-surface/firewall.rules`
- OVH traffic quota snapshot: `/var/lib/bitacora/public-surface/ovh-traffic.json`

The collector does not call `fail2ban-client`, `nft`, `iptables`, or the OVH API.
Operators may export those read-only snapshots through packaging or an external
timer without changing the agent into a control plane.
