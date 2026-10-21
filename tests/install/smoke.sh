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
  docker rm -f "${project}-occupied" >/dev/null 2>&1 || true
  if [ -f "$directory/compose.yml" ]; then compose --profile docker down --volumes >/dev/null 2>&1 || true; fi
  rm -rf "$test_root"
}
trap cleanup EXIT
python3 tests/install/interactive.py "$directory" "$bundle"
test ! -f "$directory/agent/enrollment.token"
before=$(compose exec -T agent sha256sum /state/agent.token)
identity_before=$(sha256sum "$directory/setup.token" "$directory/ca.pem")
python3 - "$directory" "$bundle" <<'PY'
import http.cookiejar
import json
import pathlib
import secrets
import ssl
import subprocess
import sys
import urllib.request
import urllib.error
import time

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
def change_port(port):
    last_receipt = api('hosts')[0]['last_seen']
    subprocess.run(['sh', 'install.sh', '--dir', str(directory), '--port', str(port),
                    '--non-interactive', '--offline', sys.argv[2]], check=True)
    global origin
    old_origin = origin
    origin = 'https://127.0.0.1' + ('' if port == 443 else f':{port}')
    assert api('me')['authenticated'], 'Existing administrator session was lost'
    api('session', {'username': 'installer-test', 'password': password})
    # Browser state changes must use the selected origin, including omitted :443.
    bad = urllib.request.Request(origin + '/api/v1/session', data=b'{}',
                                 headers={'Origin': old_origin, 'Content-Type': 'application/json'})
    try:
        client.open(bad, timeout=10)
    except urllib.error.HTTPError as error:
        assert error.code == 403
    else:
        raise AssertionError('Old origin was accepted after a port change')
    current = json.loads((directory / 'agent/agent.json').read_text())
    assert current['hub'] == origin
    original = dict(config)
    current.pop('hub')
    original.pop('hub')
    assert current == original, 'Unrelated agent settings changed'
    assert (directory / '.env').read_text().count(f'ROOTLENS_PORT={port}\n') == 1
    for _ in range(15):
        host = api('hosts')[0]
        if host['status'] == 'online' and host['last_seen'] != last_receipt:
            break
        time.sleep(1)
    assert host['status'] == 'online' and host['last_seen'] != last_receipt
change_port(443)
# Internal and published ports can differ; preserve additional .env settings.
with (directory / '.env').open('a') as output:
    output.write('ROOTLENS_INTERNAL_PORT=19443\n')
change_port(19446)
assert 'ROOTLENS_INTERNAL_PORT=19443\n' in (directory / '.env').read_text()
# Qualify saved preview.2 deployments using the actual published prior images.
# Keep their service versions pinned while the current helper edits endpoints.
(directory / 'compose.yml').write_bytes(pathlib.Path('tests/install/compose.preview2.yml').read_bytes())
marker = (directory / '.rootlens-install').read_text().splitlines()
marker[0] = 'v0.1.0-preview.2'
(directory / '.rootlens-install').write_text('\n'.join(marker) + '\n')
change_port(443)
change_port(19446)
assert (directory / '.rootlens-install').read_text().splitlines()[0] == 'v0.1.0-preview.2'
print('Verified live port changes, default HTTPS port, browser origin checks, preserved account/session and agent telemetry.')
# Enable Docker on a previously disabled deployment, from an unrelated folder.
# The installer must find its directory rather than create another installation.
import tempfile
installer = str(pathlib.Path('install.sh').resolve())
with tempfile.TemporaryDirectory(prefix='rootlens-unrelated-') as unrelated:
    subprocess.run(['sh', installer, '--docker', '--non-interactive', '--offline', sys.argv[2]],
                   cwd=unrelated, check=True)
    assert not (pathlib.Path(unrelated) / 'rootlens').exists()
host = api('hosts')[0]
assert host['capabilities']['docker']['status'] == 'available'
containers = api(f"hosts/{host['id']}/containers")
assert len(containers) >= 3, 'Running RootLens containers were not discovered'
assert any(c['entity']['metadata'].get('com.docker.compose.service') == 'hub' for c in containers)
updated = json.loads((directory / 'agent/agent.json').read_text())
assert updated['docker_socket'] == '/run/rootlens/docker.sock'
assert updated['host_id'] == config['host_id']
assert updated['credential_file'] == config['credential_file']
assert (directory / '.rootlens-install').read_text().splitlines()[3] == '1'
assert api('me')['authenticated']
print('Verified automatic installation discovery, enabling Docker on an existing account, restricted-proxy inventory and preserved identity.')
PY
# Simulate interruption after enrollment but before the final success marker.
rm "$directory/.installed"
sh install.sh --dir "$directory" --non-interactive --offline "$bundle"
sh install.sh --dir "$directory" --non-interactive --offline "$bundle"
after=$(compose exec -T agent sha256sum /state/agent.token)
[ "$before" = "$after" ] || { echo 'Repeat install changed the credential.' >&2; exit 1; }
[ "$identity_before" = "$(sha256sum "$directory/setup.token" "$directory/ca.pem")" ] || { echo 'Port selection or repeat install replaced identity.' >&2; exit 1; }
[ "$(compose --profile docker ps --services --status running | sort | tr '\n' ' ')" = 'agent docker-proxy hub ' ] || { echo 'Docker monitoring profile was not retained.' >&2; exit 1; }
docker run -d --name "${project}-occupied" --read-only --cap-drop ALL --publish 127.0.0.1:19447:8443 --entrypoint sh ghcr.io/silkute/rootlens-hub:v0.1.0-preview.4 -c 'sleep 120' >/dev/null
settings_before=$(sha256sum "$directory/.env" "$directory/agent/agent.json" "$directory/.rootlens-install")
if sh install.sh --dir "$directory" --port 19447 --non-interactive --offline "$bundle"; then
  echo 'An occupied port was accepted.' >&2; exit 1
fi
[ "$settings_before" = "$(sha256sum "$directory/.env" "$directory/agent/agent.json" "$directory/.rootlens-install")" ] || { echo 'Failed port probe changed settings.' >&2; exit 1; }
curl --fail --silent --show-error --noproxy '*' --cacert "$directory/ca.pem" https://127.0.0.1:19446/api/v1/me -o /dev/null
docker rm -f "${project}-occupied" >/dev/null
# Simulate interruption between endpoint writes and committing .env/marker.
compose stop agent hub
docker run --rm --network none --mount "type=volume,source=${project}_hub-data,target=/data" --mount "type=bind,source=$directory/agent,target=/etc/rootlens" ghcr.io/silkute/rootlens-hub:v0.1.0-preview.4 set-port --data /data --output /etc/rootlens --port 19448
printf '19448\n' > "$directory/.port-change"
sh install.sh --dir "$directory" --non-interactive --offline "$bundle"
test ! -f "$directory/.port-change"
curl --fail --silent --show-error --noproxy '*' --cacert "$directory/ca.pem" https://127.0.0.1:19448/api/v1/me -o /dev/null
[ "$before" = "$(compose exec -T agent sha256sum /state/agent.token)" ] || { echo 'Port recovery changed the credential.' >&2; exit 1; }
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
echo 'Verified repeat installation, occupied-port protection, interrupted port recovery and corrupt-download rejection.'
