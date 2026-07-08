package orchestrator

import (
	"bytes"
	"io"
	"strings"
	"sync"
)

// Broker fans streaming log lines out to SSE subscribers per job, while keeping
// the full backlog so late subscribers can replay from the start.
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

func NewBroker() *Broker { return &Broker{topics: map[int64]*topic{}} }

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
	tp := b.get(jobID)
	tp.mu.Lock()
	defer tp.mu.Unlock()
	history = append([]string(nil), tp.lines...)
	if tp.closed {
		return history, nil, true, func() {}
	}
	ch = make(chan string, 256)
	tp.subs[ch] = struct{}{}
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
	tp := b.get(jobID)
	tp.mu.Lock()
	defer tp.mu.Unlock()
	if tp.closed {
		return
	}
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

func (b *Broker) Snapshot(jobID int64) string {
	tp := b.get(jobID)
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return strings.Join(tp.lines, "\n")
}
