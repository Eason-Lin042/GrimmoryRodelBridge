"""Real, loopback-only permission and historical-source checks after live_smoke.py."""
import base64
import json
import secrets
import urllib.parse

from live_smoke import ADAPTER, DATA, UPSTREAM, request


def expect_http(status, operation):
    try:
        operation()
    except RuntimeError as error:
        if str(error).endswith(f"HTTP {status}"):
            return
        raise
    raise AssertionError(f"Expected HTTP {status}")


def main():
    admin = json.loads((DATA / "api-test-account.json").read_text())
    result = json.loads((DATA / "api-test-result.json").read_text())
    jwt = request(UPSTREAM, "/api/v1/auth/login", admin)[1]["accessToken"]
    book_id, file_id = result["bookId"], result["fileId"]
    book_path = f"/api/v1/books/{book_id}"
    book = request(UPSTREAM, book_path, token=jwt)[1]
    library_id = book["libraryId"]

    def create_user(prefix):
        credentials = dict(username=prefix + secrets.token_hex(4), password=secrets.token_urlsafe(24))
        request(UPSTREAM, "/api/v1/auth/register", dict(credentials,
            name="Isolated adapter test", email=credentials["username"] + "@example.invalid",
            selectedLibraries=[library_id], permissionDownload=True, permissionAccessOpds=False), token=jwt)
        token = request(UPSTREAM, "/api/v1/auth/login", credentials)[1]["accessToken"]
        # Admin-created accounts must complete the upstream password change before
        # its normal Web UI will open a reader. Exercise the official endpoint.
        changed = secrets.token_urlsafe(24)
        request(UPSTREAM, "/api/v1/users/change-password", dict(
            currentPassword=credentials["password"], newPassword=changed), token=token, method="PUT")
        credentials["password"] = changed
        token = request(UPSTREAM, "/api/v1/auth/login", credentials)[1]["accessToken"]
        user = request(UPSTREAM, "/api/v1/users/me", token=token)[1]
        adapter_token = request(ADAPTER, "/reader3/login", credentials)[1]["data"]["accessToken"]
        return credentials, token, user, adapter_token

    credentials, token, user, adapter_token = create_user("adapter_reader_")
    auth = {"Authorization": "Basic " + base64.b64encode((credentials["username"] + ":" + adapter_token).encode()).decode()}
    download = lambda extra=None: request(ADAPTER, f"/books/{book_id}/epub", headers=dict(auth, **(extra or {})))
    etag = download()[2]["ETag"]
    query = "?accessToken=" + adapter_token
    save = lambda url, index: request(ADAPTER, "/reader3/saveBookProgress" + query, dict(url=url, index=index))
    assert save(result["syncURL"], 1)[1]["isSuccess"]
    for can_download, can_opds in ((True, False), (True, True), (False, False), (False, True)):
        request(UPSTREAM, f'/api/v1/users/{user["id"]}', dict(
            name=user["name"], email=user["email"], assignedLibraries=[library_id],
            permissions=dict(canDownload=can_download, canAccessOpds=can_opds)), token=jwt, method="PUT")
        actual = request(UPSTREAM, "/api/v1/users/me", token=token)[1]["permissions"]
        assert actual["canDownload"] == can_download and actual["canAccessOpds"] == can_opds
        assert request(ADAPTER, "/opds", headers=auth)[0] == 200
        if can_download:
            assert download({"Range": "bytes=0-31"})[0] == 206
        else:
            expect_http(403, lambda: download({"Range": "bytes=0-31"}))
            expect_http(403, lambda: download({"If-None-Match": etag}))
            assert save(result["syncURL"], 0)[1]["isSuccess"]
    print("Real canDownload/canAccessOpds four combinations, cache/Range/conditional denial and sync without download permission: passed", flush=True)
    # Grimmory requires a non-admin user to retain at least one assigned library.
    # Move this test user's assignment to a separate, empty test library.
    empty_path = DATA / "library" / ("empty-" + str(user["id"]))
    empty_path.mkdir(parents=True, exist_ok=True)
    empty_library = request(UPSTREAM, "/api/v1/libraries", dict(
        name="Empty access test " + str(user["id"]), paths=[dict(path=empty_path.as_posix())],
        watch=False, allowedFormats=["EPUB"], formatPriority=["EPUB"],
        organizationMode="BOOK_PER_FILE", metadataSource="NONE"), token=jwt)[1]
    request(UPSTREAM, f'/api/v1/users/{user["id"]}', dict(
        name=user["name"], email=user["email"], assignedLibraries=[empty_library["id"]],
        permissions=dict(canDownload=True, canAccessOpds=True)), token=jwt, method="PUT")
    expect_http(403, lambda: download())
    expect_http(403, lambda: save(result["syncURL"], 2))
    request(UPSTREAM, f'/api/v1/users/{user["id"]}', dict(
        name=user["name"], email=user["email"], assignedLibraries=[library_id],
        permissions=dict(canDownload=True, canAccessOpds=False)), token=jwt, method="PUT")
    before = request(UPSTREAM, book_path, token=token)[1]["epubProgress"]
    forged = result["syncURL"].replace(f"/files/{file_id}/", f"/files/{file_id + 10000}/")
    expect_http(409, lambda: save(forged, 2))
    assert request(UPSTREAM, book_path, token=token)[1]["epubProgress"] == before
    reader_path = "/reader3/getShelfBook" + query + "&url=" + urllib.parse.quote(result["syncURL"], safe="")
    assert request(ADAPTER, reader_path)[1]["isSuccess"]
    # Leave this non-admin test account available for the actual Reader/Web UI check.
    (DATA / "client-test.json").write_text(json.dumps(dict(credentials, bookId=book_id,
        adapterToken=adapter_token, syncURL=result["syncURL"]), indent=2), encoding="utf-8")
    (DATA / "client-test.json").chmod(0o600)
    _, historical_jwt, _, historical_token = create_user("adapter_history_")
    request(UPSTREAM, "/api/v1/books/progress", dict(bookId=book_id, fileProgress=dict(
        bookFileId=file_id, positionHref="EPUB/Text/a.xhtml#c1", progressPercent=33.3)), token=historical_jwt)
    before = request(UPSTREAM, book_path, token=historical_jwt)[1]["epubProgress"]
    acquired = request(ADAPTER, f"/books/{book_id}/epub", token=historical_token)
    assert acquired[2]["X-Adapter-Sync-Status"] == "HISTORICAL_PROGRESS_UNBOUND"
    path = "/reader3/getShelfBook?accessToken=" + historical_token + "&url=" + urllib.parse.quote(result["syncURL"], safe="")
    expect_http(409, lambda: request(ADAPTER, path))
    expect_http(409, lambda: request(ADAPTER, "/reader3/saveBookProgress?accessToken=" + historical_token,
                                  dict(url=result["syncURL"], index=0)))
    assert request(UPSTREAM, book_path, token=historical_jwt)[1]["epubProgress"] == before
    print("Real library revocation, forged File ID and unbound historical progress protection: passed", flush=True)
    print("Local-only client test credentials:", DATA / "client-test.json", flush=True)


if __name__ == "__main__":
    main()
