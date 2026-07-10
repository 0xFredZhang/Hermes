package orchestrator

import (
	"fmt"
	"testing"
	"time"
)

func TestBrokerWriteSubscribeClose(t *testing.T) {
	b := NewBroker()
	w := b.Writer(1)

	fmt.Fprint(w, "hello\nwor") // "hello" complete; "wor" pending

	history, ch, done, cancel := b.Subscribe(1)
	defer cancel()
	if done {
		t.Fatal("topic should not be done yet")
	}
	if len(history) != 1 || history[0] != "hello" {
		t.Fatalf("history = %v, want [hello]", history)
	}

	fmt.Fprint(w, "ld\nbye\n") // completes "world", then "bye"

	var live []string
	for i := 0; i < 2; i++ {
		select {
		case l := <-ch:
			live = append(live, l)
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for live line")
		}
	}
	if live[0] != "world" || live[1] != "bye" {
		t.Fatalf("live = %v, want [world bye]", live)
	}

	b.Close(1)
	if _, ok := <-ch; ok {
		t.Fatal("subscriber channel should be closed after Close")
	}
	b.mu.Lock()
	_, retained := b.topics[1]
	b.mu.Unlock()
	if retained {
		t.Fatal("closed topic retained in broker map")
	}
	if got := b.Snapshot(1); got != "" {
		t.Fatalf("Snapshot after close = %q, want released log data", got)
	}
}

func TestBrokerCloseReleasesTerminalTopicWithoutBreakingSubscriber(t *testing.T) {
	b := NewBroker()
	w := b.Writer(42)
	history, ch, done, cancel := b.Subscribe(42)
	defer cancel()
	if done || len(history) != 0 {
		t.Fatalf("initial subscription = history %v done %v", history, done)
	}
	if _, err := fmt.Fprint(w, "final partial line"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := b.Snapshot(42); got != "final partial line" {
		t.Fatalf("snapshot before close = %q, want pending line", got)
	}

	b.Close(42)
	select {
	case line, ok := <-ch:
		if !ok || line != "final partial line" {
			t.Fatalf("subscriber terminal line = %q, %v", line, ok)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for terminal line")
	}
	if _, ok := <-ch; ok {
		t.Fatal("subscriber channel remained open")
	}
	b.mu.Lock()
	_, retained := b.topics[42]
	b.mu.Unlock()
	if retained {
		t.Fatal("closed terminal topic retained in broker map")
	}
	if got := b.Snapshot(42); got != "" {
		t.Fatalf("Snapshot after close = %q, want released log data", got)
	}
}

func TestBrokerSealRetainsExceptionalLogsAndEndsSubscribers(t *testing.T) {
	b := NewBroker()
	w := b.Writer(77)
	_, current, done, cancel := b.Subscribe(77)
	defer cancel()
	if done {
		t.Fatal("active subscription unexpectedly done")
	}
	if _, err := fmt.Fprint(w, "unpersisted terminal line"); err != nil {
		t.Fatalf("write: %v", err)
	}

	b.Seal(77)
	if line, ok := <-current; !ok || line != "unpersisted terminal line" {
		t.Fatalf("current terminal line = %q, %v", line, ok)
	}
	if _, ok := <-current; ok {
		t.Fatal("current subscriber remained open after Seal")
	}
	history, future, done, _ := b.Subscribe(77)
	if !done || future != nil || len(history) != 1 || history[0] != "unpersisted terminal line" {
		t.Fatalf("future subscription = history %v channel %v done %v", history, future, done)
	}
	if got := b.Snapshot(77); got != "unpersisted terminal line" {
		t.Fatalf("sealed Snapshot = %q", got)
	}

	b.Close(77)
	if got := b.Snapshot(77); got != "" {
		t.Fatalf("released sealed Snapshot = %q, want empty", got)
	}
}
