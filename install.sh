#!/bin/sh
# Downloaded scripts define the complete installer before executing any changes.
main() {
  set -eu
  umask 077
  VERSION='v0.1.0-preview.2'
  directory="$PWD/rootlens"
  address=''
  port=8443
  docker_monitoring=0
  offline=''
  address_set=0
  port_set=0
  docker_set=0
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --dir|--address|--port|--offline)
        [ "$#" -ge 2 ] || { echo "Missing value for $1" >&2; exit 1; }
        case "$1" in
          --dir) directory=$2 ;;
          --address) address=$2; address_set=1 ;;
          --port) port=$2; port_set=1 ;;
          --offline) offline=$2 ;;
        esac
        shift 2 ;;
      --docker) docker_monitoring=1; docker_set=1; shift ;;
      --help)
        echo 'Usage: sh install.sh [--dir PATH] [--address LAN_IPV4] [--port PORT] [--docker] [--offline BUNDLE.tar.gz]'
        echo 'Requires Linux, Docker Compose, and root. Existing installs are resumed, never upgraded or reset.'
        echo 'Offline mode requires SHA256SUMS beside the bundle and preloaded matching Docker images.'
        return 0 ;;
      *) echo "Unknown option: $1" >&2; exit 1 ;;
    esac
  done
  [ "$(uname -s)" = Linux ] || { echo 'The installer requires Linux.' >&2; exit 1; }
  case "$(uname -m)" in x86_64|aarch64|arm64) ;; *) echo 'Supported architectures: AMD64 and ARM64.' >&2; exit 1 ;; esac
  [ "$(id -u)" = 0 ] || { echo 'Run with sudo sh, or as root, to protect agent files for container UID 10001.' >&2; exit 1; }
  for tool in docker curl tar sha256sum awk mktemp sed chmod chown stat cut hostname; do
    command -v "$tool" >/dev/null 2>&1 || { echo "Required tool not found: $tool" >&2; exit 1; }
  done
  docker compose version >/dev/null
  endpoint=${DOCKER_HOST:-$(docker context inspect --format '{{.Endpoints.docker.Host}}')}
  case "$endpoint" in unix:///*) ;; *) echo 'Use a local Docker Unix socket; remote Docker hosts are not supported by this installer.' >&2; exit 1 ;; esac
  context_endpoint=$(docker context inspect --format '{{.Endpoints.docker.Host}}')
  case "$context_endpoint" in unix:///*) ;; *) echo 'Select a local Docker context before installing.' >&2; exit 1 ;; esac
  [ "$(docker info --format '{{.OSType}}')" = linux ] || { echo 'A running Linux Docker Engine is required.' >&2; exit 1; }
  if [ -z "$directory" ] || [ "$directory" = / ] || [ -L "$directory" ]; then
    echo 'Choose an installation directory, not a root path or symbolic link.' >&2; exit 1
  fi
  mkdir -p "$directory"
  directory=$(CDPATH='' cd -- "$directory" && pwd -P)
  [ "$directory" != / ] || { echo 'The filesystem root cannot be an installation directory.' >&2; exit 1; }
  [ "$(stat -c %u "$directory")" = 0 ] || { echo 'The installation directory must be owned by root.' >&2; exit 1; }
  marker="$directory/.rootlens-install"
  if [ -f "$marker" ]; then
    [ "$(sed -n '1p' "$marker")" = "$VERSION" ] || { echo 'An existing installation uses a different version. Follow the upgrade guide; this installer will not replace it.' >&2; exit 1; }
    saved_address=$(sed -n '2p' "$marker")
    saved_port=$(sed -n '3p' "$marker")
    saved_docker=$(sed -n '4p' "$marker")
    [ "$address_set" = 0 ] || [ "$address" = "$saved_address" ] || { echo 'Address conflicts with the existing installation.' >&2; exit 1; }
    [ "$port_set" = 0 ] || [ "$port" = "$saved_port" ] || { echo 'Port conflicts with the existing installation.' >&2; exit 1; }
    [ "$docker_set" = 0 ] || [ "$saved_docker" = 1 ] || { echo 'Enable additional collectors through the documented configuration procedure.' >&2; exit 1; }
    address=$saved_address
    port=$saved_port
    docker_monitoring=$saved_docker
  else
    [ -z "$(ls -A "$directory")" ] || { echo 'The directory is not empty. Choose a new folder; existing files will not be overwritten.' >&2; exit 1; }
    if [ -z "$address" ] && command -v ip >/dev/null 2>&1; then
      address=$(ip -4 route get 192.0.2.1 2>/dev/null | awk '{for(i=1;i<NF;i++)if($i=="src"){print $(i+1);exit}}')
    fi
    if [ -z "$address" ]; then
      address=$(hostname -I 2>/dev/null | awk '{print $1}')
    fi
  fi
  case "$address" in ''|*[!0-9.]*) echo 'Supply a local IPv4 address with --address.' >&2; exit 1 ;; esac
  printf '%s\n' "$address" | awk -F. 'NF!=4{exit 1} {for(i=1;i<=4;i++)if($i!~/^[0-9]+$/ || $i>255 || (length($i)>1 && substr($i,1,1)=="0"))exit 1; if($1<1 || $1>223)exit 1}' || { echo 'Supply a valid local IPv4 address with --address, for example --address 192.168.1.20.' >&2; exit 1; }
  case "$port" in ''|0*|*[!0-9]*) echo 'Invalid port.' >&2; exit 1 ;; esac
  if [ "${#port}" -gt 5 ] || [ "$port" -lt 1 ] || [ "$port" -gt 65535 ]; then
    echo 'Port must be between 1 and 65535.' >&2; exit 1
  fi
  case "$docker_monitoring" in 0|1) ;; *) echo 'Invalid installation marker.' >&2; exit 1 ;; esac
  chmod 700 "$directory"
  lock="$directory/.install-lock"
  mkdir "$lock" 2>/dev/null || { echo "An installer is already running. After an interrupted run, remove the empty lock directory: $lock" >&2; exit 1; }
  stage=$(mktemp -d)
  trap 'rm -rf "$stage"; rmdir "$lock" 2>/dev/null || true' EXIT
  trap 'exit 130' INT
  trap 'exit 143' HUP TERM
  project="rootlens-$(printf '%s' "$directory" | sha256sum | cut -c1-12)"
  compose() {
    case "$1" in
      run|up) action=$1; shift; set -- "$action" --pull never "$@" ;;
    esac
    if [ "$docker_monitoring" = 1 ]; then
      ROOTLENS_BIND_IP="$address" ROOTLENS_PORT="$port" docker compose --project-name "$project" --project-directory "$directory" -f "$directory/compose.yml" --profile docker "$@"
    else
      ROOTLENS_BIND_IP="$address" ROOTLENS_PORT="$port" docker compose --project-name "$project" --project-directory "$directory" -f "$directory/compose.yml" "$@"
    fi
  }
  if [ ! -f "$marker" ]; then
    bundle="rootlens-docker-$VERSION.tar.gz"
    if [ -n "$offline" ]; then
      cp "$offline" "$stage/$bundle"
      cp "$(dirname -- "$offline")/SHA256SUMS" "$stage/SHA256SUMS"
    else
      base="https://github.com/silkute/RootLens/releases/download/$VERSION"
      echo "Downloading RootLens $VERSION..."
      curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --connect-timeout 15 --max-time 180 "$base/$bundle" -o "$stage/$bundle"
      curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --connect-timeout 15 --max-time 60 "$base/SHA256SUMS" -o "$stage/SHA256SUMS"
    fi
    expected=$(awk -v file="$bundle" '$2==file{print $1}' "$stage/SHA256SUMS")
    [ "${#expected}" = 64 ] || { echo 'Missing or invalid bundle checksum.' >&2; exit 1; }
    actual=$(sha256sum "$stage/$bundle" | cut -d' ' -f1)
    [ "$actual" = "$expected" ] || { echo 'Bundle checksum mismatch. Installation stopped.' >&2; exit 1; }
    tar -xzf "$stage/$bundle" -C "$stage" rootlens/compose.yml rootlens/haproxy.cfg rootlens/agent.example.json rootlens/INSTALL.md rootlens/LICENSE rootlens/THIRD_PARTY_NOTICES.md
    for file in compose.yml haproxy.cfg agent.example.json INSTALL.md LICENSE THIRD_PARTY_NOTICES.md; do
      cp "$stage/rootlens/$file" "$directory/$file"
    done
    printf 'ROOTLENS_BIND_IP=%s\nROOTLENS_PORT=%s\n' "$address" "$port" > "$directory/.env"
    printf '%s\n%s\n%s\n%s\n' "$VERSION" "$address" "$port" "$docker_monitoring" > "$marker"
  fi
  compose config --quiet
  if [ -z "$offline" ]; then
    if [ "$docker_monitoring" = 1 ]; then compose pull hub agent docker-proxy; else compose pull hub agent; fi
  fi
  if [ ! -f "$directory/.installed" ]; then
    compose stop agent hub
    mkdir -p "$directory/agent"
    chown 10001:10001 "$directory/agent"
    chmod 700 "$directory/agent"
    if ! compose run --rm -T --entrypoint sh hub -c 'test -f /data/hub.json'; then
      compose run --rm -T hub init --data /data --origin "https://$address:$port" --tls-names "localhost,127.0.0.1,$address"
    fi
    host_name=$(hostname | cut -c1-100)
    if [ "$docker_monitoring" = 1 ]; then
      compose run --rm -T -v "$directory/agent:/etc/rootlens" hub local-agent --data /data --output /etc/rootlens --host-name "$host_name" --docker
    else
      compose run --rm -T -v "$directory/agent:/etc/rootlens" hub local-agent --data /data --output /etc/rootlens --host-name "$host_name"
    fi
    compose up -d hub
    compose cp hub:/data/ca.pem "$directory/ca.pem"
    compose cp hub:/data/setup.token "$directory/setup.token"
    chmod 600 "$directory/setup.token"
    ready=0
    attempt=0
    while [ "$attempt" -lt 60 ]; do
      attempt=$((attempt + 1))
      if curl --fail --silent --show-error --noproxy '*' --cacert "$directory/ca.pem" --connect-timeout 2 --max-time 3 "https://$address:$port/api/v1/me" -o /dev/null 2>/dev/null; then ready=1; break; fi
      sleep 1
    done
    [ "$ready" = 1 ] || { echo 'HTTPS startup failed. Check the address, port and system clock. Your data is preserved; rerun to resume.' >&2; exit 1; }
    if [ "$docker_monitoring" = 1 ]; then compose up -d docker-proxy agent; else compose up -d agent; fi
    ready=0
    attempt=0
    while [ "$attempt" -lt 30 ]; do
      attempt=$((attempt + 1))
      if compose run --rm -T -v "$directory/agent:/etc/rootlens:ro" hub local-agent --data /data --output /etc/rootlens --check >/dev/null 2>&1; then ready=1; break; fi
      sleep 2
    done
    [ "$ready" = 1 ] || { echo 'The agent has not submitted telemetry. Inspect compose logs in this folder, then rerun to resume; credentials were preserved.' >&2; exit 1; }
    compose stop agent
    compose run --rm -T -v "$directory/agent:/etc/rootlens" hub local-agent --data /data --output /etc/rootlens --finish
    compose up -d agent
    printf '%s\n' "$VERSION" > "$directory/.installed"
  else
    if [ "$docker_monitoring" = 1 ]; then compose up -d hub docker-proxy agent; else compose up -d hub agent; fi
  fi
  printf '\nRootLens is running: https://%s:%s\n' "$address" "$port"
  printf 'Trust the local CA certificate on your browser device: %s/ca.pem\n' "$directory"
  printf 'Open the dashboard; on first use, create your administrator account. Setup token: %s/setup.token\n' "$directory"
  printf 'Installation files and volumes are preserved on repeat runs. Guide: %s/INSTALL.md\n' "$directory"
  if [ ! -f "$directory/.account-notice" ] && ( : >/dev/tty ) 2>/dev/null; then
    { printf '\nRootLens one-time setup token (keep private): '; cat "$directory/setup.token"; } >/dev/tty
    touch "$directory/.account-notice"
  fi
}
main "$@"
