"""Permissive synthetic backend: any denial must come from the proxy."""
import os
import socketserver
from http.server import BaseHTTPRequestHandler

path = '/fake/docker.sock'
if os.path.exists(path):
    os.unlink(path)

class Handler(BaseHTTPRequestHandler):
    def handle_request(self):
        self.send_response(200)
        self.send_header('Content-Length', '2')
        self.end_headers()
        if self.command != 'HEAD':
            self.wfile.write(b'{}')
    do_GET = do_HEAD = do_POST = do_PUT = do_DELETE = do_PATCH = do_OPTIONS = handle_request
    def log_message(self, *_args):
        pass

with socketserver.UnixStreamServer(path, Handler) as server:
    os.chmod(path, 0o666)
    server.serve_forever()
