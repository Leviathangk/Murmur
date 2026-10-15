# 开发文档

Murmur 用 [MyGo](https://mygo.egoist.dev/docs) 的原生 UI 写成：纯 Go、无 cgo、不依赖 WebView。

## 开发

需要 Go 1.27 及以上。

```sh
go tool mygo dev      # 开发模式，改代码自动重启
go test ./...         # 测试（界面测试不需要窗口）
MURMUR_SNAPSHOTS=1 go test .   # 顺便把界面渲染成 testdata/*.png
```

## 项目结构

```
main.go       窗口和入口
app.go        应用状态：发送、流式接收、停止、重新生成
view.go       主界面：侧边栏、消息列表、输入框
settings.go   设置面板：模型管理、系统提示词、代理
store.go      设置和对话的读写
attach.go     附件：选择、拖入、粘贴图片和文件
image.go      图片：缩放和保存
document.go   文件：读出文字、保存，消息里的卡片和预览
search.go     搜索对话
export.go     导出对话为 Markdown
presets.go    服务商预设
tray.go       托盘图标，关掉窗口后留在后台
notify.go     回复完成时发系统通知，点通知打开对话
llm/          模型调用：OpenAI 兼容（SSE）和 Anthropic（官方 SDK）
extract/      读出 PDF、Word、Excel、PPT 和文本文件里的文字
demo.go       演示构建（-tags demo），连本地模型自动跑一遍并截图
```

## 深度思考的实现

OpenAI 兼容协议下，各家打开思考的方式不一样。「思考参数」选「自动识别」时按接口地址判断，发送的内容如下：

| 思考参数 | 适用于 | 开 | 关 |
|---|---|---|---|
| `reasoning_effort` | OpenAI、Ollama、Gemini | `"reasoning_effort": "medium"` | 不发送 |
| `enable_thinking` | 通义千问、硅基流动 | `"enable_thinking": true` | `false` |
| `thinking.type` | 智谱 GLM、DeepSeek、豆包 | `"thinking": {"type": "enabled"}` | `disabled` |
| `reasoning` | OpenRouter | `"reasoning": {"effort": "medium"}` | 不发送 |
| 不发送 | Kimi K2 Thinking、DeepSeek Reasoner 等自带思考的模型 | — | — |

思考内容从响应的 `reasoning_content` / `reasoning` 字段读取，模型如果把思考写在正文的 `<think>…</think>` 里也会被识别出来。

Anthropic 协议用官方 Go SDK：Claude 4.6 及以后的模型用自适应思考（`{"type": "adaptive", "display": "summarized"}`，思考摘要实时显示），更早的 Claude 和其他兼容 Anthropic 协议的服务用思考预算（`budget_tokens`）。

## Token 用量的实现

- OpenAI 兼容协议的请求带 `"stream_options": {"include_usage": true}`，从最后一个分块的 `usage` 读 `prompt_tokens` / `completion_tokens`；Kimi 放在 `choices[0].usage` 里，有的服务每个分块都带到目前为止的用量，都取最后一次
- 不认 `stream_options` 的服务：返回 4xx、错误信息里提到 `stream_options` 时，去掉这个参数重发一次，这条回复就没有用量
- Anthropic 协议从 `message_start` 读起，`message_delta` 里的是到目前为止的总数，覆盖之前的值，没给的字段保留 `message_start` 的；输入算上缓存：`input_tokens` + `cache_creation_input_tokens` + `cache_read_input_tokens`
- 回复正常结束后才发出用量（`llm.Event.Usage`），停止、出错的回复没有用量，界面上不显示；用量存在 `chats.json` 消息的 `input_tokens` / `output_tokens` 里，没有这两个字段的旧对话也不显示

## 图片的实现

- 选择（系统文件对话框）、拖入对话区、粘贴（截图，或在访达 / 资源管理器里复制的图片文件）三种方式添加，读取和缩放都在后台 goroutine 里做
- 发送前统一处理：长边超过 2048 像素、超过 3 MB、带 EXIF 旋转的 JPEG，或 BMP 等不是各家都收的格式，重新绘制后编码成 JPEG（有透明的编码成 PNG）；其余原样发送
- 图片存在数据目录的 `images/` 下，每张一个文件，`chats.json` 里只记文件名、MIME 类型和尺寸，免得每次保存对话都重写几 MB；删除对话时一起删除，文件丢失时显示「图片已丢失」并且不再发送
- OpenAI 兼容协议把带图的消息写成 `content` 数组：文字一段，每张图一段 `image_url`（`data:` URL）；纯文字消息仍然是字符串，兼容不认数组的服务商
- Anthropic 协议每张图一个 base64 `image` 块，放在文字前面

## 文件的实现

- 文件在本机读成文字后随消息发送，不用各家的文件接口：DeepSeek、Kimi、通义千问等的 OpenAI 兼容接口都不收 PDF，读成文字后任何模型都能读，用量也看得见
- 添加时在后台 goroutine 里读，按扩展名分：图片走图片的处理；`.pdf`、`.docx`、`.xlsx`、`.pptx` 各自解析；其余当文本读，没有 BOM 又不是有效 UTF-8 的按 GB18030 解码（中文 Windows 的记事本、Excel 存的 CSV），有 NUL 字节或大量控制字符的当作二进制拒绝。`.doc`、`.xls`、`.ppt`、WPS、iWork 格式提示另存
- PDF 用 [ledongthuc/pdf](https://github.com/ledongthuc/pdf) 解析对象、流和加密（RC4、AES-128，空用户密码；AES-256 不支持，提示另存；它只认 `%PDF-1.x` 的文件头，PDF 2.0 先把文件头换成 1.7 再读），文字和排版由 `extract/pdf.go` 自己读：
  - 字体有 ToUnicode 时以它为准。这个库在字体有 `Differences` 编码时不看 ToUnicode，macOS、Chrome 生成的 PDF 都是这样（字形名为 `/gid123`），读出来是乱码
  - 没有 ToUnicode 的：简单字体用库的编码表（WinAnsi、MacRoman、Differences 字形名）；CID 字体认 Adobe 的 `UniGB-UCS2`、`UTF16` 和 `GBK-EUC` 等 CMap；Identity 编码又没有 ToUnicode 的读不出字
  - 按文字矩阵和字宽算出每个字的位置：换了基线就换行，下移超过两行算新段落；和上一个字之间空出 0.15 个字宽以上就补空格，中日韩文字之间要空出一个字宽（两端对齐的中文会拉开字距）；没有 Unicode 的字形（有的 PDF 不给空格映射）不输出，只留下空隙
  - 按页面画的顺序读，不按坐标重排；表单 XObject 里的文字也读（最多嵌套 8 层）；某一页解析出错时保留这页已读到的字，继续读下一页
  - macOS 和 Chrome 的字体会把「文」「长」等映射到康熙部首（`⽂`、`⻓`），按 Unicode 的 EquivalentUnifiedIdeograph 表换回统一汉字（`extract/radicals.go`）
- Word 读 `word/document.xml`：标题（按 `styles.xml` 里名为 `heading N` 的样式，中文 Word 的样式 ID 是 `1`、`2`）写成 `#`，列表项写成 `- `，表格写成 Markdown 表格；文本框只读一遍（跳过 `mc:Fallback`），删除的修订、域代码不读
- Excel 按 `workbook.xml` 的顺序读可见的工作表，每个写成 `## 表名` 加 CSV；数字按 15 位有效数字输出（去掉 0.1+0.2 的尾巴），日期格式（内置的和含 y/m/d/h/s 的自定义格式）的单元格换成日期；Office 文件解压的每个部分最多读 512 MB，防止压缩炸弹
- PPT 按 `presentation.xml` 里的放映顺序读每页的文字，写在「第 N 页」下
- 文字统一换行、去掉控制字符和行尾空格，连续空行并成一个；最多保留 10 万字（够 128K 上下文的模型读），读到上限就停止解析，超出的在发送时注明「只发送了开头的 100000 字」
- 读出的文字存在数据目录的 `documents/` 下，每个文件一份，`chats.json` 只记文件名、字数和是否截断；原文件不复制。删除对话时一起删除，文字丢失时卡片显示「文件已丢失」并且不再发送
- 发送时每个文件包在 `<file name="…">…</file>` 里放在问题前面，两种协议都是普通文本：OpenAI 兼容协议没有图片时 `content` 仍是字符串，有图片时和问题合成一段 `text`；Anthropic 协议是一个 `text` 块，放在图片后面
- 只有文件没有文字的消息，对话标题取第一个文件名
- 预览在后台读文字，只显示前 2 万字

## 搜索和导出的实现

- 搜索在内存里做：查询按空格拆成词，不分大小写，每个词都出现在标题或某条消息的正文里才算匹配；思考过程不参与搜索，因为它平时是收起的
- 结果缓存到查询变化或对话保存（`saveChats`）为止，不会每帧重新搜索；流式回复中的内容要等回复结束、对话保存后才能搜到
- 结果显示第一个不在标题里的词所在的消息，命中处前几个字起的一段原文（去掉 `**` 和反引号）；打开结果时消息列表滚动到那条消息
- 重命名不改 `updated`，对话在侧边栏里的位置不变；标题留空不保存
- 导出时先在主线程把对话复制一份，再在后台弹出保存对话框、写文件，流式回复中导出的是当时的内容
- 图片按顺序编号复制到 `<文件名>.assets/`，链接用尖括号包住（路径里可以有空格），`#`、`?`、`%` 等会被当成 URL 语法的字符转义成 `%XX`；文件已丢失的图片写成「图片已丢失」
- 发送的文件把读出的文字写到同一个文件夹：文本文件用原名，其他加上 `.txt`（`报告.pdf.txt`），重名的加「 (2)」；写成「附件：[报告.pdf](…)」，文字已丢失的写成「文件已丢失」
- 用户消息在应用里按原文显示，导出时每行末尾加硬换行（代码块里不加），换行不会被合并；中途停止的回复如果停在代码块里，导出时补上结束的围栏，免得后面的内容都变成代码
- Windows 的保存对话框不会自动补扩展名：不是 `.md` / `.markdown` 结尾时补上 `.md`，补完的文件已存在时再问一次是否替换；默认文件名避开 `CON`、`NUL` 等 Windows 保留名

## 后台运行的实现

- 窗口的 `OnClose` 拦下关闭，改为隐藏窗口；退出时（`OnBeforeQuit` 之后）不再拦，窗口照常关闭，进程退出
- 托盘图标在 macOS 是菜单栏里的模板图（`resources/tray-macos.png`，黑色透明底，系统按菜单栏明暗着色），单击、右键都弹出菜单；在 Windows 是通知区域里的彩色应用图标（`resources/tray-windows.png`），单击打开窗口、右键弹出菜单，和那里的习惯一致
- 菜单只有「打开 Murmur」和「退出 Murmur」：退出走 MyGo 的退出流程（`RoleQuit`），和 ⌘Q 一样
- 窗口隐藏后，macOS 点程序坞图标（`OnActivate`）也会把窗口叫回来；最小化的窗口先还原再显示
- macOS 全屏时直接隐藏会留下一个空白的全屏桌面，所以先退出全屏，等 `OnLeaveFullScreen` 再隐藏
- 启动时 `RequestSingleInstanceLock`，再次启动的进程把命令行交给已经在运行的那个后退出，后者显示窗口（`OnSecondInstance`），不会出现两个托盘图标
- 托盘创建失败时（如 Linux 没有 AppIndicator）不拦关闭，关掉窗口就退出，免得窗口再也打不开

## 通知的实现

- 回复结束时（正常结束或出错；点「停止」的不算，用户就在跟前）调用 `a.notify`。窗口有焦点（`Window.IsFocused`）时不发，回复就在眼前；窗口隐藏、最小化或在别的应用后面都发
- 标题是对话标题，副标题是模型名（Windows 的气泡通知没有副标题，MyGo 把它放在正文第一行），正文是回复开头 100 个字，去掉 `**`、反引号和行首的 `#`、`>`，换行合成空格；出错时是「回复失败：」加错误信息
- 通知 ID 用对话 ID：同一个对话的新通知替换旧的。点击时 `App.OnNotificationClick` 拿到 ID，打开对话、滚到末尾、显示窗口
- 这个回调在 `App.Run` 之前注册：macOS 上 Murmur 已经退出时，点通知中心里留下的通知会启动它，这次点击在窗口创建之前就送到，直接把对话设为当前，窗口打开时显示的就是它
- `Notification.Show` 在 macOS 上第一次会等用户回答是否允许，所以在 goroutine 里调用；用户不允许（`ErrNotificationsDenied`）不记日志，其他错误记一行日志
- 窗口获得焦点（`OnFocus`）时调用 `mygo.ClearNotifications()`：回复已经在窗口里看到了，不再留在通知中心
- macOS 只有打包的应用能发通知，`go tool mygo dev` 和 `mygo build` 出来的都是；直接 `go run .` 发不出，日志里会有 `no notification`
- 开关存在 `settings.json` 的 `notify` 字段，没有这个字段的旧设置当作打开

## 打包

MyGo 不需要 cgo，一台 Mac 就能打出所有平台的包：

```sh
go tool mygo build -platform darwin/universal,windows/amd64,windows/arm64
```

产物在 `build/` 下：

- `darwin-universal/Murmur 0.1.0.dmg`：macOS 安装镜像（Intel 和 Apple 芯片通用，macOS 12+）
- `windows-amd64/Murmur Setup 0.1.0 amd64.exe`：Windows 安装程序（Windows 10/11），同目录的 `Murmur.exe` 是免安装版
- `windows-arm64/`：ARM 版 Windows

生成 Windows 安装程序需要 NSIS（`brew install makensis`），没装的话只会输出免安装的 `Murmur.exe`。

## 发布版本

推一个版本标签，GitHub Actions（`.github/workflows/release.yml`）会跑测试、打包，并把安装包挂到同名的 GitHub Release 上：

```sh
git tag v0.2.0
git push origin v0.2.0
```

- 版本号以标签为准（去掉开头的 `v`），构建前写进 `mygo.json`，安装包文件名和设置页里显示的版本都跟着变，不用手动改
- 标签带 `-` 的（如 `v0.2.0-beta.1`）发成预发布版本
- 也可以先在 GitHub 网页上新建 Release 并创建标签，CI 会把安装包补传到这个 Release
- Release 附件：`Murmur-<版本>-macos-universal.dmg`、`Murmur-Setup-<版本>-windows-{amd64,arm64}.exe`、`Murmur-<版本>-windows-{amd64,arm64}-portable.zip`（免安装版）和 `SHA256SUMS.txt`
- 在 Actions 页手动运行这个 workflow 只打包、不发布，安装包在运行记录的 Artifacts 里

## 签名

现在的包没有正式签名（macOS 是 ad-hoc 签名），用户第一次打开需要手动放行，见 README 的「下载安装」。

正式分发时在 `mygo.json` 里配置 `macos.signingIdentity` / `macos.notarize` 和 `windows.certificate`，详见 [MyGo 打包文档](https://mygo.egoist.dev/docs/distribution)。
