# Murmur

极简的 AI 聊天桌面应用，支持 macOS 和 Windows。用 [MyGo](https://mygo.egoist.dev/docs) 的原生 UI 写成：纯 Go、无 cgo、不依赖 WebView，启动快、体积小。

![Murmur](docs/screenshot.png)

## 功能

- **自定义模型**：支持 OpenAI 兼容协议和 Anthropic 协议，想加多少模型都行。内置 OpenAI、Claude、DeepSeek、通义千问、智谱 GLM、Kimi、豆包、硅基流动、Gemini、OpenRouter、Ollama 预设，选一个填上 API Key 就能用
- **深度思考**：输入框旁边一键开关。思考过程实时流式显示，答案开始输出后自动折叠，并显示思考用时
- **流式 Markdown**：代码高亮（带复制按钮）、表格、引用、任务列表、数学公式
- **对话管理**：按今天 / 昨天 / 7 天内 / 更早分组，右键删除；复制回复、重新生成、随时停止
- **其他**：系统提示词、网络代理（http / https / socks5）、深色模式跟随系统、macOS 毛玻璃侧边栏、Windows 11 Mica

## 快捷键

| 操作 | macOS | Windows |
|---|---|---|
| 发送 | Enter | Enter |
| 换行 | Shift+Enter | Shift+Enter |
| 新对话 | ⌘N | Ctrl+N |
| 设置 | ⌘, | Ctrl+, |

## 深度思考是怎么实现的

各家打开思考的方式不一样，Murmur 会按接口地址自动识别，也可以在模型设置的「思考参数」里手动指定：

| 思考参数 | 适用于 | 开 | 关 |
|---|---|---|---|
| `reasoning_effort` | OpenAI、Ollama、Gemini | `"reasoning_effort": "medium"` | 不发送 |
| `enable_thinking` | 通义千问、硅基流动 | `"enable_thinking": true` | `false` |
| `thinking.type` | 智谱 GLM、DeepSeek、豆包 | `"thinking": {"type": "enabled"}` | `disabled` |
| `reasoning` | OpenRouter | `"reasoning": {"effort": "medium"}` | 不发送 |
| 不发送 | Kimi K2 Thinking、DeepSeek Reasoner 等自带思考的模型 | — | — |

思考内容会从响应的 `reasoning_content` / `reasoning` 字段读取，模型如果把思考写在正文的 `<think>…</think>` 里也会被识别出来。

Anthropic 协议用官方 Go SDK：Claude 4.6 及以后的模型用自适应思考（`{"type": "adaptive", "display": "summarized"}`，思考摘要实时显示），更早的 Claude 和其他兼容 Anthropic 协议的服务用思考预算（`budget_tokens`）。

## 数据存放位置

对话和设置（包括 API Key）只保存在本机，文件权限仅当前用户可读：

- macOS：`~/Library/Application Support/Murmur/`
- Windows：`%AppData%\Murmur\`

## 开发

需要 Go 1.27 及以上。

```sh
go tool mygo dev      # 开发模式，改代码自动重启
go test ./...         # 测试（界面测试不需要窗口）
MURMUR_SNAPSHOTS=1 go test .   # 顺便把界面渲染成 testdata/*.png
```

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

### 关于签名

现在的包没有正式签名（macOS 是 ad-hoc 签名）：

- macOS 首次打开会提示「无法验证开发者」：在「应用程序」里右键 Murmur →「打开」，或者执行 `xattr -dr com.apple.quarantine /Applications/Murmur.app`
- Windows 可能弹出 SmartScreen：点「更多信息」→「仍要运行」

正式分发时在 `mygo.json` 里配置 `macos.signingIdentity` / `macos.notarize` 和 `windows.certificate`，详见 [MyGo 打包文档](https://mygo.egoist.dev/docs/distribution)。

## 项目结构

```
main.go       窗口和入口
app.go        应用状态：发送、流式接收、停止、重新生成
view.go       主界面：侧边栏、消息列表、输入框
settings.go   设置面板：模型管理、系统提示词、代理
store.go      设置和对话的读写
presets.go    服务商预设
llm/          模型调用：OpenAI 兼容（SSE）和 Anthropic（官方 SDK）
demo.go       演示构建（-tags demo），连本地模型自动跑一遍并截图
```
