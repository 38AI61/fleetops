package testinfra

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

func TestMain(m *testing.M) {
	code := m.Run()
	TerminateNATS()
	os.Exit(code)
}

func TestNATSHelperFetchesPublished(t *testing.T) {
	js := NewJetStream(t)
	ctx := context.Background()
	if _, err := js.CreateStream(ctx, jetstream.StreamConfig{
		Name:     gpsRawStream,
		Subjects: []string{"gps.raw.>"},
	}); err != nil {
		t.Fatalf("create stream: %v", err)
	}

	want := []string{"one", "two", "three"}
	for _, p := range want {
		if _, err := js.Publish(ctx, "gps.raw.7", []byte(p)); err != nil {
			t.Fatalf("publish %s: %v", p, err)
		}
	}

	got := FetchSubject(t, js, gpsRawStream, "gps.raw.7", len(want), 5*time.Second)
	var payloads []string
	for _, b := range got {
		payloads = append(payloads, string(b))
	}
	if !reflect.DeepEqual(payloads, want) {
		t.Fatalf("FetchSubject = %v, want %v", payloads, want)
	}
}
