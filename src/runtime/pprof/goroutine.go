package pprof

import (
	"runtime"
	"time"
	"unsafe"
)

type GoroutineProfiler struct {
	stacks []runtime.StackRecord
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
	var labelmaps []unsafe.Pointer
	for {
		n, ok := runtime_goroutineProfileWithLabels(g.stacks, labelmaps)
		if ok {
			g.stacks = g.stacks[0:n]
			break
		}
		g.stacks = make([]runtime.StackRecord, int(float64(n)*1.1))
		labelmaps = make([]unsafe.Pointer, len(g.stacks))
	}

	gs := make([]*GoroutineRecord, len(g.stacks))
	for i, stack := range g.stacks {
		var labels map[string]string
		if lm := (*labelMap)(labelmaps[i]); lm != nil {
			labels = *lm
		}

		gs[i] = &GoroutineRecord{
			Stack:  stack.Stack(),
			Labels: labels,
		}
	}
	return gs
}

// SetMaxGoroutines limits the profiler to return a maximum of n randomly
// chosen goroutines. TODO(fg) implement!
func (g *GoroutineProfiler) SetMaxGoroutines(n int) {

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
