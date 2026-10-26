// Package jobs holds the P10 job core: the D-23 state machine, the job store
// (D-24 semantics), the dispatcher HTTP routes, the job-event publisher, and
// the mapview.JobLookup implementation (GF-133).
package jobs

// State is one of the eight job states of the D-23 vocabulary.
type State string

// The eight job states. Only the edges in Edges move a job between them.
const (
	StateCreated   State = "created"
	StateAssigned  State = "assigned"
	StateAccepted  State = "accepted"
	StateEnRoute   State = "en_route"
	StatePickedUp  State = "picked_up"
	StateDelivered State = "delivered"
	StateFailed    State = "failed"
	StateCancelled State = "cancelled"
)

// States is the full vocabulary, in lifecycle order. The migration CHECKs mirror it.
var States = []State{
	StateCreated, StateAssigned, StateAccepted, StateEnRoute,
	StatePickedUp, StateDelivered, StateFailed, StateCancelled,
}

// Edge is one allowed transition.
type Edge struct {
	From, To State
}

// Edges is the D-23 table, the only Go source of the edge set. job_transitions
// mirrors it for the trigger; TestJobTransitionsMirror keeps the two equal.
var Edges = []Edge{
	{StateCreated, StateAssigned},   // assign
	{StateAssigned, StateAssigned},  // reassign
	{StateAssigned, StateAccepted},  // driver accept
	{StateAccepted, StateEnRoute},   // driver start
	{StateEnRoute, StatePickedUp},   // driver pickup
	{StatePickedUp, StateDelivered}, // driver deliver
	{StateAccepted, StateFailed},    // driver fail
	{StateEnRoute, StateFailed},
	{StatePickedUp, StateFailed},
	{StateCreated, StateCancelled}, // dispatcher or admin cancel
	{StateAssigned, StateCancelled},
	{StateAccepted, StateCancelled},
}

// CanTransition reports whether D-23 allows from → to.
func CanTransition(from, to State) bool {
	for _, e := range Edges {
		if e.From == from && e.To == to {
			return true
		}
	}
	return false
}

// Valid reports whether s is one of the eight vocabulary states.
func (s State) Valid() bool {
	for _, v := range States {
		if s == v {
			return true
		}
	}
	return false
}
