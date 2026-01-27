// Package cmd 提供 nimbus 命令行工具的所有子命令实现。
// 本文件实现 logs 命令，用于查看函数的调用历史记录。
//
// 该命令会显示指定函数最近的调用记录，包括调用ID、状态、执行时间等信息。
// 可以通过 --limit 参数控制显示的记录数量，默认显示最近20条。
// 支持以 JSON 或 YAML 格式输出。
package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// logsCmd 是 logs 命令的 cobra.Command 实例。
// 该命令用于查看指定函数的调用历史，显示每次调用的状态、耗时等信息。
// 可以通过 --limit 参数限制显示的记录数量。
var logsCmd = &cobra.Command{
	Use:   "logs <name>",
	Short: "View function invocation history",
	Long: `View the invocation history of a function.

Examples:
  # View recent invocations
  nimbus logs hello

  # Follow realtime logs (WebSocket stream)
  nimbus logs hello --follow

  # View last N invocations
  nimbus logs hello --limit 50

  # Output as JSON
  nimbus logs hello -o json`,
	Args: cobra.ExactArgs(1),
	RunE: runLogs,
}

// logsLimit 控制显示的调用记录数量，默认为20条。
var logsLimit int

// logsFollow 是否跟随实时日志流。
var logsFollow bool

// init 注册 logs 命令并设置命令行标志。
func init() {
	rootCmd.AddCommand(logsCmd)
	logsCmd.Flags().IntVarP(&logsLimit, "limit", "n", 20, "Number of invocations to show")
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "Follow realtime logs (WebSocket stream)")
}

// runLogs 是 logs 命令的执行函数。
// 该函数执行以下操作：
//  1. 根据函数名称获取函数信息（需要函数ID）
//  2. 调用 API 获取该函数的调用记录
//  3. 根据 --limit 参数限制输出数量
//  4. 以指定格式输出调用记录列表
//
// 参数：
//   - cmd: cobra 命令对象
//   - args: 命令行参数，args[0] 是函数名称
//
// 返回值：
//   - error: 获取记录失败时返回错误信息
func runLogs(cmd *cobra.Command, args []string) error {
	name := args[0]

	// First get the function to get its ID
	client := NewClient()
	fn, err := client.GetFunction(name)
	if err != nil {
		return err
	}

	if logsFollow {
		return followLogs(client.baseURL, fn)
	}

	invocations, err := client.ListInvocations(fn.ID, logsLimit)
	if err != nil {
		return err
	}

	if len(invocations) == 0 {
		fmt.Printf("No invocations found for function '%s'.\n", name)
		return nil
	}

	printer := NewPrinter()
	fmt.Printf("Recent invocations for function '%s':\n\n", name)
	return printer.PrintInvocations(invocations)
}

type streamLogMessage struct {
	Timestamp    string          `json:"timestamp"`
	Level        string          `json:"level"`
	FunctionID   string          `json:"function_id"`
	FunctionName string          `json:"function_name"`
	Message      string          `json:"message"`
	RequestID    string          `json:"request_id,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	Output       json.RawMessage `json:"output,omitempty"`
	Error        string          `json:"error,omitempty"`
	DurationMs   int64           `json:"duration_ms,omitempty"`
}

func followLogs(baseURL string, fn *Function) error {
	wsURL, err := buildWebSocketURL(baseURL, "/api/console/logs/stream")
	if err != nil {
		return err
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("failed to connect log stream: %w", err)
	}
	defer conn.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		_ = conn.Close()
	}()

	fmt.Printf("Following logs for function '%s' (Ctrl+C to stop)...\n", fn.Name)

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			// If user interrupted, treat as graceful exit.
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("log stream closed: %w", err)
		}

		var msg streamLogMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg.FunctionID != fn.ID && msg.FunctionName != fn.Name {
			continue
		}

		if err := printStreamLogMessage(data, &msg); err != nil {
			return err
		}
	}
}

func printStreamLogMessage(raw []byte, msg *streamLogMessage) error {
	switch viper.GetString("output") {
	case "json":
		fmt.Fprintln(os.Stdout, string(raw))
		return nil
	case "yaml":
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		out, err := yaml.Marshal(v)
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, string(out))
		return nil
	default:
		// Human-friendly output
		line := fmt.Sprintf("%s\t%s\t%s", msg.Timestamp, msg.Level, msg.Message)
		if msg.RequestID != "" {
			line += fmt.Sprintf("\trequest_id=%s", msg.RequestID)
		}
		if msg.DurationMs > 0 {
			line += fmt.Sprintf("\tduration_ms=%d", msg.DurationMs)
		}
		if msg.Error != "" {
			line += fmt.Sprintf("\terror=%s", msg.Error)
		}
		fmt.Fprintln(os.Stdout, line)

		printJSONBlock("input", msg.Input)
		printJSONBlock("output", msg.Output)
		return nil
	}
}

func printJSONBlock(label string, raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "  ", "  "); err == nil {
		fmt.Fprintf(os.Stdout, "  %s:\n%s\n", label, buf.String())
		return
	}
	fmt.Fprintf(os.Stdout, "  %s:\n  %s\n", label, string(raw))
}

func buildWebSocketURL(baseURL, path string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid api url: %w", err)
	}

	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
		// ok
	default:
		return "", fmt.Errorf("unsupported api url scheme: %s", u.Scheme)
	}

	u.Path = path
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}
