"""Exercise a real local Grimmory + adapter against an isolated test library.

Requires the dedicated local services described in dev/README.md. This script
creates test users/books and writes progress through REST only. It deliberately
does not accept a remote URL, touch SQL tables, reset progress or delete books.
Credentials and downloaded EPUB are kept under ignored .local/grimmory/.
"""
import base64
import hashlib
import io
import json
from pathlib import Path
import secrets
import time
import urllib.error
import urllib.parse
import urllib.request
import xml.etree.ElementTree as ET
import zipfile

from protocol_probe import build_epub

ROOT = Path(__file__).resolve().parents[1]
DATA = ROOT / ".local" / "grimmory"
UPSTREAM = "http://127.0.0.1:6060"
ADAPTER = "http://127.0.0.1:8080"


def request(base, path, payload=None, token=None, method=None, headers=None):
    headers = dict(headers or {})
    if token:
        headers["Authorization"] = "Bearer " + token
    if payload is not None:
        payload = json.dumps(payload).encode()
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(base + path, payload, headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=90) as result:
            raw = result.read()
            media = result.headers.get("Content-Type", "")
            return result.status, json.loads(raw) if raw and "json" in media else raw, result.headers
    except urllib.error.HTTPError as error:
        raise RuntimeError(f"{req.get_method()} {base}{path.split('?')[0]}: HTTP {error.code}") from None


def main():
    DATA.mkdir(parents=True, exist_ok=True)
    account_path = DATA / "api-test-account.json"
    if not account_path.exists():
        status = request(UPSTREAM, "/api/v1/setup/status")[1]
        if status.get("data") is not False:
            raise RuntimeError("Refusing setup on an existing instance without isolated test credentials")
        account = dict(username="adapter_api_test", password=secrets.token_urlsafe(24))
        request(UPSTREAM, "/api/v1/setup", dict(account, email="adapter-test@example.invalid", name="Adapter API test"))
        account_path.write_text(json.dumps(account), encoding="utf-8")
        account_path.chmod(0o600)
    account = json.loads(account_path.read_text(encoding="utf-8"))
    jwt = request(UPSTREAM, "/api/v1/auth/login", account)[1]["accessToken"]
    user = request(UPSTREAM, "/api/v1/users/me", token=jwt)[1]
    print("Real Grimmory authentication: user", user["id"], flush=True)
    run_id = secrets.token_hex(4)
    library_path = DATA / "library" / run_id
    library_path.mkdir(parents=True)
    source = build_epub(ADAPTER, "upstream-" + run_id, secrets.token_hex(16), 1)
    # Nested on-disk layout is scanned by Grimmory, never by the adapter.
    source_path = library_path / "zh-Hans" / "Japanese" / "测试作者" / "测试书.epub"
    source_path.parent.mkdir(parents=True)
    source_path.write_bytes(source["data"])
    source_hash = hashlib.sha256(source_path.read_bytes()).hexdigest()
    library = request(UPSTREAM, "/api/v1/libraries", dict(
        name="Adapter isolated test " + run_id, paths=[dict(path=library_path.as_posix())],
        watch=False, allowedFormats=["EPUB"], formatPriority=["EPUB"],
        organizationMode="BOOK_PER_FILE", metadataSource="PREFER_EMBEDDED"), token=jwt)[1]
    for _ in range(90):
        books = request(UPSTREAM, f'/api/v1/libraries/{library["id"]}/book', token=jwt)[1]
        if books:
            break
        time.sleep(1)
    else:
        raise RuntimeError("Grimmory did not index the test EPUB")
    book_id = books[0]["id"]
    detail = lambda: request(UPSTREAM, f"/api/v1/books/{book_id}", token=jwt)[1]
    fresh = detail()
    assert not fresh.get("epubProgress"), "Test book unexpectedly has history"
    file_id = fresh["primaryFile"]["id"]
    print("Real Grimmory scan: Book", book_id, "File", file_id, flush=True)
    adapter_token = request(ADAPTER, "/reader3/login", account)[1]["data"]["accessToken"]
    basic = base64.b64encode((account["username"] + ":" + adapter_token).encode()).decode()
    auth = {"Authorization": "Basic " + basic}
    feed = ET.fromstring(request(ADAPTER, "/opds", headers=auth)[1])
    assert any(entry.find("{*}id").text.endswith(f":book:{book_id}") for entry in feed.findall("{*}entry"))
    status, derived, headers = request(ADAPTER, f"/books/{book_id}/epub", headers=auth)
    assert status == 200 and "X-Adapter-Sync-Status" not in headers
    (DATA / "derived-test.epub").write_bytes(derived)
    with zipfile.ZipFile(io.BytesIO(derived)) as epub:
        opf = ET.fromstring(epub.read("EPUB/Package/content.opf"))
        sync_url = next(meta.attrib["content"] for meta in opf.findall("{*}metadata/{*}meta")
                        if meta.attrib.get("name") == "legado:book-url")
        for resource in ("EPUB/assets/css/style.css", "EPUB/assets/images/probe.svg"):
            with zipfile.ZipFile(io.BytesIO(source["data"])) as original:
                assert epub.read(resource) == original.read(resource)
    query = "?accessToken=" + adapter_token

    def reader_book():
        return request(ADAPTER, "/reader3/getShelfBook" + query + "&url=" + urllib.parse.quote(sync_url, safe=""))[1]["data"]

    def save(index):
        return request(ADAPTER, "/reader3/saveBookProgress" + query, dict(url=sync_url, index=index))[1]

    assert "durChapterIndex" not in reader_book()
    assert save(1)["isSuccess"]
    assert detail()["epubProgress"]["href"].endswith("a.xhtml#c1")
    assert reader_book()["durChapterIndex"] == 1
    print("First chapter write, real persisted readback and binding: passed", flush=True)
    # Same file-level payload used by the Web reader; this is real REST, not UI automation.
    # MariaDB's upstream progress columns have second precision. A position change
    # within that second is intentionally ambiguous to the adapter's time contract.
    time.sleep(1.1)
    cfi = "epubcfi(/6/4!/4/2[c2]/2/1:1)"
    request(UPSTREAM, "/api/v1/books/progress", dict(bookId=book_id, fileProgress=dict(
        bookFileId=file_id, positionData=cfi, positionHref="EPUB/Text/b.xhtml#c2",
        progressPercent=73.4, contentSourceProgressPercent=25.0)), token=jwt)
    current = reader_book()
    assert current["durChapterIndex"] == 2 and current["durChapterTime"] > 0
    assert save(2)["isSuccess"]
    assert detail()["epubProgress"]["cfi"] == cfi
    assert save(0)["isSuccess"]
    final = detail()
    assert not final["epubProgress"].get("cfi")
    assert final["epubProgress"]["href"].endswith("a.xhtml#c0")
    assert final["epubProgress"]["percentage"] < 99.5
    assert final.get("readStatus") != "READ"
    assert hashlib.sha256(source_path.read_bytes()).hexdigest() == source_hash
    response, part, _ = request(ADAPTER, f"/books/{book_id}/epub", headers=dict(auth, Range="bytes=0-31"))
    assert response == 206 and len(part) == 32
    state = request(ADAPTER, "/api/status", token=adapter_token)[1]
    assert next(b for b in state["books"] if b["bookId"] == book_id)["state"] == "bound"
    result = dict(bookId=book_id, fileId=file_id, syncURL=sync_url, sourceSha256=source_hash,
                  checkedAt=time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                  result="real Grimmory REST + MariaDB passed; Web/Rodel UI not exercised")
    (DATA / "api-test-result.json").write_text(json.dumps(result, indent=2), encoding="utf-8")
    print("Web-shaped CFI -> Reader restore -> same/cross chapter save, Range and original protection: passed", flush=True)
    print("Result:", DATA / "api-test-result.json", flush=True)


if __name__ == "__main__":
    main()
