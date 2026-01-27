package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/spf13/viper"
)

func TestWorkflowList(t *testing.T) {
	// Mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/workflows" {
			t.Errorf("expected path /api/v1/workflows, got %s", r.URL.Path)
		}
		
		resp := map[string]interface{}{
			"workflows": []map[string]interface{}{
				{
					"id":      "wf-1",
					"name":    "test-workflow",
					"status":  "active",
					"version": 1,
					"created_at": "2026-01-26T00:00:00Z",
				},
			},
			"total": 1,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Configure client to use mock server
	viper.Set("api_url", server.URL)
	defer viper.Set("api_url", "")

	// Run command
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetArgs([]string{"workflow", "list"})
	
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := buf.String()
	if !contains(output, "wf-1") || !contains(output, "test-workflow") {
		t.Errorf("unexpected output: %s", output)
	}
}

func TestWorkflowCreate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/workflows" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":   "wf-new",
			"name": "new-wf",
		})
	}))
	defer server.Close()

	viper.Set("api_url", server.URL)
	defer viper.Set("api_url", "")

	// Create dummy definition file
	tmpFile := t.TempDir() + "/wf.json"
	importData := `{"start_at": "X", "states": {"X": {"type": "Pass", "end": true}}}`
	if err := os.WriteFile(tmpFile, []byte(importData), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetArgs([]string{"workflow", "create", "new-wf", "--file", tmpFile})
	
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := buf.String()
	if !contains(output, "created") || !contains(output, "wf-new") {
		t.Errorf("unexpected output: %s", output)
	}
}

func contains(s, substr string) bool {
	return bytes.Contains([]byte(s), []byte(substr))
}
