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

func TestDeployNew(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock GET returns 404 (not found)
		if r.Method == "GET" {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
			return
		}
		
		// Mock POST creates function
		if r.Method == "POST" {
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":   "fn-1",
				"name": "test-fn",
			})
			return
		}
	}))
	defer server.Close()

	viper.Set("api_url", server.URL)
	defer viper.Set("api_url", "")

	tmpFile := t.TempDir() + "/index.js"
	if err := os.WriteFile(tmpFile, []byte("console.log('hi')"), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetArgs([]string{"deploy", "test-fn", "--runtime", "nodejs20", "--file", tmpFile})
	
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := buf.String()
	if !contains(output, "Creating new function") || !contains(output, "deployed successfully") {
		t.Errorf("unexpected output: %s", output)
	}
}

func TestUpdate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			var req map[string]interface{}
			json.NewDecoder(r.Body).Decode(&req)
			
			// Verify partial update fields
			if _, ok := req["memory_mb"]; !ok {
				t.Errorf("expected memory_mb in request")
			}
			
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":   "fn-1",
				"name": "test-fn",
			})
			return
		}
	}))
	defer server.Close()

	viper.Set("api_url", server.URL)
	defer viper.Set("api_url", "")

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetArgs([]string{"update", "test-fn", "--memory", "512"})
	
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := buf.String()
	if !contains(output, "updated successfully") {
		t.Errorf("unexpected output: %s", output)
	}
}
