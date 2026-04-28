package redisqueue

import (
	"sync"
	"sync/atomic"
	"time"
)

const retentionWindow = time.Minute

const (
	maxQueueItems = 10000
	maxQueueBytes = 16 << 20
)

type queueItem struct {
	enqueuedAt time.Time
	payload    []byte
}

type queue struct {
	mu         sync.Mutex
	items      []queueItem
	head       int
	totalBytes int
}

var (
	enabled atomic.Bool
	global  queue
)

// SetEnabled toggles the Redis-compatible usage queue. Disabling the queue
// clears any buffered usage details so stale data is not exposed later.
func SetEnabled(value bool) {
	enabled.Store(value)
	if !value {
		global.clear()
	}
}

// Enabled reports whether usage details should be queued for RESP consumers.
func Enabled() bool { return enabled.Load() }

// Enqueue appends a usage detail payload when queueing is enabled.
func Enqueue(payload []byte) {
	if !enabled.Load() || len(payload) == 0 {
		return
	}
	global.enqueue(payload)
}

// PopOldest removes and returns up to count oldest non-expired payloads.
func PopOldest(count int) [][]byte {
	if !enabled.Load() || count <= 0 {
		return nil
	}
	return global.popOldest(count)
}

// PopNewest removes and returns up to count newest non-expired payloads.
func PopNewest(count int) [][]byte {
	if !enabled.Load() || count <= 0 {
		return nil
	}
	return global.popNewest(count)
}

func (q *queue) clear() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = nil
	q.head = 0
	q.totalBytes = 0
}

func (q *queue) enqueue(payload []byte) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.pruneLocked(time.Now())
	copyPayload := append([]byte(nil), payload...)
	q.items = append(q.items, queueItem{enqueuedAt: time.Now(), payload: copyPayload})
	q.totalBytes += len(copyPayload)
	q.enforceCapacityLocked()
	q.maybeCompactLocked()
}

func (q *queue) popOldest(count int) [][]byte {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.pruneLocked(time.Now())
	if count <= 0 || q.head >= len(q.items) {
		return nil
	}

	available := len(q.items) - q.head
	if count > available {
		count = available
	}
	out := make([][]byte, 0, count)
	for i := 0; i < count; i++ {
		payload := q.items[q.head+i].payload
		out = append(out, append([]byte(nil), payload...))
		q.totalBytes -= len(payload)
	}
	q.head += count
	q.maybeCompactLocked()
	return out
}

func (q *queue) popNewest(count int) [][]byte {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.pruneLocked(time.Now())
	if count <= 0 || q.head >= len(q.items) {
		return nil
	}

	available := len(q.items) - q.head
	if count > available {
		count = available
	}
	out := make([][]byte, 0, count)
	for i := 0; i < count; i++ {
		idx := len(q.items) - 1 - i
		payload := q.items[idx].payload
		out = append(out, append([]byte(nil), payload...))
		q.totalBytes -= len(payload)
	}
	q.items = q.items[:len(q.items)-count]
	q.maybeCompactLocked()
	return out
}

func (q *queue) pruneLocked(now time.Time) {
	cutoff := now.Add(-retentionWindow)
	for q.head < len(q.items) && q.items[q.head].enqueuedAt.Before(cutoff) {
		q.totalBytes -= len(q.items[q.head].payload)
		q.head++
	}
	q.maybeCompactLocked()
}

func (q *queue) enforceCapacityLocked() {
	for q.head < len(q.items) && (len(q.items)-q.head > maxQueueItems || q.totalBytes > maxQueueBytes) {
		q.totalBytes -= len(q.items[q.head].payload)
		q.head++
	}
}

func (q *queue) maybeCompactLocked() {
	if q.head == 0 {
		return
	}
	if q.head >= len(q.items) {
		q.items = nil
		q.head = 0
		return
	}
	if q.head < 64 && q.head*2 < len(q.items) {
		return
	}
	remaining := append([]queueItem(nil), q.items[q.head:]...)
	q.items = remaining
	q.head = 0
}
