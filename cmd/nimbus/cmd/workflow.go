// Package cmd 提供 nimbus 命令行工具的所有子命令实现。
// 本文件实现 workflow 命令，用于管理工作流。
package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var workflowCmd = &cobra.Command{
	Use:   "workflow",
	Short: "Manage workflows",
	Long:  `Create, list, and execute workflows.`,
}

var workflowListCmd = &cobra.Command{
	Use:   "list",
	Short: "List workflows",
	RunE:  runWorkflowList,
}

var workflowCreateCmd = &cobra.Command{
	Use:   "create <name> --file <definition-file>",
	Short: "Create a workflow",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowCreate,
}

var workflowDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a workflow",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowDelete,
}

var workflowRunCmd = &cobra.Command{
	Use:   "run <id> [--data <json-input>]",
	Short: "Start a workflow execution",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowRun,
}

var (
	workflowFile string
	workflowData string
)

func init() {
	rootCmd.AddCommand(workflowCmd)
	workflowCmd.AddCommand(workflowListCmd)
	workflowCmd.AddCommand(workflowCreateCmd)
	workflowCmd.AddCommand(workflowDeleteCmd)
	workflowCmd.AddCommand(workflowRunCmd)

	workflowCreateCmd.Flags().StringVarP(&workflowFile, "file", "f", "", "Workflow definition file (JSON)")
	workflowCreateCmd.MarkFlagRequired("file")

	workflowRunCmd.Flags().StringVarP(&workflowData, "data", "d", "{}", "JSON input data for the workflow")
}

func runWorkflowList(cmd *cobra.Command, args []string) error {
	client := NewClient()
	workflows, err := client.ListWorkflows()
	if err != nil {
		return err
	}

	if len(workflows) == 0 {
		cmd.Println("No workflows found.")
		return nil
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tSTATUS\tVERSION\tCREATED")
	for _, wf := range workflows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n",
			wf.ID, wf.Name, wf.Status, wf.Version, wf.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	return w.Flush()
}

func runWorkflowCreate(cmd *cobra.Command, args []string) error {
	name := args[0]
	data, err := os.ReadFile(workflowFile)
	if err != nil {
		return fmt.Errorf("failed to read workflow file: %w", err)
	}

	var definition WorkflowDefinition
	if err := json.Unmarshal(data, &definition); err != nil {
		return fmt.Errorf("failed to parse workflow definition: %w", err)
	}

	req := map[string]interface{}{
		"name":       name,
		"definition": definition,
	}

	client := NewClient()
	wf, err := client.CreateWorkflow(req)
	if err != nil {
		return err
	}

	cmd.Printf("✅ Workflow '%s' created with ID: %s\n", wf.Name, wf.ID)
	return nil
}

func runWorkflowDelete(cmd *cobra.Command, args []string) error {
	client := NewClient()
	if err := client.DeleteWorkflow(args[0]); err != nil {
		return err
	}
	cmd.Println("✅ Workflow deleted.")
	return nil
}

func runWorkflowRun(cmd *cobra.Command, args []string) error {
	id := args[0]
	client := NewClient()

	var input json.RawMessage
	if err := json.Unmarshal([]byte(workflowData), &input); err != nil {
		return fmt.Errorf("invalid JSON data: %w", err)
	}

	exec, err := client.StartWorkflowExecution(id, input)
	if err != nil {
		return err
	}

	cmd.Printf("🚀 Workflow execution started. ID: %s, Status: %s\n", exec.ID, exec.Status)
	return nil
}
