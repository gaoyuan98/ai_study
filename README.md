



# go_agent_study

### 技术支持

<img src="./gzh01.png"  alt="达梦课代表公众号" />

> 微信公众号：**达梦课代表**  
> 分享DM数据库一线遇到的各类问题和解决方案

链接：https://mp.weixin.qq.com/s/B7Z_phcccBRvzr9Nb1EajQ

## 项目简介
- 该项目实现了一个兼容 ReAct 思考-行动-反馈循环的命令行 Agent，围绕 `agent.go` 中的入口函数启动，与 `react_agent.go` 内部逻辑协同，利用 `github.com/openai/openai-go` 客户端访问阿里云 DashScope 的 Qwen 模型（默认 `qwen3-max`）。
- Agent 在运行时会渲染 `prompt_template.go` 中的系统提示词，公开 `tools.go` 定义的多种工具（读写文件、终端命令、达梦数据库查询等），并将每轮对话及工具调用通过 `logger.go` 写入控制台与 `agent_run_时间戳.log`。

## 主要文件
- `agent.go`：命令行入口，负责解析参数、加载 `.env`、初始化模型客户端、拼装工具并触发 Agent 流程。
- `react_agent.go`：封装 ReAct 流程（提示词渲染、消息循环、工具调度、日志记录与用户确认）。
- `tools.go`：实现 `read_file`、`write_to_file`、`run_terminal_command`、`query_database` 等工具，数据库部分依赖 `github.com/gaoyuan98/dm` 驱动。
- `prompt_template.go`：系统提示词模板，包含工具列表与注意事项。
- `logger.go`：统一格式化日志，并将消息同步输出到终端与文件。

## 环境要求
1. **Go**：建议 Go 1.23.7 及以上（参见 `go.mod`）。
2. **网络**：能够访问 https://dashscope.aliyuncs.com/compatible-mode/v1。
3. **达梦数据库驱动**：`github.com/gaoyuan98/dm` 会在 `go run` 时自动拉取，无需单独安装；若需要访问数据库，请确保可通过 `dm://` DSN 访问。
4. **可选 `.env` 文件**：`agent.go` 会尝试加载项目根目录及当前工作目录的 `.env`，用于统一管理密钥等敏感变量。

## DashScope Key 获取与配置（key 提取）
1. 登录 [DashScope 控制台](https://dashscope.aliyun.com/)、打开 **API-Keys** 页面，点击「创建 API Key」，为 Key 取一个方便辨识的名称。
2. 复制新生成的 Key，仅在安全环境下保存；DashScope 只在首次展示明文。
3. 在项目根目录创建 `.env`（若不存在），写入：
   ```
   DASHSCOPE_API_KEY=sk-xxxxxxxxxxxxxxxx
   DASHSCOPE_BASE_URL=https://dashscope.aliyuncs.com/compatible-mode/v1
   ```
4. 打开 `agent.go` 中初始化 OpenAI 客户端的位置（约 55 行），将占位字符串 `option.WithAPIKey("填写成自己的密钥")` 替换为从环境变量读取，例如：
   ```go
   client := openai.NewClient(
   	option.WithAPIKey(os.Getenv("DASHSCOPE_API_KEY")),
   	option.WithBaseURL(os.Getenv("DASHSCOPE_BASE_URL")),
   )
   ```
   然后重新 `go run`。若暂不修改代码，则需直接把密钥写入该位置，但不建议将明文 Key 提交到版本库。

## 快速运行
1. **安装依赖（首次）**
   ```bash
   go mod tidy
   ```
2. **启动 Agent**
   - 交互式输入问题（默认）：
     ```bash
     go run . -project . -model qwen3-max
     ```
     程序将提示“请输入任务”，输入问题后回车即可。
   - 通过参数直接传入问题并指定日志文件：
     ```bash
     go run . -project E:/path/to/project -model qwen3-max ^
       -question "检查 README 是否完整" ^
       -log-file logs/session.log
     ```
3. **关键参数**
   - `-project`：项目根目录，Agent 会枚举该目录文件供模型参考，默认 `.`。
   - `-model`：DashScope 兼容模型名，可替换为 `qwen2.5-coder-32k` 等。
   - `-question`：直接指定任务；缺省则进入交互式模式。
   - `-log-file`：自定义日志路径。未指定时将在 `-project` 目录生成 `agent_run_YYYYMMDD_HHMMSS.log`。

## 运行示例：巡检报告生成
以下示例来自 `agent_run_20251219_210442.log`，演示如何让 Agent 完成“达梦数据库巡检 + HTML 报告”任务。

1. **任务输入**
   - 启动命令：`go run . -project . -model qwen3-max`
   - 在提示后输入问题：“对达梦数据库进行巡检，并生成 html 报告，美观一点。”
2. **关键交互**
   - Round 1：Agent 通过 `request_user_input` 索取 DSN，用户提供 `SYSDBA:Dameng123@192.168.112.174:5245`。
   - Round 2~6：Agent 自动整理 DSN（转为 `dm://`），依次调用 `query_database` 执行：
     - `SELECT * FROM v$version;`
     - 表空间使用率 SQL（`dba_data_files` / `dba_free_space` 聚合）
     - `SELECT COUNT(*) AS current_sessions FROM v$sessions;`
     - `SELECT * FROM v$lock;`
     - `SELECT * FROM v$system_long_exec_sqls WHERE exec_time > 1000;`
   - Round 7：将查询结果组装为带样式的 HTML，通过 `write_to_file` 写入 `dm_inspection_report.html`。
3. **最终输出**
   - Round 8 返回总结：“巡检完成，报告位于 `E:\go_code\ai_agent\go_agent_study\dm_inspection_report.html`。”
   - 生成的报告包含版本信息、表空间（提示 MAIN 使用率 98.81%）、会话数、锁明细和慢 SQL（无数据）。

## 内置工具
- `read_file(path)`：读取绝对路径文件内容。
- `write_to_file(path, content)`：写入/覆盖文件内容，支持 `\n` 表示换行。
- `run_terminal_command(command)`：执行系统命令，Windows 下调用 PowerShell，执行前需用户确认。
- `query_database(dsn, sql)`：连接指定达梦数据库并返回 tab 分隔结果；需提供真实 `dm://用户名:密码@主机:端口/数据库` 与 SQL，缺少参数时 Agent 会使用 `request_user_input` 向终端索取。
- `request_user_input(prompt)`：在信息不足时向人工提问，防止模型猜测。

## 日志与故障排查
- 每轮交互都会在日志中输出 `<thought>`、`<action>`、`<observation>`，可通过 `agent_run_*.log` 回放。
- 若终端命令或数据库连接失败，日志会包含详细报错信息，可据此重试。
- 当模型响应缺少 `<action>` 或参数校验未通过时，Agent 会立即在控制台提示；通常是因为提示词或 Key/DSN 未正确配置。
