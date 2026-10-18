"""Verify the saved probe's public behavior, using synthetic credentials only."""
import io
import json
from pathlib import Path
import socket
import subprocess
import sys
import time
import unittest
import urllib.error
import urllib.parse
import urllib.request
import xml.etree.ElementTree as ET
import zipfile


class ProbeTest(unittest.TestCase):
    def test_epub_progress_and_upgrade(self):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        process = subprocess.Popen(
            [sys.executable, str(Path(__file__).with_name("protocol_probe.py")), "--port", str(port)],
            stdout=subprocess.DEVNULL, stderr=subprocess.PIPE,
            creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0))
        base = f"http://127.0.0.1:{port}"

        def request(path, body=None):
            raw = json.dumps(body).encode() if body is not None else None
            req = urllib.request.Request(base + path, raw, {"Content-Type": "application/json"})
            try:
                with urllib.request.urlopen(req, timeout=3) as result:
                    return result.status, result.read()
            except urllib.error.HTTPError as error:
                return error.code, error.read()

        try:
            for _ in range(100):
                try:
                    status, raw = request("/status")
                    break
                except OSError:
                    if process.poll() is not None:
                        self.fail(process.stderr.read().decode())
                    time.sleep(0.05)
            else:
                self.fail("probe did not start")
            self.assertEqual(status, 200)
            urls = json.loads(raw)["urls"]
            reader = "/reader3/getBookshelf?accessToken=probe%3Aprobe"
            model = json.loads(request(reader)[1])["data"][0]
            self.assertNotIn("durChapterIndex", model)
            feed = ET.fromstring(request("/opds")[1])
            self.assertEqual(len(feed.findall("{*}entry")), 1)
            with zipfile.ZipFile(io.BytesIO(request("/r1.epub")[1])) as book:
                opf = ET.fromstring(book.read("EPUB/Package/content.opf"))
                uid = opf.find("{*}metadata/{*}identifier").text
                self.assertTrue(book.read("EPUB/assets/css/style.css"))
                self.assertTrue(book.read("EPUB/assets/images/probe.svg"))
                self.assertIn(urls["1"], book.read("EPUB/Package/content.opf").decode())
            save = "/reader3/saveBookProgress?accessToken=probe%3Aprobe"
            self.assertEqual(request(save, dict(url=urls["1"], index=1))[0], 200)
            self.assertEqual(json.loads(request(reader)[1])["data"][0]["durChapterIndex"], 1)
            request("/control?mode=ambiguous")
            self.assertFalse(json.loads(request(reader)[1])["isSuccess"])
            self.assertEqual(request(save, dict(url=urls["1"], index=0))[0], 409)
            request("/control?mode=http_failure")
            self.assertEqual(request(reader)[0], 503)
            request("/control?current=2&mode=absent")
            self.assertEqual(request(save, dict(url=urls["1"], index=1))[0], 409)
            self.assertEqual(request(save, dict(url=urls["2"], index=2))[0], 200)
            self.assertEqual(json.loads(request(reader)[1])["data"][0]["durChapterIndex"], 2)
            with zipfile.ZipFile(io.BytesIO(request("/r2.epub")[1])) as book:
                opf = ET.fromstring(book.read("EPUB/Package/content.opf"))
                self.assertEqual(opf.find("{*}metadata/{*}identifier").text, uid)
        finally:
            process.terminate()
            process.wait(timeout=10)
            process.stderr.close()


if __name__ == "__main__":
    unittest.main()
