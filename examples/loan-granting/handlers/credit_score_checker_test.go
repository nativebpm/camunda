package handlers

import (
	"log/slog"
	"os"
	"testing"
)

func TestCreditScoreChecker_Handle(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	handler := NewCreditScoreChecker(logger)

	// Note: For full unit tests, you would mock the Camunda client interface
	// and test the Handle method with various scenarios

	// For now, we test that the handler can be created
	if handler == nil {
		t.Fatal("Expected handler to be created")
	}

	// Test handler structure
	if handler.logger == nil {
		t.Error("Expected handler to have a logger")
	}
}

func TestNewCreditScoreChecker(t *testing.T) {
	logger := slog.Default()
	handler := NewCreditScoreChecker(logger)

	if handler == nil {
		t.Fatal("Expected NewCreditScoreChecker to return a non-nil handler")
	}

	if handler.logger != logger {
		t.Error("Expected handler logger to match provided logger")
	}
}
