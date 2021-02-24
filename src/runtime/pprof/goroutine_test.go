package pprof

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

func TestGoroutineProfiler(t *testing.T) {
	t.Run("basics", func(t *testing.T) {
		cleanup := spawnGoroutines(3, "Labels")
		defer cleanup()

		var got int
		g := NewGoroutineProfiler()
		for _, g := range g.GoroutineProfile() {
			if g.Labels == nil {
				fmt.Printf("%s\n", g)
			}
			if g.Labels != nil && g.Labels["test"] == "Labels" {
				got++
			}
		}
		if want := 3; got != want {
			t.Fatalf("got=%d want=%d goroutines", got, want)
		}
	})

	t.Run("SetMaxGoroutines", func(t *testing.T) {
		cleanup := spawnGoroutines(100, "SetMaxGoroutines")
		defer cleanup()

		g := NewGoroutineProfiler()
		g.SetMaxGoroutines(10)

		var randomized bool
		var prev []*GoroutineRecord
		for i := 0; i < 100; i++ {
			gs := g.GoroutineProfile()
			if got, want := len(gs), 10; got != want {
				t.Fatalf("got=%d want=%d goroutines", got, want)
			}

			if prev != nil {
				for i, g := range gs {
					if !reflect.DeepEqual(g.Labels, prev[i].Labels) {
						randomized = true
						break
					}
				}
			}
			prev = gs
		}

		if !randomized {
			t.Fatal("goroutines not randomized")
		}
	})
}

func spawnGoroutines(n int, testLabel string) func() {
	launched := make(chan struct{}, n)
	done := make(chan struct{})
	for i := 0; i < n; i++ {
		labels := Labels("test", testLabel, "test.id", fmt.Sprintf("%d", i))
		go Do(context.Background(), labels, func(context.Context) {
			launched <- struct{}{}
			done <- struct{}{}
		})
	}

	for i := 0; i < n; i++ {
		<-launched
	}

	return func() {
		for i := 0; i < n; i++ {
			<-done
		}
	}
}
