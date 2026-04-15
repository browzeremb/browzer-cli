package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/browzeremb/browzer-cli/internal/tracker"
)

func TestSender_PostsBatchAndChecksAuth(t *testing.T) {
	var received string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		received = string(body)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"accepted":1}`))
	}))
	defer srv.Close()

	s := NewSender(srv.URL+"/api/telemetry/usage", "test-token", "1.4.0")
	err := s.Send(context.Background(), []tracker.Bucket{{
		Day: "2026-04-15", Source: "hook-read", N: 1, InputBytes: 100, OutputBytes: 20, SavedTokens: 20,
	}})
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		SchemaVersion int    `json:"schemaVersion"`
		CLIVersion    string `json:"cliVersion"`
		Buckets       []map[string]any
	}
	_ = json.Unmarshal([]byte(received), &parsed)
	if parsed.SchemaVersion != 1 || parsed.CLIVersion != "1.4.0" || len(parsed.Buckets) != 1 {
		t.Fatalf("payload: %#v", parsed)
	}
}

func TestSender_4xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusBadRequest)
	}))
	defer srv.Close()
	s := NewSender(srv.URL+"/api/telemetry/usage", "tok", "1.4.0")
	err := s.Send(context.Background(), []tracker.Bucket{{Day: "2026-04-15", Source: "cli", N: 1}})
	if err == nil {
		t.Fatal("expected error on 400")
	}
}
