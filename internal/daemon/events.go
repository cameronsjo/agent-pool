package daemon

import (
	"sync"
	"time"
)

// EventType identifies the kind of daemon event.
type EventType string

const (
	EventTaskRouted      EventType = "task.routed"
	EventExpertSpawning  EventType = "expert.spawning"
	EventExpertCompleted EventType = "expert.completed"
	EventExpertFailed    EventType = "expert.failed"
	EventTaskCancelled      EventType = "task.cancelled"
	EventTaskUnblocked      EventType = "task.unblocked"
	EventCurationTriggered  EventType = "curation.triggered"
	EventConfigReloaded     EventType = "config.reloaded"
)

// Event is a structured daemon event emitted at state transitions.
type Event struct {
	Type      EventType `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Data      any       `json:"data"`
}

// Per-event data types.

type TaskRoutedData struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
}

type ExpertSpawningData struct {
	Expert string `json:"expert"`
	TaskID string `json:"task_id"`
	Model  string `json:"model"`
}

type ExpertCompletedData struct {
	Expert   string `json:"expert"`
	TaskID   string `json:"task_id"`
	Duration string `json:"duration"`
	ExitCode int    `json:"exit_code"`
	Summary  string `json:"summary"`
}

type ExpertFailedData struct {
	Expert   string `json:"expert"`
	TaskID   string `json:"task_id"`
	ExitCode int    `json:"exit_code"`
}

type TaskCancelledData struct {
	TaskID     string `json:"task_id"`
	CancelNote string `json:"cancel_note,omitempty"`
}

type TaskUnblockedData struct {
	TaskID string `json:"task_id"`
	Expert string `json:"expert"`
}

type CurationTriggeredData struct {
	Reason string `json:"reason"`
}

type ConfigReloadedData struct {
	ExpertsAdded   []string `json:"experts_added,omitempty"`
	ExpertsRemoved []string `json:"experts_removed,omitempty"`
}

// EventBufSize is the subscriber channel buffer capacity. Subscribers that
// can't keep up will miss events once the buffer fills (non-blocking emit).
const EventBufSize = 64

// eventBus fans out events to registered subscribers.
type eventBus struct {
	mu     sync.RWMutex
	nextID int
	subs   map[int]chan Event
}

func newEventBus() *eventBus {
	return &eventBus{
		subs: make(map[int]chan Event),
	}
}

// subscribe returns a subscriber ID and a buffered event channel.
func (b *eventBus) subscribe() (int, <-chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	id := b.nextID
	b.nextID++

	ch := make(chan Event, EventBufSize)
	b.subs[id] = ch
	return id, ch
}

// unsubscribe removes a subscriber and closes its channel.
func (b *eventBus) unsubscribe(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if ch, ok := b.subs[id]; ok {
		close(ch)
		delete(b.subs, id)
	}
}

// emit sends an event to all subscribers. Non-blocking: slow subscribers
// that can't keep up will miss events (their channels are buffered at 64).
func (b *eventBus) emit(e Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, ch := range b.subs {
		select {
		case ch <- e:
		default:
			// Subscriber too slow, drop event
		}
	}
}

// EventBus wraps the internal eventBus for test access.
type EventBus struct{ *eventBus }

func NewEventBusForTest() *EventBus       { return &EventBus{newEventBus()} }
func (b *EventBus) Subscribe() (int, <-chan Event) { return b.eventBus.subscribe() }
func (b *EventBus) Unsubscribe(id int)    { b.eventBus.unsubscribe(id) }
func (b *EventBus) Emit(e Event)          { b.eventBus.emit(e) }
