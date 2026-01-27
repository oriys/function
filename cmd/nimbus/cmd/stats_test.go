package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spf13/viper"
)

func TestEnvironmentList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"environments": []map[string]interface{}{
				{
					"id":      "env-1",
					"name":    "prod",
					"is_default": true,
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
	rootCmd.SetArgs([]string{"environment", "list"})
	
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := buf.String()
	if !contains(output, "env-1") || !contains(output, "prod") || !contains(output, "✅") {
		t.Errorf("unexpected output: %s", output)
	}
}

func TestStats(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"functions":   123,
			"invocations": 456,
		})
	}))
	defer server.Close()

	viper.Set("api_url", server.URL)
	defer viper.Set("api_url", "")

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetArgs([]string{"stats"})
	
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := buf.String()
	if !contains(output, "123") || !contains(output, "456") {
		t.Errorf("unexpected output: %s", output)
	}
}
