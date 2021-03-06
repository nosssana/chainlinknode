package utils

import (
	"sync"
)

// Mailbox contains a notify channel,
// a mutual exclusive lock,
// a queue of interfaces,
// and a queue capacity.
type Mailbox struct {
	chNotify chan struct{}
	mu       sync.Mutex
	queue    []interface{}

	// capacity - number of items the mailbox can buffer
	// NOTE: if the capacity is 1, it's possible that an empty Retrieve may occur after a notification.
	capacity uint64
}

// NewHighCapacityMailbox create a new mailbox with a capacity
// that is better able to handle e.g. large log replays
func NewHighCapacityMailbox() *Mailbox {
	return NewMailbox(100000)
}

// NewMailbox creates a new mailbox instance
func NewMailbox(capacity uint64) *Mailbox {
	queueCap := capacity
	if queueCap == 0 {
		queueCap = 100
	}
	return &Mailbox{
		chNotify: make(chan struct{}, 1),
		queue:    make([]interface{}, 0, queueCap),
		capacity: capacity,
	}
}

// Notify returns the contents of the notify channel
func (m *Mailbox) Notify() chan struct{} {
	return m.chNotify
}

// Deliver appends an interface to the queue
func (m *Mailbox) Deliver(x interface{}) (wasOverCapacity bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.queue = append([]interface{}{x}, m.queue...)
	if uint64(len(m.queue)) > m.capacity && m.capacity > 0 {
		m.queue = m.queue[:len(m.queue)-1]
		wasOverCapacity = true
	}

	select {
	case m.chNotify <- struct{}{}:
	default:
	}
	return
}

// Retrieve fetches an interface from the queue
func (m *Mailbox) Retrieve() (interface{}, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.queue) == 0 {
		return nil, false
	}
	x := m.queue[len(m.queue)-1]
	m.queue = m.queue[:len(m.queue)-1]
	return x, true
}

// RetrieveLatestAndClear returns the latest value (or nil), and clears the queue.
func (m *Mailbox) RetrieveLatestAndClear() interface{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.queue) == 0 {
		return nil
	}
	x := m.queue[0]
	m.queue = nil
	return x
}
