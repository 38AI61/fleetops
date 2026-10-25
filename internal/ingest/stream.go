package ingest

import (
	"context"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// StreamName is the JetStream stream that holds accepted positions (D-16).
const StreamName = "GPS_RAW"

// MaxInFlight bounds async publishes awaiting an ack (GF-60). It is a code
// constant, not an env: FLEETOPS_* tunables arrive in P5.
const MaxInFlight = 1024

// streamSubjects is every fleet subject (GF-58, GF-59).
var streamSubjects = []string{"gps.raw.>"}

// streamMaxAge matches the 60 s stale rule (D-18, GF-59).
const streamMaxAge = 60 * time.Second

// streamMaxBytes bounds the stream at 256 MiB (GF-59).
const streamMaxBytes = 256 << 20

// streamConfig is the GF-59 parameter set. Every other field keeps its default.
func streamConfig() jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:      StreamName,
		Subjects:  streamSubjects,
		Storage:   jetstream.FileStorage,
		MaxAge:    streamMaxAge,
		MaxBytes:  streamMaxBytes,
		Discard:   jetstream.DiscardOld,
		Retention: jetstream.LimitsPolicy,
		Replicas:  1,
	}
}

// EnsureStream creates or updates GPS_RAW to the GF-59 config. It is idempotent
// and runs once at API boot (group 9) and in the test helper.
func EnsureStream(ctx context.Context, js jetstream.JetStream) error {
	_, err := js.CreateOrUpdateStream(ctx, streamConfig())
	return err
}

// NewJetStream returns a JetStream context whose async publish stall limit is
// MaxInFlight (GF-60). The gate's slot count is the real bound; this option keeps
// the library from ever reaching its blocking stall path.
func NewJetStream(nc *nats.Conn) (jetstream.JetStream, error) {
	return jetstream.New(nc, jetstream.WithPublishAsyncMaxPending(MaxInFlight))
}
