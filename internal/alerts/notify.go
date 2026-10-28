package alerts

import (
	"context"
	"database/sql"
	"fmt"
	"log"
)

// eventNotify is the event type the Notifier sees for an alert (GF-300).
const eventNotify = "alert.raised"

// Notice is one notification for one alert (GF-300). Recipients are the fleet's
// active managers. Admins are not recipients.
type Notice struct {
	EventType        string
	FleetID          int64
	AlertID          int64
	Severity         string
	Kind             string
	RecipientUserIDs []int64
}

// Notifier is the D-11 seam. P19 ships the log stub only; real delivery is out of scope.
type Notifier interface {
	Notify(ctx context.Context, n Notice) error
}

// LogNotifier logs one line per recipient: the event type, kind, severity, fleet
// id and user id (GF-300). It never logs an address or a payload body (D-11).
// Logf defaults to log.Printf.
type LogNotifier struct {
	Logf func(format string, args ...any)
}

// Notify writes the log lines. It never fails.
func (l LogNotifier) Notify(_ context.Context, n Notice) error {
	logf := l.Logf
	if logf == nil {
		logf = log.Printf
	}
	for _, uid := range n.RecipientUserIDs {
		logf("event=%s kind=%s severity=%s fleet_id=%d user_id=%d", n.EventType, n.Kind, n.Severity, n.FleetID, uid)
	}
	return nil
}

// managerIDs returns the fleet's active manager user ids in id order (GF-300).
// users.disabled_at marks a deactivated user.
func managerIDs(ctx context.Context, db *sql.DB, fleetID int64) ([]int64, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id FROM users
		WHERE fleet_id = $1 AND role = 'manager' AND disabled_at IS NULL
		ORDER BY id`, fleetID)
	if err != nil {
		return nil, fmt.Errorf("manager recipients: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// notifyCritical runs after commit and only for critical alerts (GF-300). A
// failure is logged and counted, and the alert row stays.
func (e *Evaluator) notifyCritical(ctx context.Context, raised []Raised) {
	for _, a := range raised {
		if a.Severity != SeverityCritical {
			continue
		}
		recipients, err := managerIDs(ctx, e.DB, a.FleetID)
		if err == nil {
			err = e.Notifier.Notify(ctx, Notice{
				EventType:        eventNotify,
				FleetID:          a.FleetID,
				AlertID:          a.ID,
				Severity:         a.Severity,
				Kind:             a.Kind,
				RecipientUserIDs: recipients,
			})
		}
		if err != nil {
			e.Metrics.fail(stageNotify)
			log.Printf("alerts: notify alert %d: %v", a.ID, err)
		}
	}
}
