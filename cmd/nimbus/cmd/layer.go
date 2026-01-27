// Package cmd 提供 nimbus 命令行工具的所有子命令实现。
// 本文件实现 layer 命令，用于管理函数层。
package cmd

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var layerCmd = &cobra.Command{
	Use:   "layer",
	Short: "Manage function layers",
	Long:  `Create, list, and delete function layers.`,
}

var layerListCmd = &cobra.Command{
	Use:   "list",
	Short: "List layers",
	RunE:  runLayerList,
}

var layerCreateCmd = &cobra.Command{
	Use:   "create <name> --runtimes <runtime1,runtime2>",
	Short: "Create a layer",
	Args:  cobra.ExactArgs(1),
	RunE:  runLayerCreate,
}

var layerDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a layer",
	Args:  cobra.ExactArgs(1),
	RunE:  runLayerDelete,
}

var (
	layerRuntimes []string
	layerDesc     string
)

func init() {
	rootCmd.AddCommand(layerCmd)
	layerCmd.AddCommand(layerListCmd)
	layerCmd.AddCommand(layerCreateCmd)
	layerCmd.AddCommand(layerDeleteCmd)

	layerCreateCmd.Flags().StringSliceVarP(&layerRuntimes, "runtimes", "r", nil, "Compatible runtimes (comma-separated)")
	layerCreateCmd.Flags().StringVarP(&layerDesc, "description", "d", "", "Layer description")
	layerCreateCmd.MarkFlagRequired("runtimes")
}

func runLayerList(cmd *cobra.Command, args []string) error {
	client := NewClient()
	layers, err := client.ListLayers()
	if err != nil {
		return err
	}

	if len(layers) == 0 {
		cmd.Println("No layers found.")
		return nil
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tRUNTIMES\tVERSION\tCREATED")
	for _, l := range layers {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n",
			l.ID, l.Name, strings.Join(l.CompatibleRuntimes, ","), l.LatestVersion, l.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	return w.Flush()
}

func runLayerCreate(cmd *cobra.Command, args []string) error {
	name := args[0]
	req := map[string]interface{}{
		"name":                name,
		"description":         layerDesc,
		"compatible_runtimes": layerRuntimes,
	}

	client := NewClient()
	layer, err := client.CreateLayer(req)
	if err != nil {
		return err
	}

	cmd.Printf("✅ Layer '%s' created with ID: %s\n", layer.Name, layer.ID)
	return nil
}

func runLayerDelete(cmd *cobra.Command, args []string) error {
	client := NewClient()
	if err := client.DeleteLayer(args[0]); err != nil {
		return err
	}
	cmd.Println("✅ Layer deleted.")
	return nil
}
