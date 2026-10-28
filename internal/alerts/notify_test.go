package alerts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"testing"
	"time"
)

// recordNotifier keeps every Notice and returns err when set.
type recordNotifier struct {
	notices []Notice
	err     error
}

func (n *recordNotifier) Notify(_ context.Context, notice Notice) error {
	n.notices = append(n.notices, notice)
	return n.err
}

// TestNotifyCriticalOnly: a critical alert calls the notifier with the fleet's
// active manager ids, and a warning does not call it (GF-300). Admins and
// deactivated managers are not recipients.
func TestNotifyCriticalOnly(t *testing.T) {
	f := newFx(t)
	fleet := f.fleet("notify")
	m1 := newUser(t, f.db, fleet, "manager")
	m2 := newUser(t, f.db, fleet, "manager")
	newUser(t, f.db, fleet, "admin")
	gone := newUser(t, f.db, fleet, "manager")
	if _, err := f.db.Exec(`UPDATE users SET disabled_at = now() WHERE id = $1`, gone); err != nil {
		t.Fatalf("disable manager: %v", err)
	}
	// Reported now, so the pass with the real clock sees no silence (the exit is the only alert).
	v := f.vehicle(fleet, "active", true)
	f.report(fleet, v, time.Now())
	office := f.fence(fleet, `{"days":[1,2,3,4,5],"from":"08:00","to":"18:00","tz":"UTC"}`)
	f.exit(fleet, office, v, time.Date(2026, 10, 28, 20, 0, 0, 0, time.UTC))

	rec := &recordNotifier{}
	ev := &Evaluator{DB: f.db, Clock: func() time.Time { return time.Now() }, Metrics: f.m, Notifier: rec}
	raised, err := ev.Pass(context.Background())
	if err != nil {
		t.Fatalf("Pass: %v", err)
	}
	// One critical exit (notified) and one warning silence (not notified).
	if len(raised) != 1 || raised[0].Kind != KindGeofenceExit {
		t.Fatalf("raised = %+v, want one exit", raised)
	}
	if len(rec.notices) != 1 {
		t.Fatalf("notifier calls = %d, want 1 (critical only)", len(rec.notices))
	}
	n := rec.notices[0]
	if n.EventType != "alert.raised" || n.FleetID != fleet || n.AlertID != raised[0].ID || n.Severity != SeverityCritical {
		t.Fatalf("notice = %+v", n)
	}
	if fmt.Sprint(n.RecipientUserIDs) != fmt.Sprint([]int64{m1, m2}) {
		t.Fatalf("recipients = %v, want managers [%d %d]", n.RecipientUserIDs, m1, m2)
	}
}

// TestNotifyWarningNotCalled: a silence warning alone never calls the notifier.
func TestNotifyWarningNotCalled(t *testing.T) {
	f := newFx(t)
	fleet := f.fleet("warn")
	newUser(t, f.db, fleet, "manager")
	v := f.vehicle(fleet, "active", true)
	f.report(fleet, v, base)

	rec := &recordNotifier{}
	ev := &Evaluator{DB: f.db, Clock: func() time.Time { return base.Add(time.Hour) }, Metrics: f.m, Notifier: rec}
	if raised, err := ev.Pass(context.Background()); err != nil || len(raised) != 1 {
		t.Fatalf("Pass = %d, %v; want one warning", len(raised), err)
	}
	if len(rec.notices) != 0 {
		t.Fatalf("notifier called for a warning: %+v", rec.notices)
	}
}

// TestLogNotifierNoAddress: the log line has the event type and user id, and no
// manager's address (D-11, GF-300).
func TestLogNotifierNoAddress(t *testing.T) {
	db := newFx(t).db
	fleet := newFleet(t, db, "pii")
	uid := newUser(t, db, fleet, "manager")
	var email string
	if err := db.QueryRow(`SELECT email::text FROM users WHERE id = $1`, uid).Scan(&email); err != nil {
		t.Fatalf("read email: %v", err)
	}

	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	ln := LogNotifier{Logf: logger.Printf}
	err := ln.Notify(context.Background(), Notice{
		EventType: "alert.raised", FleetID: fleet, AlertID: 9, Severity: SeverityCritical,
		Kind: KindGeofenceExit, RecipientUserIDs: []int64{uid},
	})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "event=alert.raised") || !strings.Contains(out, fmt.Sprintf("user_id=%d", uid)) {
		t.Fatalf("log = %q, want event type and user id", out)
	}
	if strings.Contains(out, email) || strings.Contains(out, "@") {
		t.Fatalf("log leaks an address: %q", out)
	}
}

// TestNotifyErrorKeepsAlert: a notifier error is logged and counted, and the alert row stays.
func TestNotifyErrorKeepsAlert(t *testing.T) {
	f := newFx(t)
	fleet := f.fleet("notify-fail")
	newUser(t, f.db, fleet, "manager")
	v := f.vehicle(fleet, "active", true)
	f.report(fleet, v, time.Now())
	office := f.fence(fleet, `{"days":[1,2,3,4,5],"from":"08:00","to":"18:00","tz":"UTC"}`)
	f.exit(fleet, office, v, time.Date(2026, 10, 28, 20, 0, 0, 0, time.UTC))

	rec := &recordNotifier{err: errors.New("smtp down")}
	ev := &Evaluator{DB: f.db, Clock: func() time.Time { return time.Now() }, Metrics: f.m, Notifier: rec}
	raised, err := ev.Pass(context.Background())
	if err != nil || len(raised) != 1 {
		t.Fatalf("Pass = %d, %v; want one alert and no error", len(raised), err)
	}
	if f.count(fleet, KindGeofenceExit) != 1 {
		t.Fatal("notifier error removed the alert row")
	}
	if n := counterValue(t, f.m.Failures.WithLabelValues(stageNotify)); n != 1 {
		t.Fatalf("notify failures = %v, want 1", n)
	}
}
