# RPM and AlmaLinux packaging

AlmaLinux support uses the same agent binary and systemd units as other Linux
hosts, plus RPM/dnf packaging and SELinux policy material.

Install flow expected for the package:

1. install `bitacora-agent` and helper binaries into `/usr/bin` and
   `/usr/libexec/bitacora`
2. install `packaging/systemd/*.service` and `*.timer`
3. run `packaging/scripts/provision-user.sh`
4. compile and load `bitacora-agent.te` into the host policy
5. enable `bitacora-agent.service` and helper timers

The capability manifest reports:

- `pkg.dnf` when the RPM database exists
- `sec.selinux` with `enforcing` or `permissive` when SELinux is mounted

Do not disable SELinux in packaging. If policy blocks a read-only source, update
the policy module and document the new access.
