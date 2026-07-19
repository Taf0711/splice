<p align="center">
  <img src="docs/assets/splice-logo.png" alt="splice" width="560">
</p>

<p align="center"><strong>一个具备确定性、多阶段流水线的终端编码智能体。</strong></p>

<p align="center">
  <a href="https://github.com/Taf0711/splice/actions/workflows/ci.yml?branch=main"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/Taf0711/splice/ci.yml?branch=main"></a>
  <a href="https://www.npmjs.com/package/@taf0711/splice"><img alt="npm version" src="https://img.shields.io/npm/v/@taf0711/splice"></a>
  <a href="LICENSE"><img alt="license" src="https://img.shields.io/badge/license-MIT-blue"></a>
  <img alt="Go 1.25+" src="https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white">
  <img alt="25+ providers" src="https://img.shields.io/badge/providers-25+-34E2EA">
  <br>
  <a href="README.md">English</a> | <strong>中文</strong>
</p>

Splice 是一个用于本地终端的 AI 编码智能体。它可以检查仓库、编辑文件、运行命令、使用浏览器/终端辅助工具，并在你选择模型和权限级别的同时保持持久的本地会话。在此引擎之上，Splice 叠加了一个由编排器驱动、以模式为契约（schema-as-contract）的流水线：请求被分类、转化为类型化的执行计划，并经过专门的阶段（代码编写、静态分析、测试生成、安全审计、测试运行）运行，同时配备确定性轨迹监控器，在它们浪费 token 之前就捕捉到死亡螺旋。

```bash
splice
splice exec "fix the failing test in ./pkg"
splice exec --output-format stream-json < turns.jsonl
```

## 为什么选择 Splice

- **使用你想要的模型。** 支持 OpenAI、Anthropic、Gemini、Groq、OpenRouter、DeepSeek、Mistral、xAI、Qwen、Kimi、GitHub Models、Ollama、LM Studio，或任何 OpenAI/Anthropic 兼容端点。
- **是流水线，而不仅仅是聊天。** 请求被分类为某个层级，转化为类型化的执行计划，并经过专门的阶段运行，其输入和输出都是带有 `Validate()` 方法的 Go 结构体。编排器是工头：智能体之间从不直接传递数据，向前流动的是摘要而非原始输出。
- **确定性优先。** 任何能用代码回答的问题（ripgrep、AST、Bandit、pytest、git diff）都用代码回答。LLM 是最后的手段。上下文构建器从 Zero 的工具注册表拉取真实的文件、目录和 grep 结果，让阶段提示看到的是工作区而非猜测。
- **轨迹感知。** 一个确定性评分器监控每一次迭代。硬性限制、预算耗尽、状态哈希循环、振荡、回归和置信度坍塌都会各自显式地升级，而不是静默地循环。
- **保持控制权。** 文件写入、Shell 命令、网络访问和工作区外写入都经过 Splice 的权限和沙箱策略。
- **在终端中工作。** TUI 具有模型/提供商选择器、图片输入、斜杠命令、实时计划/工具渲染、回滚滚动、主题以及恢复/分叉支持。
- **无 TUI 也能工作。** `splice exec` 可脚本化，支持文本/JSON/stream-JSON I/O、隔离的工作树、规范优先运行，以及用于 CI 的有意义的退出码。
- **保持上下文本地化。** 会话存储在磁盘上，可搜索、可恢复，且 Splice 不会将其作为遥测数据上传。
- **需要时可扩展。** 使用 MCP 服务器、技能、插件、钩子和来自同一 CLI 的专业子智能体。

## 安装

通过 `npm install -g @taf0711/splice` 安装（下载匹配的 GitHub Release 二进制文件），或从 [GitHub Releases](https://github.com/Taf0711/splice/releases) 直接下载归档包。

```bash
npm install -g @taf0711/splice
splice
```

npm 包将安装一个小型包装器以及与你平台匹配的 Splice 二进制文件（从 GitHub Releases 获取）。它将支持 Linux、macOS 和 Windows 的 x64 和 arm64。

### Bun（计划中）

Bun 默认不运行依赖的生命周期脚本，因此获取 Splice 二进制文件的 `postinstall` 会被跳过，首次运行会失败并显示 `No native binary found next to the npm wrapper`。

最简单的解决方法是安装后信任该包，这会运行被阻止的 postinstall。项目安装和全局安装均适用：

```bash
# 项目安装
bun add @taf0711/splice
bun pm trust @taf0711/splice

# 全局安装
bun add -g @taf0711/splice
bun pm -g trust @taf0711/splice
```

其他方式：在 `bun add` 之前将 `"trustedDependencies": ["@taf0711/splice"]` 添加到项目的 package.json 以提前允许 postinstall；或在不支持 `bun pm trust` 的 Bun 版本上手动运行安装程序（`node node_modules/@taf0711/splice/scripts/postinstall.mjs`）。

### 安装脚本（计划中）

Linux/macOS：

```bash
curl -fsSL https://raw.githubusercontent.com/Taf0711/splice/main/scripts/install.sh | bash
```

Windows PowerShell：

```powershell
irm https://raw.githubusercontent.com/Taf0711/splice/main/scripts/install.ps1 | iex
```

### 从源码构建（当前可用）

源码构建需要 Go 1.25+。

```bash
git clone https://github.com/Taf0711/splice.git
cd splice
go run ./cmd/splice
```

如果你在首次公开发布之前进行测试，请从源码构建：

```bash
go build -o splice ./cmd/splice
```

在 Linux 上，如果你需要原生沙箱，还需要构建沙箱辅助程序：

```bash
go build -o splice-linux-sandbox ./cmd/splice-linux-sandbox
go build -o splice-seccomp ./cmd/splice-seccomp   # 可选的兼容性包装器
```

将 `splice` 和 `splice-linux-sandbox` 放在 `PATH` 上的同一目录中（`~/.local/bin` 是一个好的默认选择）。macOS 不需要额外的辅助二进制文件。Windows 源码构建可以使用主 `splice.exe` 作为沙箱辅助程序；发布包仍然附带独立的 Windows 辅助可执行文件。

更多安装细节：[docs/INSTALL.md](docs/INSTALL.md)。

## 首次运行

启动 TUI：

```bash
splice
```

设置向导帮助你选择提供商和模型。你也可以从命令行配置提供商：

```bash
splice setup
splice providers list
splice models list
splice doctor
```

对于 API 提供商，在设置之前设置匹配的环境变量或在向导中输入密钥：

```bash
export OPENAI_API_KEY=sk-...
export ANTHROPIC_API_KEY=...
export GEMINI_API_KEY=...
export LONGCAT_API_KEY=...
```

要直接配置美团 LongCat（LongCat-2.0），运行：

```bash
splice providers setup longcat --set-active
```

对于本地模型，运行 Ollama 或 LM Studio，然后使用 `splice setup` 或 `splice providers detect`。由模型驱动的流水线阶段要求模型支持工具调用。如果模型没有调用所需的类型化工具，或返回无效 JSON，Splice 最多会进行两次纠正重试，之后给出可操作的错误信息。Splice 绝不会从本地模型静默回退到云端提供商。

### 你的第一个提示：规划与执行

一个新的交互式会话默认从**规划模式**（design）开始。描述你想要构建的内容；Splice 会运行设计对话，然后 `/crystallize` 将其转化为类型化计划，`/approve` 将其交给执行流水线。这个两阶段流程适用于需要先规划的工作。

要跳过规划，直接通过流水线运行提示，请在 TUI 中使用 `/exec <prompt>`，或在无头模式下使用 `splice exec "<prompt>"`：

```bash
splice exec "fix the failing test in ./pkg"
```

当你已经知道自己想要什么、不需要规划步骤时，`/exec` 是快速通道。输入 `/design` 可重新进入规划模式。

## 日常使用

### 交互式 TUI

```bash
splice
```

常用控制：

| 控制 | 操作 |
|---|---|
| `Enter` | 发送提示 |
| `/` | 打开斜杠命令建议 |
| `Shift+Tab` | 切换权限模式 |
| `Ctrl+B` | 显示/隐藏侧边栏 |
| `Ctrl+C` | 取消或退出 |

常用斜杠命令：

| 命令 | 用途 |
|---|---|
| `/model`、`/provider` | 切换活动模型/提供商 |
| `/stages` | 路由由模型驱动的代码编写和测试生成阶段，并配置默认与升级目标 |
| `/spec`、`/plan` | 在构建之前起草和审查计划 |
| `/image` | 为视觉模型附加图片 |
| `/resume`、`/rewind` | 继续或回滚本地会话 |
| `/loop` | 按间隔重复一个提示或自定义 `/command`（`/loop 5m /babysit-prs`）或自定节奏 |
| `/compact`、`/context` | 管理上下文使用 |
| `/permissions`、`/tools` | 检查可用工具和策略 |
| `/add-dir` | 为此会话授予额外的写入目录 |
| `/theme`、`/doctor`、`/config` | 调整外观和检查设置 |

### 无头 `exec` 模式

```bash
splice exec "explain internal/agent/loop.go"
splice exec --model claude-sonnet-4.5 "refactor the config loader"
splice exec --use-spec "add rate limiting to the API client"
splice exec --worktree "try the migration in an isolated worktree"
splice exec --worktree --merge-back "run it isolated, then merge the result back"
splice exec --plan design-plan.json
splice exec --resume
splice exec --fork <session-id> "try the other approach"
```

`--plan` 执行一个设计计划 JSON 文件：任务按依赖关系拓扑排序，每个任务作为独立的流水线运行执行，首个未完成的任务会导致快速失败。`--merge-back`（需要配合 `--worktree`）提交 worktree 中的工作，固定一个 `splice/<name>` 恢复分支，然后以 `--no-ff` 合并回源仓库；脏的源工作树或合并冲突不会强制任何操作，且在所有未合并的情况下恢复分支都会保留。

编程使用：

```bash
splice exec --input-format stream-json --output-format stream-json < turns.jsonl
```

Stream-JSON 协议文档在 [docs/STREAM_JSON_PROTOCOL.md](docs/STREAM_JSON_PROTOCOL.md)。

## 流水线

Splice 的标志性层是 `internal/splice/` 中的确定性流水线。它从 Flug 范式移植而来（Flug 是已归档的 Python 前身），并用 Go 在 Zero 的引擎之上全新构建。

流程如下：

1. **分类。** `ClassifyRequest` 检查请求并使用与 Python 源码匹配的字符计数阈值和关键字/风险域检测，将其分配到五个层级之一（trivial、light、standard、substantial、architectural）。
2. **规划。** `BuildExecutionPlan` 将层级转化为类型化的 `ExecutionPlan`，并为每个阶段分配 `TokenBudget`。未知层级会以错误显式失败，与 Python 的 `KeyError` 行为一致。
3. **填充上下文。** 在阶段运行之前，其 `ContextRequest` 通过 `FulfillContextRequest` 确定性地填充，该函数调用 Zero 真实的 `tools.Registry`（`read_file`、`list_directory`、`grep`）。所有六种合法查询类型都被处理；载荷是文本，截断是字符安全的，失败的工具会变成一个出错的 `ContextItem` 而非静默的空载荷。
4. **运行阶段。** 每个流水线阶段都是 `internal/splice/stages/` 中的类型化 Go 函数。代码编写和测试生成阶段调用配置的提供商；静态分析、安全审计和测试执行阶段不使用模型，而是运行确定性的本地工具。`splicerun.Run` 编排器在 Zero 完整的回调契约（流式输出、用量、工具调用/结果配对、权限事件）下驱动它们，并已接入无头 `splice exec`。
5. **监控轨迹。** `ComputeIterationState` 从阶段输出构建状态向量；`EvaluateTrajectory` 对其评分并决定继续、升级、回滚、退后还是上交给用户。重复的状态哈希会作为循环升级，包括空哈希，因此编排器的 bug 会显式暴露而不是循环。
6. **迭代或中止。** 编排器根据轨迹决策行动。失败是类型化的而非被吞掉：畸形的阶段载荷会指出数据键和出错的索引。

`internal/splice/` 中的所有内容都是确定性 Go 代码，并有完整测试覆盖：JSON 往返测试覆盖了每个带 `Validate()` 方法的结构体，上下文构建器有一个针对真实工具注册表的端到端测试。

## 安全模型

Splice 旨在使副作用可见。

- 工作区读取默认允许。
- 文件写入限制在工作区内，除非你授予其他目录。
- Shell 命令、网络访问、破坏性命令和提权操作需要权限授权。
- `--add-dir <path>` 和 `/add-dir <path>` 授予额外的写入根目录，而不会给智能体整个文件系统。
- 不安全/自主模式是显式选择加入。
- 在 Splice 控制的界面上，密钥会从工具输出和日志中被脱敏。

示例：

```bash
splice --add-dir ../docs-site
splice exec --add-dir ../shared "update both repos"
```

沙箱行为可以通过以下方式检查：

```bash
splice sandbox policy
splice sandbox grants list
```

## Web 和本地控制

Splice 包含本地文件/搜索/编辑/Shell 工具、用于公共 URL 的 `web_fetch`，以及用于额外工具的 MCP 支持。

对于本地开发服务器，使用 Shell 命令（如通过 `exec_command` 的 `curl`），这样正常的沙箱和权限策略就会生效。长时间运行的命令保持附加到后台终端会话，可以从 TUI 中列出或停止。

npm 包还将包含本地浏览器/终端工具使用的浏览器和终端辅助包。源码构建可以在它们位于 `PATH` 上或在 Splice 的本地控制设置中配置时使用相同的辅助工具。

## 常用命令

```text
splice                交互式 TUI
splice exec           一次性或脚本化智能体运行
splice setup          首次运行提供商设置
splice auth           支持提供商的 OAuth/登录辅助
splice login          支持提供商的订阅登录辅助
splice models         模型注册表和能力
splice providers      提供商配置和检测
splice doctor         设置、密钥和连接检查
splice context        上下文预算报告
splice repo-map       确定性仓库映射
splice repo-info      本地仓库摘要
splice search | find  搜索本地会话历史
splice sessions       检查、恢复、分叉和回滚会话
splice spec           管理规范模式草稿
splice specialist     管理专业子智能体
splice skills         管理 Markdown 指令技能
splice plugins        管理插件
splice hooks          管理生命周期钩子
splice mcp            管理 MCP 服务器和工具
splice serve --mcp    通过 MCP stdio 暴露 Splice 工具
splice sandbox        检查沙箱策略和授权
splice worktrees      准备隔离的 Git 工作树
splice verify         检测和运行本地验证检查
splice changes        检查和提交本地 Git 变更
splice usage          Token 使用量和估算成本
splice cron           定时智能体任务
splice update         检查更新版本
splice upgrade        应用更新版本
```

## 扩展 Splice

### 项目和个人指令

Splice 将项目特定的指导附加到系统提示中，来源是按从 git 根目录到当前工作目录的每一层目录中找到的第一个 `AGENTS.md`、`SPLICE.md` 或 `.splice/AGENTS.md` 文件（每个目录按该顺序检查）。文件按从通用到具体的顺序注入，每个文件上限 8 KiB，总共上限 32 KiB。

位于
`config.UserConfigDir()/splice/SPLICE.md`
（Linux/macOS 上为 `$XDG_CONFIG_HOME/splice/SPLICE.md` 或 `~/.config/splice/SPLICE.md`，Windows 上为 `%AppData%\Roaming\splice\SPLICE.md`）的个人 `SPLICE.md` 会跨所有工作区生效，且优先于任何项目指南。

### 插件

插件从
`~/.config/splice/plugins/<name>/plugin.json`（用户作用域，每个 OS 上为 `$XDG_CONFIG_HOME` 或 `~/.config`，与上面使用的 `config.UserConfigDir()` 路径无关）和 `<cwd>/.splice/plugins/<name>/plugin.json`（项目作用域，从当前工作目录解析而非仓库根目录）发现，并通过 `splice plugins` 管理。清单可以声明：

- `tools` — 自定义工具（`command`、`args`、`inputSchema`，以及 `prompt` 或 `deny` 的 `permission`；`allow` 仅在启用清单工具自动批准时生效）
- `hooks` — 在 `beforeTool`、`afterTool`、`sessionStart` 或 `sessionEnd` 上运行的命令
- `prompts` 和 `skills` — 额外的提示和技能文件

MCP 服务器（`splice mcp`）和独立的 Markdown 技能（`splice skills`）使用相同的扩展点，也可以在插件清单之外配置。

## 外观和无障碍

| 控制 | 效果 |
|---|---|
| `NO_COLOR=<任意值>` | 禁用颜色输出 |
| `ZERO_THEME=<名称>` | 选择启动主题（`auto`、`dark`、`light`，或颜色主题如 `dracula`、`nord`、`gruvbox`、`tokyo-night`、`catppuccin`、`one-dark`、`solarized-dark`、`rose-pine`、`everforest`、`solarized-light`） |
| `--theme <名称>` | 从 CLI 选择 TUI 主题（相同名称） |
| `/theme` | 在 TUI 中打开主题选择器（实时预览；`/theme <名称>` 直接切换） |
| `ZERO_NO_FADE=1` | 禁用流式淡入动画 |

> 注意：这些主题环境变量仍使用来自上游引擎的 `ZERO_` 前缀。重命名为 `SPLICE_` 已计划但尚未应用；在过渡期间两个名称都将被接受。

含义不仅仅依赖于颜色；差异、权限和状态也使用文本或标记符号。

## 开发

```bash
go test ./...
go run ./cmd/splice-release build
go run ./cmd/splice-release smoke
go run ./cmd/splice-perf-bench
```

交叉编译示例：

```bash
go run ./cmd/splice-release build --goos linux --goarch amd64
go run ./cmd/splice-release build --goos windows --goarch amd64 --output dist/splice.exe
```

确定性流水线层位于 `internal/splice/`，是纯 Go 代码，没有引入任何提供商 SDK。其包包括：

- `internal/splice/schemas/` — 带有 `Validate()` 方法的类型化阶段和流水线结构体。
- `internal/splice/classifier.go` — 请求到层级的分类。
- `internal/splice/planner.go` — 层级到执行计划、意图蒸馏。
- `internal/splice/budget.go` — 每个层级的 token 预算。
- `internal/splice/trajectory.go` — 迭代状态、评分、轨迹决策。
- `internal/splice/context.go` + `registry_runner.go` — 通过 Zero 的工具注册表确定性地填充上下文。
- `internal/splice/stages/`：阶段智能体（代码编写、静态分析、测试生成、安全审计、测试运行、设计对话、计划评审）及内嵌提示词。
- `internal/splice/run.go`：`splicerun.Run` 编排器循环。
- `internal/splice/design_runner.go`：`RunDesignPlan`，为 `splice exec --plan` 提供拓扑任务排序。

相邻的 Splice 专属部分：`internal/worktrees/` 在 Zero 的 worktree 生命周期之上增加了 `MergeBack`，`memd/` 是记忆边车（独立 Go 模块，二进制名 `splice-memd`，通过 Unix socket 提供 SQLite/FTS5），`internal/memd/` 是边车的 HTTP 客户端（完成）。

## 文档

- [安装](docs/INSTALL.md)
- [更新流程](docs/UPDATE.md)
- [Stream-JSON 协议](docs/STREAM_JSON_PROTOCOL.md)
- [专家](docs/SPECIALISTS.md)
- [GitHub Action](docs/GITHUB_ACTION.md)
- [基准测试](docs/BENCHMARK.md)
- [性能](docs/PERFORMANCE.md)
- [智能体评估](docs/AGENT_EVALS.md)

## 贡献

欢迎贡献。阅读 [CONTRIBUTING.md](CONTRIBUTING.md)，运行相关测试，然后提交一个聚焦的拉取请求。

安全报告应遵循 [SECURITY.md](SECURITY.md)。

## 许可证

Splice 基于 [MIT 许可证](LICENSE) 发布。

## 致谢

Splice 构建于 [Gitlawb 的 Zero CLI](https://github.com/gitlawb/zero) 之上——一个开源的、MIT 许可的终端编码智能体。Splice 在 Zero 的引擎之上扩展了一个从已归档的 Flug 原型移植而来的、确定性的、由编排器驱动的多阶段流水线范式。所有上游 Zero 代码保留其原始版权和许可证。
