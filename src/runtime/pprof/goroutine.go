package pprof

import (
	"fmt"
	"runtime"
	"strings"
	"time"
	"unsafe"
)

type GoroutineProfiler struct {
	stacks        []runtime.StackRecord
	labelmaps     []unsafe.Pointer
	ids           []int64
	statuses      []string
	gopcs         []uintptr
	waitsinces    []int64
	maxGoroutines int
	labelFilter   map[string]string
	offset        uint
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
		l := g.maxGoroutines
		if l == 0 {
			l = int(float64(runtime.NumGoroutine()) * 1.1)
		}
		g.stacks = make([]runtime.StackRecord, l)
		g.labelmaps = make([]unsafe.Pointer, l)
		g.ids = make([]int64, l)
		g.gopcs = make([]uintptr, l)
		g.waitsinces = make([]int64, l)
		g.statuses = make([]string, l)

		n, more := runtime_goroutineProfileWithLabels2(
			g.stacks,
			g.labelmaps,
			g.ids,
			g.statuses,
			g.gopcs,
			g.waitsinces,
			g.labelFilter,
			g.offset,
		)
		if !more || n == l {
			g.stacks = g.stacks[0:n]
			g.offset += uint(n)
			break
		}
	}

	gs := make([]*GoroutineRecord, len(g.stacks))
	for i, stack := range g.stacks {
		var labels map[string]string
		if lm := (*labelMap)(g.labelmaps[i]); lm != nil {
			labels = *lm
		}

		gs[i] = &GoroutineRecord{
			ID:        g.ids[i],
			Stack:     stack.Stack(),
			Status:    g.statuses[i],
			CreatedBy: g.gopcs[i],
			Wait:      time.Duration(g.waitsinces[i]),
			Labels:    labels,
		}
	}

	return gs
}

// SetMaxGoroutines sets the maximum number of goroutines to be returned by
// GoroutineProfile(). If set to 0 (default) all goroutines will be returned
// which required a O(N) stop-the-world phase that might last 1ms per 1k
// goroutines or longer. TODO finish writing this
func (g *GoroutineProfiler) SetMaxGoroutines(n int) {
	g.maxGoroutines = n
}

// SetLabelFilter TODO(fg) finish writing this
// TODO(fg) figure out a way to register this with the runtime. Will probably
// require a Close() method on the profiler.
func (g *GoroutineProfiler) SetLabelFilter(filter LabelSet) {
	g.labelFilter = map[string]string{}
	for _, label := range filter.list {
		g.labelFilter[label.key] = label.value
	}
}

// TODO(fg) implement
func (g *GoroutineProfiler) SetStackDepth(n int) {
}

// GoroutineRecord represents a single goroutine and the profiling information
// associated with it.
type GoroutineRecord struct {
	// ID is the id of the goroutine. TODO(fg) I know this is a controversial
	// thing to expose. This field could be made private.
	ID int64
	// Stack is the stack trace of this record in form of program counter (pc)
	// locations.
	Stack []uintptr
	// Labels holds the profiler labels applied to the goroutine. TODO(fg) figure
	// out if this type makes sense.
	Labels map[string]string
	// Status describes the state of the goroutine. TODO(fg) should we expose
	// the real status and waitreason separately?
	Status string
	// Wait has the approximate amount of time that the GC has been parked for as
	// determined by the first GC after parking. TODO(fg) should this be exposed
	// as time.Time?
	Wait time.Duration
	// CreatedBy is the program counter of the go statement that created this
	// goroutine.
	CreatedBy uintptr
}

// String returns the goroutine in a format similar to runtime.Stack().
func (g *GoroutineRecord) String() string {
	frames := runtime.CallersFrames(g.Stack)
	var waitd string
	if g.Wait != 0 {
		// TODO(fg) round to minutes like runtime.Stack()?
		waitd = ", " + g.Wait.String()
	}
	lines := []string{fmt.Sprintf("goroutine %d [%s%s]:", g.ID, g.Status, waitd)}
	for {
		// TODO(fg) should we skip some internal frames here? e.g. the last
		// goexit() frame that seems to be everywhere?
		frame, more := frames.Next()
		// @TODO(fg) add labels here? runtime.Stack() doesn't do this right now
		// but it's been proposed in the past (find link!)
		lines = append(lines, frame.Function+"()")
		// TODO(fg) can be add the offset into the func here? e.g. +0x9f
		lines = append(lines, fmt.Sprintf("\t%s:%d", frame.File, frame.Line))
		if !more {
			break
		}
	}
	if g.CreatedBy != 0 {
		frame, _ := runtime.CallersFrames([]uintptr{g.CreatedBy}).Next()
		lines = append(lines, fmt.Sprintf("created by %s", frame.Function))
		// TODO(fg) can be add the offset into the func here? e.g. +0x9f
		lines = append(lines, fmt.Sprintf("\t%s:%d", frame.File, frame.Line))
	}
	return strings.Join(lines, "\n") + "\n"
}
