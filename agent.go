package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// loadEnvFile 从指定路径读取 .env 文件并写入当前进程环境变量。
func loadEnvFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		if key != "" {
			_ = os.Setenv(key, value)
		}
	}
	return scanner.Err()
}

// main 负责解析命令行、加载配置并启动 ReAct Agent。
func main() {
	projectDir := flag.String("project", ".", "项目根目录")
	model := flag.String("model", "qwen3-max", "模型名称")
	questionFlag := flag.String("question", "", "直接传入问题，留空则交互式输入")
	logFileFlag := flag.String("log-file", "", "日志输出文件路径（默认写入项目目录 agent_run_时间.log）")
	flag.Parse()

	absProjectDir, err := filepath.Abs(*projectDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "解析项目路径失败: %v\n", err)
		os.Exit(1)
	}

	_ = loadEnvFile(filepath.Join(absProjectDir, ".env"))
	_ = loadEnvFile(".env")

	logPath := strings.TrimSpace(*logFileFlag)
	if logPath == "" {
		logPath = filepath.Join(absProjectDir, fmt.Sprintf("agent_run_%s.log", time.Now().Format("20060102_150405")))
	} else if !filepath.IsAbs(logPath) {
		logPath = filepath.Join(absProjectDir, logPath)
	}
	logger, err := NewAgentLogger(logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化日志失败: %v\n", err)
		os.Exit(1)
	}
	defer logger.Close()
	logger.Record("日志", fmt.Sprintf("输出将同步保存到 %s", logPath))

	client := openai.NewClient(
		option.WithAPIKey("填写成自己的密钥"),
		option.WithBaseURL("https://dashscope.aliyuncs.com/compatible-mode/v1"),
	)

	question := strings.TrimSpace(*questionFlag)
	if question == "" {
		fmt.Print("请输入任务: ")
		reader := bufio.NewReader(os.Stdin)
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			fmt.Fprintf(os.Stderr, "读取输入失败: %v\n", err)
			os.Exit(1)
		}
		question = strings.TrimSpace(line)
		if question == "" {
			fmt.Fprintln(os.Stderr, "问题不能为空")
			os.Exit(1)
		}
	}
	logger.Record("问题", question)

	tools := []Tool{
		newReadFileTool(),
		newWriteFileTool(),
		newRunCommandTool(),
		newQueryDatabaseTool(),
	}

	agent := NewReActAgent(absProjectDir, *model, reactSystemPromptTemplate, client, tools, logger)

	answer, err := agent.Run(context.Background(), question)
	if err != nil {
		fmt.Fprintf(os.Stderr, "运行失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n最终答案: %s\n", answer)
}
