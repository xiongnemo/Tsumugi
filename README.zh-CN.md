# Tsumugi

[English](README.md) · **简体中文**

Tsumugi 是用 Go 编写的 Telegram 终端（TUI）客户端。

MVP 使用纯 Go 的 `gotd/td` 作为 MTProto 后端，使用 `tview`/`tcell` 构建终端界面。用户登录与 Bot 登录走不同的认证流程，认证完成后共用同一套 Telegram API 适配层。

## 当前 MVP 功能

- 通过 `gotd/td` 完成用户登录（手机号 / 验证码 / 两步验证）。
- 通过同一后端使用 Bot Token 登录。
- 连接状态、对话列表与规范化应用状态的共享事件管道。
- 基于 SQLite 的本地存储：账号、会话对象、消息、同步元数据、代理配置与设置；媒体预览文件缓存在磁盘。
- 敏感本地数据的应用层加密。Tsumugi 优先将主密钥存入系统钥匙串；若钥匙串不可用，请设置 `TSUMUGI_PASSPHRASE`，以便通过 Argon2id 派生密钥。
- TUI 外壳：文件夹栏、会话列表、可选中的消息区、撰写框、状态/底栏、快捷键、设置中心与代理设置弹窗。
- **全部**文件夹中的全局置顶会话（Saved Messages 及主列表其他置顶项），从 Telegram 同步并带 📌 标记显示在列表顶部。
- Telegram 系统消息（入群退群、修改名称/头像、置顶、清空记录、通话、自动删除计时等）以暗色单行内联显示，并出现在会话列表预览中。尚无专门文案的动作会显示为通用系统消息，而不是被静默丢弃。
- 首次启动引导向导：语言、Telegram 登录模式、API 凭证，以及在凭证不完整时填写手机号 / Bot Token。
- 支持 SOCKS5、HTTP CONNECT 与 Telegram MTProxy。
- TUI 内代理配置管理。来自环境变量的代理会以只读配置项展示，便于区分连接是来自环境变量还是本地数据库。
- 聊天记录加载：优先读本地 DB，打开会话时用 Telegram `messages.getHistory` 刷新，自动补齐缓存中的历史缺口，启动时懒同步（对话元数据 + 可选的近期会话回填）。设置 `TSUMUGI_SYNC_MODE=full` 可恢复旧版启动时全量历史同步。
- 单行状态栏：连接信息（左）、前台网络活动（中）、后台任务（右，仅在有任务时显示）。会话摘要与历史缺口提示显示在消息区标题中。
- 向当前会话发送文本消息，支持从消息操作菜单选择回复目标。
- 撰写建议：`@` 提及、`@inline_bot 查询` 内联 Bot 结果，以及当前会话的 `/` Bot 命令。
- 媒体分类：保留 emoji 的文本、静态贴纸、动画贴纸、视频贴纸、GIF、视频、图片与文件。
- 媒体缓存/打开边界，以及纯 Go 终端半块字符渲染器（用于图片/贴纸缩略图，无需外部 image-to-terminal CLI）。

内联预览使用 Unicode 半块字符与 true-color ANSI，再转换为 `tview` 可用格式。消息详情在模态框绘制后按布局在右侧栏打开预览。支持的静态格式与 Go 解码路径一致（PNG、JPEG、GIF、WebP）。终端内不解码视频容器。在设置（`?` → General）中启用 **inline GIF animation** 可在消息列表中播放已缓存的 GIF/MP4/WebM 预览（Telegram GIF 与 video sticker）。MP4/WebM 解码依赖 `PATH` 中的 **ffmpeg**。这会周期性重绘，在消息较多的会话中可能占用更多 CPU。没有 ffmpeg 时仍然能看到画面：GIF 与 video sticker 会退回渲染 Telegram 自带的 JPEG 缩略图，显示为静态图而不是一行文字标签——只有「动起来」这件事需要 ffmpeg。解码后的动画帧保存在共享的 32 MB 内存 LRU 缓存中；可通过 `TSUMUGI_INLINE_ANIM_CACHE_MB` 调整上限。若 SQLite 中尚未保存该设置，也可在首次启动前设置 `TSUMUGI_INLINE_ANIM=1`。

纯媒体消息（投票、联系人、位置及其他附件类型）在会话列表与消息视图中显示本地化占位符，而非 `(empty message)`。

## 设置

按 **`?`** 打开设置中心。首个版本包含：

- **General**：界面语言（`en` / `zh`）、内联 GIF 动画、发出的消息布局（`transcript` / `im`）
- **Account**：当前登录模式与本地 Telegram 登出
- **Network**：代理配置管理（与 **`P`** 相同界面）

设置保存在本地 SQLite 的 `settings` 表中；语言与内联动画立即生效。引导向导收集的 Telegram 凭证保存在本地，敏感字段受与应用层消息加密相同的保护。更改活动代理配置后仍需重连。

**Account** 页提供非破坏性的 Telegram 登出：仅清除已保存的 Telegram 登录凭证与会话文件，并返回引导/登录界面；本地 SQLite 历史、媒体缓存、设置与代理配置均保留。

翻译文件位于 `internal/i18n/locales/`（`en.json`、`zh.json`）。贡献者可在这些 JSON 中增改字符串而无需改 Go 代码；运行 `go test ./internal/i18n/...` 可校验各语言键名一致。命名规范与新增语言说明见 [`internal/i18n/README.md`](internal/i18n/README.md)。

当 SQLite 中尚未保存对应值时，可使用环境变量作为回退：

```pwsh
$env:TSUMUGI_LOCALE = 'zh'
$env:TSUMUGI_INLINE_ANIM = '1'
$env:TSUMUGI_OUTGOING_LAYOUT = 'im'
```

在消息区按 **`L`** 切换发出消息布局（`transcript` = 左对齐并带 `>` 前缀，`im` = 收到左 / 发出右）。在选中消息上按 **`R`** 查看反应并快速发送 emoji（键 `1`–`8`）。按 **`#`** 可把置顶栏展开为该会话的全部置顶消息列表，`Enter` 跳转，`Esc` 关闭。广播频道帖子显示浏览次数（👁），而非私聊已读回执。

Telegram API 凭证、手机号与 Bot Token 可在首次引导向导中填写，或通过环境变量/CLI 提供。数据库口令仍仅通过环境变量/提示输入，不在设置 UI 中暴露。

## 配置

在 <https://my.telegram.org/apps> 创建 Telegram API 凭证。若缺少必要凭证，Tsumugi 会在 TUI 中打开引导向导，并在配置完成前延迟连接 Telegram。

常用环境变量仍然有效，并会覆盖已保存的引导值：

```pwsh
$env:TSUMUGI_API_ID = '123456'
$env:TSUMUGI_API_HASH = 'your_api_hash'
$env:TSUMUGI_AUTH_MODE = 'user'
$env:TSUMUGI_PHONE = '+15551234567'
```

若系统钥匙串不可用，Tsumugi 会在启动 TUI 前提示输入本地数据库口令。也可提前设置：

```pwsh
$env:TSUMUGI_PASSPHRASE = 'choose-a-long-local-passphrase'
```

内存诊断为可选功能，在工作目录写入 `tsumugi-debug-mem.jsonl`（JSON 行），不污染 TUI 的 stderr。需要堆/性能分析时可配合 pprof 地址：

```pwsh
$env:TSUMUGI_DEBUG_MEM = '1'       # 周期性 MemStats、viewport、缓存与 gap-fill 计数
$env:TSUMUGI_PPROF_ADDR = ':6060'  # 在该地址启用 net/http/pprof
$env:TSUMUGI_INLINE_ANIM_CACHE_MB = '64'
```

Bot 模式：

```pwsh
$env:TSUMUGI_AUTH_MODE = 'bot'
$env:TSUMUGI_BOT_TOKEN = '123456:bot-token'
```

CLI 标志也可提供相同参数：

```pwsh
go run ./cmd/tsumugi --login=user --api-id=123456 --api-hash=your_api_hash --phone=+15551234567
go run ./cmd/tsumugi --login=bot --api-id=123456 --api-hash=your_api_hash --bot-token=123456:bot-token
```

使用 `--config-dir=<path>` 覆盖配置相关目录的存放位置。Tsumugi 目前不读取配置文件。

Bot 模式是 Bot 控制台，而非完整的个人 Telegram 客户端。Telegram 仅推送 Bot 有权接收的会话与更新。

## 代理

可通过 `--proxy` 或环境变量配置代理：

```pwsh
go run ./cmd/tsumugi --proxy=socks5://user:pass@127.0.0.1:1080
go run ./cmd/tsumugi --proxy=http://user:pass@127.0.0.1:8080
go run ./cmd/tsumugi --proxy=mtproxy://0123456789abcdef0123456789abcdef@proxy.example:443
```

环境变量查找顺序：

```text
TSUMUGI_PROXY
TSUMUGI_SOCKS5_PROXY
TSUMUGI_HTTP_PROXY
TSUMUGI_MTPROXY
HTTPS_PROXY
HTTP_PROXY
ALL_PROXY
```

当环境代理生效时，代理设置界面会显示只读的 `Environment` 条目，例如 `Environment: ALL_PROXY -> SOCKS5 127.0.0.1:11085`。UI 标签中会掩码凭证。可编辑的配置保存在本地 SQLite 数据库中。

## 快捷键

- `Tab`：在文件夹、会话列表、消息区与撰写框之间切换焦点
- 消息区 `j`/`k` 或方向键：选择消息
- `Enter`：打开会话、发送撰写内容，或打开选中消息的操作菜单
- 消息区 `PgUp` / `PgDn`：翻页滚动；向上翻页后高亮移到**第一条**可见消息，向下翻页后移到**最后一条**可见消息（鼠标滚轮同理）。已在最顶部时，`PgUp` 还会加载更早历史。内联媒体预览高度稳定，加载预览时撰写框与滚动位置不会跳动。
- `End`：跳转到最新消息，高亮移到最后一条，并清除「下方有新消息」指示
- 向上滚动时，新消息会在消息区标题中计数（例如 `Chat · 3 new`）并显示简短状态行；滚到底部后清除
- 消息列表中，**当天**的时间戳仅显示 `HH:MM`；更早消息带日期（`YYYY-MM-DD HH:MM`），便于跨日阅读
- `i`：聚焦撰写框
- 撰写框中输入 `@` 显示提及建议，`@inline_bot 查询` 显示内联 Bot 结果，`/` 显示当前会话 Bot 命令。建议面板打开时，`Up`/`Down` 切换高亮行，`Tab` 或 `Enter` 接受，`Esc` 关闭面板且不清空已输入文字。也可用鼠标点击行。
- `/`：搜索当前焦点视图
- `D`：下载/缓存选中消息的媒体预览
- `O`：在外部打开选中消息的媒体预览
- `?`：打开设置（General 与 Network）
- `P`：代理设置
- `Esc`：关闭模态框并返回会话列表
- `q` 或 `Ctrl+C`：退出

内联 Bot 结果以缩略图网格展示，而不是列表——GIF Bot 不返回标题，一列一模一样的行等于没法选。每格用纯 Go 半块渲染器画出结果自带的 JPEG 缩略图，**不会**为了预览去下载整个动图。方向键左右在行内移动、上下按整行移动，`Enter` 或 `Tab` 发送当前高亮项，鼠标点击某格即选中。

撰写建议限制：提及与命令面板最多显示 5 条可见条目。需要位置信息的内联 Bot 在本 MVP 中标记为不支持。

## 开发

在受限网络下建议使用 Go 模块代理：

```pwsh
$env:GOPROXY='https://goproxy.cn,direct'; go test ./...
```

构建单文件可执行程序：

```pwsh
$env:GOPROXY='https://goproxy.cn,direct'; New-Item -ItemType Directory -Force -Path .\dist | Out-Null; go build -trimpath -o .\dist\tsumugi.exe .\cmd\tsumugi
```
