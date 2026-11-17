# 123 云盘客户端（WebDAV · mygo 原生 UI）

用 **Go + [mygo](https://github.com/egoist/mygo)** 写的 Windows 桌面客户端：mygo 在 GPU 上直接绘制原生界面（没有 HTML / JavaScript / webview），用树形控件浏览 WebDAV 上的目录，目录树缓存到本地 SQLite，按需同步，并支持只查本地缓存的模糊搜索。

123 云盘官方 OpenAPI 需要购买「开发者权益包」，所以本项目走标准 **WebDAV** 协议。

**123 云盘官方 WebDAV 地址：`https://webdav.123pan.cn/webdav`**（Basic Auth），程序默认就填这个地址；也可以填任意其它 WebDAV 服务（NAS、Alist、自建等）。

```
┌──────────────────────────────────────────────────────────────┐
│ [同步选中目录][同步全部][停止] ☑包含子目录   [搜索…] [设置…] │
├──────────────────────────────────────────────────────────────┤
│ ▾ 我的云盘                                                   │
│   ▸ 电影/                                                    │
│   ▸ 文档/                                                    │
│     readme.txt  6 B                                          │
├──────────────────────────────────────────────────────────────┤
│ 日志                        [清空]                           │
│ 22:24:06  开始同步 我的云盘（仅本层，QPS=10.0…）             │
│ 22:24:06    ✓ 我的云盘（3 项）                                │
├──────────────────────────────────────────────────────────────┤
│ 目录：我的云盘/电影 | 4 个子目录 / 19 个文件 | 同步于 …       │
└──────────────────────────────────────────────────────────────┘
```

## 功能

- **mygo 原生 UI**：自绘界面、GPU 绘制（Windows 上是 Direct3D 11），启动快、内存小，不依赖 webview。
- **树形展示**：启动即从本地 SQLite 缓存渲染目录树，断网也能浏览已同步的内容；使用 mygo 虚拟化的 `Outline`，只构建可见行。
- **按需同步**：选中节点点「同步选中目录」，可勾选是否递归子目录；展开未缓存的目录会自动同步一层。
- **同步全部**：递归同步整棵目录树（有确认对话框）。
- **QPS 限速**：所有请求串行经过同一个限速器（默认 5 QPS，可调 0.2~50），遇到 HTTP 429 / 5xx 自动退避重试。
- **停止**：随时取消当前同步，并清空排队中的任务。
- **本地搜索**：按文件名 / 文件夹名模糊搜索（忽略大小写，空格分隔多关键字 AND），**只查本地 SQLite 缓存，不访问服务器**；双击结果在目录树中定位（同样只用缓存）。
- **日志面板**：底部实时显示同步过程；`跟随末尾`，新日志自动滚到底部。
- **窗口状态记忆**：窗口大小/位置/最大化状态由 mygo 的 `StateKey` 记住。

## 环境要求

- Windows 10 / 11（amd64）
- **Go 1.27.3+**（mygo v0.9.2 的要求；`go.mod` 里写了 `toolchain go1.27.4`，本机 Go 较旧时会自动下载对应工具链）
- 无需 CGO；原生 UI 不需要 Bun / Node（那些只用于 Web 前端模式）

## 构建

```bash
# 开发运行（会弹出一个原生窗口）
go run .

# 编译（-H windowsgui 表示不带控制台窗口）
go build -trimpath -ldflags "-s -w -H windowsgui" -o 123pan-client.exe .
```

或者直接运行 `build.bat`（gofmt + go build）。

> mygo 在启动时会自己设置 Per-Monitor V2 DPI 感知，因此不再需要 manifest / `rsrc.syso`；产物是单个 exe。

## 使用

1. 打开程序 → 点「设置…」：WebDAV 地址默认是 `https://webdav.123pan.cn/webdav`（可点「填入 123 云盘地址」恢复）。
2. 填 **用户名 / 密码**（123 云盘账号的 WebDAV 凭证，见官方文档；凭证不正确时接口会返回 401）→「测试连接」→ 保存。
3. 首次启动会自动同步根目录第一层；之后在树上选中任意目录，点「同步选中目录」即可。

### 操作提示

| 操作 | 效果 |
| --- | --- |
| 点击目录前的展开箭头 | 有缓存则直接离线展开；没有缓存则自动同步这一层（1 次 PROPFIND） |
| 选中节点 + 「同步选中目录」 | 勾选「包含子目录」时递归同步整棵子树，否则只刷新这一层 |
| 「同步全部」 | 递归同步整个目录树（有确认对话框） |
| 搜索框 | 输入即搜（仅本地缓存，忽略大小写，空格分隔多个关键字） |
| 双击搜索结果 | 在目录树中逐级展开并选中该条目（只用缓存，不联网） |
| 「停止」 | 取消当前同步并清空排队任务 |
| 底部日志面板 | 每个目录的同步结果、缓存命中、限速等待都在这里 |

## 数据存放

所有数据都在**程序当前工作目录**（通常就是 exe 所在目录），整个目录可以直接拷贝带走：

| 文件 | 说明 |
| --- | --- |
| `config.json` | WebDAV 地址 / 用户名 / 密码 / 是否忽略证书 / QPS |
| `tree.db` | SQLite 目录树缓存（使用 DELETE 日志模式，**不会产生** `-wal`/`-shm` 常驻文件） |

> `config.json` 内含密码，已和 `tree.db*` 一起加入 `.gitignore`，不要提交或分享。

`config.json` 示例：

```json
{
  "webdavUrl": "https://webdav.123pan.cn/webdav",
  "username": "user",
  "password": "pass",
  "insecureTls": false,
  "qps": 5
}
```

### SQLite 缓存结构

```sql
CREATE TABLE folder (          -- 已同步过的目录
    path      TEXT PRIMARY KEY,
    synced_at INTEGER NOT NULL
);
CREATE TABLE entry (           -- 目录下的直接子项
    parent       TEXT NOT NULL,
    name         TEXT NOT NULL,
    sort_name    TEXT NOT NULL,   -- 小写名称，用于忽略大小写排序/搜索
    is_dir       INTEGER NOT NULL,
    size         INTEGER NOT NULL,
    etag         TEXT NOT NULL DEFAULT '',
    modified     INTEGER NOT NULL DEFAULT 0,
    content_type TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (parent, name)
);
```

- 以「父目录路径 → 直接子项」为粒度：同步某个子树不会破坏其它目录的缓存；
- 刷新时按目录整段替换（单个事务），天然完成增 / 删 / 改的合并；
- 展开目录时直接 `SELECT` 子项，零网络开销；
- 每个目录记录 `synced_at`，界面上可看到上次同步时间。

## WebDAV 实现说明

| 行为 | 细节 |
| --- | --- |
| 列目录 | `PROPFIND` + `Depth: 1`，请求 `<displayname>/<resourcetype>/<getcontentlength>/<getetag>/<getlastmodified>/<getcontenttype>` |
| 连接测试 | `PROPFIND` + `Depth: 0` 访问根路径，能返回 207 即视为可用 |
| 路径处理 | 逐段 URL 转义；响应 `href` 解码后去掉 WebDAV 根路径前缀；统一的绝对路径（`/电影/2024`）作为缓存与节点标识 |
| 重定向 | 自行跟随 301/302/307/308（Go 默认会把 PROPFIND 重定向改成 GET） |
| 限速 | 全局单飞队列，两次请求至少间隔 `1/QPS`；429 支持 `Retry-After` |
| 请求头 | `User-Agent: pan123-client/1.0 (WebDAV)`，Basic Auth 预发送（不等 401 挑战） |
| 错误 | 401/403 → 鉴权错误；404 → 路径不存在；405/501 → 提示「可能不是 WebDAV 服务」 |

## 代码结构

```
main.go                  mygo 应用入口：创建原生 UI 窗口，Content: ui.View(app.View)
internal/
  config/config.go       配置读写（当前目录 config.json、tree.db 路径）
  store/db.go            SQLite 目录树缓存（modernc.org/sqlite，纯 Go；DELETE 日志模式）
  store/db_test.go       缓存 / 搜索 / 日志模式测试
  webdav/client.go       WebDAV 客户端（PROPFIND、Basic Auth、限速、重试、重定向）
  webdav/client_test.go  WebDAV 解析 / 鉴权 / 重试 / 重定向测试
  ui/app.go              应用状态 + mygo 视图（工具栏、目录树、搜索结果、日志、状态栏）
  ui/model.go            目录树节点 Node（按路径/名称复用节点，Outline 的展开规则）
  ui/syncer.go           同步队列 worker（串行、可取消、去重）
  ui/settings.go         设置页（WebDAV 地址/账号、测试连接）
  ui/format.go           文件大小 / 时间格式化
  ui/model_test.go       节点合并、展开规则、路径查找测试
  ui/syncer_test.go      同步链路测试（假 WebDAV 服务）
```

## 设计要点

- **mygo 立即模式视图**：`View(c *ui.Context)` 每帧根据状态重建界面；后台 goroutine 通过 `Window.Update(fn)` 改状态并重绘，需要「改完立刻读树模型」的地方用 `mygo.RunOnMain(fn)` 同步回到主线程。
- **虚拟化目录树**：用 `ui.Outline`，只构建可见行，大目录也不卡。未加载的文件夹 `children` 返回**非 nil 空切片**，让 Outline 显示展开箭头；展开后由 `reconcile` 读取缓存或提交「仅本层」同步。加载完成的空文件夹返回 nil（不再显示箭头）。
- **节点身份**：节点按 WebDAV 路径组织，刷新目录时按名称复用 `*Node` 指针，展开/选中状态（`OutlineState.Open` / `List.Selected`）自然保留。
- **SQLite 单连接 + DELETE 日志模式**：`MaxOpenConns(1)` 避免并发写锁冲突；`journal_mode=DELETE` 使目录里只留下 `tree.db` 一个文件。
- **限速在客户端层**：`webdav.Limiter` 保证两次请求之间至少间隔 `1/QPS`，同步 worker 是单 goroutine，所以「手动同步 + 展开自动同步」也不会并发冲击服务端。
- **可取消**：每个同步任务一个 `context`，停止按钮会取消当前任务并清空队列；网络请求和限速等待都响应取消。
- **本地搜索**：`SELECT ... WHERE sort_name LIKE ? ESCAPE '\'`，`sort_name` 是 Go `strings.ToLower` 后的小写名称（Unicode 友好）；用户输入里的 `%`/`_` 会被转义。结果按「完全匹配 > 前缀 > 包含」排序，其次名称更短、文件夹优先。整个过程只读 SQLite，不产生任何 WebDAV 请求。

## 已知限制

- 只读浏览，不含上传 / 下载 / 删除 / 重命名（本项目的目标是「目录树浏览 + 可控同步」）。
- 文件大小、修改时间等元数据来自 PROPFIND，取决于服务端是否提供。
- 大目录树递归同步耗时受 QPS 限制（默认 5 QPS），建议先同步一层再逐级展开。
- 搜索范围仅限**已同步到本地缓存**的条目（未同步的目录不在结果里），这也是「不与服务器交互」的代价。
- 123 云盘官方 WebDAV（`https://webdav.123pan.cn/webdav`）可能需要对应会员/权限，凭证以官方 WebDAV 文档为准；连接失败时先检查 401 提示里的账号密码。

## 安全提示

本项目是**个人本地使用**的工具：`config.json` 以明文保存 WebDAV 用户名/密码，请勿把该文件分享给他人（该文件已在 `.gitignore` 中）。
