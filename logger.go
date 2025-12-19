package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// AgentLogger 将代理运行日志同步写入控制台与文件。
type AgentLogger struct {
	writer io.Writer
	file   *os.File
	path   string
	mu     sync.Mutex
}

// NewAgentLogger 创建日志记录器，如有需要会自动创建目录。
func NewAgentLogger(path string) (*AgentLogger, error) {
	dir := filepath.Dir(path)
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}

	return &AgentLogger{
		writer: io.MultiWriter(os.Stdout, file),
		file:   file,
		path:   path,
	}, nil
}

// Close 关闭底层文件句柄。
func (l *AgentLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}

// Path 返回日志文件路径。
func (l *AgentLogger) Path() string {
	return l.path
}

// StartRound 输出轮次标题，便于人工阅读。
func (l *AgentLogger) StartRound(round int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.writer, "\n==== Round %d ====\n", round)
}

// Record 输出结构化日志块，包含统一时间戳与标签。
func (l *AgentLogger) Record(label, content string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	body := strings.TrimRight(content, "\r\n")
	if body == "" {
		body = "(空)"
	}

	fmt.Fprintf(l.writer, "[%s] [%s]\n", timestamp, label)
	for _, line := range strings.Split(body, "\n") {
		fmt.Fprintf(l.writer, "  %s\n", strings.TrimRight(line, " \t"))
	}
	fmt.Fprintln(l.writer)
}
