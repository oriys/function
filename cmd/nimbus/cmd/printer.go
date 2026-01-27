// Package cmd 提供 nimbus 命令行工具的所有子命令实现。
// 本文件实现输出格式化打印功能，支持多种输出格式。
//
// Printer 支持以下输出格式：
//   - table: 表格格式（默认），适合人类阅读
//   - json:  JSON 格式，适合程序处理
//   - yaml:  YAML 格式，适合配置文件
//
// 提供了针对不同数据类型的打印方法：
//   - PrintFunctions:   打印函数列表
//   - PrintFunction:    打印单个函数详情
//   - PrintInvocations: 打印调用记录列表
//   - PrintInvokeResult: 打印调用结果
//   - PrintStatus:      打印系统状态
package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// Printer 是格式化输出的处理器。
// 根据配置的输出格式（table/json/yaml）将数据格式化后输出到指定的 writer。
type Printer struct {
	format string    // 输出格式：table、json 或 yaml
	writer io.Writer // 输出目标，默认为 os.Stdout
}

// NewPrinter 创建一个新的 Printer 实例。
// 从 viper 配置中读取 output 格式，如果未配置则默认使用 table 格式。
//
// 返回值：
//   - *Printer: 新创建的打印器实例
func NewPrinter() *Printer {
	format := viper.GetString("output")
	if format == "" {
		format = "table"
	}
	return &Printer{
		format: format,
		writer: os.Stdout,
	}
}

// PrintFunctions 打印函数列表。
// 根据配置的输出格式（table/json/yaml）格式化输出函数列表。
//
// 参数：
//   - functions: 要打印的函数列表
//
// 返回值：
//   - error: 打印失败时返回错误信息
func (p *Printer) PrintFunctions(functions []Function) error {
	switch p.format {
	case "json":
		return p.printJSON(functions)
	case "yaml":
		return p.printYAML(functions)
	default:
		return p.printFunctionsTable(functions)
	}
}

// PrintFunction 打印单个函数的详细信息。
// 根据配置的输出格式格式化输出函数详情。
//
// 参数：
//   - fn: 要打印的函数
//
// 返回值：
//   - error: 打印失败时返回错误信息
func (p *Printer) PrintFunction(fn *Function) error {
	switch p.format {
	case "json":
		return p.printJSON(fn)
	case "yaml":
		return p.printYAML(fn)
	default:
		return p.printFunctionDetail(fn)
	}
}

// PrintInvocations 打印调用记录列表。
// 根据配置的输出格式格式化输出调用记录。
//
// 参数：
//   - invocations: 要打印的调用记录列表
//
// 返回值：
//   - error: 打印失败时返回错误信息
func (p *Printer) PrintInvocations(invocations []Invocation) error {
	switch p.format {
	case "json":
		return p.printJSON(invocations)
	case "yaml":
		return p.printYAML(invocations)
	default:
		return p.printInvocationsTable(invocations)
	}
}

// PrintInvokeResult 打印函数调用结果。
// 根据配置的输出格式格式化输出调用响应。
//
// 参数：
//   - resp: 调用响应结果
//
// 返回值：
//   - error: 打印失败时返回错误信息
func (p *Printer) PrintInvokeResult(resp *InvokeResponse) error {
	switch p.format {
	case "json":
		return p.printJSON(resp)
	case "yaml":
		return p.printYAML(resp)
	default:
		return p.printInvokeResultDetail(resp)
	}
}

// PrintStatus 打印系统状态信息。
// 根据配置的输出格式格式化输出系统状态。
//
// 参数：
//   - status: 系统状态信息
//
// 返回值：
//   - error: 打印失败时返回错误信息
func (p *Printer) PrintStatus(status *SystemStatus) error {
	switch p.format {
	case "json":
		return p.printJSON(status)
	case "yaml":
		return p.printYAML(status)
	default:
		return p.printStatusDetail(status)
	}
}

// printJSON 以 JSON 格式输出数据。
// 使用 2 空格缩进美化输出。
func (p *Printer) printJSON(v interface{}) error {
	enc := json.NewEncoder(p.writer)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// printYAML 以 YAML 格式输出数据。
// 使用 2 空格缩进。
func (p *Printer) printYAML(v interface{}) error {
	enc := yaml.NewEncoder(p.writer)
	enc.SetIndent(2)
	return enc.Encode(v)
}

// printFunctionsTable 以表格形式输出函数列表。
// 显示名称、运行时、状态、内存、超时、调用次数和创建时间。
func (p *Printer) printFunctionsTable(functions []Function) error {
	if len(functions) == 0 {
		fmt.Fprintln(p.writer, "No functions found.")
		return nil
	}

	w := tabwriter.NewWriter(p.writer, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tRUNTIME\tSTATUS\tMEMORY\tTIMEOUT\tINVOCATIONS\tCREATED")

	for _, fn := range functions {
		fmt.Fprintf(w, "%s\t%s\t%s\t%dMB\t%ds\t%d\t%s\n",
			fn.Name,
			fn.Runtime,
			colorStatus(fn.Status),
			fn.MemoryMB,
			fn.TimeoutSec,
			fn.Invocations,
			timeAgo(fn.CreatedAt),
		)
	}

	return w.Flush()
}

// printFunctionDetail 以详细格式输出单个函数信息。
// 显示函数的所有配置项、状态和统计信息。
func (p *Printer) printFunctionDetail(fn *Function) error {
	fmt.Fprintf(p.writer, "Name:        %s\n", fn.Name)
	fmt.Fprintf(p.writer, "ID:          %s\n", fn.ID)
	fmt.Fprintf(p.writer, "Runtime:     %s\n", fn.Runtime)
	fmt.Fprintf(p.writer, "Handler:     %s\n", fn.Handler)
	fmt.Fprintf(p.writer, "Status:      %s\n", colorStatus(fn.Status))
	fmt.Fprintf(p.writer, "Memory:      %d MB\n", fn.MemoryMB)
	fmt.Fprintf(p.writer, "Timeout:     %d seconds\n", fn.TimeoutSec)
	fmt.Fprintf(p.writer, "Invocations: %d\n", fn.Invocations)
	fmt.Fprintf(p.writer, "Created:     %s\n", fn.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(p.writer, "Updated:     %s\n", fn.UpdatedAt.Format(time.RFC3339))

	if len(fn.EnvVars) > 0 {
		fmt.Fprintln(p.writer, "Environment:")
		for k, v := range fn.EnvVars {
			fmt.Fprintf(p.writer, "  %s=%s\n", k, v)
		}
	}

	if fn.Code != "" {
		fmt.Fprintln(p.writer, "\nCode:")
		fmt.Fprintln(p.writer, "---")
		fmt.Fprintln(p.writer, fn.Code)
		fmt.Fprintln(p.writer, "---")
	}

	return nil
}

// printInvocationsTable 以表格形式输出调用记录列表。
// 显示调用ID、状态、耗时、冷启动标识和开始时间。
func (p *Printer) printInvocationsTable(invocations []Invocation) error {
	if len(invocations) == 0 {
		fmt.Fprintln(p.writer, "No invocations found.")
		return nil
	}

	w := tabwriter.NewWriter(p.writer, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tDURATION\tCOLD START\tSTARTED")

	for _, inv := range invocations {
		coldStart := "No"
		if inv.ColdStart {
			coldStart = "Yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%dms\t%s\t%s\n",
			truncate(inv.ID, 12),
			colorStatus(inv.Status),
			inv.DurationMs,
			coldStart,
			timeAgo(inv.StartedAt),
		)
	}

	return w.Flush()
}

// printInvokeResultDetail 以详细格式输出调用结果。
// 显示调用ID、状态、耗时、冷启动标识、错误信息和结果。
func (p *Printer) printInvokeResultDetail(resp *InvokeResponse) error {
	status := "success"
	if resp.StatusCode < 200 || resp.StatusCode > 299 || resp.Error != "" {
		status = "failed"
	}

	fmt.Fprintf(p.writer, "Invocation ID: %s\n", resp.RequestID)
	fmt.Fprintf(p.writer, "Status:        %s\n", colorStatus(status))
	fmt.Fprintf(p.writer, "Status Code:   %d\n", resp.StatusCode)
	fmt.Fprintf(p.writer, "Duration:      %d ms\n", resp.DurationMs)

	coldStart := "No"
	if resp.ColdStart {
		coldStart = "Yes"
	}
	fmt.Fprintf(p.writer, "Cold Start:    %s\n", coldStart)

	if resp.Error != "" {
		fmt.Fprintf(p.writer, "Error:         %s\n", resp.Error)
	}

	if resp.BilledTimeMs > 0 {
		fmt.Fprintf(p.writer, "Billed Time:   %d ms\n", resp.BilledTimeMs)
	}

	if len(resp.Body) > 0 {
		fmt.Fprintln(p.writer, "\nBody:")
		var prettyJSON bytes.Buffer
		if err := json.Indent(&prettyJSON, resp.Body, "", "  "); err == nil {
			fmt.Fprintln(p.writer, prettyJSON.String())
		} else {
			fmt.Fprintln(p.writer, string(resp.Body))
		}
	}

	return nil
}

// printStatusDetail 以详细格式输出系统状态。
// 显示服务状态、版本、运行时间和 VM 池统计。
func (p *Printer) printStatusDetail(status *SystemStatus) error {
	fmt.Fprintf(p.writer, "Status:  %s\n", colorStatus(status.Status))
	fmt.Fprintf(p.writer, "Version: %s\n", status.Version)
	fmt.Fprintf(p.writer, "Uptime:  %s\n", status.Uptime)

	if len(status.PoolStats) > 0 {
		fmt.Fprintln(p.writer, "\nVM Pool Stats:")
		w := tabwriter.NewWriter(p.writer, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "RUNTIME\tWARM\tBUSY\tTOTAL\tMAX")
		for _, ps := range status.PoolStats {
			fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\n",
				ps.Runtime,
				ps.WarmVMs,
				ps.BusyVMs,
				ps.TotalVMs,
				ps.MaxVMs,
			)
		}
		w.Flush()
	}

	return nil
}

// PrintTemplates 打印模板列表。
func (p *Printer) PrintTemplates(templates []Template) error {
	if len(templates) == 0 {
		fmt.Fprintln(p.writer, "No templates found.")
		return nil
	}

	w := tabwriter.NewWriter(p.writer, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tRUNTIME\tCATEGORY\tDESCRIPTION")

	for _, t := range templates {
		popularSuffix := ""
		if t.Popular {
			popularSuffix = " [popular]"
		}
		fmt.Fprintf(w, "%s%s\t%s\t%s\t%s\n",
			t.Name,
			popularSuffix,
			t.Runtime,
			t.Category,
			truncate(t.Description, 50),
		)
	}

	return w.Flush()
}

// ====== 辅助函数 ======

// colorStatus 根据状态值返回带颜色的字符串。
// 使用 ANSI 转义序列：
//   - 绿色: active、success、healthy、running
//   - 黄色: pending、building
//   - 红色: failed、error、unhealthy
func colorStatus(status string) string {
	switch strings.ToLower(status) {
	case "active", "success", "healthy", "running":
		return "\033[32m" + status + "\033[0m" // Green
	case "pending", "building":
		return "\033[33m" + status + "\033[0m" // Yellow
	case "failed", "error", "unhealthy":
		return "\033[31m" + status + "\033[0m" // Red
	default:
		return status
	}
}

// timeAgo 将时间转换为相对时间字符串。
// 例如："5s ago"、"3m ago"、"2h ago"、"1d ago"
func timeAgo(t time.Time) string {
	if t.IsZero() {
		return "-"
	}

	d := time.Since(t)

	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// truncate 截断字符串到指定长度。
// 如果字符串超过最大长度，则截断并添加 "..." 后缀。
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
