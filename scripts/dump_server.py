import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

class H(BaseHTTPRequestHandler):
    def do_GET(self):
        with open(r"C:\Users\Administrator\AppData\Local\Temp\dump2.txt", "a", encoding="utf-8") as f:
            f.write("=== REQUEST ===\n")
            f.write(self.command + " " + self.path + "\n")
            for k, v in self.headers.items():
                f.write(f"  {k}: {v}\n")
        body = json.dumps({"data": [{"id": "doubao-seed-2-0-pro-260215"}]}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *a):
        pass

import json
HTTPServer(("127.0.0.1", 9999), H).serve_forever()
