package simulator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"fleetops/internal/devicesfile"
)

// ErrUsage marks flag and config problems the operator can fix. The CLI maps it to exit code 2.
var ErrUsage = errors.New("usage")

// Flag bounds and defaults (GF-141).
const (
	DefaultFleet = 50
	DefaultRate  = 5 * time.Second
	MinRate      = 500 * time.Millisecond
	MaxRate      = 60 * time.Second
	DefaultAPI   = "http://127.0.0.1:10010"
)

// DefaultAccuracyM is the accuracy every position reports, within the default
// FLEETOPS_ACCURACY_MAX_M (GF-143).
const DefaultAccuracyM = 5.0

// timeLayout is the device timestamp format: RFC3339 UTC with milliseconds (GF-144).
const timeLayout = "2006-01-02T15:04:05.000Z07:00"

// CheckRate enforces the --rate bounds (GF-141).
func CheckRate(d time.Duration) error {
	if d < MinRate || d > MaxRate {
		return fmt.Errorf("%w: --rate must be from %s to %s", ErrUsage, MinRate, MaxRate)
	}
	return nil
}

// CheckFleet enforces 1 <= n <= devices (GF-141). The count is a vehicle count, not a fleet id.
func CheckFleet(n, devices int) error {
	if n < 1 || n > devices {
		return fmt.Errorf("%w: --fleet must be from 1 to %d (devices in the file)", ErrUsage, devices)
	}
	return nil
}

// NormalizeAPI requires an http or https URL with a host and trims any trailing slash (GF-141).
func NormalizeAPI(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("%w: --api must be an http or https URL with a host", ErrUsage)
	}
	return strings.TrimRight(raw, "/"), nil
}

// Vehicle is one simulated device: the key, the route, and its index for logs.
type Vehicle struct {
	Index    int
	DeviceID int64
	Key      string
	Route    Route
}

// Vehicles builds the first n vehicles of the devices file (GF-141).
func Vehicles(f devicesfile.File, n int) ([]Vehicle, error) {
	if err := CheckFleet(n, len(f.Devices)); err != nil {
		return nil, err
	}
	out := make([]Vehicle, 0, n)
	for i := 0; i < n; i++ {
		d := f.Devices[i]
		r, err := NewRoute(d.DeviceID, d.Waypoints)
		if err != nil {
			return nil, fmt.Errorf("device %d: %w", d.DeviceID, err)
		}
		out = append(out, Vehicle{Index: i, DeviceID: d.DeviceID, Key: d.Key, Route: r})
	}
	return out, nil
}

// Tally counts outcomes (GF-147). Sent equals Accepted plus Rejected plus Failed.
type Tally struct {
	Sent     int64
	Accepted int64
	Rejected int64
	Failed   int64
	Skipped  int64
}

// Summary is the result of a run: totals and one tally per vehicle.
type Summary struct {
	Total      Tally
	PerVehicle []Tally
}

// Pacer ties the scheduler to a sender. It owns no clock and no goroutines (GF-148).
type Pacer struct {
	start    time.Time
	sched    *Scheduler
	vehicles []Vehicle
	send     Sender
	log      io.Writer
	counts   []Tally
}

// NewPacer paces the vehicles from start at one position per rate each.
func NewPacer(start time.Time, rate time.Duration, vehicles []Vehicle, send Sender, log io.Writer) *Pacer {
	if log == nil {
		log = io.Discard
	}
	return &Pacer{
		start:    start,
		sched:    NewScheduler(start, rate, len(vehicles)),
		vehicles: vehicles,
		send:     send,
		log:      log,
		counts:   make([]Tally, len(vehicles)),
	}
}

// Step handles one Due call: it sends every slot due at now and returns them.
func (p *Pacer) Step(ctx context.Context, now time.Time) []Slot {
	slots := p.sched.Due(now)
	for _, sl := range slots {
		p.emit(ctx, sl)
	}
	return slots
}

// emit sends one slot. The position is computed at the slot's due instant, so
// device_ts advances by exactly one rate per slot (GF-144).
func (p *Pacer) emit(ctx context.Context, sl Slot) {
	v := p.vehicles[sl.Vehicle]
	fix := v.Route.PositionAt(sl.DueAt.Sub(p.start))
	body := PositionBody{
		Lat:      fix.Lat,
		Lng:      fix.Lng,
		Speed:    fix.SpeedKmh,
		Heading:  fix.HeadingDeg,
		Accuracy: DefaultAccuracyM,
		TS:       sl.DueAt.UTC().Format(timeLayout),
	}
	// In-flight requests finish within RequestTimeout even after shutdown starts (GF-147).
	outcome, err := p.send.Send(context.WithoutCancel(ctx), v.Key, body)
	c := &p.counts[sl.Vehicle]
	c.Sent++
	switch outcome {
	case Accepted:
		c.Accepted++
	case Rejected:
		c.Rejected++
		p.logFailure(v.Index, err)
	default:
		c.Failed++
		p.logFailure(v.Index, err)
	}
}

// logFailure writes one line with the vehicle index and the error, which carries
// the status or error class and the response code. It never carries the key (GF-146).
func (p *Pacer) logFailure(index int, err error) {
	msg := "unknown error"
	if err != nil {
		msg = err.Error()
	}
	_, _ = fmt.Fprintf(p.log, "simulator: vehicle %d: %s\n", index, msg)
}

// Summary returns the counts so far, with skipped slots from the scheduler.
func (p *Pacer) Summary() Summary {
	s := Summary{PerVehicle: make([]Tally, len(p.counts))}
	for i, c := range p.counts {
		c.Skipped = p.sched.Skipped(i)
		s.PerVehicle[i] = c
		s.Total.Sent += c.Sent
		s.Total.Accepted += c.Accepted
		s.Total.Rejected += c.Rejected
		s.Total.Failed += c.Failed
		s.Total.Skipped += c.Skipped
	}
	return s
}

// Config is one run: the vehicles, the rate, and the sender.
type Config struct {
	Vehicles []Vehicle
	Rate     time.Duration
	Sender   Sender
	Log      io.Writer
}

// Run paces the vehicles until ctx is done, then returns the summary (GF-144, GF-147).
// Failures are counted and never fatal. Cancellation stops new slots, and
// in-flight requests finish within RequestTimeout.
func Run(ctx context.Context, cfg Config) (Summary, error) {
	if len(cfg.Vehicles) == 0 {
		return Summary{}, errors.New("simulator: no vehicles")
	}
	if cfg.Rate <= 0 {
		return Summary{}, errors.New("simulator: rate must be positive")
	}
	if cfg.Sender == nil {
		return Summary{}, errors.New("simulator: no sender")
	}

	p := NewPacer(time.Now(), cfg.Rate, cfg.Vehicles, cfg.Sender, cfg.Log)
	for {
		timer := time.NewTimer(time.Until(p.sched.NextDue()))
		select {
		case <-ctx.Done():
			timer.Stop()
			return p.Summary(), nil
		case <-timer.C:
		}
		p.Step(ctx, time.Now())
	}
}
