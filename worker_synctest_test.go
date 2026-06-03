// Test file for synctest

package camunda

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nativebpm/httpstream"
)

type mockTransport struct {
	roundTrip func(*http.Request) (*http.Response, error)
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.roundTrip(req)
}

func TestWorker_Start_Synctest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Mock HTTP transport to avoid real network calls
		transport := &mockTransport{
			roundTrip: func(req *http.Request) (*http.Response, error) {
				if req.Body != nil {
					_, _ = io.Copy(io.Discard, req.Body)
					_ = req.Body.Close()
				}
				if req.URL.Path == "/engine-rest/external-task/fetchAndLock" {
					respJSON := `[]`
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(bytes.NewBufferString(respJSON)),
						Header:     make(http.Header),
					}, nil
				}
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(bytes.NewBufferString("")),
					Header:     make(http.Header),
				}, nil
			},
		}

		httpClient, err := httpstream.NewClient(&http.Client{Transport: transport}, "http://localhost:8080")
		if err != nil {
			t.Fatalf("failed to create http client: %v", err)
		}

		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		worker := testNewWorker(httpClient, "test-worker", logger)

		// Set a poll interval of 5 seconds (default).
		// Real-world testing would wait 15 seconds for 3 polls.
		// Inside the synctest bubble, it advances instantly!
		worker.SetPollInterval(5 * time.Second)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Start worker loop in background goroutine (within bubble)
		go worker.Start(ctx)

		// Sleep for 16 virtual seconds. This will instantly trigger 3 poll ticks!
		time.Sleep(16 * time.Second)

		// Cancel context to stop the worker
		cancel()

		// Give the worker a moment to stop
		time.Sleep(100 * time.Millisecond)
	})
}
