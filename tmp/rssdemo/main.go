// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// rssdemo creates several kinds of resident memory and writes an "rss" pprof
// profile to disk.
//
// Run with a Go toolchain that contains the runtime/pprof "rss" profile:
//
//	go run ./tmp/rssdemo -out rss.pprof
//	go tool pprof -http=:0 rss.pprof
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"runtime/metrics"
	"runtime/pprof"
	"sync"
	"syscall"
	"time"
)

var (
	liveHeap [][]byte
	jsonHeap []jsonDoc
	nonGoMem [][]byte
	sink     uint64
)

func main() {
	out := flag.String("out", "rss.pprof", "output pprof file")
	heapMiB := flag.Int("heap-mib", 512, "live Go heap to retain, in MiB")
	jsonMiB := flag.Int("json-mib", 128, "additional live Go heap produced through JSON unmarshal, in MiB")
	nonGoMiB := flag.Int("non-go-mib", 512, "non-Go memory to mmap and dirty, in MiB")
	goroutines := flag.Int("goroutines", 2000, "goroutines to start with committed stack pages")
	stackKiB := flag.Int("stack-kib", 64, "approximate stack bytes touched per goroutine, in KiB")
	flag.Parse()

	// Increase heap profiling fidelity so the RSS profile has useful heap stack
	// attribution. Set this before allocating the heap below.
	oldRate := runtime.MemProfileRate
	runtime.MemProfileRate = 64 * 1024
	defer func() { runtime.MemProfileRate = oldRate }()

	fmt.Printf("allocating %d MiB live Go heap\n", *heapMiB)
	allocateLiveHeap(*heapMiB)

	fmt.Printf("allocating about %d MiB via JSON unmarshal\n", *jsonMiB)
	allocateJSONHeap(*jsonMiB)

	fmt.Printf("starting %d goroutines touching about %d KiB of stack each\n", *goroutines, *stackKiB)
	ready, release := startStackGoroutines(*goroutines, *stackKiB)
	<-ready
	defer close(release)

	// Allocate non-Go memory last so those pages are hot when RSS is captured;
	// otherwise, under memory pressure, the OS may compress or swap out these
	// untouched-again pages before the profile is written.
	fmt.Printf("allocating and dirtying %d MiB non-Go memory\n", *nonGoMiB)
	allocateNonGo(*nonGoMiB)
	defer freeNonGo()

	// Let goroutines settle, then force two GCs. The memory profiler reports data
	// as of the previous GC cycle, so this makes the live heap and metrics fresh.
	time.Sleep(500 * time.Millisecond)
	runtime.GC()
	runtime.GC()
	printMemoryMetrics("before rss profile")

	prof := pprof.Lookup("rss")
	if prof == nil {
		panic(`runtime/pprof profile "rss" is not available in this toolchain`)
	}

	f, err := os.Create(*out)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := prof.WriteTo(f, 0); err != nil {
		panic(err)
	}
	fmt.Printf("wrote %s\n", *out)

	// Keep references alive until after profile capture.
	runtime.KeepAlive(liveHeap)
	runtime.KeepAlive(jsonHeap)
	runtime.KeepAlive(nonGoMem)
	runtime.KeepAlive(sink)
}

func printMemoryMetrics(label string) {
	names := []string{
		"/gc/heap/live:bytes",
		"/gc/heap/goal:bytes",
		"/memory/classes/total:bytes",
		"/memory/classes/heap/free:bytes",
		"/memory/classes/heap/objects:bytes",
		"/memory/classes/heap/released:bytes",
		"/memory/classes/heap/stacks:bytes",
		"/memory/classes/heap/unused:bytes",
		"/memory/classes/os-stacks:bytes",
	}
	samples := make([]metrics.Sample, len(names))
	for i, name := range names {
		samples[i].Name = name
	}
	metrics.Read(samples)
	fmt.Printf("%s runtime metrics:\n", label)
	for _, sample := range samples {
		fmt.Printf("  %-45s %8.2f MiB\n", sample.Name, float64(sample.Value.Uint64())/(1<<20))
	}
	var total, released uint64
	for _, sample := range samples {
		switch sample.Name {
		case "/memory/classes/total:bytes":
			total = sample.Value.Uint64()
		case "/memory/classes/heap/released:bytes":
			released = sample.Value.Uint64()
		}
	}
	fmt.Printf("  %-45s %8.2f MiB\n", "Go memory estimate (total-released)", float64(total-released)/(1<<20))
}

func allocateLiveHeap(mib int) {
	const chunk = 1 << 20
	liveHeap = make([][]byte, mib)
	for i := range liveHeap {
		b := make([]byte, chunk)
		// Dirty every page so the allocation is resident.
		for off := 0; off < len(b); off += os.Getpagesize() {
			b[off] = byte(i)
		}
		liveHeap[i] = b
	}
}

func allocateNonGo(mib int) {
	const chunkMiB = 64
	remaining := mib
	for remaining > 0 {
		n := chunkMiB
		if remaining < n {
			n = remaining
		}
		b, err := syscall.Mmap(-1, 0, n<<20, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
		if err != nil {
			panic(err)
		}
		// Dirty every byte so it contributes to RSS and so any platform/kernel
		// optimizations around sparse page faults are avoided in this demo.
		for off := range b {
			b[off] = 0xa5
		}
		nonGoMem = append(nonGoMem, b)
		remaining -= n
	}
}

func freeNonGo() {
	for _, b := range nonGoMem {
		if err := syscall.Munmap(b); err != nil {
			panic(err)
		}
	}
	nonGoMem = nil
}

type jsonDoc struct {
	ID      int               `json:"id"`
	Name    string            `json:"name"`
	Tags    []string          `json:"tags"`
	Payload map[string]string `json:"payload"`
	Nested  []jsonNested      `json:"nested"`
}

type jsonNested struct {
	Key   string  `json:"key"`
	Value float64 `json:"value"`
}

func allocateJSONHeap(targetMiB int) {
	if targetMiB <= 0 {
		return
	}
	blob := makeJSONBlob()
	// JSON decoding expands this input substantially (maps, strings, slices,
	// structs). Keep decoding until HeapAlloc has grown by roughly targetMiB.
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	target := before.HeapAlloc + uint64(targetMiB)<<20
	for {
		jsonHeap = append(jsonHeap, decodeDocument(blob))
		if len(jsonHeap)%128 == 0 {
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			if ms.HeapAlloc >= target {
				return
			}
		}
	}
}

func makeJSONBlob() []byte {
	doc := jsonDoc{
		ID:   42,
		Name: "rss profile demonstration document with moderately nested JSON",
		Tags: []string{"rss", "pprof", "runtime", "heap", "json", "profiling"},
		Payload: map[string]string{
			"alpha":   stringsOf('a', 2048),
			"bravo":   stringsOf('b', 2048),
			"charlie": stringsOf('c', 2048),
			"delta":   stringsOf('d', 2048),
		},
	}
	for i := 0; i < 128; i++ {
		doc.Nested = append(doc.Nested, jsonNested{Key: fmt.Sprintf("field-%03d", i), Value: float64(i) * 1.25})
	}
	b, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return b
}

func stringsOf(c byte, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = c
	}
	return string(b)
}

//go:noinline
func decodeDocument(blob []byte) jsonDoc {
	var doc jsonDoc
	if err := json.Unmarshal(blob, &doc); err != nil {
		panic(err)
	}
	return doc
}

func startStackGoroutines(n, stackKiB int) (<-chan struct{}, chan<- struct{}) {
	ready := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(seed int) {
			touchStack(stackKiB*1024, uintptr(seed))
			wg.Done()
			<-release
		}(i)
	}
	go func() {
		wg.Wait()
		close(ready)
	}()
	return ready, release
}

//go:noinline
func touchStack(bytes int, seed uintptr) {
	if bytes <= 0 {
		sink += uint64(seed)
		return
	}
	// A small fixed local array makes recursion commit stack pages without a huge
	// single frame. Use it so the compiler cannot optimize the frame away.
	var pad [1024]byte
	for i := range pad {
		pad[i] = byte(seed + uintptr(i))
	}
	sink += uint64(pad[seed%uintptr(len(pad))])
	touchStack(bytes-len(pad), seed+1)
	runtime.KeepAlive(pad)
}
