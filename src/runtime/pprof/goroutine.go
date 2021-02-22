package pprof

import (
	"math/rand"
	"runtime"
	"time"
	"unsafe"
)

type GoroutineProfiler struct {
	stacks        []runtime.StackRecord
	labelmaps     []unsafe.Pointer
	maxGoroutines int
}

// NewGoroutineProfiler returns a new goroutine profiler. The profiler will use
// O(N) memory where N is the maximum number of profiled goroutines. GC will
// free this memory when the profiler itself is freed.
func NewGoroutineProfiler() *GoroutineProfiler {
	return &GoroutineProfiler{}
}

// GoroutineProfile returns a goroutine profile. The slice and contained data
// can be overwritten by subsequent calls to GoroutineProfile.
func (g *GoroutineProfiler) GoroutineProfile() []*GoroutineRecord {
	for {
		n, ok := runtime_goroutineProfileWithLabels(g.stacks, g.labelmaps)
		if ok {
			g.stacks = g.stacks[0:n]
			break
		}
		g.stacks = make([]runtime.StackRecord, int(float64(n)*1.1))
		g.labelmaps = make([]unsafe.Pointer, len(g.stacks))
	}

	gs := make([]*GoroutineRecord, len(g.stacks))
	for i, stack := range g.stacks {
		var labels map[string]string
		if lm := (*labelMap)(g.labelmaps[i]); lm != nil {
			labels = *lm
		}

		gs[i] = &GoroutineRecord{
			Stack:  stack.Stack(),
			Labels: labels,
		}
	}

	// TODO(fg) do this efficiently in runtime pkg
	if g.maxGoroutines > 0 {
		rand.Shuffle(len(gs), func(i, j int) {
			gs[i], gs[j] = gs[j], gs[i]
		})
		gs = gs[0:g.maxGoroutines]
	}

	return gs
}

// SetMaxGoroutines limits the profiler to return a maximum of n randomly
// chosen goroutines. TODO(fg) implement!
func (g *GoroutineProfiler) SetMaxGoroutines(n int) {
	g.maxGoroutines = n
}

// GoroutineRecord represents a single goroutine and the profiling information
// associated with it.
type GoroutineRecord struct {
	// Stack is the stack trace of this record in form of program counter (pc)
	// locations.
	Stack []uintptr
	// Labels holds the profiler labels applied to the goroutine. TODO(fg) figure
	// out if this type makes sense.
	Labels map[string]string
	// TODO(fg) Implement
	Status string
	// TODO(fg) Implement
	Waitsince time.Time
	// TODO(fg) Implement
	CreatedBy uintptr
}
