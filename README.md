# GrimmoryRodelBridge

**English** | [简体中文](README.zh-CN.md)

Read EPUBs from your [Grimmory](https://grimmory.org/) library in [Rodel.Reader](https://reader.richasy.net/), and sync chapter progress with Grimmory Web.

Grimmory continues to manage your books. The bridge serves complete EPUB files through OPDS and syncs reading positions through the Reader API. It requires no changes to either application and does not move, rename, or overwrite your original books.

## Quick start

Clone the repository and enter the project directory:

```bash
git clone https://github.com/Eason-Lin042/GrimmoryRodelBridge.git
cd GrimmoryRodelBridge
```

### Docker Compose (recommended)

1. Prepare the configuration and build the image:

   ```bash
   cp .env.example .env
   docker compose build
   ```

2. Generate an encryption key using the image you just built. Go does not need to be installed on the host:

   ```bash
   docker compose run --rm --no-deps adapter --generate-key
   ```

   Copy the output into `ADAPTER_ENCRYPTION_KEY` in `.env`.

3. Edit `.env` with your actual addresses. This example uses `192.168.1.100`:

   ```env
   GRIMMORY_URL=http://192.168.1.100:6060
   PUBLIC_BASE_URL=http://192.168.1.100:8080
   ADAPTER_ENCRYPTION_KEY=your-generated-base64-key
   ```

   `GRIMMORY_URL` must be reachable from the container. `PUBLIC_BASE_URL` must be reachable from Rodel.Reader. Inside a container, `127.0.0.1` refers to that container itself. Use `http://grimmory:6060` only when the container network and service alias are configured accordingly.

4. Start the service:

   ```bash
   docker compose up -d
   ```

The default port is `8080`. Compose uses a separate volume for the bridge's data; there is no need to mount your book library or Grimmory database. Use HTTPS when exposing the service outside your local network.

### Run locally

Go **1.27.2 or later** is required. No separate SQLite installation or C compiler is needed. Generate a key from the project directory:

```bash
go run ./cmd/adapter --generate-key
```

Set the environment variables, then build and run the service. The following example uses Bash:

```bash
export GRIMMORY_URL=http://127.0.0.1:6060
export PUBLIC_BASE_URL=http://127.0.0.1:8080
export ADAPTER_ENCRYPTION_KEY='your-base64-key'
export DATA_DIR=./data

go build -o dist/grimmory-rodel-bridge ./cmd/adapter
./dist/grimmory-rodel-bridge
```

In Windows PowerShell, set the same variables using `$env:VARIABLE='value'` and use `dist/grimmory-rodel-bridge.exe` as the build output filename. The local executable does not automatically load `.env`.

## Connect Rodel.Reader

1. **Get a device token:**
   - Open the service's `PUBLIC_BASE_URL` in a browser, for example `http://192.168.1.100:8080`.
   - Sign in with your Grimmory username and password.
   - Copy the generated device token. Both sources below use this token.

2. **Add a Reader source for progress sync:**
   - Open the open-source reading section in Rodel.Reader's sidebar and click **+** to add a source.
   - **Server type:** `Hectorqin Reader`.
   - **Server address:** `PUBLIC_BASE_URL`, without `/reader3` at the end.
   - **Access token:** the device token you just generated.

3. **Add an OPDS source for browsing and downloading books:**
   - Open the OPDS service list and click **+**.
   - **Server address:** `PUBLIC_BASE_URL/opds`.
   - **Username:** your Grimmory username.
   - **Password:** the same device token.

4. **Read and sync:**
   - Download and import an EPUB through OPDS, then open it from Rodel.Reader's main library. Keep the Reader source configured for progress sync.

Use your Grimmory password to sign in to the web page, and the device token as the OPDS password. Generate a separate token for each device. Closing the browser does not revoke the session; selecting "Revoke this device token" does.

## Configuration

| Environment variable | Default | Description |
|---|---|---|
| `GRIMMORY_URL` | Required | Grimmory address reachable from the bridge |
| `PUBLIC_BASE_URL` | Required | Fixed root URL reachable from Rodel.Reader; deployment under a URL subpath is not supported |
| `ADAPTER_ENCRYPTION_KEY` | Required | Base64-encoded 32-byte key used to encrypt Grimmory tokens stored locally; keep a backup |
| `LISTEN_ADDR` | `127.0.0.1:8080` locally<br>`0.0.0.0:8080` in the container | HTTP listen address |
| `DATA_DIR` | `./data` locally<br>`/data` in the container | Persistent data and cache directory, including the SQLite database |
| `CACHE_MAX_BYTES` | `2147483648` (2 GiB) | Disk cache limit for source and derived files |
| `MAX_EPUB_BYTES` | `268435456` (256 MiB) | Maximum size of a single EPUB |
| `BUILD_CONCURRENCY` | `2` | Maximum concurrent EPUB processing requests, from 1 to 16 |

Choose `PUBLIC_BASE_URL` before importing books. The data directory is bound to the Grimmory address and the public address, so it cannot be reused directly after changing those addresses. Back up both the data directory and the encryption key.

## Sync support and limitations

- **Each book must have exactly one readable file, and it must be the primary EPUB.** Books with multiple formats, such as EPUB and PDF, are currently excluded from both the OPDS catalog and progress sync.
- **Existing Grimmory progress is adopted automatically.** If the bridge has no binding, a valid position and reading time for the current EPUB establish one without changing the original record. Invalid positions, missing times and conflicting format progress pause sync. If a save arrives first, it is rejected until a download or Reader request loads the cloud position. Books paused only by the earlier historical-progress rule are checked again automatically, provided the file and version match and no previous sync evidence exists.
- **Sync works at table-of-contents chapter level.** Different navigation anchors in the same XHTML file can be distinguished, but restoring the exact character position is not guaranteed. Estimated chapter progress does not mark a book as finished.
- **The last valid write to arrive wins across devices.** Moving back to an earlier chapter is allowed, and an old device can overwrite a newer position. Rodel.Reader may save automatically when opening a book. There is currently no background queue for offline progress uploads.
- **Sync pauses when the progress source or timestamp is unclear.** Grimmory v3.5.0 returns progress timestamps with second precision, so position changes within the same second can also cause a pause.

The configuration page shows the last validation result, last successful save time, and pause reason. Refresh it manually to check for updates. A paused binding does not resume automatically, and there is currently no option to force it to resume.

See the [protocol and sync contract](docs/protocol.md) (in Chinese) for interfaces, position mapping, and error codes.

## Development and testing

```bash
go test ./...
go vet ./...
node --test internal/adapter/web_test.mjs
python -m unittest discover -s dev -p 'test_*.py'
```

Run `go test -race ./...` as well in an environment that supports the race detector.

## License

Released under the [MIT License](LICENSE). Bug reports through [Issues](https://github.com/Eason-Lin042/GrimmoryRodelBridge/issues) and pull requests are welcome.
