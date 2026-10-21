import socket
import time

def request(method, path):
    with socket.socket(socket.AF_UNIX) as client:
        client.settimeout(3)
        client.connect('/run/rootlens/docker.sock')
        client.sendall(f'{method} {path} HTTP/1.1\r\nHost: docker\r\nConnection: close\r\nContent-Length: 0\r\n\r\n'.encode())
        return int(client.recv(4096).split(b' ', 2)[1])

for attempt in range(30):
    try:
        if request('GET', '/version') == 200:
            break
    except (OSError, IndexError):
        pass
    time.sleep(0.2)
else:
    raise RuntimeError('Proxy fixture failed to become ready')

container = 'a' * 64
allowed = ['/version', '/_ping', '/v1.41/version', '/v1.55/containers/json?all=true',
           f'/v1.55/containers/{container}/json',
           f'/v1.55/containers/{container}/stats?stream=false&one-shot=true',
           '/v1.55/events?since=123&filters=%7B%22type%22%3A%5B%22container%22%5D%7D']
denied = ['/v1.55/containers/json', '/v1.55/containers/json?all=true&size=true',
          f'/v1.55/containers/{container}/logs', f'/v1.55/containers/{container}/archive?path=/',
          f'/v1.55/containers/{container}/export', f'/v1.55/containers/{container}/attach',
          f'/v1.55/containers/{container}/stats?stream=true',
          '/v1.55/containers/%2e%2e/info', '/v1.55/containers//json?all=true',
          '/v1.55/containers/../info', '/v1.55/containers/json;anything?all=true',
          '/v1.55/containers/json%3Fall=true', '/v1.55/secrets', '/v1.55/info',
          '/v1.99/containers/json?all=true', '/v1.55/events', '/containers/json?all=true']
for path in allowed:
    assert request('GET', path) == 200, ('allowed route failed', path)
for path in denied:
    assert request('GET', path) in (400, 403), ('unsafe route allowed', path)
for method in ['POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS']:
    for path in allowed:
        assert request(method, path) in (400, 403), ('unsafe method allowed', method, path)
assert request('HEAD', '/version') == 200
print(f'Passed {len(allowed) + len(denied) + 5 * len(allowed) + 1} Docker proxy policy checks against a permissive fake engine.')
