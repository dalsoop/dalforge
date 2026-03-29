#!/usr/bin/env python3
import json
import os
import re
import subprocess
from http.server import BaseHTTPRequestHandler, HTTPServer


LISTEN = os.environ.get("DALCENTER_CRED_OPS_HTTP_LISTEN", "10.50.0.1:11191")
TOKEN = os.environ.get("DALCENTER_CRED_OPS_HTTP_TOKEN", "")
VALID_PLAYERS = {"claude", "codex", "gemini", "all"}


def run(cmd):
    proc = subprocess.run(cmd, capture_output=True, text=True)
    output = (proc.stdout or "") + (proc.stderr or "")
    if proc.returncode != 0:
        raise RuntimeError(output.strip() or f"exit {proc.returncode}")
    return output.strip()


class Handler(BaseHTTPRequestHandler):
    def _auth(self):
        return TOKEN and self.headers.get("Authorization") == f"Bearer {TOKEN}"

    def _json(self, code, body):
        payload = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def do_POST(self):
        if self.path != "/sync":
            self._json(404, {"error": "not found"})
            return
        if not self._auth():
            self._json(401, {"error": "unauthorized"})
            return
        length = int(self.headers.get("Content-Length", "0"))
        try:
            data = json.loads(self.rfile.read(length) or b"{}")
        except json.JSONDecodeError:
            self._json(400, {"error": "invalid json"})
            return
        player = str(data.get("player", "")).strip()
        vmid = str(data.get("vmid", "")).strip()
        if player not in VALID_PLAYERS:
            self._json(400, {"error": "invalid player"})
            return
        if not re.fullmatch(r"[0-9]+", vmid):
            self._json(400, {"error": "invalid vmid"})
            return
        try:
            out1 = run(["/usr/local/bin/proxmox-host-setup", "ai", "sync", "--agent", player])
            out2 = run(["/usr/local/bin/pve-sync-creds", vmid])
        except Exception as exc:
            self._json(500, {"error": str(exc)})
            return
        self._json(200, {"status": "ok", "player": player, "vmid": vmid, "sync": out1, "copy": out2})

    def do_GET(self):
        if self.path == "/health":
            self._json(200, {"status": "ok"})
            return
        self._json(404, {"error": "not found"})

    def log_message(self, fmt, *args):
        return


def main():
    host, port = LISTEN.rsplit(":", 1)
    server = HTTPServer((host, int(port)), Handler)
    server.serve_forever()


if __name__ == "__main__":
    main()
