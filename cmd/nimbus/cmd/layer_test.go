package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spf13/viper"
)

func TestLayerList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"layers": []map[string]interface{}{
				{
					"id":      "layer-1",
					"name":    "test-layer",
					"compatible_runtimes": []string{"python3.11"},
					"latest_version": 1,
					"created_at": "2026-01-26T00:00:00Z",
				},
			},
			"total": 1,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	viper.Set("api_url", server.URL)
	defer viper.Set("api_url", "")

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetArgs([]string{"layer", "list"})
	
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := buf.String()
	if !contains(output, "layer-1") || !contains(output, "test-layer") {
		t.Errorf("unexpected output: %s", output)
	}
}

func TestApiKeyCreate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":      "key-1",
			"name":    "test-key",
			"api_key": "nimbus_key_123",
		})
	}))
	defer server.Close()

	viper.Set("api_url", server.URL)
	defer viper.Set("api_url", "")

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetArgs([]string{"apikey", "create", "test-key"})
	
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := buf.String()
	if !contains(output, "created") || !contains(output, "nimbus_key_123") {
		t.Errorf("unexpected output: %s", output)
	}
}

func TestQuota(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"function_count": 5,
			"max_functions": 10,
			"function_usage_percent": 50.0,
			"total_memory_mb": 1024,
			"max_memory_mb": 2048,
			"memory_usage_percent": 50.0,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	viper.Set("api_url", server.URL)
	defer viper.Set("api_url", "")

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetArgs([]string{"quota"})
	
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := buf.String()
	if !contains(output, "Functions") || !contains(output, "50.0%") {
		t.Errorf("unexpected output: %s", output)
	}
}
