"""Loopback-only, synthetic OPDS + Reader protocol probe. No Grimmory connection.

Run: python dev/protocol_probe.py
OPDS: http://127.0.0.1:8000/opds ; Reader: http://127.0.0.1:8000
Reader test token: probe:probe
Controls: /control?mode=absent|known|unknown_time|ambiguous|http_failure
          &current=1|2&index=N&time=UNIX_MILLISECONDS
The control endpoint changes synthetic state only. Never use real credentials.
"""

import argparse
import hashlib
import io
import json
from pathlib import Path
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlsplit
from uuid import uuid4
from xml.sax.saxutils import escape
import zipfile


def build_epub(base, run_id, identifier, revision, font=None):
    labels = ["C1 Amber", "C2 Green", "C3 Purple"]
    if revision == 2:
        labels.insert(0, "C0 New opening")
    title = f"Sync contract {run_id} R{revision}"
    style = "body{font-family:serif}section{break-before:page}.probe{border:5px solid #1967d2;padding:16px;color:#1967d2}img{width:240px;height:90px}"
    if font:
        style += "@font-face{font-family:ProbeFont;src:url('../fonts/probe.woff2')}body{font-family:ProbeFont,serif}"
    sections = []
    for index, label in enumerate(labels):
        paragraphs = "".join(f"<p>{label}, revision {revision}, paragraph {j + 1}. Synthetic text for testing local position, navigation and synchronization.</p>" for j in range(22))
        sections.append(f'<section id="c{index}"><!-- legado:chapter-index={index} --><!-- legado:status=downloaded --><h1>{label}</h1><div class="probe">{title}</div><p>Font sample: 0 O 1 l I 8 B</p><img src="../assets/images/probe.svg" alt="EPUB IMAGE OK"/>{paragraphs}</section>')
    raw = ("".join(sections) + style).encode()
    digest = hashlib.sha256(raw).hexdigest()[:16]
    sync_url = f"{base}/sync/{run_id}/books/42/files/901/revisions/{digest}"
    hrefs = [f"EPUB/Text/{'b' if i == len(labels) - 1 else 'a'}.xhtml#c{i}" for i in range(len(labels))]
    def document(content):
        return f'<html xmlns="http://www.w3.org/1999/xhtml"><head><title>{title}</title><link rel="stylesheet" href="../assets/css/style.css"/></head><body>{content}</body></html>'
    links = [f'<li><a href="../Text/{"b" if i == len(labels) - 1 else "a"}.xhtml#c{i}">{label}</a></li>' for i, label in enumerate(labels)]
    # Nested TOC, with multiple anchors in the first XHTML file.
    nested = links[0][:-5] + "<ol>" + "".join(links[1:-1]) + "</ol></li>" + links[-1]
    nav = f'<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Contents</title></head><body><nav epub:type="toc"><ol>{nested}</ol></nav></body></html>'
    metadata = "".join(f'<meta name="{key}" content="{escape(value)}"/>' for key, value in {"legado:book-url": sync_url, "legado:server-url": base, "legado:book-source": "Synthetic probe", "legado:chapter-count": str(len(labels))}.items())
    font_item = '<item id="font" href="../assets/fonts/probe.woff2" media-type="font/woff2"/><item id="license" href="../assets/fonts/LICENSE.txt" media-type="text/plain"/>' if font else ""
    opf = f'''<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="uid">urn:uuid:{identifier}</dc:identifier><dc:title>{title}</dc:title><dc:creator>Protocol test</dc:creator><dc:language>en</dc:language><meta property="dcterms:modified">2026-10-18T00:00:00Z</meta>{metadata}</metadata><manifest><item id="nav" href="../Navigation/nav.xhtml" media-type="application/xhtml+xml" properties="nav"/><item id="a" href="../Text/a.xhtml" media-type="application/xhtml+xml"/><item id="b" href="../Text/b.xhtml" media-type="application/xhtml+xml"/><item id="css" href="../assets/css/style.css" media-type="text/css"/><item id="image" href="../assets/images/probe.svg" media-type="image/svg+xml"/>{font_item}</manifest><spine><itemref idref="a"/><itemref idref="b"/></spine></package>'''
    buffer = io.BytesIO()
    with zipfile.ZipFile(buffer, "w", zipfile.ZIP_DEFLATED) as archive:
        archive.writestr("mimetype", "application/epub+zip", compress_type=zipfile.ZIP_STORED)
        archive.writestr("META-INF/container.xml", '<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0"><rootfiles><rootfile full-path="EPUB/Package/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>')
        archive.writestr("EPUB/Package/content.opf", opf)
        archive.writestr("EPUB/Navigation/nav.xhtml", nav)
        archive.writestr("EPUB/Text/a.xhtml", document("".join(sections[:-1])))
        archive.writestr("EPUB/Text/b.xhtml", document(sections[-1]))
        archive.writestr("EPUB/assets/css/style.css", style)
        archive.writestr("EPUB/assets/images/probe.svg", '<svg xmlns="http://www.w3.org/2000/svg" width="240" height="90"><rect width="240" height="90" fill="#16803d"/><text x="12" y="54" fill="white" font-size="25">EPUB IMAGE OK</text></svg>')
        if font:
            archive.writestr("EPUB/assets/fonts/probe.woff2", font[0])
            archive.writestr("EPUB/assets/fonts/LICENSE.txt", font[1])
    return dict(url=sync_url, labels=labels, hrefs=hrefs, title=title, data=buffer.getvalue())


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--port", type=int, default=8000)
    parser.add_argument("--font", type=Path, help="optional local WOFF2 font (requires --font-license)")
    parser.add_argument("--font-license", type=Path)
    args = parser.parse_args()
    if args.font and not args.font_license:
        parser.error("--font requires --font-license")
    font = (args.font.read_bytes(), args.font_license.read_bytes()) if args.font else None
    base = f"http://127.0.0.1:{args.port}"
    run_id, identifier = uuid4().hex[:8], str(uuid4())
    books = {revision: build_epub(base, run_id, identifier, revision, font) for revision in (1, 2)}
    state = dict(current=1, mode="absent")
    progress = {revision: dict(index=None, time=None) for revision in (1, 2)}
    guard = threading.RLock()

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def reply(self, data, media="application/json", status=200):
            raw = data if isinstance(data, bytes) else json.dumps(data, ensure_ascii=False).encode()
            self.send_response(status)
            for name, value in [("Content-Type", media), ("Content-Length", str(len(raw))), ("Cache-Control", "no-store")]:
                self.send_header(name, value)
            self.end_headers()
            if self.command != "HEAD":
                self.wfile.write(raw)

        def fail(self, code, status=200):
            self.reply(dict(isSuccess=False, errorMsg=code, data=None), status=status)

        def handle_probe(self):
            with guard:
                self._handle_probe()

        def _handle_probe(self):
            parsed = urlsplit(self.path)
            query = parse_qs(parsed.query)
            try:
                size = int(self.headers.get("Content-Length", "0"))
            except ValueError:
                return self.fail("INVALID_CONTENT_LENGTH", 400)
            if size < 0 or size > 16384:
                return self.fail("REQUEST_TOO_LARGE", 413)
            try:
                body = json.loads(self.rfile.read(size)) if size else {}
            except (ValueError, UnicodeDecodeError):
                return self.fail("INVALID_JSON", 400)
            if not isinstance(body, dict):
                return self.fail("INVALID_JSON_OBJECT", 400)
            token_ok = query.get("accessToken", [""])[0] == "probe:probe"
            # Never print credentials, arbitrary bodies, or full query strings.
            print(json.dumps(dict(method=self.command, path=parsed.path, bookUrl=body.get("url", query.get("url", [None])[0]), index=body.get("index"), testTokenMatches=token_ok, mode=state["mode"], current=state["current"], agent=self.headers.get("User-Agent")), ensure_ascii=False), flush=True)
            if parsed.path in ("/control", "/status"):
                try:
                    candidate = dict(state)
                    if "current" in query:
                        revision = int(query["current"][0])
                        if revision not in books:
                            raise ValueError()
                        candidate["current"] = revision
                    if "mode" in query:
                        mode = query["mode"][0]
                        if mode not in ("absent", "known", "unknown_time", "ambiguous", "http_failure"):
                            raise ValueError()
                        candidate["mode"] = mode
                    current = dict(progress[candidate["current"]])
                    if "index" in query:
                        index = int(query["index"][0])
                        if not 0 <= index < len(books[candidate["current"]]["labels"]):
                            raise ValueError()
                        current["index"] = index
                    if "time" in query:
                        current["time"] = int(query["time"][0])
                    if candidate["mode"] in ("known", "unknown_time") and current["index"] is None:
                        raise ValueError()
                except ValueError:
                    return self.fail("INVALID_CONTROL_VALUE", 400)
                state.update(candidate)
                progress[state["current"]] = current
                return self.reply(dict(state=state, progress=progress, titles={r: b["title"] for r, b in books.items()}, urls={r: b["url"] for r, b in books.items()}))
            revision = state["current"]
            if parsed.path == "/opds":
                book = books[revision]
                feed = f'<feed xmlns="http://www.w3.org/2005/Atom"><id>urn:uuid:{identifier}</id><title>Synthetic OPDS probe</title><updated>2026-10-18T00:00:00Z</updated><entry><id>urn:uuid:{identifier}</id><title>{book["title"]}</title><updated>2026-10-18T00:00:0{revision}Z</updated><author><name>Protocol test</name></author><link rel="http://opds-spec.org/acquisition" href="{base}/r{revision}.epub" type="application/epub+zip"/></entry></feed>'
                return self.reply(feed.encode(), "application/atom+xml;profile=opds-catalog;kind=acquisition")
            if parsed.path in ("/r1.epub", "/r2.epub"):
                return self.reply(books[int(parsed.path[2])]["data"], "application/epub+zip")
            if not token_ok:
                return self.fail("TEST_TOKEN_REQUIRED", 401)
            requested = body.get("url", query.get("url", [""])[0])
            if requested and requested != books[revision]["url"]:
                return self.fail("EPUB_VERSION_MISMATCH", 409)
            endpoint = parsed.path.rsplit("/", 1)[-1]
            if endpoint == "saveBookProgress":
                if self.command != "POST" or requested != books[revision]["url"] or type(body.get("index")) is not int or not 0 <= body["index"] < len(books[revision]["labels"]):
                    return self.fail("INVALID_PROGRESS", 400)
                if state["mode"] in ("ambiguous", "http_failure"):
                    return self.fail("PROGRESS_UNAVAILABLE_WRITE_REJECTED", 409)
                progress[revision] = dict(index=body["index"], time=int(time.time() * 1000))
                state["mode"] = "known"
                print("SAVED " + json.dumps(dict(revision=revision, **progress[revision])), flush=True)
                data = ""
            elif endpoint == "getChapterList":
                b = books[revision]
                data = [dict(index=i, title=label, url=b["hrefs"][i], bookUrl=b["url"], isVolume=False, isVip=False, isPay=False) for i, label in enumerate(b["labels"])]
            elif endpoint in ("getBookshelf", "getShelfBook", "getBookInfo"):
                if state["mode"] == "ambiguous":
                    return self.fail("PROGRESS_SOURCE_UNKNOWN")
                if state["mode"] == "http_failure":
                    return self.fail("UPSTREAM_PROGRESS_UNAVAILABLE", 503)
                b = books[revision]
                model = dict(bookUrl=b["url"], tocUrl=b["url"], name=b["title"], author="Protocol test", origin="loc_book", originName="probe.epub", type=0, totalChapterNum=len(b["labels"]), canUpdate=False)
                if state["mode"] in ("known", "unknown_time"):
                    p = progress[revision]
                    model.update(durChapterIndex=p["index"], durChapterTitle=b["labels"][p["index"]], durChapterPos=0)
                    if state["mode"] == "known" and p["time"] is not None:
                        model["durChapterTime"] = p["time"]
                data = [model] if endpoint == "getBookshelf" else model
            else:
                return self.fail("UNSUPPORTED_PROBE_ENDPOINT", 404)
            return self.reply(dict(isSuccess=True, errorMsg="", data=data))

        do_GET = handle_probe
        do_POST = handle_probe
        do_HEAD = handle_probe

    print(f"Probe ready: {base}/opds ; Reader token is the synthetic value probe:probe", flush=True)
    print(json.dumps({r: b["title"] for r, b in books.items()}), flush=True)
    server = ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    main()
