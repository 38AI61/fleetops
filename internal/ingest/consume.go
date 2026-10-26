package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// ErrMalformedPayload is returned for any GF-58 payload the consumer cannot
// decode. The consumer terminates such a message on first delivery (GF-89).
var ErrMalformedPayload = errors.New("ingest: malformed payload")

// wireIn mirrors wirePayload with pointers, so a missing or null key is
// detected (GF-96). Unknown keys are rejected by the decoder.
type wireIn struct {
	MsgID      *string  `json:"msg_id"`
	FleetID    *int64   `json:"fleet_id"`
	DeviceID   *int64   `json:"device_id"`
	Lat        *float64 `json:"lat"`
	Lng        *float64 `json:"lng"`
	SpeedKmh   *float64 `json:"speed_kmh"`
	HeadingDeg *float64 `json:"heading_deg"`
	AccuracyM  *float64 `json:"accuracy_m"`
	DeviceTS   *string  `json:"device_ts"`
	ServerTS   *string  `json:"server_ts"`
}

// ParseMessage is the inverse of Message.Payload (GF-96). It rejects unknown
// keys, missing keys, trailing data, a bad UUID, non-positive ids, and bad
// timestamps. The subject is not parsed: the fleet id comes from the payload.
func ParseMessage(data []byte) (Message, error) {
	var w wireIn
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return Message{}, fmt.Errorf("%w: %w", ErrMalformedPayload, err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Message{}, fmt.Errorf("%w: trailing data", ErrMalformedPayload)
	}

	for _, present := range []bool{
		w.MsgID != nil, w.FleetID != nil, w.DeviceID != nil,
		w.Lat != nil, w.Lng != nil, w.SpeedKmh != nil,
		w.HeadingDeg != nil, w.AccuracyM != nil,
		w.DeviceTS != nil, w.ServerTS != nil,
	} {
		if !present {
			return Message{}, fmt.Errorf("%w: missing key", ErrMalformedPayload)
		}
	}

	id, err := uuid.Parse(*w.MsgID)
	if err != nil {
		return Message{}, fmt.Errorf("%w: msg_id", ErrMalformedPayload)
	}
	if *w.FleetID <= 0 {
		return Message{}, fmt.Errorf("%w: fleet_id", ErrMalformedPayload)
	}
	if *w.DeviceID <= 0 {
		return Message{}, fmt.Errorf("%w: device_id", ErrMalformedPayload)
	}
	deviceTS, err := time.Parse(time.RFC3339Nano, *w.DeviceTS)
	if err != nil {
		return Message{}, fmt.Errorf("%w: device_ts", ErrMalformedPayload)
	}
	serverTS, err := time.Parse(time.RFC3339Nano, *w.ServerTS)
	if err != nil {
		return Message{}, fmt.Errorf("%w: server_ts", ErrMalformedPayload)
	}

	return Message{
		MsgID:    id,
		FleetID:  *w.FleetID,
		DeviceID: *w.DeviceID,
		Position: Position{
			Lat:      *w.Lat,
			Lng:      *w.Lng,
			Speed:    *w.SpeedKmh,
			Heading:  *w.HeadingDeg,
			Accuracy: *w.AccuracyM,
			DeviceTS: deviceTS.UTC(),
		},
		ServerTS: serverTS.UTC(),
	}, nil
}

// NAK schedule (GF-88): 1 s doubling, capped at 8 s.
const (
	nakBase = time.Second
	nakCap  = 8 * time.Second
)

// nakDelay is min(1 s × 2^(numDelivered−1), 8 s). The first delivery waits 1 s.
func nakDelay(numDelivered int) time.Duration {
	d := nakBase
	for i := 1; i < numDelivered && d < nakCap; i++ {
		d *= 2
	}
	return min(d, nakCap)
}

// shouldTerm reports whether a failed message has used its last attempt (GF-89).
func shouldTerm(numDelivered, maxDeliver int) bool {
	return numDelivered >= maxDeliver
}

// Consumer identity and timing (GF-86, GF-98). The durable name and filter are
// the production values; tests override the timing fields.
type consumerSpec struct {
	durable         string
	filter          string
	ackWait         time.Duration
	maxDeliver      int
	maxAckPending   int
	flushTimeout    time.Duration
	shutdownTimeout time.Duration
}

func defaultConsumerSpec() consumerSpec {
	return consumerSpec{
		durable:         "ingest-positions",
		filter:          "gps.raw.>",
		ackWait:         30 * time.Second,
		maxDeliver:      8,
		maxAckPending:   10000,
		flushTimeout:    5 * time.Second,
		shutdownTimeout: 10 * time.Second,
	}
}

// Fetch wait bounds. The wait is the time to the interval trigger, floored at
// 10 ms and capped at 100 ms, so a cancel is seen within 100 ms (GF-86, GF-95).
const (
	minFetchWait = 10 * time.Millisecond
	maxFetchWait = 100 * time.Millisecond
)

// seams are unexported fault and observation hooks for tests. The zero value
// changes nothing.
type seams struct {
	// afterOffer runs after the n-th message of the run is offered to the buffer.
	afterOffer func(n int)
	// beforeAck runs once per committed batch, before its acks.
	beforeAck func(batch []Message)
	// ackFault returns a non-nil error to fail the next batch ack.
	ackFault func() error
	// flushResult may replace a successful FlushResult, to exercise the mismatch path.
	flushResult func(FlushResult) FlushResult
}

// Consumer drives the GF-86 loop: fetch, parse, offer, flush, ack. One goroutine
// owns fetch, Buffer, Writer.Flush, and every ack (GF-86, GF-87).
type Consumer struct {
	js    jetstream.JetStream
	buf   *Buffer
	w     *Writer
	m     *Metrics
	clock Clock
	pub   *Publisher
	spec  consumerSpec
	seams seams

	// pending holds the raw messages of the messages now in buf, in buf order.
	pending []jetstream.Msg
	offered int
}

// NewConsumer returns a consumer with the GF-86 production spec. Every flush
// hands its stored messages to pub after commit (GF-103).
func NewConsumer(js jetstream.JetStream, buf *Buffer, w *Writer, m *Metrics, clock Clock, pub *Publisher) *Consumer {
	return &Consumer{js: js, buf: buf, w: w, m: m, clock: clock, pub: pub, spec: defaultConsumerSpec()}
}

// EnsureConsumer creates or updates the durable pull consumer with the GF-86
// config. It is idempotent and runs at boot after EnsureStream (GF-86).
func EnsureConsumer(ctx context.Context, js jetstream.JetStream) error {
	_, err := ensureConsumer(ctx, js, defaultConsumerSpec())
	return err
}

func ensureConsumer(ctx context.Context, js jetstream.JetStream, spec consumerSpec) (jetstream.Consumer, error) {
	s, err := js.Stream(ctx, StreamName)
	if err != nil {
		return nil, fmt.Errorf("stream %s: %w", StreamName, err)
	}
	cons, err := s.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       spec.durable,
		FilterSubject: spec.filter,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       spec.ackWait,
		MaxDeliver:    spec.maxDeliver,
		MaxAckPending: spec.maxAckPending,
	})
	if err != nil {
		return nil, fmt.Errorf("consumer %s: %w", spec.durable, err)
	}
	return cons, nil
}

// Run consumes until ctx is cancelled, then drains the buffer and returns (GF-95).
// EnsureConsumer must have run first.
func (c *Consumer) Run(ctx context.Context) error {
	s, err := c.js.Stream(ctx, StreamName)
	if err != nil {
		return fmt.Errorf("stream %s: %w", StreamName, err)
	}
	cons, err := s.Consumer(ctx, c.spec.durable)
	if err != nil {
		return fmt.Errorf("consumer %s: %w", c.spec.durable, err)
	}

	for ctx.Err() == nil {
		c.cycle(ctx, cons)
	}
	c.shutdown()
	return nil
}

// cycle runs one fetch and offers its messages. It flushes on the interval
// trigger after the fetched slice (GF-86).
func (c *Consumer) cycle(ctx context.Context, cons jetstream.Consumer) {
	batch, err := cons.Fetch(c.buf.flushMax, jetstream.FetchMaxWait(c.fetchWait(c.clock())))
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("ingest: fetch: %v", err)
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
		return
	}

	var unprocessed []jetstream.Msg
	for raw := range batch.Messages() {
		if ctx.Err() != nil {
			unprocessed = append(unprocessed, raw)
			continue
		}
		c.handle(ctx, raw)
	}
	if err := batch.Error(); err != nil && !errors.Is(err, nats.ErrTimeout) && !errors.Is(err, jetstream.ErrNoMessages) {
		log.Printf("ingest: fetch batch: %v", err)
	}
	// Fetched but not offered: NAK with no delay (GF-95). This runs after the
	// batch closes. A NAK during the batch would redeliver into the same open
	// pull request and loop inside it.
	for _, raw := range unprocessed {
		if err := raw.Nak(); err != nil {
			log.Printf("ingest: nak on shutdown: %v", err)
		}
	}

	if ctx.Err() == nil {
		if due := c.buf.Tick(c.clock()); due != nil {
			c.flushWithTimeout(ctx, due, c.takePending(), c.spec.flushTimeout)
		}
	}
}

// fetchWait is the time to the interval trigger, floored and capped (GF-86).
func (c *Consumer) fetchWait(now time.Time) time.Duration {
	wait := maxFetchWait
	if deadline, armed := c.buf.Deadline(); armed {
		wait = deadline.Sub(now)
	}
	return min(max(wait, minFetchWait), maxFetchWait)
}

// handle parses one fetched message and offers it (GF-86, GF-87, GF-89, GF-96).
func (c *Consumer) handle(ctx context.Context, raw jetstream.Msg) {
	m, err := ParseMessage(raw.Data())
	if err != nil {
		c.terminate(raw, "malformed")
		return
	}

	buffered, batch := c.buf.Offer(m, c.clock())
	c.offered++
	if c.seams.afterOffer != nil {
		c.seams.afterOffer(c.offered)
	}
	if !buffered {
		// Accuracy drop: nothing is written, so there is nothing to commit (GF-87).
		c.m.MessagesTotal.WithLabelValues("dropped_accuracy").Inc()
		c.ack(raw)
		return
	}

	c.pending = append(c.pending, raw)
	if batch != nil {
		c.flushWithTimeout(ctx, batch, c.takePending(), c.spec.flushTimeout)
	}
}

// takePending hands the raw messages of the buffered batch to the caller.
func (c *Consumer) takePending() []jetstream.Msg {
	p := c.pending
	c.pending = nil
	return p
}

// flushWithTimeout runs one flush under its own deadline (GF-98).
func (c *Consumer) flushWithTimeout(parent context.Context, batch []Message, raws []jetstream.Msg, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	c.flush(ctx, batch, raws)
}

// flush writes one batch and acks or NAKs every message in it (GF-87, GF-88).
func (c *Consumer) flush(ctx context.Context, batch []Message, raws []jetstream.Msg) {
	start := time.Now()
	res, err := c.w.Flush(ctx, batch, c.clock())
	c.m.FlushDuration.Observe(time.Since(start).Seconds())
	if err == nil && c.seams.flushResult != nil {
		res = c.seams.flushResult(res)
	}

	if err == nil && len(res.Stored)+res.Stale+res.Unpaired != len(batch) {
		log.Printf("ingest: flush accounts for %d of %d messages", len(res.Stored)+res.Stale+res.Unpaired, len(batch))
		c.failBatch(raws, "result_mismatch", len(batch))
		return
	}
	if err != nil {
		reason := flushReason(ctx, err)
		log.Printf("ingest: flush of %d messages failed (%s): %v", len(batch), reason, err)
		c.failBatch(raws, reason, len(batch))
		return
	}

	c.m.FlushSize.Observe(float64(len(batch)))
	c.m.MessagesTotal.WithLabelValues("stored").Add(float64(len(res.Stored)))
	c.m.MessagesTotal.WithLabelValues("stale").Add(float64(res.Stale))
	c.m.MessagesTotal.WithLabelValues("unpaired").Add(float64(res.Unpaired))

	// The batch is committed, so a published point is durable (GF-103).
	c.pub.Publish(ctx, res.Stored)

	if c.seams.beforeAck != nil {
		c.seams.beforeAck(batch)
	}
	for _, raw := range raws {
		c.ackAfterCommit(raw)
	}
}

// flushReason classifies a flush error for the failure counter (GF-88, GF-90, GF-93).
func flushReason(ctx context.Context, err error) string {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23514" {
		return "missing_partition"
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	return "db"
}

// failBatch counts the failure and NAKs or terminates every message of the batch.
// The batch is dropped from memory; there is no in-process retry (GF-88).
func (c *Consumer) failBatch(raws []jetstream.Msg, reason string, size int) {
	c.m.FlushFailures.WithLabelValues(reason).Inc()
	if reason == "missing_partition" {
		c.m.MissingPartition.Add(float64(size))
	}
	for _, raw := range raws {
		c.failMessage(raw)
	}
}

// failMessage NAKs with the GF-88 delay, or terminates on the last attempt (GF-89).
func (c *Consumer) failMessage(raw jetstream.Msg) {
	md, err := raw.Metadata()
	if err != nil {
		log.Printf("ingest: metadata: %v", err)
		c.nak(raw, nakDelay(1))
		return
	}
	// Clamped to MaxInt32 before the conversion; the server never reports more.
	n := int(min(md.NumDelivered, math.MaxInt32)) //nolint:gosec // clamped above
	if n > c.spec.maxDeliver {
		n = c.spec.maxDeliver
	}
	if shouldTerm(n, c.spec.maxDeliver) {
		c.terminate(raw, "max_deliver")
		return
	}
	c.nak(raw, nakDelay(n))
}

func (c *Consumer) nak(raw jetstream.Msg, delay time.Duration) {
	if err := raw.NakWithDelay(delay); err != nil {
		log.Printf("ingest: nak: %v", err)
	}
}

// terminate stops redelivery and counts the reason (GF-89).
func (c *Consumer) terminate(raw jetstream.Msg, reason string) {
	c.m.Terminated.WithLabelValues(reason).Inc()
	if err := raw.Term(); err != nil {
		log.Printf("ingest: term (%s): %v", reason, err)
	}
}

// ackAfterCommit acks one message whose batch has committed (GF-87). A failed
// ack is counted; the message redelivers after AckWait and D-15 absorbs it.
func (c *Consumer) ackAfterCommit(raw jetstream.Msg) {
	if c.seams.ackFault != nil {
		if err := c.seams.ackFault(); err != nil {
			c.m.AckErrors.Inc()
			log.Printf("ingest: ack: %v", err)
			return
		}
	}
	c.ack(raw)
}

// ack publishes one ack and counts a failure.
func (c *Consumer) ack(raw jetstream.Msg) {
	if err := raw.Ack(); err != nil {
		c.m.AckErrors.Inc()
		log.Printf("ingest: ack: %v", err)
	}
}

// shutdown drains the buffer and flushes it under a fresh ShutdownTimeout (GF-95).
// Its messages are acked on nil and NAKed on error.
func (c *Consumer) shutdown() {
	raws := c.takePending()
	batch := c.buf.Drain()
	if len(batch) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.spec.shutdownTimeout)
	defer cancel()
	c.flush(ctx, batch, raws)
}
