// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This program runs multiple cpu hogging threads and outputs the resulting
// CPU profile to stdout. This is used by the cpu bias tests in runtime/pprof.
//
// For quick manual testing, pprof2text (https://github.com/felixge/pprofutils)
// can be used:
//
// ./biastest -goCgo=2 | jq -r .Profile | base64 --decode | pprof2text
// main.glob..func1;main._Cfunc_goCgo0;runtime.cgocall;runtime.asmcgocall;_cgo_262011f26828_Cfunc_goCgo0;goCgo0;cgoHog 99
// main.glob..func2;main._Cfunc_goCgo1;runtime.cgocall;runtime.asmcgocall;_cgo_262011f26828_Cfunc_goCgo1;goCgo1;cgoHog 98

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"syscall"
	"time"
	"unsafe"
)

/*
// avoid inlining to get better cgo stack traces
#cgo CFLAGS: -O0 -g

void goCgo0Thread();
void goCgo1Thread();
void goCgo2Thread();
void goCgo3Thread();
void goCgo4Thread();
void goCgo5Thread();
void goCgo6Thread();
void goCgo7Thread();

void startCgoCgoThread(int);
void startCgoGoThread(int);
void startCgoGoCgoThread(int);
void startCgoGoReturnCgoThread(int);

void callGoHog();
void cgoHogFn();

void cgoTraceback(void*);
void cgoContext(void*);
*/
import "C"

func init() {
	if v := os.Getenv("SETCGOTRACEBACK"); v == "1" {
		// Collect some PCs from C-side, but don't symbolize.
		runtime.SetCgoTraceback(0, unsafe.Pointer(C.cgoTraceback), unsafe.Pointer(C.cgoContext), nil)
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var (
		goGoF           = flag.Int("goGo", 0, "Number of Go-created threads running Go code.")
		goCgoF          = flag.Int("goCgo", 0, "Number of Go-created threads running Cgo code.")
		goCgoGoF        = flag.Int("goCgoGo", 0, "Number of Go-created threads that call a Cgo function that calls Go code.")
		cgoGoF          = flag.Int("cgoGo", 0, "Number of Cgo-created threads running Go code.")
		cgoCgoF         = flag.Int("cgoCgo", 0, "Number of Cgo-created threads running Cgo code.")
		cgoGoCgoF       = flag.Int("cgoGoCgo", 0, "Number of Cgo-created thread that call a Go that call Cgo code.")
		cgoGoReturnCgoF = flag.Int("cgoGoReturnCgo", 0, "Number of Cgo-created threads that call a Go function that immediately returns and then calls Cgo code from Cgo.")
		presleepF       = flag.Duration("presleep", 0, "How long to wait for threads to start before profiling begins")
		durationF       = flag.Duration("duration", time.Second, "The CPU profiling duration after presleep.")
	)
	flag.Parse()
	threads := map[threadKind]int{
		threadGoGo:           *goGoF,
		threadGoCgo:          *goCgoF,
		threadGoCgoGo:        *goCgoGoF,
		threadCgoGo:          *cgoGoF,
		threadCgoCgo:         *cgoCgoF,
		threadCgoGoCgo:       *cgoGoCgoF,
		threadCgoGoReturnCgo: *cgoGoReturnCgoF,
	}

	var (
		// jsonOutput should be kept in sync with src/runtime/pprof/pprof_test.go
		jsonOutput struct {
			RUsage  time.Duration
			Profile []byte
		}
		profBuf bytes.Buffer
	)
	defer func() {
		jsonOutput.Profile = profBuf.Bytes()
		json.NewEncoder(os.Stdout).Encode(jsonOutput)
	}()

	var rusageBefore time.Duration
	startProfiler := func() (err error) {
		if rusageBefore, err = rusage(); err != nil {
			return err
		}
		return pprof.StartCPUProfile(&profBuf)
	}

	defer pprof.StopCPUProfile()
	if *presleepF <= 0 {
		if err := startProfiler(); err != nil {
			return err
		}
	}

	for kind, count := range threads {
		for i := 0; i < count; i++ {
			kind.Start(i)
		}
	}

	if *presleepF > 0 {
		time.Sleep(*presleepF)
		if err := startProfiler(); err != nil {
			return err
		}
	}

	time.Sleep(*durationF)

	rusageAfter, err := rusage()
	if err != nil {
		return err
	}
	jsonOutput.RUsage = rusageAfter - rusageBefore

	return nil
}

type threadKind string

// keep in sync with src/runtime/pprof/pprof_test.go
const (
	threadGoGo           threadKind = "goGo"
	threadGoCgo          threadKind = "goCgo"
	threadGoCgoGo        threadKind = "goCgoGo"
	threadCgoGo          threadKind = "cgoGo"
	threadCgoCgo         threadKind = "cgoCgo"
	threadCgoGoCgo       threadKind = "cgoGoCgo"
	threadCgoGoReturnCgo threadKind = "cgoGoReturnCgo"
)

var goFuncs = map[threadKind][]func(){
	threadGoGo: {
		goGo0Thread,
		goGo1Thread,
		goGo2Thread,
		goGo3Thread,
		goGo4Thread,
		goGo5Thread,
		goGo6Thread,
		goGo7Thread,
	},
	threadGoCgoGo: {
		goCgoGo0Thread,
		goCgoGo1Thread,
		goCgoGo2Thread,
		goCgoGo3Thread,
		goCgoGo4Thread,
		goCgoGo5Thread,
		goCgoGo6Thread,
		goCgoGo7Thread,
	},
	threadGoCgo: {
		func() { C.goCgo0Thread() },
		func() { C.goCgo1Thread() },
		func() { C.goCgo2Thread() },
		func() { C.goCgo3Thread() },
		func() { C.goCgo4Thread() },
		func() { C.goCgo5Thread() },
		func() { C.goCgo6Thread() },
		func() { C.goCgo7Thread() },
	},
}

func (t threadKind) Start(threadID int) {
	switch t {
	case threadGoGo, threadGoCgo, threadGoCgoGo:
		fns := goFuncs[t]
		if threadID < len(fns) {
			go fns[threadID]()
			return
		}
	case threadCgoCgo:
		if threadID < 8 {
			C.startCgoCgoThread(C.int(threadID))
			return
		}
	case threadCgoGo:
		if threadID < 8 {
			C.startCgoGoThread(C.int(threadID))
			return
		}
	case threadCgoGoCgo:
		if threadID < 8 {
			C.startCgoGoCgoThread(C.int(threadID))
			return
		}
	case threadCgoGoReturnCgo:
		if threadID < 8 {
			C.startCgoGoReturnCgoThread(C.int(threadID))
			return
		}
	}
	panic(fmt.Sprintf("thread %d for %q is not implemented", threadID, t))
}

func goGo0Thread() { goHog() }
func goGo1Thread() { goHog() }
func goGo2Thread() { goHog() }
func goGo3Thread() { goHog() }
func goGo4Thread() { goHog() }
func goGo5Thread() { goHog() }
func goGo6Thread() { goHog() }
func goGo7Thread() { goHog() }

func goCgoGo0Thread() { C.callGoHog() }
func goCgoGo1Thread() { C.callGoHog() }
func goCgoGo2Thread() { C.callGoHog() }
func goCgoGo3Thread() { C.callGoHog() }
func goCgoGo4Thread() { C.callGoHog() }
func goCgoGo5Thread() { C.callGoHog() }
func goCgoGo6Thread() { C.callGoHog() }
func goCgoGo7Thread() { C.callGoHog() }

//export cgoGoCgo0Thread
func cgoGoCgo0Thread() { C.cgoHogFn() }

//export cgoGoCgo1Thread
func cgoGoCgo1Thread() { C.cgoHogFn() }

//export cgoGoCgo2Thread
func cgoGoCgo2Thread() { C.cgoHogFn() }

//export cgoGoCgo3Thread
func cgoGoCgo3Thread() { C.cgoHogFn() }

//export cgoGoCgo4Thread
func cgoGoCgo4Thread() { C.cgoHogFn() }

//export cgoGoCgo5Thread
func cgoGoCgo5Thread() { C.cgoHogFn() }

//export cgoGoCgo6Thread
func cgoGoCgo6Thread() { C.cgoHogFn() }

//export cgoGoCgo7Thread
func cgoGoCgo7Thread() { C.cgoHogFn() }

//export cgoGo0Thread
func cgoGo0Thread() { goHog() }

//export cgoGo1Thread
func cgoGo1Thread() { goHog() }

//export cgoGo2Thread
func cgoGo2Thread() { goHog() }

//export cgoGo3Thread
func cgoGo3Thread() { goHog() }

//export cgoGo4Thread
func cgoGo4Thread() { goHog() }

//export cgoGo5Thread
func cgoGo5Thread() { goHog() }

//export cgoGo6Thread
func cgoGo6Thread() { goHog() }

//export cgoGo7Thread
func cgoGo7Thread() { goHog() }

//export goHog
func goHog() {
	// CL 324129 tries to introduce per-thread timers. A pathological scheduler
	// might create a new thread every time it schedules a goroutines and run it
	// for < 10ms before discarding the thread. This would hide the work from the
	// profiler. Rhys suggested calling runtime.LockOSThread() here to make sure
	// our profiling tests would be robust against such scheduler behavior in the
	// future. However, on second thought I think we'd want our profiling tests
	// to fail if the scheduler ever started to behave in such a way, so
	// runtime.LockOSThread() is not being called here for now.

	// TODO(fg) use fancy cpu hog function?
	for {
	}
}

// goNoOp is a function that does nothing and immediately returns.
//export goNoOp
//go:noinline
func goNoOp() {
}

// rusage uses getrusage(2) to return the CPU time used by the process,
// including the system on behalf of the process.
func rusage() (time.Duration, error) {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0, err
	}
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano()), nil
}
