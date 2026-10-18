"""Download verified portable tools for the isolated, non-Docker upstream test.

No system installation, service registration, registry or PATH changes are made.
The tools are GPL-licensed upstream distributions; retain their included notices.
"""
import hashlib
from pathlib import Path
import urllib.request
import zipfile


ROOT = Path(__file__).resolve().parents[1] / ".local" / "tools"
PACKAGES = (
    ("jdk25", "https://corretto.aws/downloads/latest/amazon-corretto-25-x64-windows-jdk.zip",
     "https://corretto.aws/downloads/latest_sha256/amazon-corretto-25-x64-windows-jdk.zip"),
    ("mariadb", "https://archive.mariadb.org/mariadb-11.4.10/winx64-packages/mariadb-11.4.10-winx64.zip",
     "https://archive.mariadb.org/mariadb-11.4.10/winx64-packages/sha256sums.txt"),
    ("node24", "https://nodejs.org/dist/v24.22.0/node-v24.22.0-win-x64.zip",
     "https://nodejs.org/dist/v24.22.0/SHASUMS256.txt"),
)


def fetch(name, url, checksum_url):
    target = ROOT / name
    if target.is_dir():
        print(f"Already present: {target}", flush=True)
        return
    sums = urllib.request.urlopen(checksum_url, timeout=45).read().decode()
    if name in ("mariadb", "node24"):
        filename = url.rsplit("/", 1)[-1]
        expected = next(line.split()[0] for line in sums.splitlines()
                        if line.split()[-1].removeprefix("./") == filename)
    else:
        expected = sums.split()[0]
    ROOT.mkdir(parents=True, exist_ok=True)
    archive = ROOT / (name + ".zip")
    print(f"Downloading {name} from {url}", flush=True)
    digest = hashlib.sha256()
    with urllib.request.urlopen(url, timeout=90) as response, archive.open("wb") as out:
        while chunk := response.read(1024 * 1024):
            digest.update(chunk)
            out.write(chunk)
    if digest.hexdigest() != expected:
        archive.unlink()
        raise RuntimeError(f"SHA-256 mismatch for {name}")
    with zipfile.ZipFile(archive) as bundle:
        for entry in bundle.infolist():
            path = (target / entry.filename).resolve()
            if not path.is_relative_to(target.resolve()):
                raise RuntimeError("Unsafe archive path")
        bundle.extractall(target)
    archive.unlink()
    print(f"Verified and extracted: {target}", flush=True)


if __name__ == "__main__":
    for package in PACKAGES:
        fetch(*package)
