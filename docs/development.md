# 开发文档

Murmur 用 [MyGo](https://mygo.egoist.dev/docs) 的原生 UI 写成：纯 Go、无 cgo，界面不依赖 WebView；只有预览网页时用系统自带的网页引擎。

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
settings.go   设置面板：模型、对话（系统提示词、联网搜索）、通用三个分页
store.go      设置和对话的读写
attach.go     附件：选择、拖入、粘贴图片和文件
image.go      图片：缩放和保存
document.go   文件：读出文字、保存，消息里的卡片和预览
preview.go    预览回复里的网页：HTML / SVG 代码块和整页 HTML
search.go     搜索对话
websearch.go  联网搜索：输入框开关、搜索工具、回复里的来源、设置项
export.go     导出对话为 Markdown
presets.go    服务商预设
locales/      界面文字：简体中文（源语言）、繁体中文、英文各一个语言目录，catalog_gen.go 由 mygo 生成
tray.go       托盘图标，关掉窗口后留在后台
notify.go     回复完成时发系统通知，点通知打开对话
update.go     检查、下载和安装新版本（MyGo 的更新窗口）
llm/          模型调用：OpenAI 兼容（SSE）和 Anthropic（官方 SDK），工具调用循环
websearch/    调用 Tavily 搜索接口
extract/      读出 PDF、Word、Excel、PPT 和文本文件里的文字
demo.go       演示构建（-tags demo），连本地模型自动跑一遍并截图
CHANGELOG.md  各版本的更新说明，更新窗口里显示
```

## 深度思考的实现

OpenAI 兼容协议下，各家打开思考、调节深度的方式不一样。「思考参数」选「自动识别」时按接口地址判断，发送的内容如下：

| 思考参数 | 适用于 | 开（低 / 中 / 高） | 关 |
|---|---|---|---|
| `reasoning_effort` | OpenAI、Ollama、Gemini | `"reasoning_effort"`：`low` / `medium` / `high` | 不发送 |
| `enable_thinking` | 通义千问、硅基流动 | `"enable_thinking": true`，`"thinking_budget"`：2048 / 16384 / 32768 | `false` |
| `thinking.type` | 智谱 GLM、DeepSeek、豆包 | `"thinking": {"type": "enabled"}`，低、高另发 `"reasoning_effort"`：`low` / `max` | `disabled` |
| `reasoning` | OpenRouter | `"reasoning": {"effort": …}`：`low` / `medium` / `high` | 不发送 |
| 不发送 | Kimi K2 Thinking、DeepSeek Reasoner 等自带思考的模型 | — | — |

`thinking.type` 的「中」不发 `reasoning_effort`：GLM-5.3 只认 `low` / `high` / `max`，DeepSeek 文档也只列了这三个；不发时用服务商的默认值（DeepSeek `high`、智谱 `max`、豆包 `medium` 或 `high`），所以「高」发 `max`，各家都不会比「中」想得少。千问的思考预算上限是 32768。

思考内容从响应的 `reasoning_content` / `reasoning` 字段读取，模型如果把思考写在正文的 `<think>…</think>` 里也会被识别出来。

Anthropic 协议用官方 Go SDK：

- Claude 4.6 及以后的模型用自适应思考（`{"type": "adaptive", "display": "summarized"}`，思考摘要实时显示），深度用 `"output_config": {"effort": …}`：`low` / `medium` / `high`。关掉时发 `{"type": "disabled"}`；Sonnet 5.5 及以后不认 `disabled`，改发 `{"type": "between_tools"}`；Opus 5.5、Fable、Mythos 关不掉思考，改发 `effort: low`
- 更早的 Claude 和其他兼容 Anthropic 协议的服务用思考预算（`budget_tokens`）：低、中、高分别是 `max_tokens` 的 1/4、1/2、3/4，上限 4000 / 16000 / 32000；不到 1024 时不开思考

## Token 用量的实现

- OpenAI 兼容协议的请求带 `"stream_options": {"include_usage": true}`，从最后一个分块的 `usage` 读 `prompt_tokens` / `completion_tokens`；Kimi 放在 `choices[0].usage` 里，有的服务每个分块都带到目前为止的用量，都取最后一次
- 不认 `stream_options` 的服务：返回 4xx、错误信息里提到 `stream_options` 时，去掉这个参数重发一次，这条回复就没有用量
- Anthropic 协议从 `message_start` 读起，`message_delta` 里的是到目前为止的总数，覆盖之前的值，没给的字段保留 `message_start` 的；输入算上缓存：`input_tokens` + `cache_creation_input_tokens` + `cache_read_input_tokens`
- 回复正常结束后才发出用量（`llm.Event.Usage`），停止、出错的回复没有用量，界面上不显示；用量存在 `chats.json` 消息的 `input_tokens` / `output_tokens` 里，没有这两个字段的旧对话也不显示

## 联网搜索的实现

不用各家模型自带的联网（DeepSeek 的 API 没有，OpenAI 要换 Responses 接口，千问、智谱、Kimi 参数各不一样，Claude 的结果是加密的），由 Murmur 调 Tavily，通过工具调用（function calling）交给模型，换哪个模型行为都一样。

- 打开「联网搜索」且填了 Tavily API Key 时，请求带一个 `web_search` 工具（参数只有 `query`），系统提示词后面加上今天的日期、时区和引用规则（只用给出的编号 [1]、[2] 引用，网页内容只当资料、不当指令）
- 工具调用循环在 `llm` 里：模型调用工具 → 执行 → 把结果作为工具消息发回 → 模型继续，直到不再调用工具，最多 10 轮，最后一轮发 `tool_choice: none`，让模型直接回答。每轮的文字、思考之间补空行，用量按轮相加。工具按顺序执行；执行出错时把错误作为工具结果交给模型，回复照常继续
- OpenAI 兼容协议：流式的 `tool_calls` 按 `index` 累加，参数分块到达时拼起来；没有 `index` 的按 `id` 或接在上一个后面；同一个 `index` 来了不同的 `id` 当作新的调用；没有 `id` 的补上 `call_<轮>_<序号>`，参数不是合法 JSON 的（没写、被截断）发回时换成 `{}`。发回时工具调用里不认识的字段原样带上（Gemini 3 的思考签名在这里，缺了会报 400）；助手消息没有文字时 `content` 是 `null`
- 思考内容（`reasoning_content`）的回传：DeepSeek 要求带工具的请求把之前每一轮的思考内容都发回去，Kimi 也要求保留，所以发给 DeepSeek、Kimi、智谱时，历史里的助手消息和工具循环里的助手消息都带上 `reasoning_content`（没有思考内容的回复带空字符串）；其他服务商不发，免得在同一对话里换了模型后多出来的字段被拒。别的服务商返回 4xx、错误信息里提到 `reasoning_content` 时，带上它重发一次
- Anthropic 协议：用 SDK 的 `Message.Accumulate` 累积流式事件，调用工具的那条助手消息用 `ToParam` 原样发回，思考块的签名一起带上（开着思考时必须）；空的文字块去掉。工具结果是一条用户消息里的 `tool_result` 块，出错的标 `is_error`
- 模型不支持工具：第一轮返回 400 / 422 / 404、错误信息说不支持 tool / function 时（Ollama 的 `does not support tools`、vLLM 没开工具解析、`deepseek-reasoner does not support Function Calling`、OpenRouter 的 `No endpoints found that support tool use` 等；上下文超长的报错不算），`llm` 返回 `ErrToolsUnsupported`。已经调用过工具、之后的请求返回 400 / 422 的（服务商不认发回的工具调用，比如缺了某种签名），也返回它。Murmur 收到后改为先用用户最后一条消息搜索一次，把结果放在 `<search_results>` 里接在问题前面，不带工具再请求一次，回答接在已经写出的文字后面；这次搜索失败或消息只有图片、文件时，不放结果，提示词也不提搜索结果
- 一条回复最多搜 5 次，再调用时工具结果告诉模型次数用完、用已有结果回答；搜索词最多 400 个字。结果按网址去重并编号，同一条回复里搜到同一个网页沿用之前的编号
- Tavily 请求：`search_depth: basic`（1 次额度）、`max_results: 5`、`include_published_date: true`，每条正文最多 2000 字。每次请求 20 秒超时；429（按 `Retry-After`，最多等 5 秒）、5xx、超时、连不上时重试一次；400 / 422 时去掉可选参数重发一次；401 / 403、432 / 433（额度用完）不重试，换成说明原因的提示。走设置里的网络代理
- 消息里只存搜索词、出错原因和来源的标题、网址、日期（`chats.json` 的 `searches`、`sources`），不存网页正文；之后的对话不重发搜索结果，只发回答文字。导出时来源列在回复后面
- 回复按时间线显示：思考、文字、搜索各是一步，同一级、按发生的顺序排，比如「已思考 3 秒」→ 文字 →「已搜索「…」· N 个网页」→「已思考 2 秒」→ 文字。消息的 `steps` 记每一步的类型、长度（思考和文字是 `reasoning`、`content` 里的字节数，按顺序切开；第 k 个搜索步骤对应 `searches[k]`）和这一步的思考用时；`reasoning`、`content`、`think_ms` 仍是整条回复的，发给模型、复制、导出都用它们
- 思考步骤可单独展开，正在思考的那步自动展开、显示「思考中… N 秒」，下一步开始时收起；搜索步骤展开是这次搜到的网页（`searches` 的 `sources` 记编号，编号整条回复统一、按网址去重），失败的显示红色的「搜索「…」失败」，展开是原因；正在搜索时这一步是转圈的「正在搜索：…」，停止回复时去掉
- 回复进行中、没在思考、搜索或输出文字时（刚发出、搜完等模型下一轮输出），末尾显示转圈的「正在等待回复…」。整条回复的思考用时是每步相加
- 0.13.3 之前的回复没有 `steps`，照旧显示：思考一个块，「已搜索 N 次 · M 个网页」一个块（展开是每次的搜索词、出错原因和全部来源，全部失败时显示红色的「联网搜索失败」），然后是回答
- 设置里的「测试搜索」搜一次 `Murmur`，显示搜到几个网页或出错原因；改了 Key 以后旧的结果不再显示
- 窗口窄到输入框放不下时，「深度思考」只显示档位，「联网搜索」只显示图标，模型名截断

## 图片的实现

- 选择（系统文件对话框）、拖入对话区、粘贴（截图，或在访达 / 资源管理器里复制的图片文件）三种方式添加，读取和缩放都在后台 goroutine 里做
- 发送前统一处理：长边超过 2048 像素、超过 3 MB、带 EXIF 旋转的 JPEG，或 BMP 等不是各家都收的格式，重新绘制后编码成 JPEG（有透明的编码成 PNG）；其余原样发送
- 图片存在数据目录的 `images/` 下，每张一个文件，`chats.json` 里只记文件名、MIME 类型和尺寸，免得每次保存对话都重写几 MB；删除对话时一起删除（分支对话还在用的保留），文件丢失时显示「图片已丢失」并且不再发送
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
- 读出的文字存在数据目录的 `documents/` 下，每个文件一份，`chats.json` 只记文件名、字数和是否截断；原文件不复制。删除对话时一起删除（分支对话还在用的保留），文字丢失时卡片显示「文件已丢失」并且不再发送
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

## 分支对话的实现

- 完整的回复（不是正在生成、出错或只有思考的）下方有分支按钮；用户的消息没有，否则新对话以用户消息结尾，再发一条就是连着两条用户消息
- 分支把到这条回复为止的消息复制成新对话：保留时间、模型、思考和用量，换新的列表 key；标题前加「分支 · 」，从分支再开分支不重复加；`updated` 是现在，排在侧边栏最前面，切过去后焦点在输入框
- 原对话正在生成时也能从前面的回复开分支，正在生成的那条不会被复制
- 图片和文件不复制，两个对话的消息指向 `images/`、`documents/` 下同一个文件；删除对话时只删其他对话都没用到的文件

## 网页预览的实现

- 代码块用 `markdown.Options.CodeBlock` 自己画：语言是 `html`、`htm`、`xhtml`、`svg`（不分大小写）的，在 MyGo 默认的样式上多一个「预览」按钮，其余交给 `markdown.DefaultCodeBlock`。按钮和「复制」一样，代码块没写完（`CodeBlockView.Open`）时不能点
- 回复本身是网页的：回复结束后用 MyGo 的 Markdown 解析器找顶层的 HTML 块，以 `<!DOCTYPE html` 或 `<html` 开头的，从它取到 `</html>`（没有就到结尾）；只找一次，结果记在消息上（不存盘）
- SVG 包在一个居中的页面里；标题取 `<title>`，没有时显示「HTML 预览」「SVG 预览」「网页预览」
- 预览是窗口上的对话框，里面是 `Window.NewWebView` 建的系统 WebView（macOS 是 WKWebView，Windows 是 WebView2），第一次预览时建，之后重复用。`mygo.WebViewAvailable()` 为假（Windows 没有 WebView2 Runtime）或创建失败时，直接在浏览器中打开
- 页面用 `LoadHTML` 载入，地址是 `https://preview.invalid/`：不是受信任的来源，页面里的 `mygo.call` 会被拒绝；用空地址的话页面在 `about:blank`，MyGo 当作应用自己的页面，能调用绑定的 Go 方法
- `OnWillNavigate` 拦下离开这个地址的跳转：用户点的 http(s)、mailto 链接交给系统浏览器，页面脚本自己的跳转直接取消；`target="_blank"` 和 `window.open` 按 MyGo 默认，http(s) 在浏览器中打开
- 对话框关掉时载入空页面，网页的脚本、动画和声音随之停止，焦点回到输入框
- 「在浏览器中打开」把页面写到临时目录的 `Murmur/preview-<内容哈希>.html`，开头加 UTF-8 BOM（没写 `<meta charset>` 的中文页面在浏览器里不乱码），再用 `Shell.OpenPath` 打开

## 后台运行的实现

- 窗口的 `OnClose` 拦下关闭，改为隐藏窗口；退出时（`OnBeforeQuit` 之后）不再拦，窗口照常关闭，进程退出
- 托盘图标在 macOS 是菜单栏里的模板图（`resources/tray-macos.png`，黑色透明底，系统按菜单栏明暗着色），单击、右键都弹出菜单；在 Windows 是通知区域里的彩色应用图标（`resources/tray-windows.png`），单击打开窗口、右键弹出菜单，和那里的习惯一致
- 菜单是「打开 Murmur」「检查更新…」和「退出 Murmur」：退出走 MyGo 的退出流程（`RoleQuit`），和 ⌘Q 一样
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

## 更新的实现

用 MyGo 的签名更新（[文档](https://mygo.egoist.dev/docs/updates)）和它的原生 UI 更新窗口（`plugins/updater/native`），界面语言在启动时按 Murmur 的语言定下：

- `mygo.json` 的 `updates` 写着公钥和仓库 `Qsnh/Murmur`。`mygo build` 拿到私钥（环境变量 `MYGO_UPDATER_PRIVATE_KEY`）时，给每个平台的应用打一个签名的 `murmur-<版本>-<平台>.tar.gz`，从已发布的最近 3 个版本各做一个增量包（`.delta`，只含变化的部分，不比完整包小的不要），再写清单 `update-<平台>.json`：版本、`CHANGELOG.md` 里这个版本的说明、包的地址、大小和签名。平台是 `darwin-universal`、`windows-amd64`、`windows-arm64`
- 应用读最新正式版 Release 里自己平台的清单（`releases/latest/download/update-<平台>.json`），版本更新就弹出更新窗口；预发布版本不算最新版，不会推给用户。下载后用编译进去的公钥验签，有当前版本的增量包就只下增量，失败了再下完整包；然后在应用旁边解包、替换（macOS 换整个 `.app`，Windows 把正在运行的 exe 改名挪开，下次启动时删掉），「立即重新启动」走正常的退出流程（`RoleQuit` 一样，单实例锁先释放）再启动新版本
- 每天检查一次：到期的检查在启动 10 秒后做，失败了一小时后重试，电脑睡醒时补上。用户的选择（自动检查、自动下载安装、跳过的版本、上次检查时间）存在数据目录的 `updater.json`，设置页通过 `updater.AutomaticChecks` 等读写，更新窗口改了以后 `updater.OnChange` 让窗口重绘
- 能写入安装位置才能更新（`mygo.Updater.Enabled`，它会在应用旁边试建目录；设置页用的结果在启动时到后台问一次记下来）：macOS 的 `/Applications`，Windows 安装程序装到的 `%LOCALAPPDATA%\Programs\Murmur`，或解压到用户自己目录的免安装版。在 DMG 里、被系统「App 转移」到只读位置运行的，或者不是管理员的账户打开 `/Applications` 里的，都不行；设置页说明原因，更新窗口的「无法更新」文案也换成了 Murmur 自己的说法
- MyGo 用 `http.DefaultClient` 下载，`update.go` 给它换了一个按设置里的「网络代理」走的 Transport（`autosave` 把代理存进 `updateProxy`），没填时按环境变量
- `go tool mygo dev` 和 `go run` 都是开发版本，不检查更新，点「检查更新」只会提示「开发版本不会自行更新」
- 设置页、托盘菜单、macOS 应用菜单（「关于」后面）都有「检查更新」；macOS 的应用菜单因此改为 `SetMenu` 自己设，其余和 MyGo 默认的一样

## 多语言的实现

用 MyGo 的多语言（[文档](https://mygo.egoist.dev/docs/i18n)）：`mygo.json` 的 `i18n` 指向 `locales/`，源语言是 `zh-Hans`。

- 界面上的文字都在 `locales/zh-Hans.json`、`zh-Hant.json`、`en.json` 里，格式是 ICU MessageFormat（复数、日期都写在消息里）。代码里调用生成的函数，如 `locales.NewChat()`、`locales.SidebarFound(n)`，按当前语言返回
- 加一条文字：三个文件都加上同一个键（带参数的在 `zh-Hans.json` 用 `@键名` 写明参数类型），运行 `go tool mygo generate` 重新生成 `locales/catalog_gen.go`（`mygo dev` / `mygo build` 也会自动生成），生成的文件要一起提交。`TestCatalogs` 检查三种语言的键一样，`TestInterfaceTranslated` 逐屏检查英文、繁体界面里没有漏翻的简体文字
- 语言的选择：系统首选语言匹配到三种之一就用它（`zh-TW`、`zh-HK` 用繁体），都匹配不上用英文（`main.go` 里 `SetLanguages` 把 `en` 放第一个）。设置里手动选的语言存在 `settings.json` 的 `language` 字段，启动时在开窗前 `App.SetLocale`；选「跟随系统」存空字符串
- 切换语言时窗口立即重绘；托盘菜单和 macOS 应用菜单在 `OnLocaleChanged` 里重建。MyGo 的更新窗口在第一次用到时定下语言，运行中切换要重新打开 Murmur 才变
- `llm/`、`extract/` 返回的错误也走 `locales`，在出错时按当前语言生成
- 试某种语言：`go tool mygo dev -lang en`；测试里用 `t.Setenv("MYGO_LOCALE", "en")`

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
- 也可以先在 GitHub 网页上新建 Release 并创建标签，CI 会把安装包补传到这个 Release。但这样 CI 跑完前已安装的 Murmur 检查更新会出错，这个版本也做不出增量更新（构建时最新版已经是它自己），最好直接推标签
- Release 附件：`Murmur-<版本>-macos-universal.dmg`、`Murmur-Setup-<版本>-windows-{amd64,arm64}.exe`、`Murmur-<版本>-windows-{amd64,arm64}-portable.zip`（免安装版）和 `SHA256SUMS.txt`
- 在 Actions 页手动运行这个 workflow 只打包、不发布，安装包在运行记录的 Artifacts 里
- 正式版本还会发布更新：用仓库 Secret `MYGO_UPDATER_PRIVATE_KEY` 签名，更新包（`murmur-<版本>-<平台>.tar.gz`、`.delta`）、清单 `update-<平台>.json` 和安装包都先传到草稿 Release，传完再发布，免得已安装的应用读到清单时包还没传完。没配这个 Secret 时正式版本的构建直接失败；预发布版本和在分支上手动运行不签名
- **发版前先在 `CHANGELOG.md` 加上这个版本的说明**，标题写成 `## 0.8.0`，下面列给用户看的变化，更新窗口里显示的就是这段。没有的话测试（`TestChangelog`）失败，不会发布

### 更新签名的密钥

私钥在维护者电脑的 `~/Library/Application Support/mygo/update-keys/mygo-update.key`，公钥在 `mygo.json` 的 `updates.publicKey`。把私钥文件的内容填到 GitHub 仓库的 Settings → Secrets and variables → Actions → New repository secret，名称 `MYGO_UPDATER_PRIVATE_KEY`，或者：

```sh
gh secret set MYGO_UPDATER_PRIVATE_KEY < ~/Library/Application\ Support/mygo/update-keys/mygo-update.key
```

私钥另外备份到密码管理器里，不要提交到仓库：已安装的 Murmur 只认这把私钥签的更新，丢了就没法再推送更新，只能让用户手动下载；泄露了别人就能给所有用户推送代码。

本地打一个带更新的包验证：

```sh
MYGO_UPDATER_PRIVATE_KEY="$(cat ~/Library/Application\ Support/mygo/update-keys/mygo-update.key)" \
  go tool mygo build -platform darwin/universal,windows/amd64,windows/arm64
```

## 签名

现在的包没有正式签名（macOS 是 ad-hoc 签名），用户第一次打开需要手动放行，见 README 的「下载安装」。

正式分发时在 `mygo.json` 里配置 `macos.signingIdentity` / `macos.notarize` 和 `windows.certificate`，详见 [MyGo 打包文档](https://mygo.egoist.dev/docs/distribution)。
