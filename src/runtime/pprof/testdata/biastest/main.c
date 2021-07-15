// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#include <pthread.h>
#include <assert.h>
#include <stdlib.h>
#include <stdint.h>
#include <stdio.h>
#include "_cgo_export.h"

#if defined(__linux__) && defined(__x86_64__)
#define __USE_GNU // allow access to uc_mcontext.gregs[REG_RIP]
#include <ucontext.h>
#endif

// cgoHog is a macro so that goCgo0Thread, etc. will be the leaf function in
// our our profile. This allows their pcs to show up in CPU profiles even so
// we're not using a cgosymbolizer to unwind and symbolize the stack.
// TODO(fg) use fancy cpu hog implementation?
#define cgoHog() while(1) { asm(""); }

// cgoHogFn is called by Go functions to hog the CPU running using cgo code.
void cgoHogFn() {
  cgoHog();
}

// goCgo
void goCgo0Thread() { cgoHog(); }
void goCgo1Thread() { cgoHog(); }
void goCgo2Thread() { cgoHog(); }
void goCgo3Thread() { cgoHog(); }
void goCgo4Thread() { cgoHog(); }
void goCgo5Thread() { cgoHog(); }
void goCgo6Thread() { cgoHog(); }
void goCgo7Thread() { cgoHog(); }

// cgoCgo
void *cgoCgo0Thread(void* arg) { cgoHog(); }
void *cgoCgo1Thread(void* arg) { cgoHog(); }
void *cgoCgo2Thread(void* arg) { cgoHog(); }
void *cgoCgo3Thread(void* arg) { cgoHog(); }
void *cgoCgo4Thread(void* arg) { cgoHog(); }
void *cgoCgo5Thread(void* arg) { cgoHog(); }
void *cgoCgo6Thread(void* arg) { cgoHog(); }
void *cgoCgo7Thread(void* arg) { cgoHog(); }

// cgoCgo
void startCgoCgoThread(int threadID) {
  pthread_t thread;
  void *(*fns[])(void*) = {
    cgoCgo0Thread,
    cgoCgo1Thread,
    cgoCgo2Thread,
    cgoCgo3Thread,
    cgoCgo4Thread,
    cgoCgo5Thread,
    cgoCgo6Thread,
    cgoCgo7Thread
  };
  assert(threadID < 8);
  assert(pthread_create(&thread, NULL, fns[threadID], NULL) == 0);
}

// cgoGo
void *runCgoGoThread(void *arg) {
  int* threadID = arg;
  void (*fns[])() = {
    cgoGo0Thread,
    cgoGo1Thread,
    cgoGo2Thread,
    cgoGo3Thread,
    cgoGo4Thread,
    cgoGo5Thread,
    cgoGo6Thread,
    cgoGo7Thread
  };
  assert(*threadID < 8);
  fns[*threadID]();
  return NULL;
}

// cgoGo
void startCgoGoThread(int threadID) {
  pthread_t thread;
  int* arg = malloc(sizeof(int));
  *arg = threadID;
  assert(pthread_create(&thread, NULL, runCgoGoThread, arg) == 0);
}

// cgoGoCgo
void *runCgoGoCgoThread(void *arg) {
  int* threadID = arg;
  void (*fns[])() = {
    cgoGoCgo0Thread,
    cgoGoCgo1Thread,
    cgoGoCgo2Thread,
    cgoGoCgo3Thread,
    cgoGoCgo4Thread,
    cgoGoCgo5Thread,
    cgoGoCgo6Thread,
    cgoGoCgo7Thread
  };
  assert(*threadID < 8);
  fns[*threadID]();
  return NULL;
}

// cgoGoCgo
void startCgoGoCgoThread(int threadID) {
  pthread_t thread;
  int* arg = malloc(sizeof(int));
  *arg = threadID;
  assert(pthread_create(&thread, NULL, runCgoGoCgoThread, arg) == 0);
}

// cgoGoReturnCgo
void cgoGoReturnCgo0Thread(void* arg) { cgoHog(); }
void cgoGoReturnCgo1Thread(void* arg) { cgoHog(); }
void cgoGoReturnCgo2Thread(void* arg) { cgoHog(); }
void cgoGoReturnCgo3Thread(void* arg) { cgoHog(); }
void cgoGoReturnCgo4Thread(void* arg) { cgoHog(); }
void cgoGoReturnCgo5Thread(void* arg) { cgoHog(); }
void cgoGoReturnCgo6Thread(void* arg) { cgoHog(); }
void cgoGoReturnCgo7Thread(void* arg) { cgoHog(); }

// cgoGoReturnCgo
void *runCgoGoReturnCgoThread(void *arg) {
  goNoOp();

  int* threadID = arg;
  void (*fns[])() = {
    cgoGoReturnCgo0Thread,
    cgoGoReturnCgo1Thread,
    cgoGoReturnCgo2Thread,
    cgoGoReturnCgo3Thread,
    cgoGoReturnCgo4Thread,
    cgoGoReturnCgo5Thread,
    cgoGoReturnCgo6Thread,
    cgoGoReturnCgo7Thread
  };
  assert(*threadID < 8);
  fns[*threadID]();
  return NULL;
}

// cgoGoReturnCgo
void startCgoGoReturnCgoThread(int threadID) {
  pthread_t thread;
  int* arg = malloc(sizeof(int));
  *arg = threadID;
  assert(pthread_create(&thread, NULL, runCgoGoReturnCgoThread, arg) == 0);
}

void callGoHog() {
  goHog();
}

struct cgoTracebackArg {
  uintptr_t context;
  uintptr_t sigContext;
  uintptr_t *buf;
  uintptr_t max;
};

void cgoTraceback(void* parg) {
  // mcontext_t is machine-specific, so we'll only support 64bit linux for now
#if defined(__linux__) && defined(__x86_64__)
    struct cgoTracebackArg* arg = (struct cgoTracebackArg*)(parg);
    // gregs[REG_RIP] is the pc of our program before signal handler interrupted it
    arg->buf[0] = ((ucontext_t *)arg->sigContext)->uc_mcontext.gregs[REG_RIP];
    // Don't attempt to unwind the stack further since that's not the point of
    // biastest. The goal is to cover the branches in the runtime that invoke
    // the cgoTraceback function.
    arg->buf[1] = 0;
#else
    fprintf(stderr, "error: biastest does not support cgoTraceback on this platform\n");
    exit(1);
#endif
};

void cgoContext(void* parg) {
  // We don't do anything here, other than exercising the code path in the
  // go runtime that invokes this function.
};
