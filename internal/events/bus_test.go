package events

import (
	"sync"
	"testing"
)

func TestBusPublishFilterAndUnsubscribe(t *testing.T) {
	t.Parallel()

	// FR-012, FR-090, FR-091: subscribers receive only matching events.
	bus := NewBus()
	defer bus.Close()

	subA, unsubA := bus.Subscribe(ProjectFilter(1))
	defer unsubA()

	pset := NewProjectSetFilter(42, []int64{2})
	subB, unsubB := bus.Subscribe(pset.Match)
	defer unsubB()

	bus.Publish(Event{Type: TypeMessageCreated, ProjectID: 1, Payload: "p1"})
	bus.Publish(Event{Type: TypeMessageCreated, ProjectID: 2, Payload: "p2"})

	evA := <-subA.Events()
	if evA.ProjectID != 1 || evA.Payload != "p1" {
		t.Fatalf("subA got %+v, want project 1", evA)
	}
	select {
	case unexpected := <-subA.Events():
		t.Fatalf("subA got unexpected event: %+v", unexpected)
	default:
	}

	evB := <-subB.Events()
	if evB.ProjectID != 2 || evB.Payload != "p2" {
		t.Fatalf("subB got %+v, want project 2", evB)
	}

	// Dynamic project-set refresh (FR-091): remove project 2, add project 3.
	pset.Set([]int64{3})
	bus.Publish(Event{Type: TypeMessageCreated, ProjectID: 2, Payload: "p2-ignored"})
	bus.Publish(Event{Type: TypeMessageCreated, ProjectID: 3, Payload: "p3"})
	bus.Publish(Event{Type: TypeMembershipChanged, UserID: 42})

	evB2 := <-subB.Events()
	if evB2.ProjectID != 3 {
		t.Fatalf("subB got project %d, want 3", evB2.ProjectID)
	}
	evB3 := <-subB.Events()
	if evB3.Type != TypeMembershipChanged || evB3.UserID != 42 {
		t.Fatalf("subB got %+v, want membership.changed for user 42", evB3)
	}

	// Unsubscribe is idempotent and closes channel.
	unsubA()
	unsubA()
	if _, ok := <-subA.Events(); ok {
		t.Fatalf("expected closed channel after unsubscribe")
	}
	if got := subA.CloseReason(); got != CloseReasonUnsubscribed {
		t.Fatalf("CloseReason = %q, want %q", got, CloseReasonUnsubscribed)
	}
}

func TestBusSlowConsumerDisconnected(t *testing.T) {
	t.Parallel()

	// FR-092: slow consumer exceeding buffer is disconnected without blocking Publish.
	bus := NewBusWithBuffer(4)
	defer bus.Close()

	sub, unsub := bus.Subscribe(AllFilter())
	defer unsub()

	for i := range 10 {
		bus.Publish(Event{Type: TypeMessageCreated, ProjectID: 1, Payload: i})
	}

	if got := sub.CloseReason(); got != CloseReasonSlowConsumer {
		t.Fatalf("CloseReason = %q, want %q", got, CloseReasonSlowConsumer)
	}
	if bus.SubscriberCount() != 0 {
		t.Fatalf("SubscriberCount = %d, want 0", bus.SubscriberCount())
	}

	// Drain buffered events until channel closes.
	drained := 0
	for range sub.Events() {
		drained++
	}
	if drained != 4 {
		t.Fatalf("drained %d events, want 4", drained)
	}
}

func TestBusCloseAndConcurrentAccess(t *testing.T) {
	t.Parallel()

	bus := NewBus()

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sub, unsub := bus.Subscribe(AllFilter())
			defer unsub()
			for i := range 50 {
				bus.Publish(Event{Type: TypeMessageCreated, ProjectID: 1, Payload: i})
			}
			for {
				select {
				case _, ok := <-sub.Events():
					if !ok {
						return
					}
				default:
					return
				}
			}
		}()
	}

	subHold, _ := bus.Subscribe(ProjectFilter(999))
	wg.Wait()

	bus.Close()
	if got := subHold.CloseReason(); got != CloseReasonShutdown {
		t.Fatalf("CloseReason = %q, want %q", got, CloseReasonShutdown)
	}

	// Subscribing after Close returns an already-closed subscription.
	subAfter, unsubAfter := bus.Subscribe(AllFilter())
	defer unsubAfter()
	if _, ok := <-subAfter.Events(); ok {
		t.Fatalf("expected closed subscription after Bus.Close")
	}
	if got := subAfter.CloseReason(); got != CloseReasonShutdown {
		t.Fatalf("CloseReason after Close = %q, want %q", got, CloseReasonShutdown)
	}
}

// FR-091: ProjectSetFilter respects allowAll and scopes membership.changed strictly to target user.
func TestProjectSetFilterAllowAllAndMembershipIsolation(t *testing.T) {
	t.Parallel()

	f := NewProjectSetFilter(42, []int64{10})
	if !f.Match(Event{Type: TypeMessageCreated, ProjectID: 10}) {
		t.Fatal("expected match for project 10")
	}
	if f.Match(Event{Type: TypeMessageCreated, ProjectID: 20}) {
		t.Fatal("expected no match for project 20")
	}
	if !f.Match(Event{Type: TypeMembershipChanged, UserID: 42}) {
		t.Fatal("expected match for own membership.changed")
	}
	if f.Match(Event{Type: TypeMembershipChanged, UserID: 99}) {
		t.Fatal("expected no match for other user's membership.changed")
	}

	f.SetAllowAll(true)
	if !f.Match(Event{Type: TypeMessageCreated, ProjectID: 999}) {
		t.Fatal("expected allowAll to match any project event")
	}
	if f.Match(Event{Type: TypeMembershipChanged, UserID: 99}) {
		t.Fatal("allowAll must not leak another user's membership.changed")
	}

	f.SetAllowAll(false)
	f.Set(nil)
	if f.Match(Event{Type: TypeMessageCreated, ProjectID: 10}) {
		t.Fatal("expected no match after clearing projects and allowAll")
	}
}

// FR-091: Remove drops a single deleted project from the set without touching the others.
func TestProjectSetFilterRemove(t *testing.T) {
	t.Parallel()

	f := NewProjectSetFilter(42, []int64{1, 2})
	f.Remove(2)
	if f.AllowsProject(2) {
		t.Fatal("expected project 2 to be removed")
	}
	if !f.AllowsProject(1) {
		t.Fatal("expected project 1 to remain")
	}
	f.Remove(99) // absent ID is a no-op
	if !f.AllowsProject(1) {
		t.Fatal("expected project 1 to remain after removing an absent id")
	}
}
