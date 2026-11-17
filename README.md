# 123 云盘客户端（个人版）

基于 **Go + [walk](https://github.com/lxn/walk)** 的 Windows 桌面客户端：用树形控件浏览自己的 123 云盘目录，目录树缓存在本地，按需同步，避免频繁请求开放平台接口触发 QPS 限制。

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
│ 15:04:05  开始同步 我的云盘/电影（含子目录，QPS=1.0…）        │
│ 15:04:06    ✓ 我的云盘/电影（23 项）                          │
├──────────────────────────────────────────────────────────────┤
│ 目录：我的云盘/电影 | 4 个子目录 / 19 个文件 | 同步于 …       │
└──────────────────────────────────────────────────────────────┘
```

## 功能

- **树形展示**：启动即从本地缓存渲染目录树，离线也能浏览已同步的内容。
- **按需同步**：选中节点点「同步选中目录」，可勾选是否递归子目录。
- **展开即加载**：展开未加载的目录时，先读本地缓存；缓存不存在才请求接口（自动排队限速）。
- **同步全部**：一次性递归同步整棵云盘（有二次确认）。
- **QPS 限速**：所有请求串行经过同一个限速器（默认 1 QPS，可调 0.2~20），遇到 HTTP 429 / 5xx 自动退避重试。
- **停止**：随时取消当前同步，并清空排队中的任务。
- **日志面板**：底部实时显示每个目录的同步结果、缓存命中情况。
- **鉴权**：可直接填 `access_token`，也可填 `clientID/clientSecret` 让程序自动换取并续期 token。
- **回收站过滤**：列表接口返回的回收站条目（`trashed=1`）自动忽略。

## 环境要求

- Windows 10 / 11（amd64）
- Go 1.21+（无需 CGO、无需 MinGW）

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

1. 登录 [123 云盘开放平台](https://www.123pan.com/developer) 控制台，创建应用，获取 **clientID + clientSecret**（或直接拿 access_token）。
2. 启动程序 → 点「设置…」→ 填写 `clientID`/`clientSecret` → 保存。程序会自动换取 access_token 并写入当前目录的 `config.json`。
3. 首次启动会自动同步根目录第一层；之后在树上选中任意目录，点「同步选中目录」即可。

### 操作提示

| 操作 | 效果 |
| --- | --- |
| 点击目录前的展开箭头 | 有缓存则直接离线展开；没有缓存则自动同步这一层（消耗 1 次接口请求） |
| 选中节点 + 「同步选中目录」 | 勾选「包含子目录」时递归同步整棵子树，否则只刷新这一层 |
| 「同步全部」 | 递归同步整个云盘（有二次确认） |
| 双击目录 | 未加载的目录会自动同步一层 |
| 「停止」 | 取消当前同步并清空排队任务 |
| 底部日志面板 | 每个目录的同步结果、缓存命中、限速等待都在这里 |

## 数据存放

| 文件 | 位置 | 说明 |
| --- | --- | --- |
| `config.json` | **程序当前工作目录**（通常就是 exe 所在目录） | access_token / tokenExpiredAt / clientID / clientSecret / QPS / 每页条数 / 服务地址 |
| `tree.json` | `%APPDATA%\123pan-client\` | 目录树缓存（可再生成，所以不放在程序目录） |
| `tree.json.bad` | `%APPDATA%\123pan-client\` | 缓存损坏时的自动备份 |

> `config.json` 内含凭证，已加入 `.gitignore`，不要提交或分享。

缓存结构是「**父目录 ID → 该目录的直接子项列表**」：

- 同步某个子树不会破坏其它目录的缓存；
- 展开目录时可以直接离线读取子项，几乎零开销；
- 每次同步整段替换该目录的列表，天然完成增 / 删 / 改的合并；
- 同时记录每个目录的 `syncedAt`，界面上可看到上次同步时间。

`config.json` 示例：

```json
{
  "accessToken": "eyJhbGciOi...",
  "tokenExpiredAt": "2025-03-23T15:48:37+08:00",
  "clientId": "123456789",
  "clientSecret": "123456789",
  "qps": 1,
  "pageSize": 100,
  "baseUrl": "https://open-api.123pan.com"
}
```

## access_token 自动获取

开放平台文档：<https://123yunpan.yuque.com/org-wiki-123yunpan-muaork/cr6ced/gn1nai4x0v0ry9ki>

只填 `clientId` + `clientSecret`（`accessToken` 留空）即可全自动：

1. 启动时 / 每次同步前检查 `accessToken` 是否缺失、已过期或 **5 分钟内即将过期**；
2. 需要时调用 `POST /api/v1/access_token`（Header: `Platform: open_platform`，Body: `clientID`、`clientSecret`）换取新 token；
3. 把返回的 `accessToken` 与 `expiredAt` 写入 `config.json`（`tokenExpiredAt`），下次启动直接复用；
4. 接口返回 401 / token 无效时，自动丢弃旧 token、重新获取并重试一次；
5. 设置对话框里显示当前 token 状态，也可以点「立即获取 access_token」强制刷新。

同时填写了 `accessToken` 与 `clientId/clientSecret` 时：优先使用本地 token，直到它过期前 5 分钟再自动续期。

`baseUrl` 可以在配置文件里改成自建的反向代理地址（设置界面未暴露该字段）。

## 接口说明

获取文件列表（推荐）：

```
GET {baseUrl}/api/v2/file/list?parentFileId=0&limit=100&lastFileId=...
Header:
  Authorization: Bearer <access_token>
  Platform: open_platform
```

- `parentFileId`：目录 ID，根目录传 `0`
- `limit`：每页条数，最大 100
- `lastFileId`：上一页返回的 `lastFileId`，为 `-1` 表示没有下一页
- 响应 `data.fileList[]`：`fileId / filename / type(0 文件 1 目录) / size / etag / status / parentFileId / category / trashed / createAt / updateAt`

官方文档：<https://123yunpan.yuque.com/org-wiki-123yunpan-muaork/cr6ced/zrip9b0ye81zimv4>

## 代码结构

```
main.go                  程序入口（walk 主窗口由 internal/ui 构建）
app.manifest             Windows manifest（视觉样式 / DPI）
build.bat                一键构建脚本
internal/
  api/api.go             123 云盘 OpenAPI 客户端：文件列表翻页、token 换取、限速、重试
  api/api_test.go        翻页 / 回收站过滤 / token 换取 / 错误处理测试
  config/config.go       配置读写（当前目录的 config.json）与缓存路径
  store/cache.go         本地目录树缓存（原子写、损坏自愈、排序、过滤回收站）
  store/cache_test.go    缓存测试
  ui/app.go              主窗口、按钮 / 展开 / 选中事件、日志与状态栏
  ui/model.go            树节点 Node 与 walk.TreeModel（懒加载、原地刷新）
  ui/syncer.go           同步队列 worker（串行、可取消、去重）
  ui/settings.go         设置对话框
  ui/format.go           文件大小格式化
```

## 设计要点

- **为什么用「父目录 → 子项」缓存**：接口本身按目录拉取，缓存粒度与接口一致，局部同步成本最低。
- **树节点指针稳定**：刷新目录时复用已有 `*Node`，只替换 `children` 切片，然后 `PublishItemsReset(node)`，这样 walk 的 item↔handle 映射不会失效，展开状态也得以保留。
- **懒加载**：`TreeModel.LazyPopulation() = true`，未展开的子树不会插入控件；未同步的文件夹通过 `HasChild() = true` 显示展开箭头。
- **占位子项（重要）**：ComCtl32 的 TreeView 只有在节点「确实有子项」时才允许展开。为了让未同步的文件夹可展开（否则点击展开箭头毫无反应，也就永远不会触发懒加载/自动同步），未加载的文件夹会带一个灰色的占位子项；同步完成后占位子项被真实子项整体替换。
- **限速在 API 层**：`api.Limiter` 保证两次请求之间至少间隔 `1/QPS`，同步 worker 是单 goroutine，所以「手动同步 + 展开自动同步」也不会并发冲击接口。
- **可取消**：每个同步任务一个 `context`，停止按钮会取消当前任务并清空队列；网络请求和限速等待都响应取消。

## 已知限制

- 只读浏览，不含上传 / 下载 / 删除 / 重命名（本项目的目标是「目录树浏览 + 可控同步」）。
- 文件大小、更新时间等元数据来自列表接口，不含秒传/分享等其它字段。
- 大目录树递归同步耗时受 QPS 限制（默认 1 QPS 时，每个目录至少 1 秒），建议先同步一层再逐级展开。

## 安全提示

本项目是**个人本地使用**的工具：`access_token` / `clientSecret` 以明文保存在程序目录下的 `config.json`，请勿把该文件或 token 分享给他人（该文件已在 `.gitignore` 中）。
