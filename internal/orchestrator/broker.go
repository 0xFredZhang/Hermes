package orchestrator

import (
	"bytes"
	"io"
	"strings"
	"sync"
)

// Broker fans streaming log lines out to SSE subscribers per active job, while
// keeping an active backlog so late subscribers can replay from the start.
type Broker struct {
	mu     sync.Mutex
	topics map[int64]*topic
}

type topic struct {
	mu      sync.Mutex
	lines   []string
	pending []byte // partial line not yet terminated by '\n'
	subs    map[chan string]struct{}
	closed  bool
}

func NewBroker() *Broker {
	return &Broker{topics: map[int64]*topic{}}
}

func (b *Broker) get(jobID int64) *topic {
	b.mu.Lock()
	defer b.mu.Unlock()
	tp := b.topics[jobID]
	if tp == nil {
		tp = &topic{subs: map[chan string]struct{}{}}
		b.topics[jobID] = tp
	}
	return tp
}

func (b *Broker) Writer(jobID int64) io.Writer { return &topicWriter{tp: b.get(jobID)} }

type topicWriter struct{ tp *topic }

func (w *topicWriter) Write(p []byte) (int, error) {
	w.tp.mu.Lock()
	defer w.tp.mu.Unlock()
	if w.tp.closed {
		return 0, io.ErrClosedPipe
	}
	w.tp.pending = append(w.tp.pending, p...)
	for {
		i := bytes.IndexByte(w.tp.pending, '\n')
		if i < 0 {
			break
		}
		line := string(w.tp.pending[:i])
		w.tp.pending = w.tp.pending[i+1:]
		w.tp.emit(line)
	}
	return len(p), nil
}

// emit must be called with tp.mu held.
func (tp *topic) emit(line string) {
	tp.lines = append(tp.lines, line)
	for ch := range tp.subs {
		select {
		case ch <- line:
		default: // slow subscriber: drop live line; full backlog stays in tp.lines
		}
	}
}

func (b *Broker) Subscribe(jobID int64) (history []string, ch chan string, done bool, cancel func()) {
	b.mu.Lock()
	tp := b.topics[jobID]
	if tp == nil {
		tp = &topic{subs: map[chan string]struct{}{}}
		b.topics[jobID] = tp
	}
	tp.mu.Lock()
	b.mu.Unlock()
	history = append([]string(nil), tp.lines...)
	if tp.closed {
		tp.mu.Unlock()
		return history, nil, true, func() {}
	}
	ch = make(chan string, 256)
	tp.subs[ch] = struct{}{}
	tp.mu.Unlock()
	cancel = func() {
		tp.mu.Lock()
		defer tp.mu.Unlock()
		if _, ok := tp.subs[ch]; ok {
			delete(tp.subs, ch)
			close(ch)
		}
	}
	return history, ch, false, cancel
}

func (b *Broker) Close(jobID int64) {
	b.finish(jobID, true)
}

// Seal ends all subscriptions but retains an exceptional job's log backlog.
// Close releases the sealed topic after terminal persistence is confirmed.
func (b *Broker) Seal(jobID int64) {
	b.finish(jobID, false)
}

func (b *Broker) finish(jobID int64, release bool) {
	b.mu.Lock()
	tp := b.topics[jobID]
	if tp == nil {
		b.mu.Unlock()
		return
	}
	tp.mu.Lock()
	if !tp.closed {
		if len(tp.pending) > 0 {
			tp.emit(string(tp.pending))
			tp.pending = nil
		}
		tp.closed = true
		for ch := range tp.subs {
			delete(tp.subs, ch)
			close(ch)
		}
	}
	if release {
		delete(b.topics, jobID)
	}
	tp.mu.Unlock()
	b.mu.Unlock()
}

func (b *Broker) Snapshot(jobID int64) string {
	b.mu.Lock()
	tp := b.topics[jobID]
	if tp == nil {
		b.mu.Unlock()
		return ""
	}
	tp.mu.Lock()
	b.mu.Unlock()
	defer tp.mu.Unlock()
	lines := append([]string(nil), tp.lines...)
	if len(tp.pending) > 0 {
		lines = append(lines, string(tp.pending))
	}
	return strings.Join(lines, "\n")
}
