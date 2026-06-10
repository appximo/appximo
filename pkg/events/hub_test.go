package events

import (
	"fmt"
	"sync"
	"testing"
)

func TestSubscribePublishUnsubscribe(t *testing.T) {
	h := NewHub(0)
	sub, err := h.Subscribe("t1", "guides", nil, "", nil)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if got := h.Count("t1"); got != 1 {
		t.Fatalf("count after subscribe = %d, want 1", got)
	}

	h.Publish("t1", Event{Type: "create", Resource: "guides", ID: "a", Record: map[string]any{"id": "a"}})
	select {
	case ev := <-sub.C:
		if ev.Type != "create" || ev.ID != "a" {
			t.Fatalf("got %+v", ev)
		}
	default:
		t.Fatal("event not delivered")
	}

	// Other resource / other tenant must NOT reach this subscriber.
	h.Publish("t1", Event{Type: "create", Resource: "users", ID: "u"})
	h.Publish("t2", Event{Type: "create", Resource: "guides", ID: "x"})
	select {
	case ev := <-sub.C:
		t.Fatalf("unexpected event %+v", ev)
	default:
	}

	h.Unsubscribe(sub)
	if got := h.Count("t1"); got != 0 {
		t.Fatalf("count after unsubscribe = %d, want 0", got)
	}
	h.Unsubscribe(sub) // idempotent
	if got := h.Count("t1"); got != 0 {
		t.Fatalf("count after double unsubscribe = %d, want 0", got)
	}
}

func TestSlowSubscriberIsClosedNotBlocking(t *testing.T) {
	h := NewHub(0)
	sub, _ := h.Subscribe("t1", "guides", nil, "", nil)
	defer h.Unsubscribe(sub)

	// Fill the buffer past capacity without draining: Publish must never block
	// and must close Slow exactly once.
	for i := 0; i < subscriberBuffer+10; i++ {
		h.Publish("t1", Event{Type: "create", Resource: "guides", ID: fmt.Sprint(i)})
	}
	select {
	case <-sub.Slow:
		// closed as expected
	default:
		t.Fatal("Slow not closed after buffer overflow")
	}
	if len(sub.C) != subscriberBuffer {
		t.Fatalf("buffer holds %d, want %d", len(sub.C), subscriberBuffer)
	}
}

func TestTenantLimit(t *testing.T) {
	h := NewHub(2)
	s1, err1 := h.Subscribe("t1", "guides", nil, "", nil)
	_, err2 := h.Subscribe("t1", "users", nil, "", nil)
	if err1 != nil || err2 != nil {
		t.Fatalf("first two subscribes must pass: %v %v", err1, err2)
	}
	if _, err := h.Subscribe("t1", "guides", nil, "", nil); err != ErrTenantLimit {
		t.Fatalf("third subscribe: got %v, want ErrTenantLimit", err)
	}
	// Another tenant is unaffected.
	if _, err := h.Subscribe("t2", "guides", nil, "", nil); err != nil {
		t.Fatalf("other tenant blocked: %v", err)
	}
	// Freeing a slot re-admits.
	h.Unsubscribe(s1)
	if _, err := h.Subscribe("t1", "guides", nil, "", nil); err != nil {
		t.Fatalf("after unsubscribe: %v", err)
	}
}

func TestRowConditionScoping(t *testing.T) {
	h := NewHub(0)
	mine, _ := h.Subscribe("t1", "guides", nil, "operator_id", "u-1")
	defer h.Unsubscribe(mine)

	h.Publish("t1", Event{Type: "create", Resource: "guides", ID: "a",
		Record: map[string]any{"id": "a", "operator_id": "u-1"}})
	h.Publish("t1", Event{Type: "create", Resource: "guides", ID: "b",
		Record: map[string]any{"id": "b", "operator_id": "u-2"}})
	// Delete carries no record → fail closed for a row-scoped subscriber.
	h.Publish("t1", Event{Type: "delete", Resource: "guides", ID: "a", Record: nil})

	if n := len(mine.C); n != 1 {
		t.Fatalf("row-scoped subscriber got %d events, want exactly 1 (own row)", n)
	}
	ev := <-mine.C
	if ev.ID != "a" {
		t.Fatalf("delivered wrong row: %+v", ev)
	}
}

func TestNilHubIsInert(t *testing.T) {
	var h *Hub
	h.Publish("t1", Event{Type: "create", Resource: "guides"}) // must not panic
	if got := h.Count("t1"); got != 0 {
		t.Fatalf("nil hub count = %d", got)
	}
	if _, err := h.Subscribe("t1", "guides", nil, "", nil); err == nil {
		t.Fatal("nil hub Subscribe must error")
	}
}

// TestConcurrentPublishSubscribe exercises the lock paths under -race: parallel
// publishers, subscribers joining/leaving, and slow consumers being closed.
func TestConcurrentPublishSubscribe(t *testing.T) {
	h := NewHub(0)
	var pubWg, subWg sync.WaitGroup
	stop := make(chan struct{})

	for p := 0; p < 4; p++ {
		pubWg.Add(1)
		go func() {
			defer pubWg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				h.Publish("t1", Event{Type: "create", Resource: "guides", ID: fmt.Sprint(i),
					Record: map[string]any{"id": fmt.Sprint(i)}})
			}
		}()
	}
	for s := 0; s < 8; s++ {
		subWg.Add(1)
		go func() {
			defer subWg.Done()
			for i := 0; i < 50; i++ {
				sub, err := h.Subscribe("t1", "guides", nil, "", nil)
				if err != nil {
					t.Errorf("subscribe: %v", err)
					return
				}
				// Drain a few then leave (some subs will overflow → Slow). The
				// publishers guarantee a steady stream, so this never blocks long.
				for j := 0; j < 5; j++ {
					select {
					case <-sub.C:
					case <-sub.Slow:
					}
				}
				h.Unsubscribe(sub)
			}
		}()
	}
	subWg.Wait()
	close(stop)
	pubWg.Wait()
	if got := h.Count("t1"); got != 0 {
		t.Fatalf("subscribers leaked: %d", got)
	}
}
