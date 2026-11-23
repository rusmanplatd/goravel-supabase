package realtime

import "sync"

// PresenceState maps presence keys to their list of active presence metas.
type PresenceState map[string][]map[string]any

// PresenceDiffEvent holds join/leave diffs delivered to presence callbacks.
type PresenceDiffEvent struct {
	Event            string           // "sync", "join", or "leave"
	Key              string           // populated for "join" and "leave"
	CurrentPresences []map[string]any // current presences for Key
	NewPresences     []map[string]any // joined presences for Key
	LeftPresences    []map[string]any // left presences for Key
}

// RealtimePresence tracks Phoenix Presence state and diffs for a channel.
type RealtimePresence struct {
	mu    sync.RWMutex
	state PresenceState
}

func newRealtimePresence() *RealtimePresence {
	return &RealtimePresence{state: make(PresenceState)}
}

// State returns a snapshot copy of the current presence state.
func (p *RealtimePresence) State() PresenceState {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(PresenceState, len(p.state))
	for k, metas := range p.state {
		cp := make([]map[string]any, len(metas))
		copy(cp, metas)
		out[k] = cp
	}
	return out
}

func extractMetas(entry any) []map[string]any {
	m, ok := entry.(map[string]any)
	if !ok {
		return nil
	}
	rawMetas, ok := m["metas"].([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(rawMetas))
	for _, rm := range rawMetas {
		if mp, ok := rm.(map[string]any); ok {
			out = append(out, mp)
		}
	}
	return out
}

// ApplyState replaces the presence state from a `presence_state` server payload
// and returns join/sync events to dispatch.
func (p *RealtimePresence) ApplyState(payload map[string]any) []PresenceDiffEvent {
	p.mu.Lock()
	next := make(PresenceState)
	var events []PresenceDiffEvent
	for k, v := range payload {
		metas := extractMetas(v)
		next[k] = metas
		events = append(events, PresenceDiffEvent{
			Event:            "join",
			Key:              k,
			CurrentPresences: nil,
			NewPresences:     metas,
		})
	}
	p.state = next
	p.mu.Unlock()
	events = append(events, PresenceDiffEvent{Event: "sync"})
	return events
}

// ApplyDiff merges a `presence_diff` (`joins` and `leaves`) into the presence state
// and returns join/leave/sync events to dispatch.
func (p *RealtimePresence) ApplyDiff(payload map[string]any) []PresenceDiffEvent {
	p.mu.Lock()
	var events []PresenceDiffEvent

	if joins, ok := payload["joins"].(map[string]any); ok {
		for k, v := range joins {
			newMetas := extractMetas(v)
			cur := p.state[k]
			merged := append(append([]map[string]any(nil), cur...), newMetas...)
			p.state[k] = merged
			events = append(events, PresenceDiffEvent{
				Event:            "join",
				Key:              k,
				CurrentPresences: cur,
				NewPresences:     newMetas,
			})
		}
	}

	if leaves, ok := payload["leaves"].(map[string]any); ok {
		for k, v := range leaves {
			leftMetas := extractMetas(v)
			cur := p.state[k]
			leftRefs := make(map[any]struct{}, len(leftMetas))
			for _, lm := range leftMetas {
				if ref, ok := lm["phx_ref"]; ok {
					leftRefs[ref] = struct{}{}
				}
			}
			var remaining []map[string]any
			for _, cm := range cur {
				if ref, ok := cm["phx_ref"]; ok {
					if _, removed := leftRefs[ref]; removed {
						continue
					}
				}
				remaining = append(remaining, cm)
			}
			if len(remaining) == 0 {
				delete(p.state, k)
			} else {
				p.state[k] = remaining
			}
			events = append(events, PresenceDiffEvent{
				Event:            "leave",
				Key:              k,
				CurrentPresences: remaining,
				LeftPresences:    leftMetas,
			})
		}
	}

	p.mu.Unlock()
	events = append(events, PresenceDiffEvent{Event: "sync"})
	return events
}
