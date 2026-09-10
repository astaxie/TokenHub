package server

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"
	"time"
)

func TestPingLogsPoolStatsWhenConnectionIsUnavailable(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	sqlDB, err := store.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	connection, err := sqlDB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	var logs bytes.Buffer
	previousOutput := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previousOutput)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := store.Ping(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Ping error = %v, want context deadline exceeded", err)
	}
	for _, expected := range []string{"database ping failed", "in_use=1", "idle=0", "wait_count=", "wait_duration=", "error=context deadline exceeded"} {
		if !strings.Contains(logs.String(), expected) {
			t.Fatalf("Ping log %q does not contain %q", logs.String(), expected)
		}
	}
}
