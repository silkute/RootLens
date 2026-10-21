#!/bin/sh
# Disposable Linux integration: real Docker, HTTPS, enrollment and first telemetry.
set -eu
bundle=${1:?usage: smoke.sh /absolute/path/to/bundle.tar.gz}
test_root=$(mktemp -d /tmp/rootlens-install-test.XXXXXX)
directory="$test_root/install"
project="rootlens-$(printf '%s' "$directory" | sha256sum | cut -c1-12)"
compose() {
  docker compose --project-name "$project" --project-directory "$directory" -f "$directory/compose.yml" "$@"
}
cleanup() {
  if [ -f "$directory/compose.yml" ]; then compose --profile docker down --volumes >/dev/null 2>&1 || true; fi
  rm -rf "$test_root"
}
trap cleanup EXIT
sh install.sh --dir "$directory" --address 127.0.0.1 --port 18446 --offline "$bundle"
test ! -f "$directory/agent/enrollment.token"
before=$(compose exec -T agent sha256sum /state/agent.token)
identity_before=$(sha256sum "$directory/setup.token" "$directory/ca.pem" "$directory/agent/agent.json")
python3 - "$directory" <<'PY'
import http.cookiejar
import json
import pathlib
import secrets
import ssl
import sys
import urllib.request

directory = pathlib.Path(sys.argv[1])
origin = 'https://127.0.0.1:18446'
context = ssl.create_default_context(cafile=str(directory / 'ca.pem'))
client = urllib.request.build_opener(
    urllib.request.ProxyHandler({}),
    urllib.request.HTTPSHandler(context=context),
    urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()),
)
def api(path, data=None):
    request = urllib.request.Request(origin + '/api/v1/' + path,
        data=None if data is None else json.dumps(data).encode(),
        headers={'Origin': origin, 'Content-Type': 'application/json'})
    with client.open(request, timeout=10) as response:
        return json.load(response)
assert api('me')['setup_required']
password = secrets.token_urlsafe(24)
api('setup', {'token': (directory / 'setup.token').read_text().strip(),
              'username': 'installer-test', 'password': password})
api('session', {'username': 'installer-test', 'password': password})
hosts = api('hosts')
assert len(hosts) == 1 and hosts[0]['status'] == 'online'
assert hosts[0].get('last_seen') and hosts[0].get('agent_version')
config = json.loads((directory / 'agent/agent.json').read_text())
assert not config.get('docker_socket') and not config.get('enrollment_file')
assert not (directory / 'agent/ca.key').exists()
assert (directory / 'agent/agent.json').stat().st_mode & 0o777 == 0o600
print('Verified trusted HTTPS, administrator setup, one enrolled host and fresh real telemetry.')
PY
# Simulate interruption after enrollment but before the final success marker.
rm "$directory/.installed"
sh install.sh --dir "$directory" --offline "$bundle"
sh install.sh --dir "$directory" --offline "$bundle"
after=$(compose exec -T agent sha256sum /state/agent.token)
[ "$before" = "$after" ] || { echo 'Repeat install changed the credential.' >&2; exit 1; }
[ "$identity_before" = "$(sha256sum "$directory/setup.token" "$directory/ca.pem" "$directory/agent/agent.json")" ] || { echo 'Repeat install replaced configuration or identity.' >&2; exit 1; }
[ "$(compose ps --services --status running | sort | tr '\n' ' ')" = 'agent hub ' ] || { echo 'Unexpected services started.' >&2; exit 1; }
if sh install.sh --dir "$directory" --address 127.0.0.2 --offline "$bundle"; then
  echo 'Conflicting repeat install accepted.' >&2; exit 1
fi
mkdir "$test_root/corrupt"
cp "$bundle" "$test_root/corrupt/$(basename "$bundle")"
cp "$(dirname "$bundle")/SHA256SUMS" "$test_root/corrupt/SHA256SUMS"
printf 'corrupted' >> "$test_root/corrupt/$(basename "$bundle")"
if sh install.sh --dir "$test_root/rejected" --address 127.0.0.1 --offline "$test_root/corrupt/$(basename "$bundle")"; then
  echo 'Corrupt bundle accepted.' >&2; exit 1
fi
test ! -f "$test_root/rejected/compose.yml"
echo 'Verified repeat installation preserves credentials and rejects conflicting settings and corrupt downloads.'
