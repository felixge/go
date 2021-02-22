package pprof

import (
	"context"
	"testing"
)

func TestGoroutineProfiler_Labels(t *testing.T) {
	labels := Labels("foo", "bar")
	ctx := WithLabels(context.Background(), labels)
	ch := make(chan struct{})
	go Do(ctx, labels, func(context.Context) {
		ch <- struct{}{}
		<-ch
	})

	// wait for goroutine above, defer shutting it down
	<-ch
	defer close(ch)

	var found int
	g := NewGoroutineProfiler()
	for _, g := range g.GoroutineProfile() {
		if g.Labels != nil && g.Labels["foo"] == "bar" {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("found %d goroutines with matching label", found)
	}
}


