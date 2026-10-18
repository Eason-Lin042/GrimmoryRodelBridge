"""Print incoming HTTP requests and respond with an empty JSON object."""

import argparse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class RequestHandler(BaseHTTPRequestHandler):
    def read_body(self):
        if self.headers.get("Transfer-Encoding", "").lower() == "chunked":
            chunks = []
            while True:
                line = self.rfile.readline()
                size = int(line.split(b";", 1)[0], 16)
                if size == 0:
                    while self.rfile.readline() not in (b"\r\n", b"\n", b""):
                        pass
                    return b"".join(chunks)
                chunk = self.rfile.read(size)
                if len(chunk) != size or self.rfile.read(2) != b"\r\n":
                    raise ValueError("Invalid chunked body")
                chunks.append(chunk)

        length = int(self.headers.get("Content-Length", "0"))
        if length < 0:
            raise ValueError("Invalid Content-Length")
        body = self.rfile.read(length)
        if len(body) != length:
            raise ValueError("Incomplete request body")
        return body

    def handle_request(self):
        try:
            body = self.read_body()
        except ValueError as error:
            self.send_error(400, str(error))
            return

        url = self.path
        if url.startswith("/"):
            url = f"http://{self.headers.get('Host', '')}{url}"
        print(
            f"\n{'=' * 60}\n"
            f"Client: {self.client_address[0]}:{self.client_address[1]}\n"
            f"Request: {self.requestline}\n"
            f"URL: {url}\n"
            f"Headers:\n{self.headers}"
            f"Body ({len(body)} bytes):\n"
            f"{body.decode('utf-8', errors='backslashreplace')}\n"
            f"{'=' * 60}",
            flush=True,
        )

        response = b"{}"
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(response)))
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(response)

    def __getattr__(self, name):
        if name.startswith("do_"):
            return self.handle_request
        raise AttributeError(name)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--host", default="0.0.0.0")
    parser.add_argument("--port", type=int, default=8000)
    args = parser.parse_args()
    server = ThreadingHTTPServer((args.host, args.port), RequestHandler)
    print(f"Listening on http://{args.host}:{args.port}", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
