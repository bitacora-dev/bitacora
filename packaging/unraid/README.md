# UnRaid plugin packaging

UnRaid does not use systemd and rebuilds its root filesystem on boot. The agent
must live under `/boot/config/plugins/bitacora-agent/` and be controlled through
an rc.d script.

Files:

- `rc.bitacora-agent` starts, stops, restarts, and reports status for the agent.
- `bitacora-agent.plg` is the plugin manifest skeleton for emhttp/plugin
  installation.

The capability manifest detects UnRaid through `/proc/mdcmd` and syslog through
`/var/log/syslog`, so collectors can depend on `storage.unraid_array`,
`logs.syslogfile`, and `public.exposed` without checking distro names.
