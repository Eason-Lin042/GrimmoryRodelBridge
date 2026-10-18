# GrimmoryRodelBridge

[English](README.md) | **简体中文**

在 [小幻阅读（Rodel.Reader）](https://reader.richasy.net/) 中浏览和下载 [Grimmory](https://grimmory.org/) 的 EPUB，并与 Grimmory Web 同步章节进度。

书籍继续由 Grimmory 管理。桥接服务通过 OPDS 提供完整 EPUB，通过 Reader API 同步阅读位置，不需要修改两端程序，也不会移动、重命名或覆盖原书。

## 快速上手

先克隆项目并进入目录：

```bash
git clone https://github.com/Eason-Lin042/GrimmoryRodelBridge.git
cd GrimmoryRodelBridge
```

### Docker Compose（推荐）

1. 准备配置并构建镜像：

   ```bash
   cp .env.example .env
   docker compose build
   ```

2. 使用刚构建的镜像生成加密密钥，无需在宿主机安装 Go：

   ```bash
   docker compose run --rm --no-deps adapter --generate-key
   ```

   将输出填入 `.env` 的 `ADAPTER_ENCRYPTION_KEY`。

3. 编辑 `.env`，填写实际地址。以下假设 IP 为 `192.168.1.100`：

   ```env
   GRIMMORY_URL=http://192.168.1.100:6060
   PUBLIC_BASE_URL=http://192.168.1.100:8080
   ADAPTER_ENCRYPTION_KEY=your-generated-base64-key
   ```

   `GRIMMORY_URL` 需要从容器内访问，`PUBLIC_BASE_URL` 需要从小幻访问。容器内的 `127.0.0.1` 指向容器自身；只有配置了相应网络和服务别名时，才能使用 `http://grimmory:6060`。

4. 启动服务：

   ```bash
   docker compose up -d
   ```

默认端口为 `8080`。Compose 使用独立数据卷保存桥接服务的数据，不需要挂载书库或 Grimmory 数据库。对外访问时建议使用 HTTPS。

### 本地运行

需要 Go **1.27.2 或更新版本**，无需单独安装 SQLite 或 C 编译器。在项目目录中生成密钥：

```bash
go run ./cmd/adapter --generate-key
```

设置环境变量后编译并运行，以下以 Bash 为例：

```bash
export GRIMMORY_URL=http://127.0.0.1:6060
export PUBLIC_BASE_URL=http://127.0.0.1:8080
export ADAPTER_ENCRYPTION_KEY='你的Base64密钥'
export DATA_DIR=./data

go build -o dist/grimmory-rodel-bridge ./cmd/adapter
./dist/grimmory-rodel-bridge
```

Windows PowerShell 使用 `$env:变量名='值'` 设置相同变量，编译输出文件名使用 `dist/grimmory-rodel-bridge.exe`。本地程序不会自动读取 `.env`。

## 连接小幻阅读

1. **获取设备令牌**：
   - 浏览器访问本服务的 `PUBLIC_BASE_URL`（如 `http://192.168.1.100:8080`）。
   - 输入自己的 Grimmory 用户名和密码登录。
   - 复制页面生成的设备令牌，接下来两个源都使用它。

2. **在小幻阅读中添加 Reader 源（用于进度同步）**：
   - 打开小幻阅读侧边栏，进入 **开源阅读**，点击 **`+`** 添加源。
   - **服务器类型**：选择 `Hectorqin Reader`。
   - **服务器地址**：填入 `PUBLIC_BASE_URL`（**注意：结尾不要追加 `/reader3`**）。
   - **访问令牌**：填入刚才生成的设备令牌。

3. **在小幻阅读中添加 OPDS 源（用于书籍浏览与下载）**：
   - 进入 **OPDS 服务列表**，点击 **`+`** 添加。
   - **服务器地址**：填入 `PUBLIC_BASE_URL/opds`。
   - **用户名**：你的 Grimmory 用户名。
   - **密码**：**同一个设备访问令牌**。

4. **阅读与同步**：
   - 从 OPDS 下载并导入 EPUB，再到小幻主书库打开阅读。保留 Reader 源，用于进度同步。

网页登录用 Grimmory 密码，OPDS 的密码填写设备令牌。每台设备分别登录生成令牌；关闭浏览器不会撤销会话，点击“注销此设备令牌”才会使对应令牌失效。

## 环境变量说明

| 环境变量 | 默认值 | 描述 |
|---|---|---|
| `GRIMMORY_URL` | 必填 | 桥接服务能够访问的 Grimmory 地址 |
| `PUBLIC_BASE_URL` | 必填 | 小幻能够访问的固定根地址，不支持子路径部署 |
| `ADAPTER_ENCRYPTION_KEY` | 必填 | 32 字节密钥的 Base64 字符串，用于加密本地保存的 Grimmory 令牌，请备份 |
| `LISTEN_ADDR` | `127.0.0.1:8080`（本地）<br>`0.0.0.0:8080`（容器） | HTTP 服务监听地址 |
| `DATA_DIR` | `./data`（本地）<br>`/data`（容器） | 持久化数据与缓存目录（含 SQLite 数据库） |
| `CACHE_MAX_BYTES` | `2147483648` (2 GiB) | 源文件与派生文件的最大磁盘缓存配额 |
| `MAX_EPUB_BYTES` | `268435456` (256 MiB) | 单本 EPUB 允许的最大体积 |
| `BUILD_CONCURRENCY` | `2` | EPUB 处理请求的并发上限（范围 1–16） |

请在导书前确定 `PUBLIC_BASE_URL`。已有数据目录会绑定 Grimmory 地址和对外地址，更换地址后不能直接复用原数据目录。备份时请同时保留数据目录和加密密钥。

## 同步支持与限制

- **每本书只能有一份可阅读文件，且必须是主 EPUB。** EPUB＋PDF 等多格式书籍暂不提供 OPDS 条目和进度同步。
- **首次绑定要求 Grimmory 中没有历史阅读进度。** 已有进度无法确认来源时，会保留原记录并暂停自动恢复和写回。目前不支持历史进度迁移。
- **同步精度为目录章节级。** 可以区分同一 XHTML 中的不同目录锚点，但不保证恢复到相同字符位置，也不会通过估算的章节进度主动标记读完。
- **多设备采用最后到达的有效写入。** 允许主动回退，旧设备也可能覆盖新位置。小幻打开书籍时可能自动保存；目前没有后台离线补传队列。
- **替换 EPUB 后需要重新获取。** 旧副本停止写回，重新导入可能在小幻中形成第二个条目。旧条目和历史进度不会自动迁移或删除。
- **位置来源或时间不明确时会暂停。** Grimmory v3.5.0 的进度读回为秒级时间，同一秒内的位置变化也可能触发暂停。

配置页展示最近一次校验结果、最后一次成功保存时间和暂停原因，可手动刷新查看。暂停状态不会自动解除，目前也没有强制解锁入口。

接口、位置转换和错误代码详见 [协议与同步契约](docs/protocol.md)。

## 本地开发与测试

```bash
go test ./...
go vet ./...
node --test internal/adapter/web_test.mjs
python -m unittest discover -s dev -p 'test_*.py'
```

支持 race detector 的环境可以再运行 `go test -race ./...`。

## 开源许可证

本项目基于 [MIT 许可证](LICENSE) 发布。欢迎通过 [Issues](https://github.com/Eason-Lin042/GrimmoryRodelBridge/issues) 反馈问题，或提交 Pull Request。
