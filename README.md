# 123 云盘客户端（WebDAV 版）

基于 **Go + [walk](https://github.com/lxn/walk)** 的 Windows 桌面客户端：用树形控件浏览 WebDAV 上的目录，目录树缓存到本地 SQLite，按需同步。

123 云盘官方 OpenAPI 需要购买「开发者权益包」，所以本项目改为走标准 **WebDAV** 协议。

**123 云盘官方 WebDAV 地址：`https://webdav.123pan.cn/webdav`**（Basic Auth），程序默认就填这个地址；也可以填任意其它 WebDAV 服务（NAS、Alist、自建等）。

```
┌──────────────────────────────────────────────────────────────┐
│ [同步选中目录] [同步全部] [停止]  ☑包含子目录          [设置…] │
├──────────────────────────────────────────────────────────────┤
│ 我的云盘                                                     │
│  ├─ 电影/                                                    │
│  │   ├─ 2024/                                                │
│  │   └─ a.mp4   [1.2 GB]                                     │
│  └─ 文档/                                                     │
├──────────────────────────────────────────────────────────────┤
│ 15:04:05  开始同步 我的云盘/电影（含子目录，QPS=5.0…）        │
│ 15:04:06    ✓ 我的云盘/电影（23 项）                          │
├──────────────────────────────────────────────────────────────┤
│ 目录：我的云盘/电影 | 4 个子目录 / 19 个文件 | 同步于 …       │
└──────────────────────────────────────────────────────────────┘
```

## 功能

- **树形展示**：启动即从本地 SQLite 缓存渲染目录树，断网也能浏览已同步的内容。
- **按需同步**：选中节点点「同步选中目录」，可勾选是否递归子目录。
- **展开即加载**：展开未加载的目录时，先读本地缓存；缓存不存在才发 `PROPFIND`（自动排队限速）。
- **同步全部**：一次性递归同步整棵目录树（有二次确认）。
- **QPS 限速**：所有请求串行经过同一个限速器（默认 5 QPS，可调 0.2~50），遇到 HTTP 429 / 5xx 自动退避重试。
- **停止**：随时取消当前同步，并清空排队中的任务。
- **日志面板**：底部实时显示每个目录的同步结果、缓存命中情况。
- **鉴权**：WebDAV Basic Auth；可勾选忽略 HTTPS 证书错误（自建 / 内网常用）。
- **连接测试**：设置里可以「测试连接」，用当前填写的地址/账号即时验证。
- **本地搜索**：按文件名 / 文件夹名模糊搜索（忽略大小写，空格分隔多关键字 AND），**只查本地 SQLite 缓存，不访问服务器**；双击结果可在目录树中自动定位（同样只用缓存）。

## 环境要求

- Windows 10 / 11（amd64）
- Go 1.24+（无需 CGO、无需 MinGW；SQLite 用纯 Go 驱动 modernc.org/sqlite）

## 构建

```bash
# 1. 生成资源文件：嵌入 manifest（启用 Visual Styles + DPI 感知）
go run github.com/akavel/rsrc@v0.10.2 -manifest app.manifest -o rsrc.syso

# 2. 编译（-H windowsgui 表示不带控制台窗口）
go build -trimpath -ldflags "-H windowsgui" -o 123pan-client.exe .
```

或者直接运行 `build.bat`（会依次执行 gofmt、rsrc、go build）。

> `rsrc.syso` 只是让界面使用通用控件 6.0（现代外观）并支持高 DPI；没有它也能编译运行，只是外观是旧式风格。

## 使用

1. 打开程序 → 「设置…」：WebDAV 地址默认是 `https://webdav.123pan.cn/webdav`（可点「填入 123 云盘地址」恢复）。
2. 填 **用户名 / 密码**（123 云盘账号的 WebDAV 凭证，具体见官方文档；凭证不正确时接口会返回 401）→「测试连接」→ 保存。
3. 首次启动会自动同步根目录第一层；之后在树上选中任意目录，点「同步选中目录」即可。

### 操作提示

| 操作 | 效果 |
| --- | --- |
| 点击目录前的展开箭头 | 有缓存则直接离线展开；没有缓存则自动同步这一层（1 次 PROPFIND） |
| 选中节点 + 「同步选中目录」 | 勾选「包含子目录」时递归同步整棵子树，否则只刷新这一层 |
| 「同步全部」 | 递归同步整个目录树（有二次确认） |
| 双击目录 | 未加载的目录会自动同步一层 |
| 「停止」 | 取消当前同步并清空排队任务 |
| 搜索框 + 回车 /「搜索」 | 只在本地缓存里模糊搜索名称（忽略大小写，可空格分隔多个关键字） |
| 双击搜索结果 | 在目录树中逐级展开并选中该条目（只用缓存，不联网） |
| 「清除」 | 清空搜索并切回目录树 |
| 底部日志面板 | 每个目录的同步结果、缓存命中、限速等待都在这里 |

## 数据存放

所有数据都在**程序当前工作目录**（通常就是 exe 所在目录），整个目录可以直接拷贝带走：

| 文件 | 说明 |
| --- | --- |
| `config.json` | WebDAV 地址 / 用户名 / 密码 / 是否忽略证书 / QPS |
| `tree.db` | SQLite 目录树缓存（含 `tree.db-wal`、`tree.db-shm`） |

> `config.json` 内含密码，已和 `tree.db*` 一起加入 `.gitignore`，不要提交或分享。

`config.json` 示例：

```json
{
  "webdavUrl": "https://dav.example.com/dav/123pan",
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
    sort_name    TEXT NOT NULL,   -- 小写名称，用于不分大小写排序
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
main.go                  程序入口（walk 主窗口由 internal/ui 构建）
app.manifest             Windows manifest（视觉样式 / DPI）
build.bat                一键构建脚本
internal/
  config/config.go       配置读写（当前目录 config.json、tree.db 路径）
  store/db.go            SQLite 目录树缓存（modernc.org/sqlite，纯 Go）
  store/db_test.go       缓存测试
  webdav/client.go       WebDAV 客户端（PROPFIND、Basic Auth、限速、重试、重定向）
  webdav/client_test.go  WebDAV 解析/鉴权/重试/重定向测试
  ui/app.go              主窗口、按钮 / 展开 / 选中事件、日志与状态栏
  ui/model.go            树节点 Node 与 walk.TreeModel（懒加载、原地刷新）
  ui/syncer.go           同步队列 worker（串行、可取消、去重）
  ui/search.go           搜索结果表模型（walk.TableModel）
  ui/settings.go         设置对话框（WebDAV 地址/账号/连接测试）
  ui/format.go           文件大小 / 时间格式化
```

## 设计要点

- **为什么用「父目录 → 子项」缓存**：WebDAV 只能按目录列（Depth 1），缓存粒度与之对齐，局部同步成本最低。
- **树节点指针稳定**：刷新目录时按名称复用已有 `*Node`，只替换 `children` 切片，然后 `PublishItemsReset(node)`，walk 的 item↔handle 映射和展开状态都不会失效。
- **懒加载 + 占位子项**：`TreeModel.LazyPopulation() = true`，未展开的子树不插入控件；而未同步的文件夹会带一个灰色占位子项——因为 ComCtl32 的 TreeView 只有节点「确实有子项」时才允许展开，否则点击展开箭头毫无反应，懒加载/自动同步永远不会触发。同步完成后占位子项被真实子项整体替换。
- **限速在客户端层**：`webdav.Limiter` 保证两次请求之间至少间隔 `1/QPS`，同步 worker 是单 goroutine，所以「手动同步 + 展开自动同步」也不会并发冲击服务端。
- **可取消**：每个同步任务一个 `context`，停止按钮会取消当前任务并清空队列；网络请求和限速等待都响应取消。
- **SQLite 单连接**：`MaxOpenConns(1)` + WAL，避免并发写锁冲突；同步过程中定期 `wal_checkpoint`，便于拷贝数据库。
- **本地搜索**：`SELECT ... WHERE sort_name LIKE ? ESCAPE '\'`，`sort_name` 是 Go `strings.ToLower` 后的小写名称（Unicode 友好）；用户输入里的 `%`/`_` 会被转义，不会变成通配符。结果按「完全匹配 > 前缀 > 包含」排序，其次名称更短、文件夹优先。整个过程只读 SQLite，不产生任何 WebDAV 请求。

## 已知限制

- 只读浏览，不含上传 / 下载 / 删除 / 重命名（本项目的目标是「目录树浏览 + 可控同步」）。
- 文件大小、修改时间等元数据来自 PROPFIND，取决于服务端是否提供。
- 大目录树递归同步耗时受 QPS 限制（默认 5 QPS），建议先同步一层再逐级展开。
- 搜索范围仅限**已同步到本地缓存**的条目（未同步的目录不在结果里），这也是「不与服务器交互」的代价。
- 123 云盘官方 WebDAV（`https://webdav.123pan.cn/webdav`）可能需要对应会员/权限，且凭证以官方 WebDAV 文档为准；连接失败时先检查 401 提示里的账号密码。

## 安全提示

本项目是**个人本地使用**的工具：`config.json` 以明文保存 WebDAV 用户名/密码，请勿把该文件分享给他人（该文件已在 `.gitignore` 中）。
