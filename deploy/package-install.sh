#!/bin/sh
# Build a portable, version-pinned installation bundle from repository sources.
set -eu
version=${1:?usage: package-install.sh vVERSION OUTPUT_DIRECTORY}
output=${2:?usage: package-install.sh vVERSION OUTPUT_DIRECTORY}
case "$version" in
  v[0-9]*) ;;
  *) echo 'Version must start with v and a number' >&2; exit 1 ;;
esac
case "$version" in
  *[!A-Za-z0-9._-]*) echo 'Invalid version characters' >&2; exit 1 ;;
esac
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
mkdir -p "$output"
output=$(CDPATH= cd -- "$output" && pwd)
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT HUP INT TERM
mkdir "$stage/rootlens"
sed "s|ghcr.io/silkute/rootlens-hub:[^[:space:]]*|ghcr.io/silkute/rootlens-hub:$version|; s|ghcr.io/silkute/rootlens-agent:[^[:space:]]*|ghcr.io/silkute/rootlens-agent:$version|" \
  "$root/deploy/compose.install.yml" > "$stage/rootlens/compose.yml"
cp "$root/deploy/haproxy.cfg" "$root/deploy/agent.example.json" "$root/deploy/INSTALL.md" "$stage/rootlens/"
cp "$root/LICENSE" "$root/THIRD_PARTY_NOTICES.md" "$stage/rootlens/"
(cd "$stage" && zip -qr "$output/rootlens-docker-$version.zip" rootlens)
tar -czf "$output/rootlens-docker-$version.tar.gz" -C "$stage" rootlens
