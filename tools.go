package main

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	_ "github.com/gaoyuan98/dm"
)

// ToolFunc 定义单个工具的执行函数签名。
type ToolFunc func(args ...string) (string, error)

// Tool 描述一个可供模型调用的工具。
type Tool struct {
	Name        string
	Signature   string
	Description string
	Handler     ToolFunc
}

// newReadFileTool 构造 read_file 工具，用于读取文件内容。
func newReadFileTool() Tool {
	return Tool{
		Name:        "read_file",
		Signature:   "(file_path string)",
		Description: "用于读取文件内容",
		Handler: func(args ...string) (string, error) {
			if len(args) != 1 {
				return "", errors.New("read_file 需要 1 个参数")
			}
			data, err := os.ReadFile(args[0])
			if err != nil {
				return "", err
			}
			return string(data), nil
		},
	}
}

// newWriteFileTool 构造 write_to_file 工具，用于写入文件。
func newWriteFileTool() Tool {
	return Tool{
		Name:        "write_to_file",
		Signature:   "(file_path string, content string)",
		Description: "将内容写入目标文件",
		Handler: func(args ...string) (string, error) {
			if len(args) != 2 {
				return "", errors.New("write_to_file 需要 2 个参数")
			}
			content := strings.ReplaceAll(args[1], "\\n", "\n")
			if err := os.WriteFile(args[0], []byte(content), 0o644); err != nil {
				return "", err
			}
			return "写入成功", nil
		},
	}
}

// newRunCommandTool 构造 run_terminal_command 工具，执行系统命令。
func newRunCommandTool() Tool {
	return Tool{
		Name:        "run_terminal_command",
		Signature:   "(command string)",
		Description: "执行本地终端命令",
		Handler: func(args ...string) (string, error) {
			if len(args) != 1 {
				return "", errors.New("run_terminal_command 需要 1 个参数")
			}
			cmd := buildShellCommand(args[0])
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				return "", fmt.Errorf("%v: %s", err, stderr.String())
			}
			result := strings.TrimSpace(stdout.String())
			if result == "" {
				return "命令执行成功（无输出）", nil
			}
			return result, nil
		},
	}
}

// newQueryDatabaseTool 构造 query_database 工具，用于连接数据库并查询 SQL。
func newQueryDatabaseTool() Tool {
	return Tool{
		Name:        "query_database",
		Signature:   "(dsn string, sql string)",
		Description: "连接达梦数据库并执行查询，返回表格化结果",
		Handler: func(args ...string) (string, error) {
			if len(args) != 2 {
				return "", errors.New("query_database 需要 2 个参数")
			}
			dsn, err := normalizeDMDSN(args[0])
			if err != nil {
				return "", err
			}
			query := strings.TrimSpace(args[1])
			if dsn == "" {
				return "", errors.New("数据库连接串不能为空")
			}
			if query == "" {
				return "", errors.New("SQL 语句不能为空")
			}

			db, err := sql.Open("dm", dsn)
			if err != nil {
				return "", fmt.Errorf("打开数据库失败: %w", err)
			}
			defer db.Close()

			if err := db.Ping(); err != nil {
				return "", fmt.Errorf("数据库不可用: %w", err)
			}

			rows, err := db.Query(query)
			if err != nil {
				return "", fmt.Errorf("查询失败: %w", err)
			}
			defer rows.Close()

			columns, err := rows.Columns()
			if err != nil {
				return "", err
			}
			if len(columns) == 0 {
				return "查询成功，但无返回列", nil
			}

			var builder strings.Builder
			builder.WriteString(strings.Join(columns, "\t"))
			builder.WriteString("\n")

			rowCount := 0
			for rows.Next() {
				rowCount++
				rawValues := make([]sql.RawBytes, len(columns))
				scanValues := make([]interface{}, len(columns))
				for i := range rawValues {
					scanValues[i] = &rawValues[i]
				}
				if err := rows.Scan(scanValues...); err != nil {
					return "", err
				}

				rowValues := make([]string, len(columns))
				for i, raw := range rawValues {
					if raw == nil {
						rowValues[i] = "NULL"
						continue
					}
					rowValues[i] = string(raw)
				}
				builder.WriteString(strings.Join(rowValues, "\t"))
				builder.WriteString("\n")
			}

			if err := rows.Err(); err != nil {
				return "", err
			}

			if rowCount == 0 {
				return "查询成功，但没有数据返回", nil
			}

			return strings.TrimSpace(builder.String()), nil
		},
	}
}

// buildShellCommand 根据操作系统封装终端执行命令。
func buildShellCommand(command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("powershell", "-Command", command)
	}
	return exec.Command("bash", "-lc", command)
}

// normalizeDMDSN 去除 BOM/空白并强制以小写 dm:// 开头，避免驱动大小写敏感。
func normalizeDMDSN(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimPrefix(trimmed, "\ufeff")
	if trimmed == "" {
		return "", errors.New("数据库连接串不能为空")
	}
	if len(trimmed) < 5 {
		return "", errors.New("数据库连接串格式不正确")
	}
	if strings.EqualFold(trimmed[:5], "dm://") {
		return "dm://" + trimmed[5:], nil
	}
	return "", errors.New("数据库连接串必须以 dm:// 开头")
}
