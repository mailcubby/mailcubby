// Package events provides the in-process publish/subscribe event bus used by
// ingest, API mutations, SSE streams, and the wait-for-message endpoint.
package events

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"
)

const rfc3339Millis = "2006-01-02T15:04:05.000Z"

// DefaultBufferSize is the per-subscriber event buffer size before slow-consumer
// disconnection (SPEC FR-092).
const DefaultBufferSize = 256

// Type identifies a Mailcubby realtime event type (SPEC FR-090).
type Type string

const (
	// TypeMessageCreated is published after a message transaction commits (FR-012).
	TypeMessageCreated Type = "message.created"
	// TypeMessageUpdated is published when seen state or tags change.
	TypeMessageUpdated Type = "message.updated"
	// TypeMessageDeleted is published when one, many, or all messages are deleted.
	TypeMessageDeleted Type = "message.deleted"
	// TypeProjectUpdated is published when project metadata or counters change.
	TypeProjectUpdated Type = "project.updated"
	// TypeProjectCreated is published when a project is created.
	TypeProjectCreated Type = "project.created"
	// TypeProjectDeleted is published when a project is deleted.
	TypeProjectDeleted Type = "project.deleted"
	// TypeMembershipChanged is published when a user's project memberships change.
	TypeMembershipChanged Type = "membership.changed"
)

// CloseReason explains why a Subscription's channel was closed.
type CloseReason string

const (
	// CloseReasonNone indicates the subscription is still active.
	CloseReasonNone CloseReason = ""
	// CloseReasonUnsubscribed indicates the caller invoked the unsubscribe function.
	CloseReasonUnsubscribed CloseReason = "unsubscribed"
	// CloseReasonSlowConsumer indicates the subscriber's buffer filled (SPEC FR-092).
	CloseReasonSlowConsumer CloseReason = "slow_consumer"
	// CloseReasonShutdown indicates the event bus was closed during server shutdown.
	CloseReasonShutdown CloseReason = "shutdown"
)

// Address mirrors the REST API email address pair used in event payloads.
type Address struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

// MessageSummary mirrors SPEC 8.4 MessageSummary for realtime event payloads.
type MessageSummary struct {
	ID              string    `json:"id"`
	ReceivedAt      time.Time `json:"received_at"`
	From            Address   `json:"from"`
	To              []Address `json:"to"`
	Cc              []Address `json:"cc"`
	Subject         string    `json:"subject"`
	Snippet         string    `json:"snippet"`
	SizeBytes       int64     `json:"size_bytes"`
	Seen            bool      `json:"seen"`
	AttachmentCount int64     `json:"attachment_count"`
	HasHTML         bool      `json:"has_html"`
	HasText         bool      `json:"has_text"`
	Tags            []string  `json:"tags"`
	CredentialLabel string    `json:"credential_label,omitempty"`
}

// MarshalJSON encodes MessageSummary with RFC 3339 UTC millisecond timestamps (SPEC 8.1).
func (m MessageSummary) MarshalJSON() ([]byte, error) {
	type alias struct {
		ID              string    `json:"id"`
		ReceivedAt      string    `json:"received_at"`
		From            Address   `json:"from"`
		To              []Address `json:"to"`
		Cc              []Address `json:"cc"`
		Subject         string    `json:"subject"`
		Snippet         string    `json:"snippet"`
		SizeBytes       int64     `json:"size_bytes"`
		Seen            bool      `json:"seen"`
		AttachmentCount int64     `json:"attachment_count"`
		HasHTML         bool      `json:"has_html"`
		HasText         bool      `json:"has_text"`
		Tags            []string  `json:"tags"`
		CredentialLabel string    `json:"credential_label,omitempty"`
	}
	to := m.To
	if to == nil {
		to = []Address{}
	}
	cc := m.Cc
	if cc == nil {
		cc = []Address{}
	}
	tags := m.Tags
	if tags == nil {
		tags = []string{}
	}
	return json.Marshal(alias{
		ID:              m.ID,
		ReceivedAt:      m.ReceivedAt.UTC().Format(rfc3339Millis),
		From:            m.From,
		To:              to,
		Cc:              cc,
		Subject:         m.Subject,
		Snippet:         m.Snippet,
		SizeBytes:       m.SizeBytes,
		Seen:            m.Seen,
		AttachmentCount: m.AttachmentCount,
		HasHTML:         m.HasHTML,
		HasText:         m.HasText,
		Tags:            tags,
		CredentialLabel: m.CredentialLabel,
	})
}

// MessageCreatedPayload is the JSON payload for TypeMessageCreated events.
type MessageCreatedPayload struct {
	Project string         `json:"project"`
	Message MessageSummary `json:"message"`
}

// MessageUpdatedPayload is the JSON payload for TypeMessageUpdated events (SPEC FR-090).
type MessageUpdatedPayload struct {
	Project string    `json:"project"`
	IDs     []string  `json:"ids"`
	Seen    *bool     `json:"seen,omitempty"`
	Tags    *[]string `json:"tags,omitempty"`
}

// MessageDeletedPayload is the JSON payload for TypeMessageDeleted events (SPEC FR-090).
type MessageDeletedPayload struct {
	Project string   `json:"project"`
	IDs     []string `json:"ids,omitempty"`
	All     bool     `json:"all,omitempty"`
}

// ProjectCreatedPayload is the JSON payload for TypeProjectCreated events.
type ProjectCreatedPayload struct {
	Project string `json:"project"`
}

// ProjectDeletedPayload is the JSON payload for TypeProjectDeleted events.
type ProjectDeletedPayload struct {
	Project string `json:"project"`
}

// MembershipChangedPayload is the JSON payload for TypeMembershipChanged events (SPEC FR-090).
type MembershipChangedPayload struct {
	Project string `json:"project,omitempty"`
}

// ProjectUpdatedPayload is the JSON payload for TypeProjectUpdated events (SPEC FR-090).
type ProjectUpdatedPayload struct {
	Project       string     `json:"project"`
	MessageCount  int64      `json:"message_count"`
	UnreadCount   int64      `json:"unread_count"`
	SizeBytes     int64      `json:"size_bytes"`
	LastMessageAt *time.Time `json:"last_message_at"`
}

// MarshalJSON encodes ProjectUpdatedPayload with RFC 3339 UTC millisecond timestamps.
func (p ProjectUpdatedPayload) MarshalJSON() ([]byte, error) {
	type alias struct {
		Project       string  `json:"project"`
		MessageCount  int64   `json:"message_count"`
		UnreadCount   int64   `json:"unread_count"`
		SizeBytes     int64   `json:"size_bytes"`
		LastMessageAt *string `json:"last_message_at"`
	}
	var last *string
	if p.LastMessageAt != nil {
		s := p.LastMessageAt.UTC().Format(rfc3339Millis)
		last = &s
	}
	return json.Marshal(alias{
		Project:       p.Project,
		MessageCount:  p.MessageCount,
		UnreadCount:   p.UnreadCount,
		SizeBytes:     p.SizeBytes,
		LastMessageAt: last,
	})
}

// Event is a single notification routed through the Bus.
type Event struct {
	Type      Type
	ProjectID int64
	UserID    int64
	Payload   any
}

// Filter decides whether a subscriber should receive e. Filters must be fast
// and non-blocking and must not call back into the Bus.
type Filter func(Event) bool

// AllFilter returns a Filter that accepts every event.
func AllFilter() Filter {
	return func(Event) bool { return true }
}

// ProjectFilter returns a Filter that accepts only events for projectID.
func ProjectFilter(projectID int64) Filter {
	return func(e Event) bool {
		return e.ProjectID == projectID
	}
}

// ProjectSetFilter dynamically filters events by a subscriber's accessible project
// set, updated atomically on membership changes (ARCHITECTURE 3.8, SPEC FR-091).
type ProjectSetFilter struct {
	userID   int64
	allowAll atomic.Bool
	projects atomic.Pointer[map[int64]struct{}]
}

// NewProjectSetFilter creates a filter initialized with projectIDs for userID.
func NewProjectSetFilter(userID int64, projectIDs []int64) *ProjectSetFilter {
	f := &ProjectSetFilter{userID: userID}
	f.Set(projectIDs)
	return f
}

// SetAllowAll atomically controls whether the subscriber receives events for
// every project (used for unfiltered instance admin streams).
func (f *ProjectSetFilter) SetAllowAll(allow bool) {
	f.allowAll.Store(allow)
}

// Set atomically replaces the set of accessible project IDs.
func (f *ProjectSetFilter) Set(projectIDs []int64) {
	m := make(map[int64]struct{}, len(projectIDs))
	for _, id := range projectIDs {
		m[id] = struct{}{}
	}
	f.projects.Store(&m)
}

// AllowsProject reports whether projectID is currently permitted by the filter.
func (f *ProjectSetFilter) AllowsProject(projectID int64) bool {
	if f.allowAll.Load() {
		return true
	}
	setPtr := f.projects.Load()
	if setPtr == nil {
		return false
	}
	_, ok := (*setPtr)[projectID]
	return ok
}

// Match returns true if e belongs to an accessible project or is a membership
// change targeting this filter's user.
func (f *ProjectSetFilter) Match(e Event) bool {
	if e.Type == TypeMembershipChanged {
		return f.userID != 0 && e.UserID == f.userID
	}
	return f.AllowsProject(e.ProjectID)
}

// Subscription delivers matching events over C until unsubscribed, disconnected
// as a slow consumer, or shut down.
type Subscription struct {
	ch     chan Event
	filter Filter
	closed bool
	reason atomic.Value // stores CloseReason
}

// Events returns the read-only channel of delivered events.
func (s *Subscription) Events() <-chan Event {
	return s.ch
}

// CloseReason returns why the subscription was closed, or CloseReasonNone while open.
func (s *Subscription) CloseReason() CloseReason {
	v := s.reason.Load()
	if v == nil {
		return CloseReasonNone
	}
	return v.(CloseReason)
}

// Bus fans out published events to matching subscribers without blocking publishers.
type Bus struct {
	bufSize int

	mu     sync.Mutex
	closed bool
	subs   map[*Subscription]struct{}
}

// NewBus constructs a Bus with DefaultBufferSize per subscriber.
func NewBus() *Bus {
	return NewBusWithBuffer(DefaultBufferSize)
}

// NewBusWithBuffer constructs a Bus with a custom per-subscriber buffer size.
func NewBusWithBuffer(bufSize int) *Bus {
	if bufSize <= 0 {
		bufSize = DefaultBufferSize
	}
	return &Bus{
		bufSize: bufSize,
		subs:    make(map[*Subscription]struct{}),
	}
}

// Subscribe registers a subscriber with filter (nil accepts all events) and returns
// the Subscription plus an idempotent unsubscribe function.
func (b *Bus) Subscribe(filter Filter) (*Subscription, func()) {
	if filter == nil {
		filter = AllFilter()
	}
	sub := &Subscription{
		ch:     make(chan Event, b.bufSize),
		filter: filter,
	}

	b.mu.Lock()
	if b.closed {
		sub.closed = true
		sub.reason.Store(CloseReasonShutdown)
		close(sub.ch)
		b.mu.Unlock()
		return sub, func() {}
	}
	b.subs[sub] = struct{}{}
	b.mu.Unlock()

	unsubscribe := func() {
		b.mu.Lock()
		b.closeSubLocked(sub, CloseReasonUnsubscribed)
		b.mu.Unlock()
	}
	return sub, unsubscribe
}

// Publish delivers e to every matching subscriber without blocking.
// Subscribers whose buffers are full are disconnected with CloseReasonSlowConsumer.
func (b *Bus) Publish(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}
	for sub := range b.subs {
		if !sub.filter(e) {
			continue
		}
		select {
		case sub.ch <- e:
		default:
			b.closeSubLocked(sub, CloseReasonSlowConsumer)
		}
	}
}

// Close shuts down the bus and closes all active subscriptions with CloseReasonShutdown.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}
	b.closed = true
	for sub := range b.subs {
		b.closeSubLocked(sub, CloseReasonShutdown)
	}
}

// SubscriberCount returns the number of currently active subscriptions.
func (b *Bus) SubscriberCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

func (b *Bus) closeSubLocked(sub *Subscription, reason CloseReason) {
	if sub.closed {
		return
	}
	sub.closed = true
	sub.reason.Store(reason)
	delete(b.subs, sub)
	close(sub.ch)
}
