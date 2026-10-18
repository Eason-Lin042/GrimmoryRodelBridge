# GrimmoryRodelBridge

独立的 Grimmory ↔ Rodel.Reader 桥接服务。OPDS 提供保留图片、CSS、字体的完整 EPUB；Reader API 将小幻目录章节与 Grimmory 阅读位置关联。Grimmory 仍是书库、用户权限和进度的权威，适配器不访问其数据库，也不挂载或修改原书目录。

基线：Grimmory **v3.5.0**、Rodel.Reader **2.2609.1.0**。首版是受限兼容实现，尚须按 [验收记录](docs/validation.md) 完成实际环境验收后投入使用。

## 本地运行

需要 Go **1.27.2**。SQLite 为纯 Go 实现，无需数据库服务或 C 编译器。

```bash
go run ./cmd/adapter --generate-key
```

保存生成的密钥，然后设置环境变量。例如 Bash：

```bash
export GRIMMORY_URL=http://127.0.0.1:6060
export PUBLIC_BASE_URL=http://127.0.0.1:8080
export ADAPTER_ENCRYPTION_KEY='上一步生成的密钥'
export DATA_DIR=./data
go run ./cmd/adapter
```

也可 `go build -o dist/grimmory-rodel-bridge ./cmd/adapter` 后运行可执行文件；Windows 使用 `-o dist/grimmory-rodel-bridge.exe`。Windows PowerShell 使用 `$env:变量名='值'` 设置相同变量。程序不自动加载 `.env`；Compose 会读取该文件。

浏览器打开 `PUBLIC_BASE_URL`，使用自己的 Grimmory 本地账户登录，取得当前设备的适配器令牌。密码不保存，令牌只显示一次。小幻添加两个源：

| 设置 | 值 |
|---|---|
| Reader 服务地址 | `PUBLIC_BASE_URL`，不要追加 `/reader3` |
| Reader 访问令牌 | 配置页生成的适配器令牌 |
| OPDS 地址 | `PUBLIC_BASE_URL/opds` |
| OPDS 用户名 | Grimmory 用户名 |
| OPDS 密码 | 同一个适配器令牌 |

从 **OPDS** 获取 EPUB 后，在小幻主书库中打开。Reader 的在线正文接口会提示使用 OPDS；Reader 书架只列出已获取并建立版本映射的书籍。未下载的新书会直接出现在 OPDS 分页目录，不需要适配器导入或后台扫描。

每台设备单独在配置页登录并签发令牌；每台设备的 Reader 与 OPDS 使用同一个令牌。配置页注销只撤销该设备会话。服务支持 Grimmory 本地账户，不包括 OIDC、MFA 或远程头认证流程。

## 同步规则

- Book 的唯一可阅读文件必须为主 EPUB。其他格式或多个阅读文件暂不提供同步和 OPDS 条目。
- 第一次接入必须无历史位置；第一次成功写入并读回才建立绑定。已有来源不明历史进度的书仍可下载阅读，但自动进度读写暂停，原有记录不动。
- 绑定后，在文件内容不变、位置有效、时间可归属的受限契约下接受 Grimmory Web 更新。聚合 DTO 不提供来源 File ID，适配器不声称可以证明其他客户端或管理操作写入的来源。
- 双向精度为**目录章节级**，区分同 XHTML 的不同锚点；不承诺相同字符位置。既有精确 CFI 能确认仍在目标章节时予以保留，否则清空并写入有效 href/fragment。估算百分比最高 99.4%，不借章节序号标记读完。
- 使用最后到达的有效写入，允许主动回退。小幻打开时也可能自动保存；旧设备的有效旧位置可能覆盖新位置。这是所选策略的限制，不能按百分比判断新旧。
- 云端时间来自 Grimmory，不能伪造当前时间。无时间、重置、来源冲突、版本变化或写入结果不确定时暂停。没有后台离线写入队列。
- 本地 MariaDB 实测进度时间为秒级；同一秒内外部位置变化不能得到严格较新的时间，会触发 `PROGRESS_TIME_AMBIGUOUS` 并持久暂停。首版不伪造毫秒或自动解锁；时钟差异也可能影响小幻提示恢复。
- 同步身份绑定 File ID 和内容指纹。原文件变化时旧 URL 拒写；新版需重新获取，实测会形成第二个小幻本地条目。历史位置不自动迁移，旧条目不自动删除。

配置页显示当前用户的绑定状态、暂停原因及最后成功保存时间。暂停不会自动清零或重新建立绑定；首版不提供人工历史迁移、强制解锁或重置接口。详情见 [协议与同步契约](docs/protocol.md)。


| 环境变量 | 默认或说明 |
|---|---|
| `GRIMMORY_URL` | 必填，内部 Grimmory 地址 |
| `PUBLIC_BASE_URL` | 必填，客户端可访问的固定根地址 |
| `ADAPTER_ENCRYPTION_KEY` | 必填，32 字节密钥的 Base64，务必备份 |
| `LISTEN_ADDR` | 本地 `127.0.0.1:8080`；容器 `0.0.0.0:8080` |
| `DATA_DIR` | 本地 `data`；容器 `/data` |
| `CACHE_MAX_BYTES` | 2 GiB，源文件和派生文件的缓存总量 |
| `MAX_EPUB_BYTES` | 256 MiB；缓存上限至少为其两倍 |
| `BUILD_CONCURRENCY` | 2，范围 1–16 |

解析缓存还限制最多 16 本、128 MiB 估算 DOM 权重；超大或结构异常 EPUB 会明确报错。磁盘上限不包含正在传输的临时文件；并发数影响瞬时空间和内存需求。`/healthz` 检查进程，`/readyz` 检查本地存储，均不证明上游可用。

v3.5.0 内容接口缺少可证明内容不变的强校验器，因此每次同步需要流式读取源 EPUB 并计算 SHA-256，解析和派生结果可以复用。此项安全选择会增加网络和磁盘读取，NAS 大文件场景应实测。适配器无权与文件替换、Web 写入做跨服务原子事务；操作中同时替换内容仍存在竞态，不能宣称绝对的来源证明。

备份 SQLite 数据与密钥；关闭服务后备份整个数据目录最简单，缓存可丢弃。不要记录请求完整 URL 或查询参数：小幻协议的 `accessToken` 在查询串中，反向代理访问日志也须去掉查询串。程序日志只输出公开错误代码，不输出令牌、密码或正文。

## 开发与验证

`Check` 在 `main` 推送和 PR 时运行：静态检查及探针测试仅执行一次，Linux 运行 race 测试，Windows 运行普通测试。`Publish GHCR` 仅在创建 Git tag 时复用这些检查，再构建并验证双架构镜像；Linux 二进制从已验证镜像提取，Windows 二进制单独编译，均作为构建产物提供。发布直接上传这些镜像并合并对应 tag 和 `sha-<短提交号>`，不重新构建，也不自动更新 `latest`。

```bash
go test ./...
go vet ./...
# 在支持 race detector 的环境中：
go test -race ./...
python -m unittest discover -s dev -p 'test_*.py'
```
