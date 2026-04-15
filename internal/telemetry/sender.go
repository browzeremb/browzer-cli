package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/browzeremb/browzer-cli/internal/tracker"
)

// Sender posts buckets to the apps/api telemetry endpoint.
type Sender struct {
	url        string
	bearer     string
	cliVersion string
	client     *http.Client
}

// NewSender constructs a sender.
func NewSender(url, bearer, cliVersion string) *Sender {
	return &Sender{
		url:        url,
		bearer:     bearer,
		cliVersion: cliVersion,
		client:     &http.Client{Timeout: 5 * time.Second},
	}
}

type wireBucket struct {
	Day         string  `json:"day"`
	Source      string  `json:"source"`
	FilterLevel *string `json:"filterLevel"`
	Model       *string `json:"model"`
	N           int     `json:"n"`
	InputBytes  int64   `json:"inputBytes"`
	OutputBytes int64   `json:"outputBytes"`
	SavedTokens int64   `json:"savedTokens"`
}

type wirePayload struct {
	SchemaVersion int          `json:"schemaVersion"`
	CLIVersion    string       `json:"cliVersion"`
	Buckets       []wireBucket `json:"buckets"`
}

// Send POSTs the batch as a single JSON document.
func (s *Sender) Send(ctx context.Context, buckets []tracker.Bucket) error {
	w := wirePayload{SchemaVersion: 1, CLIVersion: s.cliVersion, Buckets: make([]wireBucket, 0, len(buckets))}
	for _, b := range buckets {
		w.Buckets = append(w.Buckets, wireBucket{
			Day: b.Day, Source: b.Source, FilterLevel: b.FilterLevel, Model: b.Model,
			N: b.N, InputBytes: b.InputBytes, OutputBytes: b.OutputBytes, SavedTokens: b.SavedTokens,
		})
	}
	body, err := json.Marshal(w)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", s.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.bearer)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("telemetry POST failed: %s", resp.Status)
	}
	return nil
}
