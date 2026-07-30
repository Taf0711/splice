<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/splice-logo-inverted.svg">
    <img src="docs/assets/splice-logo.svg" alt="splice" width="560">
  </picture>
</p>

<p align="center"><strong>把提示变成经过检查的改动。</strong></p>

<p align="center">
  <a href="https://github.com/Taf0711/splice/actions/workflows/ci.yml?branch=main"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/Taf0711/splice/ci.yml?branch=main"></a>
  <a href="https://www.npmjs.com/package/@taf0711/splice"><img alt="npm version" src="https://img.shields.io/npm/v/@taf0711/splice"></a>
  <a href="LICENSE"><img alt="license" src="https://img.shields.io/badge/license-MIT-blue"></a>
  <img alt="Go 1.25+" src="https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white">
  <br>
  <a href="README.md">English</a> | <strong>中文</strong>
</p>

Splice 是一个本地优先的编码智能体，适合不想只把模型接到 Shell 循环上的
开发者。它可以检查仓库、编辑文件、运行检查，并使用你选择的模型和提供商。
对于较大的任务，它会先分类请求，只组装阶段真正需要的上下文，再通过专门的
执行计划处理改动。

## 三条命令开始

```bash
npm install -g @taf0711/splice
splice
```

然后描述你想要的改动。需要直接脚本化运行时：

```bash
splice exec "fix the failing test in ./pkg"
```

首次启动会打开提供商和模型设置向导。源码构建、平台说明和可选记忆边车请参阅
[安装文档](docs/INSTALL.md)。

## Splice 为什么存在

大多数编码智能体让一段长对话同时负责理解、编辑、测试和决定是否继续。Splice
把这些工作分开，但不会把它们变成嘈杂的智能体群聊。

- **类型化流水线。** 阶段之间传递经过验证的 Go 结构体，而不是松散的聊天记录。
  编排器决定每个阶段收到什么以及下一步运行什么。
- **确定性优先。** 仓库读取、搜索、AST 检查、静态分析、测试、差异和退出码来自
  本地工具。模型只在需要判断时介入。
- **轨迹感知。** 运行监控器能发现硬限制、重复状态、振荡、回归和信心下降。卡住
  的运行会报告问题，而不是无限消耗 token。
- **最小上下文。** 原始请求在进入后续阶段前会被提炼。每个阶段只收到摘要和所需
  证据。
- **Zero 的安全基础。** 文件写入、Shell 命令、网络访问和额外写入目录仍经过
  Zero 的权限、沙箱、钩子和工具注册表。
- **默认本地运行。** 会话和可选记忆保存在本地磁盘。Splice 不增加遥测，也不需要
  托管式协调服务。

## Splice 循环

```text
请求
 │
 ▼
分类 ──► 类型化执行计划 ──► 聚焦上下文
                              │
                              ▼
      编写 ──► 分析 ──► 测试 ──► 审计
                              │
                              ▼
                    轨迹决策
                    继续、升级或停止
```

流水线包含从 trivial 到 architectural 的五个请求层级。普通运行可以使用模型
驱动的代码编写和测试生成，同时让静态分析、安全检查和测试执行保持确定性并在本地
完成。每个阶段都有契约和验证。格式错误会指出出错字段，而不是静默回退为空值。

## 选择运行方式

### 交互式运行

```bash
splice
```

适合需要实时对话、提供商和模型选择器、权限提示、计划审查、主题、图片输入、
恢复会话或探索分叉的场景。

新会话默认进入设计模式。描述任务后，使用 `/crystallize` 将对话变成类型化计划，
再用 `/approve` 执行。如果不需要规划，使用 `/exec <prompt>` 直接进入流水线。

### 无头运行

```bash
splice exec "explain internal/agent/loop.go"
splice exec --use-spec "add rate limiting to the API client"
splice exec --worktree "try the migration in isolation"
splice exec --worktree --merge-back "run isolated, then merge the result back"
splice exec --plan design-plan.json
```

`splice exec` 支持文本、JSON、stream-JSON 输入输出，隔离 Git worktree、可恢复会话
以及适用于自动化的退出码。流式客户端示例：

```bash
splice exec \
  --input-format stream-json \
  --output-format stream-json < turns.jsonl
```

事件契约见 [docs/STREAM_JSON_PROTOCOL.md](docs/STREAM_JSON_PROTOCOL.md)。

## 提供商和本地模型

你可以选择 OpenAI、Anthropic、Gemini、Groq、OpenRouter、DeepSeek、Mistral、xAI、
Qwen、Kimi、GitHub Models、Ollama、LM Studio，或任何 OpenAI/Anthropic 兼容端点。

```bash
splice setup
splice providers list
splice models list
splice doctor
```

在设置前导出对应的 API 密钥，或在向导中输入：

```bash
export OPENAI_API_KEY=sk-...
export ANTHROPIC_API_KEY=...
export GEMINI_API_KEY=...
```

本地模型可以通过 Ollama 或 LM Studio 使用。模型驱动的阶段需要工具调用能力。
如果提供商返回无效的类型化载荷，Splice 会尝试纠正，之后报告可操作的错误，
不会把本地运行静默切换到云端提供商。

## 安全是工作流的一部分

Splice 让副作用可见，而不是把它们隐藏在自主模式之后：

- 工作区读取默认允许；
- 写入默认限制在工作区内；
- Shell 命令、网络、破坏性操作和提权操作可能需要授权；
- `--add-dir <path>` 只授予指定的额外写入根目录；
- 不安全和自主模式需要显式选择；
- 运行前可以检查沙箱策略和授权。

```bash
splice sandbox policy
splice sandbox grants list
splice exec --add-dir ../shared "update both repositories"
```

威胁模型和漏洞报告流程见 [SECURITY.md](SECURITY.md)。

## 常用命令

| 命令 | 用途 |
|---|---|
| `splice` | 交互式 TUI |
| `splice exec` | 一次性和脚本化运行 |
| `splice setup` | 首次提供商设置 |
| `splice providers` / `models` | 提供商配置和模型能力 |
| `splice doctor` | 设置和连接检查 |
| `splice spec` | 规范模式草稿 |
| `splice sessions` | 恢复、分叉和查看会话 |
| `splice specialist` | 专业子智能体 |
| `splice skills` / `plugins` / `hooks` | 在本地扩展智能体 |
| `splice mcp` | 配置 MCP 服务器和工具 |
| `splice worktrees` | 准备隔离 Git worktree |
| `splice verify` | 查找并运行仓库检查 |
| `splice update` / `upgrade` | 更新二进制文件 |

## 文档

- [安装 Splice](docs/INSTALL.md)
- [更新流程](docs/UPDATE.md)
- [Stream-JSON 协议](docs/STREAM_JSON_PROTOCOL.md)
- [专业子智能体](docs/SPECIALISTS.md)
- [GitHub Action](docs/GITHUB_ACTION.md)
- [基准测试](docs/BENCHMARK.md)
- [性能](docs/PERFORMANCE.md)
- [智能体评估](docs/AGENT_EVALS.md)
- [OAuth 登录和订阅](docs/oauth-subscriptions.md)

## 贡献者入口

Splice 专属的流水线代码位于 `internal/splice/`，是纯 Go 代码，不在流水线层引入
提供商 SDK。可选的记忆边车位于独立的 `memd/` 模块，通过 Unix socket 通信。

```bash
go test ./...
go vet ./...
gofmt -l .
cd memd && go test ./...
```

提交改动前请阅读 [CONTRIBUTING.md](CONTRIBUTING.md)。当前项目采用先提交 issue
再贡献代码的政策。

## 构建于 Zero 的 Engine 之上

Splice 构建于 [Gitlawb 的 Zero Engine](https://github.com/gitlawb/zero) 之上。
Zero 是开源、MIT 许可的终端编码智能体，提供 TUI、提供商适配器、会话、工具、
沙箱、权限、worktree、MCP、技能、插件和钩子。Splice 保留这些基础设施，并增加
编排器驱动的流水线、类型化阶段契约、确定性检查、轨迹监控和可选的记忆边车。

上游 Zero 代码保留其原始版权和许可证。详见 [LICENSE](LICENSE) 和
[SECURITY.md](SECURITY.md)。

## 许可证

Splice 使用 [MIT 许可证](LICENSE) 发布。
