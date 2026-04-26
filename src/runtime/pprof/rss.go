// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pprof

import (
	"fmt"
	"internal/profilerecord"
	"io"
	"runtime"
)

// rssVirtualFrame is a virtual frame name used in the RSS profile to represent
// memory categories. The bracketed names cannot collide with real Go function names.
type rssVirtualFrame string

const (
	rssFrameGoMemory    rssVirtualFrame = "[Go Memory]"
	rssFrameNonGoMemory rssVirtualFrame = "[Non-Go Memory]"
	rssFrameStack       rssVirtualFrame = "[Stack]"
	rssFrameGoroutines  rssVirtualFrame = "[Goroutines]"
	rssFrameOSThreads   rssVirtualFrame = "[OS Threads]"
	rssFrameRuntime     rssVirtualFrame = "[Runtime]"
	rssFrameAllocator   rssVirtualFrame = "[Allocator]"
	rssFrameMSpan       rssVirtualFrame = "[MSpan]"
	rssFrameMCache      rssVirtualFrame = "[MCache]"
	rssFrameLive        rssVirtualFrame = "[Live]"
	rssFrameFree        rssVirtualFrame = "[Free]"
	rssFrameGCMetadata  rssVirtualFrame = "[GC Metadata]"
	rssFrameProfiling   rssVirtualFrame = "[Profiling]"
	rssFrameOther       rssVirtualFrame = "[Other]"
	rssFrameHeap        rssVirtualFrame = "[Heap]"
	rssFrameDead        rssVirtualFrame = "[Dead]"
)

var rssProfile = &Profile{
	name:  "rss",
	count: countRSS,
	write: writeRSS,
}

func countRSS() int { return 0 }

func writeRSS(w io.Writer, debug int) error {
	if debug != 0 {
		return fmt.Errorf("rss profile: only debug=0 is supported")
	}
	p, metrics := readMemProfileWithMetrics()
	if err := validateRSSMetrics(&metrics); err != nil {
		return fmt.Errorf("rss profile: %w", err)
	}
	return writeRSSProto(w, p, int64(runtime.MemProfileRate), &metrics)
}

func readMemProfileWithMetrics() ([]profilerecord.MemProfileRecord, profilerecord.MemProfileMetrics) {
	var p []profilerecord.MemProfileRecord
	var metrics profilerecord.MemProfileMetrics
	n, ok := pprof_memProfileInternal(nil, true, nil)
	for {
		p = make([]profilerecord.MemProfileRecord, n+50)
		n, ok = pprof_memProfileInternal(p, true, &metrics)
		if ok {
			return p[:n], metrics
		}
	}
}

func validateRSSMetrics(m *profilerecord.MemProfileMetrics) error {
	leafSum := m.MemoryClassesHeapStacks + m.MemoryClassesOSStacks +
		m.MemoryClassesMetadataMSpanInuse + m.MemoryClassesMetadataMSpanFree +
		m.MemoryClassesMetadataMCacheInuse + m.MemoryClassesMetadataMCacheFree +
		m.MemoryClassesMetadataOther + m.MemoryClassesProfilingBuckets + m.MemoryClassesOther +
		m.MemoryClassesHeapFree + m.MemoryClassesHeapObjects + m.MemoryClassesHeapUnused +
		m.MemoryClassesHeapReleased
	if leafSum != m.MemoryClassesTotal {
		return fmt.Errorf("memory/classes leaf sum (%d) != total (%d)", leafSum, m.MemoryClassesTotal)
	}
	heapTotal := m.MemoryClassesHeapFree + m.MemoryClassesHeapObjects + m.MemoryClassesHeapUnused
	if m.HeapLive > heapTotal {
		return fmt.Errorf("heap live (%d) > heap total (%d)", m.HeapLive, heapTotal)
	}
	return nil
}

func writeRSSProto(w io.Writer, p []profilerecord.MemProfileRecord, rate int64, m *profilerecord.MemProfileMetrics) error {
	b := newProfileBuilder(w)
	b.pbValueType(tagProfile_PeriodType, "space", "bytes")
	b.pb.int64Opt(tagProfile_Period, 1)
	b.pbValueType(tagProfile_SampleType, "rss_space", "bytes")

	type heapEntry struct {
		inuseSpace int64
		allocSpace int64
		locs       []uint64
	}
	entries := make([]heapEntry, 0, len(p))
	var tmpLocs []uint64
	for _, r := range p {
		_, allocSpace := scaleHeapSample(r.AllocObjects, r.ObjectSize, rate)
		_, inuseSpace := scaleHeapSample(r.InUseObjects(), r.ObjectSize, rate)
		tmpLocs = b.appendLocsForStack(tmpLocs[:0], r.Stack)
		entries = append(entries, heapEntry{inuseSpace: inuseSpace, allocSpace: allocSpace, locs: append([]uint64(nil), tmpLocs...)})
	}

	frames := []rssVirtualFrame{rssFrameGoMemory, rssFrameNonGoMemory, rssFrameStack, rssFrameGoroutines, rssFrameOSThreads, rssFrameRuntime, rssFrameAllocator, rssFrameMSpan, rssFrameMCache, rssFrameLive, rssFrameFree, rssFrameGCMetadata, rssFrameProfiling, rssFrameOther, rssFrameHeap, rssFrameDead}
	nextLocID := uint64(len(b.locs)) + 1
	nextFuncID := uint64(len(b.funcs)) + 1
	vLoc := make(map[rssVirtualFrame]uint64, len(frames))
	for _, frame := range frames {
		name := string(frame)
		funcID := nextFuncID
		nextFuncID++
		fStart := b.pb.startMessage()
		b.pb.uint64Opt(tagFunction_ID, funcID)
		b.pb.int64Opt(tagFunction_Name, b.stringIndex(name))
		b.pb.int64Opt(tagFunction_SystemName, b.stringIndex(name))
		b.pb.int64Opt(tagFunction_Filename, b.stringIndex("[rss profile]"))
		b.pb.endMessage(tagProfile_Function, fStart)
		locID := nextLocID
		nextLocID++
		lStart := b.pb.startMessage()
		b.pb.uint64Opt(tagLocation_ID, locID)
		b.pbLine(tagLocation_Line, funcID, 0)
		b.pb.endMessage(tagProfile_Location, lStart)
		vLoc[frame] = locID
	}

	emitSample := func(value int64, vpath []rssVirtualFrame, realLocs []uint64) {
		if value == 0 {
			return
		}
		locs := make([]uint64, 0, len(realLocs)+len(vpath))
		locs = append(locs, realLocs...)
		for i := len(vpath) - 1; i >= 0; i-- {
			locs = append(locs, vLoc[vpath[i]])
		}
		b.pbSample([]int64{value}, locs, nil)
	}

	fixed := []struct {
		value uint64
		path  []rssVirtualFrame
	}{
		{m.MemoryClassesHeapStacks, []rssVirtualFrame{rssFrameGoMemory, rssFrameStack, rssFrameGoroutines}},
		{m.MemoryClassesOSStacks, []rssVirtualFrame{rssFrameGoMemory, rssFrameStack, rssFrameOSThreads}},
		{m.MemoryClassesMetadataMSpanInuse, []rssVirtualFrame{rssFrameGoMemory, rssFrameRuntime, rssFrameAllocator, rssFrameMSpan, rssFrameLive}},
		{m.MemoryClassesMetadataMSpanFree, []rssVirtualFrame{rssFrameGoMemory, rssFrameRuntime, rssFrameAllocator, rssFrameMSpan, rssFrameFree}},
		{m.MemoryClassesMetadataMCacheInuse, []rssVirtualFrame{rssFrameGoMemory, rssFrameRuntime, rssFrameAllocator, rssFrameMCache, rssFrameLive}},
		{m.MemoryClassesMetadataMCacheFree, []rssVirtualFrame{rssFrameGoMemory, rssFrameRuntime, rssFrameAllocator, rssFrameMCache, rssFrameFree}},
		{m.MemoryClassesMetadataOther, []rssVirtualFrame{rssFrameGoMemory, rssFrameRuntime, rssFrameGCMetadata}},
		{m.MemoryClassesProfilingBuckets, []rssVirtualFrame{rssFrameGoMemory, rssFrameRuntime, rssFrameProfiling}},
		{m.MemoryClassesOther, []rssVirtualFrame{rssFrameGoMemory, rssFrameRuntime, rssFrameOther}},
	}
	for _, f := range fixed {
		emitSample(int64(f.value), f.path, nil)
	}

	emitWeightedHeap := func(total int64, path []rssVirtualFrame, weight func(heapEntry) int64) {
		var weights []int64
		var indices []int
		for i, e := range entries {
			if w := weight(e); w > 0 {
				weights = append(weights, w)
				indices = append(indices, i)
			}
		}
		if len(weights) == 0 {
			emitSample(total, path, nil)
			return
		}
		values := rssDistributeTotal(total, weights)
		for i, idx := range indices {
			emitSample(values[i], path, entries[idx].locs)
		}
	}
	emitWeightedHeap(int64(m.HeapLive), []rssVirtualFrame{rssFrameGoMemory, rssFrameHeap, rssFrameLive}, func(e heapEntry) int64 { return e.inuseSpace })
	heapDead := int64(m.MemoryClassesHeapFree + m.MemoryClassesHeapObjects + m.MemoryClassesHeapUnused - m.HeapLive)
	emitWeightedHeap(heapDead, []rssVirtualFrame{rssFrameGoMemory, rssFrameHeap, rssFrameDead}, func(e heapEntry) int64 { return e.allocSpace - e.inuseSpace })

	goMemory := m.MemoryClassesTotal - m.MemoryClassesHeapReleased
	if m.RSS > goMemory {
		emitSample(int64(m.RSS-goMemory), []rssVirtualFrame{rssFrameNonGoMemory}, nil)
	}
	return b.build()
}

func rssDistributeTotal(total int64, weights []int64) []int64 {
	if len(weights) == 0 {
		return nil
	}
	var sumW int64
	for _, w := range weights {
		sumW += w
	}
	result := make([]int64, len(weights))
	if sumW == 0 {
		result[0] = total
		return result
	}
	var cumW, cumAssigned int64
	for i, w := range weights {
		cumW += w
		target := total * cumW / sumW
		result[i] = target - cumAssigned
		cumAssigned = target
	}
	return result
}
