package core

import "sync"

type Event struct {
	ID      uint64 `json:"id"`
	Name    string `json:"name"`
	Payload any    `json:"payload"`
}
type eventBatch struct {
	Events  []Event `json:"events"`
	Cursor  uint64  `json:"cursor"`
	Dropped bool    `json:"dropped"`
}
type eventBus struct {
	mu       sync.Mutex
	sequence uint64
	items    []Event
}

func (b *eventBus) publish(name string, payload any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sequence++
	if len(b.items) == 2048 {
		copy(b.items, b.items[1:])
		b.items = b.items[:2047]
	}
	b.items = append(b.items, Event{ID: b.sequence, Name: name, Payload: payload})
}
func (b *eventBus) poll(after uint64) eventBatch {
	b.mu.Lock()
	defer b.mu.Unlock()
	result := eventBatch{Events: []Event{}, Cursor: after}
	if len(b.items) > 0 {
		result.Dropped = after+1 < b.items[0].ID
	}
	for _, item := range b.items {
		if item.ID > after {
			result.Events = append(result.Events, item)
			result.Cursor = item.ID
			if len(result.Events) >= 500 {
				break
			}
		}
	}
	// A restarted core begins its sequence again; let an existing WebView resynchronize.
	if after > b.sequence {
		result.Cursor = 0
		result.Dropped = true
	}
	return result
}
