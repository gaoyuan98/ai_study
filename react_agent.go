package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/openai/openai-go"
)

// ReActAgent 封装 ReAct 交互流程及相关依赖。
type ReActAgent struct {
	projectDir string
	model      string
	template   string
	client     openai.Client
	tools      map[string]Tool
	toolOrder  []Tool
	reader     *bufio.Reader
	logger     *AgentLogger
	round      int
}

// NewReActAgent 构造带指定工具及模型配置的 ReActAgent。
func NewReActAgent(projectDir, model, template string, client openai.Client, toolList []Tool, logger *AgentLogger) *ReActAgent {
	clonedTools := append([]Tool(nil), toolList...)
	tools := make(map[string]Tool, len(clonedTools))
	for _, t := range clonedTools {
		tools[t.Name] = t
	}

	agent := &ReActAgent{
		projectDir: projectDir,
		model:      model,
		template:   template,
		client:     client,
		tools:      tools,
		toolOrder:  clonedTools,
		reader:     bufio.NewReader(os.Stdin),
		logger:     logger,
	}

	agent.registerInteractiveTools()
	return agent
}

// Run 按 ReAct 协议与模型交互直到得到最终答案。
func (a *ReActAgent) Run(ctx context.Context, question string) (string, error) {
	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage(a.renderSystemPrompt()),
		openai.UserMessage(fmt.Sprintf("<question>%s</question>", question)),
	}

	for {
		a.round++
		if a.logger != nil {
			a.logger.StartRound(a.round)
			a.logger.Record("模型", "正在请求模型，请稍候...")
		}
		content, err := a.callModel(ctx, messages)
		if err != nil {
			return "", err
		}
		messages = append(messages, openai.AssistantMessage(content))

		if thought, ok := extractTag(content, "thought"); ok && a.logger != nil {
			a.logger.Record("思考", thought)
		}

		if finalAnswer, ok := extractTag(content, "final_answer"); ok {
			if a.logger != nil {
				a.logger.Record("最终答案", finalAnswer)
			}
			return strings.TrimSpace(finalAnswer), nil
		}

		actionPayload, ok := extractTag(content, "action")
		if !ok {
			return "", errors.New("模型输出缺少 <action>，无法继续执行")
		}

		toolName, args, err := parseToolCall(actionPayload)
		if err != nil {
			return "", err
		}

		if err := a.validateToolCall(toolName, args); err != nil {
			observation := fmt.Sprintf("action 参数校验失败: %v", err)
			if a.logger != nil {
				a.logger.Record("参数校验失败", err.Error())
			}
			messages = append(messages, openai.UserMessage(fmt.Sprintf("<observation>%s</observation>", observation)))
			continue
		}

		if a.logger != nil {
			argText := strings.Join(args, ", ")
			if argText == "" {
				argText = "(无参数)"
			}
			a.logger.Record("动作", fmt.Sprintf("%s(%s)", toolName, argText))
		}

		if toolName == "run_terminal_command" {
			confirmed, err := a.confirmInteractiveTool()
			if err != nil {
				return "", err
			}
			if !confirmed {
				return "", errors.New("操作被用户取消")
			}
		}

		observation := a.executeTool(toolName, args)
		if a.logger != nil {
			a.logger.Record("反馈", observation)
		}

		observationMsg := fmt.Sprintf("<observation>%s</observation>", observation)
		messages = append(messages, openai.UserMessage(observationMsg))
	}
}

// executeTool 根据名称调度工具并返回结果。
func (a *ReActAgent) executeTool(name string, args []string) string {
	tool, ok := a.tools[name]
	if !ok {
		return fmt.Sprintf("未知工具: %s", name)
	}
	result, err := tool.Handler(args...)
	if err != nil {
		return fmt.Sprintf("工具执行错误: %v", err)
	}
	return result
}

// validateToolCall 在执行工具前校验关键参数，避免占位符被误用。
func (a *ReActAgent) validateToolCall(toolName string, args []string) error {
	switch toolName {
	case "query_database":
		if len(args) != 2 {
			return errors.New(`query_database 需要同时提供 dsn 与 sql 参数，例如 query_database("dm://用户名:密码@主机:端口/数据库", "SELECT ...")。缺失信息时请先调用 request_user_input。`)
		}
		dsn := strings.TrimSpace(args[0])
		query := strings.TrimSpace(args[1])
		lowerDSN := strings.ToLower(dsn)
		if dsn == "" || strings.EqualFold(dsn, "your_dsn_here") {
			return errors.New(`缺少有效的 dsn。请先调用 request_user_input("请提供形如 dm://用户名:密码@主机:端口/数据库 的达梦连接串") 获取真实连接串。`)
		}
		if !strings.HasPrefix(lowerDSN, "dm://") {
			return errors.New(`达梦连接串必须以 dm:// 开头，可提示用户按照 dm://用户名:密码@主机:端口/数据库 的格式提供。`)
		}
		if !strings.Contains(dsn, "@") {
			return errors.New(`dsn 缺少主机信息，请确认包含 "@主机:端口" 段，并在必要时向用户询问完整连接串。`)
		}
		if query == "" {
			return errors.New(`SQL 语句不能为空。请先调用 request_user_input("需要执行的 SQL 是什么？") 获取真实语句。`)
		}
	}
	return nil
}

// registerInteractiveTools 注入可与用户继续对话的工具，避免因信息不足而直接退出。
func (a *ReActAgent) registerInteractiveTools() {
	userTool := Tool{
		Name:        "request_user_input",
		Signature:   "(prompt string)",
		Description: "当信息不足时向终端用户提问并等待回复",
		Handler:     a.requestUserInput,
	}

	if _, exists := a.tools[userTool.Name]; !exists {
		a.tools[userTool.Name] = userTool
		a.toolOrder = append(a.toolOrder, userTool)
	}
}

// requestUserInput 处理 request_user_input 工具调用，持续提示用户补全信息。
func (a *ReActAgent) requestUserInput(args ...string) (string, error) {
	prompt := "模型需要更多信息，请输入补充内容: "
	if len(args) > 0 {
		prompt = strings.TrimSpace(args[0])
		if prompt == "" {
			prompt = "模型需要更多信息，请输入补充内容: "
		}
	}

	if a.logger != nil {
		a.logger.Record("补充信息请求", prompt)
	}
	fmt.Printf("\n模型请求补充信息: %s\n", prompt)
	for {
		fmt.Print("请输入补充信息: ")
		response, err := a.reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		trimmed := strings.TrimSpace(response)
		if trimmed == "" {
			fmt.Println("输入不能为空，请重新输入。")
			continue
		}
		if a.logger != nil {
			a.logger.Record("用户补充信息", trimmed)
		}
		return trimmed, nil
	}
}

// renderSystemPrompt 将模板与工具列表、项目文件信息渲染为系统提示词。
func (a *ReActAgent) renderSystemPrompt() string {
	toolList := a.formatToolList()
	fileList := a.projectFiles()
	replacer := strings.NewReplacer(
		"${tool_list}", toolList,
		"${operating_system}", operatingSystemName(),
		"${file_list}", fileList,
	)
	return replacer.Replace(a.template)
}

// formatToolList 返回供 Prompt 使用的工具描述。
func (a *ReActAgent) formatToolList() string {
	lines := make([]string, 0, len(a.toolOrder))
	for _, t := range a.toolOrder {
		lines = append(lines, fmt.Sprintf("- %s%s: %s", t.Name, t.Signature, t.Description))
	}
	return strings.Join(lines, "\n")
}

// projectFiles 枚举项目目录下的文件并返回绝对路径列表。
func (a *ReActAgent) projectFiles() string {
	entries, err := os.ReadDir(a.projectDir)
	if err != nil {
		return ""
	}

	var paths []string
	for _, entry := range entries {
		fullPath := filepath.Join(a.projectDir, entry.Name())
		absPath, err := filepath.Abs(fullPath)
		if err != nil {
			continue
		}
		paths = append(paths, absPath)
	}
	sort.Strings(paths)
	return strings.Join(paths, ", ")
}

// callModel 调用大模型获取下一步响应。
func (a *ReActAgent) callModel(ctx context.Context, messages []openai.ChatCompletionMessageParamUnion) (string, error) {
	if a.logger == nil {
		fmt.Println("\n正在请求模型，请稍候...")
	}
	resp, err := a.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: messages,
		Model:    openai.ChatModel(a.model),
	})
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		return "", errors.New("模型返回为空")
	}
	return resp.Choices[0].Message.Content, nil
}

// confirmInteractiveTool 在执行危险命令前与用户确认。
func (a *ReActAgent) confirmInteractiveTool() (bool, error) {
	fmt.Print("是否继续执行终端命令? (Y/N): ")
	input, err := a.reader.ReadString('\n')
	if err != nil {
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(input), "y"), nil
}

// extractTag 从模型输出中提取指定 XML 标签内容。
func extractTag(content, tag string) (string, bool) {
	pattern := fmt.Sprintf("(?s)<%s>(.*?)</%s>", tag, tag)
	re := regexp.MustCompile(pattern)
	match := re.FindStringSubmatch(content)
	if len(match) < 2 {
		return "", false
	}
	return strings.TrimSpace(match[1]), true
}

// parseToolCall 解析形如 foo("bar") 的工具调用字符串。
func parseToolCall(payload string) (string, []string, error) {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return "", nil, errors.New("action 内容为空")
	}
	openParen := strings.Index(payload, "(")
	closeParen := strings.LastIndex(payload, ")")
	if openParen == -1 || closeParen == -1 || closeParen < openParen {
		return "", nil, fmt.Errorf("无法解析函数调用: %s", payload)
	}

	name := strings.TrimSpace(payload[:openParen])
	if name == "" {
		return "", nil, fmt.Errorf("缺少函数名: %s", payload)
	}
	argsStr := strings.TrimSpace(payload[openParen+1 : closeParen])
	args, err := parseJSONArguments(argsStr)
	if err != nil {
		return "", nil, err
	}
	return name, args, nil
}

// parseJSONArguments 使用 JSON 解码器解析参数列表，只接受字符串实参。
func parseJSONArguments(content string) ([]string, error) {
	if strings.TrimSpace(content) == "" {
		return nil, nil
	}

	wrapped := fmt.Sprintf("[%s]", content)
	var rawArgs []json.RawMessage
	if err := json.Unmarshal([]byte(wrapped), &rawArgs); err != nil {
		return nil, fmt.Errorf("action 参数必须是合法的 JSON 字面量: %w", err)
	}

	args := make([]string, len(rawArgs))
	for i, item := range rawArgs {
		if err := json.Unmarshal(item, &args[i]); err != nil {
			return nil, fmt.Errorf("第 %d 个参数必须是字符串: %w", i+1, err)
		}
	}
	return args, nil
}

// operatingSystemName 返回人类可读的操作系统名称。
func operatingSystemName() string {
	switch runtime.GOOS {
	case "darwin":
		return "macOS"
	case "windows":
		return "Windows"
	case "linux":
		return "Linux"
	default:
		return "Unknown"
	}
}
