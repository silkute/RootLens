# Installation and operations

## One-command installation

Install Docker Engine and the Docker Compose plugin first. On TerraMaster, install
Docker Manager and enable SSH to run this command as a Docker administrator. The
installer requires root for private file ownership, Linux AMD64/ARM64, and a local
Docker Unix socket. It does not install system packages or change firewall rules.

```sh
curl -fsSL https://raw.githubusercontent.com/silkute/RootLens/main/install.sh | sudo sh
```

Inspect the script before executing it if desired. The script pins its release,
downloads over verified HTTPS, and checks the bundle's SHA-256 against the release
manifest. It initializes TLS, prepares a host-bound enrollment, starts the hub and
agent, and waits for fresh telemetry. It removes the consumed enrollment token.
The administrator account is still created through the browser; its password is
never passed to the shell. Trust the printed CA certificate on that device first.
The setup token is displayed only through an interactive terminal and retained in
the installation's root-readable `setup.token`; it is not placed in a URL or logs.

Default directory: `./rootlens`; HTTPS port: selected at the terminal, with 8443 as
the default on first install and the saved port on repeat runs. Press Enter to keep
that default, or enter another port from 1 to 65535. Prompts read `/dev/tty`, so they
work with the piped command above. `--port 9443` skips the port prompt; add
`--non-interactive` to disable terminal interaction entirely. Without a terminal,
the installer uses the supplied port or the default/saved value.

Address: the IPv4 source selected by
the local default route, falling back to the first `hostname -I` address. No packet
is sent to the route-lookup destination. On hosts with multiple interfaces, specify
the intended LAN address. Use a dedicated empty folder on local NAS storage:

```sh
curl -fsSL https://raw.githubusercontent.com/silkute/RootLens/main/install.sh | sudo sh -s -- --dir /path/to/rootlens --address 192.168.1.20 --port 9443
```

Add `--docker` only when opting into container monitoring through the restricted
proxy. By default the proxy is not started and the agent receives no Docker socket.
The proxy retains powerful access to the real Docker socket; see the section below.

Each installation uses a distinct Compose project derived from its directory.
Repeat runs retain the installed service versions, accounts, CA, host identity and
agent credentials. To change the public port, rerun with the original `--dir` and
select the new port (or supply `--port`). The installer checks the binding before
stopping services, then updates `.env`, the hub origin and the local agent URL
together. Other settings, including a custom internal listener, are retained.
Address/profile changes are still refused. Preview.2 installs can change ports
using the current configuration helper without upgrading their service images.
An existing nonempty directory without an installer marker is left untouched. This
command is not an upgrade or reset command; follow the backup/upgrade procedure.
After an interruption, rerun with the same directory and options. If the process was
forcibly killed, first ensure it stopped, then remove its empty `.install-lock`
directory. A revoked agent is not silently granted access again after account setup.
Interrupted port changes leave a private `.port-change` marker; rerun to complete
that same port before selecting another. Certificates do not depend on the port,
so changing only the port does not replace the CA. Additional remote agents must
have their locally configured `hub` URL updated by their operator. Port 443 uses
the canonical URL `https://HOST`, matching the browser's Origin header.

For staged, offline installation, download the release's `install.sh`, tar.gz bundle
and `SHA256SUMS`, and preload the matching images using `docker load`. Put the
checksum manifest beside the bundle, then run:

```sh
sudo sh install.sh --dir /path/to/rootlens --address 192.168.1.20 --port 9443 --non-interactive --offline /path/to/rootlens-docker-v0.1.0-preview.3.tar.gz
```

Offline mode never pulls images; `--docker` additionally requires the pinned proxy
image to be preloaded. Native ARM64 and TerraMaster device qualification remain
outstanding; a Linux CI install does not qualify every NAS/kernel combination.
An offline port change for an older install also requires the current release's
hub image as its configuration helper; the existing service images remain pinned.

## Binary installation

Build as described in the README, or verify checksums and signatures on published
artifacts from [GitHub Releases](https://github.com/silkute/RootLens/releases).

```sh
ROOTLENS_PORT=9443
rootlens init --data /var/lib/rootlens --origin "https://nas.example:$ROOTLENS_PORT" --tls-names nas.example,192.168.1.20
rootlens serve --data /var/lib/rootlens --listen "192.168.1.20:$ROOTLENS_PORT"
```

Run as a dedicated `rootlens` account that owns the private data directory. The
example systemd files assume binaries in `/usr/local/bin`. Initialize as that account.
Keep keys and tokens owner-readable only; on Windows set directory ACLs explicitly.

Native serving also accepts `ROOTLENS_LISTEN=192.168.1.20:9443`; an explicit
`--listen` takes precedence. For systemd, copy `deploy/hub.env.example` to
`/etc/rootlens/hub.env` and edit that listener. It defaults to loopback port 8443
when the optional environment file is absent. Non-root services normally use an
unprivileged listener; a reverse proxy can expose standard HTTPS port 443.

With the hub and local agent stopped, change saved public endpoints with:

```sh
rootlens set-port --data /var/lib/rootlens --output /etc/rootlens/agent --port 10443
```

`--output` is optional for hub-only installations. Update the native listener or
reverse-proxy mapping as appropriate, update remote agent URLs, then restart. This
command preserves certificates, account/session data, identities and credentials.

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

For installation without a source build, download the Docker ZIP from
[Releases](https://github.com/silkute/RootLens/releases) and follow its bundled
`INSTALL.md`. It includes a pull-only Compose file, agent template and proxy policy.
Images are `ghcr.io/silkute/rootlens-hub` and `ghcr.io/silkute/rootlens-agent`, with
matching version tags and Linux AMD64/ARM64 manifests. The install Compose defaults
to loopback; set `ROOTLENS_BIND_IP` in `.env` for LAN access, matching the HTTPS origin
and certificate names. Docker Manager can import the extracted Compose project;
Set `ROOTLENS_PORT` in that same file to choose the public HTTPS port. Both the
source and pull-only Compose files support `ROOTLENS_INTERNAL_PORT` (default 8443)
for the container listener and target mapping. Public origins and agent URLs use
the public port; the two ports may differ. Initialization and enrollment still
require the documented steps. No TerraMaster
hardware or TOS release is qualified by an image build alone.

`deploy/compose.install.yml` is the repository copy of this pull-only configuration.
`deploy/compose.yml` below is retained for developers building from source.

On a modern Linux machine, from the repository, copy `deploy/.env.example` to
`deploy/.env`, set the desired bind address and port, and use that file explicitly.
This example uses the default loopback address with public port 9443:

```sh
docker compose --env-file deploy/.env -f deploy/compose.yml build
docker compose --env-file deploy/.env -f deploy/compose.yml run --rm hub init --data /data --origin https://localhost:9443 --tls-names localhost,127.0.0.1
docker compose --env-file deploy/.env -f deploy/compose.yml up -d hub
```

The port is loopback-bound by default. For another machine, explicitly configure the
publish address, canonical origin and certificate names; do not merely change one.
Copy `ca.pem` out of the hub volume and read `setup.token` locally. Complete account
setup in the browser, add a system, and download its one-time enrollment token.

Create `deploy/agent/agent.json` from `deploy/agent.example.json`. Set the returned
host ID. Place `ca.pem` and `enrollment.token` in the same directory. Set the agent's
`hub` URL to the chosen public port (9443 in this example), and ensure container UID
10001 can read these files. Keep enrollment tokens private. The
generated long-lived agent credential is persisted in the separate `agent-state`
volume before enrollment is attempted.

```sh
docker compose --env-file deploy/.env -f deploy/compose.yml up -d agent
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
docker compose --env-file deploy/.env -f deploy/compose.yml --profile docker up -d docker-proxy agent
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
