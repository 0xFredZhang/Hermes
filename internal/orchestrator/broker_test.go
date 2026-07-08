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

	hist2, ch2, done2, _ := b.Subscribe(1)
	if !done2 || ch2 != nil {
		t.Fatal("resubscribe after close should report done with nil channel")
	}
	if len(hist2) != 3 {
		t.Fatalf("history after close = %v, want 3 lines", hist2)
	}
	if got := b.Snapshot(1); got != "hello\nworld\nbye" {
		t.Fatalf("Snapshot = %q", got)
	}
}
