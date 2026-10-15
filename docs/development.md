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
image.go      图片：选择、粘贴、拖入，缩放和保存
search.go     搜索对话
export.go     导出对话为 Markdown
presets.go    服务商预设
llm/          模型调用：OpenAI 兼容（SSE）和 Anthropic（官方 SDK）
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

## 图片的实现

- 选择（系统文件对话框）、拖入对话区、粘贴（截图，或在访达 / 资源管理器里复制的图片文件）三种方式添加，读取和缩放都在后台 goroutine 里做
- 发送前统一处理：长边超过 2048 像素、超过 3 MB、带 EXIF 旋转的 JPEG，或 BMP 等不是各家都收的格式，重新绘制后编码成 JPEG（有透明的编码成 PNG）；其余原样发送
- 图片存在数据目录的 `images/` 下，每张一个文件，`chats.json` 里只记文件名、MIME 类型和尺寸，免得每次保存对话都重写几 MB；删除对话时一起删除，文件丢失时显示「图片已丢失」并且不再发送
- OpenAI 兼容协议把带图的消息写成 `content` 数组：文字一段，每张图一段 `image_url`（`data:` URL）；纯文字消息仍然是字符串，兼容不认数组的服务商
- Anthropic 协议每张图一个 base64 `image` 块，放在文字前面

## 搜索和导出的实现

- 搜索在内存里做：查询按空格拆成词，不分大小写，每个词都出现在标题或某条消息的正文里才算匹配；思考过程不参与搜索，因为它平时是收起的
- 结果缓存到查询变化或对话保存（`saveChats`）为止，不会每帧重新搜索；流式回复中的内容要等回复结束、对话保存后才能搜到
- 结果显示第一个不在标题里的词所在的消息，命中处前几个字起的一段原文（去掉 `**` 和反引号）；打开结果时消息列表滚动到那条消息
- 重命名不改 `updated`，对话在侧边栏里的位置不变；标题留空不保存
- 导出时先在主线程把对话复制一份，再在后台弹出保存对话框、写文件，流式回复中导出的是当时的内容
- 图片按顺序编号复制到 `<文件名>.assets/`，链接用尖括号包住（路径里可以有空格），`#`、`?`、`%` 等会被当成 URL 语法的字符转义成 `%XX`；文件已丢失的图片写成「图片已丢失」
- 用户消息在应用里按原文显示，导出时每行末尾加硬换行（代码块里不加），换行不会被合并；中途停止的回复如果停在代码块里，导出时补上结束的围栏，免得后面的内容都变成代码
- Windows 的保存对话框不会自动补扩展名：不是 `.md` / `.markdown` 结尾时补上 `.md`，补完的文件已存在时再问一次是否替换；默认文件名避开 `CON`、`NUL` 等 Windows 保留名

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
