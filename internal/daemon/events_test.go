// Test plan for events.go:
//
// EventBus:
//   - TestEventBus_SubscribeReceives: subscriber gets emitted events
//   - TestEventBus_Multiple: multiple subscribers each get events
//   - TestEventBus_UnsubscribeCleans: unsubscribed channel is closed
//   - TestEventBus_SlowDrops: slow subscriber misses events (buffer overflow)
package daemon_test

import (
	"testing"
	"time"

	"github.com/cameronsjo/agent-pool/internal/daemon"
)

func TestEventBus_SubscribeReceives(t *testing.T) {
	bus := daemon.NewEventBusForTest()

	_, ch := bus.Subscribe()

	bus.Emit(daemon.Event{
		Type:      daemon.EventTaskRouted,
		Timestamp: time.Now(),
		Data:      daemon.TaskRoutedData{ID: "t1", From: "a", To: "b", Type: "task"},
	})

	select {
	case e := <-ch:
		if e.Type != daemon.EventTaskRouted {
			t.Errorf("type = %v, want task.routed", e.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestEventBus_Multiple(t *testing.T) {
	bus := daemon.NewEventBusForTest()

	_, ch1 := bus.Subscribe()
	_, ch2 := bus.Subscribe()

	bus.Emit(daemon.Event{
		Type:      daemon.EventExpertCompleted,
		Timestamp: time.Now(),
	})

	for _, ch := range []<-chan daemon.Event{ch1, ch2} {
		select {
		case e := <-ch:
			if e.Type != daemon.EventExpertCompleted {
				t.Errorf("type = %v, want expert.completed", e.Type)
			}
		case <-time.After(time.Second):
			t.Fatal("subscriber did not receive event")
		}
	}
}

func TestEventBus_UnsubscribeCleans(t *testing.T) {
	bus := daemon.NewEventBusForTest()

	id, ch := bus.Subscribe()
	bus.Unsubscribe(id)

	// Channel should be closed
	_, ok := <-ch
	if ok {
		t.Error("channel should be closed after unsubscribe")
	}
}

func TestEventBus_SlowDrops(t *testing.T) {
	bus := daemon.NewEventBusForTest()

	_, ch := bus.Subscribe()

	// Fill the buffer (capacity 64) plus extra
	for i := 0; i < 80; i++ {
		bus.Emit(daemon.Event{
			Type:      daemon.EventExpertSpawning,
			Timestamp: time.Now(),
		})
	}

	// Should have 64 events (buffer capacity), not 80
	count := 0
	for {
		select {
		case <-ch:
			count++
		default:
			goto done
		}
	}
done:
	if count != 64 {
		t.Errorf("received %d events, want 64 (buffer capacity)", count)
	}
}
