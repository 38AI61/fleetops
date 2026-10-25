package ingest

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"fleetops/internal/testinfra"
)

func TestEnsureStreamIdempotent(t *testing.T) {
	js := testinfra.NewJetStream(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := EnsureStream(ctx, js); err != nil {
			t.Fatalf("EnsureStream call %d: %v", i+1, err)
		}
	}

	s, err := js.Stream(ctx, StreamName)
	if err != nil {
		t.Fatalf("stream %s: %v", StreamName, err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	cfg := info.Config
	if !reflect.DeepEqual(cfg.Subjects, []string{"gps.raw.>"}) {
		t.Errorf("subjects = %v", cfg.Subjects)
	}
	if cfg.MaxAge != 60*time.Second {
		t.Errorf("MaxAge = %v, want 60s", cfg.MaxAge)
	}
	if cfg.MaxBytes != 268435456 {
		t.Errorf("MaxBytes = %d, want 268435456", cfg.MaxBytes)
	}
	if cfg.Storage != jetstream.FileStorage {
		t.Errorf("Storage = %v, want file", cfg.Storage)
	}
	if cfg.Discard != jetstream.DiscardOld {
		t.Errorf("Discard = %v, want old", cfg.Discard)
	}
	if cfg.Retention != jetstream.LimitsPolicy {
		t.Errorf("Retention = %v, want limits", cfg.Retention)
	}
	if cfg.Replicas != 1 {
		t.Errorf("Replicas = %d, want 1", cfg.Replicas)
	}
}
