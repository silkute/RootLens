"""Exercise /dev/tty prompts as curl | sh sees them; never print setup secrets."""
import errno
import os
import pty
import re
import select
import signal
import sys
import time

pid, terminal = pty.fork()
if pid == 0:
    os.execvp("sh", ["sh", "-c", 'cat install.sh | sh -s -- --dir "$1" --address 127.0.0.1 --offline "$2"',
                      "interactive-installer", sys.argv[1], sys.argv[2]])

answers = [b"0", b"65536", b"abc", b"0443", b"18446"]
transcript = b""
cursor = 0
deadline = time.monotonic() + 180
try:
    while True:
        if time.monotonic() > deadline:
            os.killpg(pid, signal.SIGTERM)
            raise SystemExit("Interactive installer timed out")
        if not select.select([terminal], [], [], 1)[0]:
            continue
        try:
            chunk = os.read(terminal, 8192)
        except OSError as error:
            if error.errno == errno.EIO:
                break
            raise
        if not chunk:
            break
        transcript += chunk
        if len(transcript) > 256 * 1024:
            raise SystemExit("Interactive installer output exceeded its test budget")
        match = re.search(rb"Dashboard HTTPS port \[[0-9]+\]: ", transcript[cursor:])
        if match:
            cursor += match.end()
            if not answers:
                raise SystemExit("Unexpected extra port prompt")
            os.write(terminal, answers.pop(0) + b"\n")
finally:
    os.close(terminal)
_, status = os.waitpid(pid, 0)
if os.waitstatus_to_exitcode(status) != 0:
    # No raw terminal transcript: an interactive install intentionally shows its
    # one-time token to the terminal, which must never enter CI logs.
    safe = [line for line in transcript.decode(errors="replace").splitlines()
            if "setup token" not in line.lower() and not re.search(r"[A-Za-z0-9_-]{43}", line)]
    print("\n".join(safe[-30:]))
    raise SystemExit("Interactive install failed")
assert not answers, "Port validation prompts were skipped"
print("Verified terminal port selection, invalid-input recovery and a custom-port installation.")
