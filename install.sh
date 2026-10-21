#!/bin/sh
# Downloaded scripts define the complete installer before executing any changes.
main() {
  set -eu
  umask 077
  VERSION='v0.1.0-preview.3'
  directory="$PWD/rootlens"
  address=''
  port=8443
  docker_monitoring=0
  offline=''
  address_set=0
  port_set=0
  docker_set=0
  non_interactive=0
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
      --non-interactive) non_interactive=1; shift ;;
      --help)
        echo 'Usage: sh install.sh [--dir PATH] [--address LAN_IPV4] [--port PORT] [--non-interactive] [--docker] [--offline BUNDLE.tar.gz]'
        echo 'Choose the HTTPS port at the terminal, or supply --port. Repeat runs retain the saved port unless changed.'
        echo 'Requires Linux, Docker Compose, and root. Existing service versions and data are preserved.'
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
  installed_version=$VERSION
  saved_port=''
  if [ -f "$marker" ]; then
    installed_version=$(sed -n '1p' "$marker")
    case "$installed_version" in "$VERSION"|v0.1.0-preview.2) ;; *) echo 'This installation version is not supported by this installer. Follow the upgrade guide.' >&2; exit 1 ;; esac
    saved_address=$(sed -n '2p' "$marker")
    saved_port=$(sed -n '3p' "$marker")
    saved_docker=$(sed -n '4p' "$marker")
    [ "$address_set" = 0 ] || [ "$address" = "$saved_address" ] || { echo 'Address conflicts with the existing installation.' >&2; exit 1; }
    [ "$docker_set" = 0 ] || [ "$saved_docker" = 1 ] || { echo 'Enable additional collectors through the documented configuration procedure.' >&2; exit 1; }
    address=$saved_address
    if [ "$port_set" = 0 ]; then port=$saved_port; fi
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
  valid_port() {
    case "$1" in ''|0*|*[!0-9]*) return 1 ;; esac
    [ "${#1}" -le 5 ] && [ "$1" -ge 1 ] && [ "$1" -le 65535 ]
  }
  pending="$directory/.port-change"
  if [ -f "$pending" ]; then
    pending_port=$(cat "$pending")
    if [ "$port_set" = 1 ] && [ "$port" != "$pending_port" ]; then
      echo "Resume the pending port change to $pending_port before selecting another port." >&2; exit 1
    fi
    port=$pending_port
  elif [ "$port_set" = 0 ] && [ "$non_interactive" = 0 ] && ( : <>/dev/tty ) 2>/dev/null; then
    while :; do
      printf 'Dashboard HTTPS port [%s]: ' "$port" >/dev/tty
      IFS= read -r selected_port </dev/tty || { echo 'Port selection cancelled.' >&2; exit 1; }
      selected_port=${selected_port:-$port}
      if valid_port "$selected_port"; then port=$selected_port; break; fi
      printf 'Enter a port from 1 to 65535, without leading zeroes.\n' >/dev/tty
    done
  fi
  valid_port "$port" || { echo 'Port must be from 1 to 65535, without leading zeroes.' >&2; exit 1; }
  origin="https://$address:$port"
  if [ "$port" = 443 ]; then origin="https://$address"; fi
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
    tar -xzf "$stage/$bundle" -C "$stage" rootlens/compose.yml rootlens/haproxy.cfg rootlens/agent.example.json rootlens/INSTALL.md rootlens/LICENSE rootlens/THIRD_PARTY_NOTICES.md rootlens/.env.example
    for file in compose.yml haproxy.cfg agent.example.json INSTALL.md LICENSE THIRD_PARTY_NOTICES.md .env.example; do
      cp "$stage/rootlens/$file" "$directory/$file"
    done
    printf 'ROOTLENS_BIND_IP=%s\nROOTLENS_PORT=%s\n' "$address" "$port" > "$directory/.env"
    printf '%s\n%s\n%s\n%s\n' "$VERSION" "$address" "$port" "$docker_monitoring" > "$marker"
  fi
  compose config --quiet
  if [ -z "$offline" ]; then
    if [ "$docker_monitoring" = 1 ]; then compose pull hub agent docker-proxy; else compose pull hub agent; fi
  fi
  helper_image="ghcr.io/silkute/rootlens-hub:$VERSION"
  reconfigure=0
  if [ -n "$saved_port" ]; then
    if [ "$saved_port" != "$port" ] || [ -f "$pending" ] || [ "$port_set" = 1 ]; then reconfigure=1; fi
  fi
  if [ -z "$saved_port" ] || [ "$reconfigure" = 1 ]; then
    if [ -z "$offline" ] && [ "$installed_version" != "$VERSION" ]; then docker pull "$helper_image"; fi
    # Docker checks the host-side binding, including ports below 1024. The probe
    # has no application listener and exits immediately, releasing the binding.
    if [ "$saved_port" != "$port" ]; then
      docker run --rm --pull never --read-only --cap-drop ALL --security-opt no-new-privileges:true --publish "$address:$port:8443" --entrypoint sh "$helper_image" -c ':' || {
        echo 'The selected address/port cannot be bound. Choose a free port with --port; existing services have not been stopped.' >&2; exit 1
      }
    fi
  fi
  if [ "$reconfigure" = 1 ]; then
    printf '%s\n' "$port" > "$pending.new"
    mv "$pending.new" "$pending"
    compose stop agent hub
    mkdir -p "$directory/agent"
    chown 10001:10001 "$directory/agent"
    chmod 700 "$directory/agent"
    docker run --rm --pull never --read-only --cap-drop ALL --security-opt no-new-privileges:true --network none \
      --mount "type=volume,source=${project}_hub-data,target=/data" \
      --mount "type=bind,source=$directory/agent,target=/etc/rootlens" \
      --entrypoint sh "$helper_image" -c 'if [ -f /data/hub.json ]; then exec rootlens set-port --data /data --output /etc/rootlens --port "$1"; fi' sh "$port"
    awk -v port="$port" 'BEGIN{found=0} /^ROOTLENS_PORT=/{print "ROOTLENS_PORT="port;found=1;next} {print} END{if(!found)print "ROOTLENS_PORT="port}' "$directory/.env" > "$directory/.env.new"
    mv "$directory/.env.new" "$directory/.env"
    printf '%s\n%s\n%s\n%s\n' "$installed_version" "$address" "$port" "$docker_monitoring" > "$marker.new"
    mv "$marker.new" "$marker"
    rm -f "$pending"
  fi
  if [ ! -f "$directory/.installed" ]; then
    compose stop agent hub
    mkdir -p "$directory/agent"
    chown 10001:10001 "$directory/agent"
    chmod 700 "$directory/agent"
    if ! compose run --rm -T --entrypoint sh hub -c 'test -f /data/hub.json'; then
      compose run --rm -T hub init --data /data --origin "$origin" --tls-names "localhost,127.0.0.1,$address"
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
      if curl --fail --silent --show-error --noproxy '*' --cacert "$directory/ca.pem" --connect-timeout 2 --max-time 3 "$origin/api/v1/me" -o /dev/null 2>/dev/null; then ready=1; break; fi
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
    printf '%s\n' "$installed_version" > "$directory/.installed"
  else
    if [ "$docker_monitoring" = 1 ]; then compose up -d hub docker-proxy agent; else compose up -d hub agent; fi
  fi
  if [ "$reconfigure" = 1 ]; then
    ready=0
    attempt=0
    while [ "$attempt" -lt 30 ]; do
      attempt=$((attempt + 1))
      if curl --fail --silent --show-error --noproxy '*' --cacert "$directory/ca.pem" --connect-timeout 2 --max-time 3 "$origin/api/v1/me" -o /dev/null 2>/dev/null; then ready=1; break; fi
      sleep 1
    done
    [ "$ready" = 1 ] || { echo 'The new HTTPS endpoint is not ready. Data is preserved; rerun with the same --port to resume.' >&2; exit 1; }
  fi
  printf '\nRootLens is running: %s\n' "$origin"
  printf 'Trust the local CA certificate on your browser device: %s/ca.pem\n' "$directory"
  printf 'Open the dashboard; on first use, create your administrator account. Setup token: %s/setup.token\n' "$directory"
  printf 'Installation files and volumes are preserved on repeat runs. Guide: %s/INSTALL.md\n' "$directory"
  if [ "$non_interactive" = 0 ] && [ ! -f "$directory/.account-notice" ] && ( : >/dev/tty ) 2>/dev/null; then
    { printf '\nRootLens one-time setup token (keep private): '; cat "$directory/setup.token"; } >/dev/tty
    touch "$directory/.account-notice"
  fi
}
main "$@"
