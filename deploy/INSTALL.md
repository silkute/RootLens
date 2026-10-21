# RootLens Docker preview

This bundle contains everything needed for a source-free Docker installation on
modern Linux AMD64 or ARM64. Docker Engine and the Docker Compose plugin are
required. This is a development preview; TerraMaster models and TOS versions have
not yet been qualified. Missing PSI or cgroup capabilities are shown in the app.

## 1. Download and pull

Extract the release ZIP into a private local folder on the machine being monitored.
On TerraMaster, use a local folder through File Manager and import `compose.yml`
as a Docker Manager project. Keep all bundle files together. These shell commands
are the equivalent Docker Compose workflow; run them in the extracted folder.

Create `.env` with your NAS's LAN IP, for example:

```text
ROOTLENS_BIND_IP=192.168.1.20
```

This publishes port 8443 on that address. Without it, the dashboard is accessible
only from the machine itself. Do not forward this port through your router.

```sh
docker compose pull hub agent
```

The images are version-pinned. No compiler, Git checkout or Docker image build is
needed. Do not replace the version with `latest`.

## 2. Initialize HTTPS and create the account

Replace `192.168.1.20` below with the same NAS IP used in `.env`:

```sh
docker compose run --rm hub init --data /data --origin https://192.168.1.20:8443 --tls-names localhost,127.0.0.1,192.168.1.20
docker compose up -d hub
mkdir -p agent
docker compose cp hub:/data/ca.pem ./agent/ca.pem
docker compose cp hub:/data/setup.token ./setup.token
```

Install the public `agent/ca.pem` certificate in your browser/device trust store.
Open `https://192.168.1.20:8443`, enter the secret from `setup.token`, and create the
administrator account. Keep tokens private and remove `setup.token` after setup.
Never copy the CA private key to the agent or bypass TLS verification.

## 3. Enroll the agent

In Settings, add a host and create an enrollment token. It expires in ten minutes.
Copy `agent.example.json` to `agent/agent.json` and set:

- `hub` to `https://192.168.1.20:8443` using your actual IP.
- `host_id` to the new host ID shown in Settings.

Save the token, without quotes, as `agent/enrollment.token`. The container runs as
UID 10001: it needs read access to the agent files. Restrict enrollment-token access
using file ownership/permissions or NAS ACLs. Do not grant access to all NAS users.

```sh
docker compose up -d agent
docker compose logs --tail 30 agent
```

After enrollment succeeds, delete `agent/enrollment.token` and remove
`enrollment_file` from `agent/agent.json`. Its persistent credential is kept in
the `agent-state` Docker volume. Do not delete that volume during upgrades.

## Optional Docker container monitoring

Set `docker_socket` to `/run/rootlens/docker.sock` in `agent/agent.json`, then:

```sh
docker compose --profile docker pull
docker compose --profile docker up -d
```

The profile uses the bundled restricted proxy. It publishes no proxy port, but
the proxy itself holds the real Docker socket and must be trusted. Do not mount
the Docker socket into the hub or add privileged mode to either container.

## Backups and upgrades

Keep the hub and agent volumes on local storage. Back up the database through
`rootlens backup`, not by copying the live database file. Preserve TLS/config files
separately. Before upgrading, back up, stop the stack, replace the versioned bundle,
then run `docker compose pull` and `docker compose up -d`. Keep your `.env`, `agent/`
directory and named volumes. Never run `down --volumes` against a retained install.

Full backup, restore, TLS renewal and permissions guidance:
https://github.com/silkute/RootLens/blob/main/docs/installation.md
