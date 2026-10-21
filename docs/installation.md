# Installation and operations

## Binary installation

Build as described in the README, or verify checksums and signatures on published
artifacts. There are no published release URLs in this initial local repository.

```sh
rootlens init --data /var/lib/rootlens --origin https://nas.example:8443 --tls-names nas.example,192.168.1.20
rootlens serve --data /var/lib/rootlens --listen 192.168.1.20:8443
```

Run as a dedicated `rootlens` account that owns the private data directory. The
example systemd files assume binaries in `/usr/local/bin`. Initialize as that account.
Keep keys and tokens owner-readable only; on Windows set directory ACLs explicitly.

Trust the generated `ca.pem` in the browser/device trust store and copy only that
public CA certificate to agents. Do not bypass certificate warnings. Read
`setup.token` locally to create the administrator through the UI. A second setup is
rejected. Remove the setup-token file after completing setup; the database prevents
reuse regardless. The server certificate lasts one year; replace `server.pem` and
`server.key` with a properly signed renewal before expiry. Restart the hub to reload.

The CA private key is not required for normal serving and should be kept offline
after initialization. Operator-supplied certificate/key files may replace the server
files, provided their names and trust chain match the configured origin.

## Compose

On a modern Linux machine, from the repository:

```sh
docker compose -f deploy/compose.yml build
docker compose -f deploy/compose.yml run --rm hub init --data /data --origin https://localhost:8443 --tls-names localhost,127.0.0.1
docker compose -f deploy/compose.yml up -d hub
```

The port is loopback-bound by default. For another machine, explicitly configure the
publish address, canonical origin and certificate names; do not merely change one.
Copy `ca.pem` out of the hub volume and read `setup.token` locally. Complete account
setup in the browser, add a system, and download its one-time enrollment token.

Create `deploy/agent/agent.json` from `deploy/agent.example.json`. Set the returned
host ID. Place `ca.pem` and `enrollment.token` in the same directory. Ensure the
container UID 10001 can read these files; keep enrollment tokens private. The
generated long-lived agent credential is persisted in the separate `agent-state`
volume before enrollment is attempted.

```sh
docker compose -f deploy/compose.yml up -d agent
```

Agent networking shares the host namespace explicitly. proc, sysfs and cgroups are
read-only mounts. No host root filesystem is mounted. To monitor filesystem capacity,
bind only the desired local mount read-only and list its container-visible path in
`filesystems`. Do not use network mounts.

After successful enrollment, remove `enrollment_file` from the agent configuration
and delete the temporary enrollment token. The persistent credential remains in
`/state/agent.token`. A lost enrollment response is recoverable using that credential.

## Optional Docker monitoring

Set `docker_socket` to `/run/rootlens/docker.sock`, then:

```sh
docker compose -f deploy/compose.yml --profile docker up -d docker-proxy agent
```

The proxy publishes no network port and only shares a dedicated Unix socket volume.
It has the real Docker socket and is therefore trusted. Its allowlist rejects writes,
logs, file archives, export, exec and attach. A read-only mount alone does not enforce
this policy. Rootless Docker and arbitrary vendor layouts are not qualified yet.

A standalone agent may use a direct local Docker socket if the operator knowingly
accepts its power and grants access. Do not add the agent to the Docker group without
understanding that this is effectively an administrative grant. No such grant is
performed by RootLens.

## Agent configuration

For a native Linux agent, use `/proc`, `/sys` and `/sys/fs/cgroup` instead of the
container mount prefixes. `sampling_seconds` accepts 5, 10 or 15. `hub` must be HTTPS;
redirects and untrusted certificates are rejected. Token paths and local filesystem
paths are configured only on the agent.

`collect_processes: true` enables current CPU/RSS leaders at a fifteen-second cadence.
This is optional because process names and PIDs are sensitive operational information.
Command lines and environments are not read, and the hub does not retain process history.

Credential revocation is immediate for new requests. Rotation creates a new token
and revokes the previous one; replace the local token file and restart the agent.
Use Settings to revoke existing access before re-enrolling a replacement agent.

## Reverse proxies

Keep TLS on the upstream connection. Configure `hub.json` with the external canonical
origin and configure the proxy to preserve browser Origin and cookies. RootLens does
not trust forwarded identity or forwarded-client-IP headers. Authentication rate
limits therefore apply to the proxy's source address unless the proxy also enforces
its own limits. Plain HTTP production serving is not supported.

## Backup, restore and upgrade

```sh
rootlens backup --data /var/lib/rootlens --output /safe-backups/rootlens-2026-10-21.db
```

The destination must not exist. This uses SQLite `VACUUM INTO`; reserve enough local
space for the complete backup. Separately preserve TLS keys, CA trust material and
configuration. Treat backups as sensitive host information.

Before an upgrade, take a backup and stop the old hub. Start the new binary against
the same data directory. A database with a newer schema is refused. Version 1 is the
only migration currently defined; future migrations must back up first and run in a
transaction. Downgrade by restoring the prior backup while the hub is stopped, not
by editing schema-version numbers.

To restore, stop the hub, preserve the old data directory, place the verified backup
at `rootlens.db` in a fresh private directory, restore the corresponding TLS/config
files, and restart. Do not copy stale `-wal` or `-shm` files beside the restored backup.

An offline deployment needs the binaries or preloaded images and local CA trust.
Runtime uses no CDNs, external APIs, analytics, update checks or font downloads.
