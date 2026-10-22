package main

import (
	"log/slog"
	"strings"
	"testing"
)

func TestInvalidDrainTimeout(t *testing.T) {
	for _, value := range []string{"0s", "-1s", "invalid"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("DRAIN_TIMEOUT", value)
			err := run(slog.New(slog.DiscardHandler))
			if err == nil || !strings.Contains(err.Error(), "DRAIN_TIMEOUT") {
				t.Fatalf("got %v", err)
			}
		})
	}
}
