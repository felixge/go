// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pprof

import (
	"bytes"
	"internal/profile"
	"internal/profilerecord"
	"strings"
	"testing"
)

func TestRSSProfileStructure(t *testing.T) {
	addr1, addr2, _, _ := testPCs(t)
	a1, a2 := uintptr(addr1)+1, uintptr(addr2)+1

	metrics := profilerecord.MemProfileMetrics{
		RSS: 100 * 1024 * 1024,

		MemoryClassesHeapStacks: 2 * 1024 * 1024,
		MemoryClassesOSStacks:   512 * 1024,

		MemoryClassesMetadataMSpanInuse:  256 * 1024,
		MemoryClassesMetadataMSpanFree:   64 * 1024,
		MemoryClassesMetadataMCacheInuse: 32 * 1024,
		MemoryClassesMetadataMCacheFree:  8 * 1024,

		MemoryClassesMetadataOther:    1 * 1024 * 1024,
		MemoryClassesProfilingBuckets: 128 * 1024,
		MemoryClassesOther:            64 * 1024,
		MemoryClassesHeapFree:         10 * 1024 * 1024,
		MemoryClassesHeapObjects:      40 * 1024 * 1024,
		MemoryClassesHeapUnused:       5 * 1024 * 1024,
		MemoryClassesHeapReleased:     20 * 1024 * 1024,
		HeapLive:                      30 * 1024 * 1024,
	}
	metrics.MemoryClassesTotal = metrics.MemoryClassesHeapStacks + metrics.MemoryClassesOSStacks +
		metrics.MemoryClassesMetadataMSpanInuse + metrics.MemoryClassesMetadataMSpanFree +
		metrics.MemoryClassesMetadataMCacheInuse + metrics.MemoryClassesMetadataMCacheFree +
		metrics.MemoryClassesMetadataOther + metrics.MemoryClassesProfilingBuckets + metrics.MemoryClassesOther +
		metrics.MemoryClassesHeapFree + metrics.MemoryClassesHeapObjects + metrics.MemoryClassesHeapUnused +
		metrics.MemoryClassesHeapReleased

	rec := []profilerecord.MemProfileRecord{
		{ObjectSize: 1024, AllocObjects: 100, FreeObjects: 20, Stack: []uintptr{a1, a2}},
		{ObjectSize: 4096, AllocObjects: 50, FreeObjects: 50, Stack: []uintptr{a2, a1}},
	}

	if err := validateRSSMetrics(&metrics); err != nil {
		t.Fatalf("metrics validation: %v", err)
	}
	var buf bytes.Buffer
	if err := writeRSSProto(&buf, rec, 1, &metrics); err != nil {
		t.Fatalf("writeRSSProto: %v", err)
	}
	p, err := profile.Parse(&buf)
	if err != nil {
		t.Fatalf("profile.Parse: %v", err)
	}
	if len(p.SampleType) != 1 {
		t.Fatalf("got %d sample types, want 1", len(p.SampleType))
	}
	if st := p.SampleType[0]; st.Type != "rss_space" || st.Unit != "bytes" {
		t.Errorf("sample type = {%q, %q}, want {rss_space, bytes}", st.Type, st.Unit)
	}

	var leaves []rssLeafSample
	for _, sample := range p.Sample {
		if len(sample.Value) != 1 {
			t.Fatalf("sample has %d values, want 1", len(sample.Value))
		}
		if sample.Value[0] < 0 {
			t.Errorf("negative sample value: %d", sample.Value[0])
		}
		var vframes []string
		for i := len(sample.Location) - 1; i >= 0; i-- {
			for j := range sample.Location[i].Line {
				line := sample.Location[i].Line[len(sample.Location[i].Line)-1-j]
				if isRSSVirtualFrame(line.Function.Name) {
					vframes = append(vframes, line.Function.Name)
				}
			}
		}
		leaves = append(leaves, rssLeafSample{virtualPath: strings.Join(vframes, ";"), value: sample.Value[0]})
	}

	var total int64
	for _, l := range leaves {
		total += l.value
	}
	if total != int64(metrics.RSS) {
		t.Errorf("sum of leaf values = %d, want RSS = %d", total, metrics.RSS)
	}

	goMemory := metrics.MemoryClassesTotal - metrics.MemoryClassesHeapReleased
	heapDead := metrics.MemoryClassesHeapFree + metrics.MemoryClassesHeapObjects + metrics.MemoryClassesHeapUnused - metrics.HeapLive
	checks := map[string]uint64{
		rssPath(rssFrameGoMemory, rssFrameStack, rssFrameGoroutines):                                metrics.MemoryClassesHeapStacks,
		rssPath(rssFrameGoMemory, rssFrameStack, rssFrameOSThreads):                                 metrics.MemoryClassesOSStacks,
		rssPath(rssFrameGoMemory, rssFrameRuntime, rssFrameAllocator, rssFrameMSpan, rssFrameLive):  metrics.MemoryClassesMetadataMSpanInuse,
		rssPath(rssFrameGoMemory, rssFrameRuntime, rssFrameAllocator, rssFrameMSpan, rssFrameFree):  metrics.MemoryClassesMetadataMSpanFree,
		rssPath(rssFrameGoMemory, rssFrameRuntime, rssFrameAllocator, rssFrameMCache, rssFrameLive): metrics.MemoryClassesMetadataMCacheInuse,
		rssPath(rssFrameGoMemory, rssFrameRuntime, rssFrameAllocator, rssFrameMCache, rssFrameFree): metrics.MemoryClassesMetadataMCacheFree,
		rssPath(rssFrameGoMemory, rssFrameRuntime, rssFrameGCMetadata):                              metrics.MemoryClassesMetadataOther,
		rssPath(rssFrameGoMemory, rssFrameRuntime, rssFrameProfiling):                               metrics.MemoryClassesProfilingBuckets,
		rssPath(rssFrameGoMemory, rssFrameRuntime, rssFrameOther):                                   metrics.MemoryClassesOther,
		rssPath(rssFrameNonGoMemory):                          metrics.RSS - goMemory,
		rssPath(rssFrameGoMemory, rssFrameHeap, rssFrameLive): metrics.HeapLive,
		rssPath(rssFrameGoMemory, rssFrameHeap, rssFrameDead): heapDead,
	}
	for path, want := range checks {
		if got := sumRSSLeavesUnder(leaves, path); got != int64(want) {
			t.Errorf("subtree %s: got %d, want %d", path, got, want)
		}
	}
}

func TestRSSProfileDebugUnsupported(t *testing.T) {
	p := Lookup("rss")
	if p == nil {
		t.Fatal(`Lookup("rss") returned nil`)
	}
	var buf bytes.Buffer
	if err := p.WriteTo(&buf, 1); err == nil || !strings.Contains(err.Error(), "only debug=0") {
		t.Fatalf("WriteTo debug=1 error = %v, want only debug=0 error", err)
	}
}

func rssPath(frames ...rssVirtualFrame) string {
	parts := make([]string, len(frames))
	for i, f := range frames {
		parts[i] = string(f)
	}
	return strings.Join(parts, ";")
}

func isRSSVirtualFrame(name string) bool {
	return len(name) >= 3 && name[0] == '[' && name[len(name)-1] == ']'
}

type rssLeafSample struct {
	virtualPath string
	value       int64
}

func sumRSSLeavesUnder(leaves []rssLeafSample, prefix string) int64 {
	var total int64
	for _, l := range leaves {
		if l.virtualPath == prefix || strings.HasPrefix(l.virtualPath, prefix+";") {
			total += l.value
		}
	}
	return total
}
