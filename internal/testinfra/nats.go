package testinfra

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	natsmodule "github.com/testcontainers/testcontainers-go/modules/nats"
)

// NATSImage is the NATS server image used by tests (JetStream is enabled by the module).
const NATSImage = "nats:2"

// gpsRawStream is the stream NewJetStream clears between tests. The caller
// re-creates it with the production config (GF-65). testinfra does not import
// internal/ingest, which imports this package in its tests.
const gpsRawStream = "GPS_RAW"

// natsShared holds the one NATS container a test process uses (GF-65).
var natsShared struct {
	once      sync.Once
	ctr       *natsmodule.NATSContainer
	url       string
	err       error
	terminate sync.Once
}

// sharedNATSURL starts the container on first use and returns its client URL.
func sharedNATSURL(t testing.TB) string {
	t.Helper()
	natsShared.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		natsShared.ctr, natsShared.err = natsmodule.Run(ctx, NATSImage)
		if natsShared.err != nil {
			return
		}
		natsShared.url, natsShared.err = natsShared.ctr.ConnectionString(ctx)
	})
	if natsShared.err != nil {
		t.Fatalf("start %s (is Docker running?): %v", NATSImage, natsShared.err)
	}
	return natsShared.url
}

// TerminateNATS removes the shared NATS container. Call it from TestMain after
// m.Run so the container does not outlive the test process (GF-65).
func TerminateNATS() {
	natsShared.terminate.Do(func() {
		if natsShared.ctr == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = natsShared.ctr.Terminate(ctx)
	})
}

// NewJetStream connects to the shared JetStream server and deletes GPS_RAW so
// the test starts empty. The caller re-ensures the stream with ingest.EnsureStream.
func NewJetStream(t testing.TB) jetstream.JetStream {
	t.Helper()
	nc, err := nats.Connect(sharedNATSURL(t))
	if err != nil {
		t.Fatalf("connect nats: %v", err)
	}
	t.Cleanup(nc.Close)

	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := js.DeleteStream(ctx, gpsRawStream); err != nil && !errors.Is(err, jetstream.ErrStreamNotFound) {
		t.Fatalf("delete %s: %v", gpsRawStream, err)
	}
	return js
}

func deadline(ctx context.Context) time.Time {
	d, _ := ctx.Deadline()
	return d
}

// FetchSubject reads the stream's messages on subject, in stream order, through
// an ordered consumer that starts at the first message. It returns the first
// want payloads. It fails the test if fewer arrive before timeout.
func FetchSubject(t testing.TB, js jetstream.JetStream, stream, subject string, want int, timeout time.Duration) [][]byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	s, err := js.Stream(ctx, stream)
	if err != nil {
		t.Fatalf("stream %s: %v", stream, err)
	}
	cons, err := s.OrderedConsumer(ctx, jetstream.OrderedConsumerConfig{
		FilterSubjects: []string{subject},
		DeliverPolicy:  jetstream.DeliverAllPolicy,
	})
	if err != nil {
		t.Fatalf("ordered consumer on %s: %v", subject, err)
	}

	var got [][]byte
	for len(got) < want && ctx.Err() == nil {
		batch, err := cons.Fetch(want-len(got), jetstream.FetchMaxWait(time.Until(deadline(ctx))))
		if err != nil {
			t.Fatalf("fetch %s: %v", subject, err)
		}
		for msg := range batch.Messages() {
			got = append(got, msg.Data())
		}
	}
	if len(got) < want {
		t.Fatalf("fetched %d messages on %s, want %d before %s", len(got), subject, want, timeout)
	}
	return got
}

// NATSURL returns the client URL of the shared NATS server, starting it on first
// use. Core-NATS tests use it without JetStream (GF-122).
func NATSURL(t testing.TB) string {
	t.Helper()
	return sharedNATSURL(t)
}
