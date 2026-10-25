package hub

// room is one fleet's set of connections. slots counts every connection that
// holds a place, authenticated or not, so the cap is a strict bound (GF-112).
// members holds only the authenticated connections that receive broadcasts.
type room struct {
	slots   int
	members map[*conn]struct{}
}

// reserve takes a slot in the fleet's room before the upgrade. It refuses when
// the room is at RoomCap (GF-112).
func (h *Hub) reserve(fleet int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	r := h.rooms[fleet]
	if r == nil {
		r = &room{members: map[*conn]struct{}{}}
		h.rooms[fleet] = r
	}
	if r.slots >= h.opts.RoomCap {
		return false
	}
	r.slots++
	return true
}

// release returns a slot on any teardown path and drops an empty room.
func (h *Hub) release(fleet int64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	r := h.rooms[fleet]
	if r == nil {
		return
	}
	r.slots--
	if r.slots == 0 {
		delete(h.rooms, fleet)
	}
}

// join adds an authenticated connection to its room (GF-110 step 8).
func (h *Hub) join(c *conn) {
	h.mu.Lock()
	defer h.mu.Unlock()

	r := h.rooms[c.fleet]
	r.members[c] = struct{}{}
	h.counters.ConnsOpen.Add(1)
}

// leave removes a connection from its room.
func (h *Hub) leave(c *conn) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if r := h.rooms[c.fleet]; r != nil {
		if _, ok := r.members[c]; ok {
			delete(r.members, c)
			h.counters.ConnsOpen.Add(-1)
		}
	}
}

// broadcast enqueues one frame to every authenticated member of fleet's room.
// accept, when set, filters the recipients. It never blocks on a client (GF-113).
func (h *Hub) broadcast(fleet int64, frame []byte, accept func(*conn) bool) {
	h.mu.Lock()
	r := h.rooms[fleet]
	var members []*conn
	if r != nil {
		members = make([]*conn, 0, len(r.members))
		for c := range r.members {
			members = append(members, c)
		}
	}
	h.mu.Unlock()

	for _, c := range members {
		if accept != nil && !accept(c) {
			continue
		}
		c.enqueue(frame)
	}
}
