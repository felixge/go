// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/goarch"
	"runtime/internal/sys"
	"unsafe"
)

func gentraceback2(pc0, sp0, lr0 uintptr, gp *g, skip int, pcbuf *uintptr, max int, callback func(*stkframe, unsafe.Pointer) bool, v unsafe.Pointer, flags uint) int {
	if skip > 0 && callback != nil {
		throw("gentraceback callback cannot be used with non-zero skip")
	}

	// Don't call this "g"; it's too easy get "g" and "gp" confused.
	if ourg := getg(); ourg == gp && ourg == ourg.m.curg {
		// The starting sp has been passed in as a uintptr, and the caller may
		// have other uintptr-typed stack references as well.
		// If during one of the calls that got us here or during one of the
		// callbacks below the stack must be grown, all these uintptr references
		// to the stack will not be updated, and gentraceback will continue
		// to inspect the old stack memory, which may no longer be valid.
		// Even if all the variables were updated correctly, it is not clear that
		// we want to expose a traceback that begins on one stack and ends
		// on another stack. That could confuse callers quite a bit.
		// Instead, we require that gentraceback and any other function that
		// accepts an sp for the current goroutine (typically obtained by
		// calling getcallersp) must not run on that goroutine's stack but
		// instead on the g0 stack.
		throw("gentraceback cannot trace user goroutine on its own stack")
	}
	level, _, _ := gotraceback()

	if pc0 == ^uintptr(0) && sp0 == ^uintptr(0) { // Signal to fetch saved values from gp.
		if gp.syscallsp != 0 {
			pc0 = gp.syscallpc
			sp0 = gp.syscallsp
			if usesLR {
				lr0 = 0
			}
		} else {
			pc0 = gp.sched.pc
			sp0 = gp.sched.sp
			if usesLR {
				lr0 = gp.sched.lr
			}
		}
	}

	nprint := 0
	var frame stkframe
	frame.pc = pc0
	frame.sp = sp0
	if usesLR {
		frame.lr = lr0
	}
	waspanic := false
	cgoCtxt := gp.cgoCtxt
	stack := gp.stack
	printing := pcbuf == nil && callback == nil

	// If the PC is zero, it's likely a nil function call.
	// Start in the caller's frame.
	if frame.pc == 0 {
		if usesLR {
			frame.pc = *(*uintptr)(unsafe.Pointer(frame.sp))
			frame.lr = 0
		} else {
			frame.pc = uintptr(*(*uintptr)(unsafe.Pointer(frame.sp)))
			frame.sp += goarch.PtrSize
		}
	}

	// runtime/internal/atomic functions call into kernel helpers on
	// arm < 7. See runtime/internal/atomic/sys_linux_arm.s.
	//
	// Start in the caller's frame.
	if GOARCH == "arm" && goarm < 7 && GOOS == "linux" && frame.pc&0xffff0000 == 0xffff0000 {
		// Note that the calls are simple BL without pushing the return
		// address, so we use LR directly.
		//
		// The kernel helpers are frameless leaf functions, so SP and
		// LR are not touched.
		frame.pc = frame.lr
		frame.lr = 0
	}

	f := findfunc(frame.pc)
	if !f.valid() {
		if callback != nil || printing {
			print("runtime: g ", gp.goid, ": unknown pc ", hex(frame.pc), "\n")
			tracebackHexdump(stack, &frame, 0)
		}
		if callback != nil {
			throw("unknown pc")
		}
		return 0
	}
	frame.fn = f

	var cache pcvalueCache

	lastFuncID := funcID_normal
	n := 0
	for n < max {
		// Typically:
		//	pc is the PC of the running function.
		//	sp is the stack pointer at that program counter.
		//	fp is the frame pointer (caller's stack pointer) at that program counter, or nil if unknown.
		//	stk is the stack containing sp.
		//	The caller's program counter is lr, unless lr is zero, in which case it is *(uintptr*)sp.
		f = frame.fn
		if f.pcsp == 0 {
			// No frame information, must be external function, like race support.
			// See golang.org/issue/13568.
			break
		}

		// Compute function info flags.
		flag := f.flag
		if f.funcID == funcID_cgocallback {
			// cgocallback does write SP to switch from the g0 to the curg stack,
			// but it carefully arranges that during the transition BOTH stacks
			// have cgocallback frame valid for unwinding through.
			// So we don't need to exclude it with the other SP-writing functions.
			flag &^= funcFlag_SPWRITE
		}
		if frame.pc == pc0 && frame.sp == sp0 && pc0 == gp.syscallpc && sp0 == gp.syscallsp {
			// Some Syscall functions write to SP, but they do so only after
			// saving the entry PC/SP using entersyscall.
			// Since we are using the entry PC/SP, the later SP write doesn't matter.
			flag &^= funcFlag_SPWRITE
		}

		// Found an actual function.
		// Derive frame pointer and link register.
		if frame.fp == 0 {
			// Jump over system stack transitions. If we're on g0 and there's a user
			// goroutine, try to jump. Otherwise this is a regular call.
			// We also defensively check that this won't switch M's on us,
			// which could happen at critical points in the scheduler.
			// This ensures gp.m doesn't change from a stack jump.
			if flags&_TraceJumpStack != 0 && gp == gp.m.g0 && gp.m.curg != nil && gp.m.curg.m == gp.m {
				switch f.funcID {
				case funcID_morestack:
					// morestack does not return normally -- newstack()
					// gogo's to curg.sched. Match that.
					// This keeps morestack() from showing up in the backtrace,
					// but that makes some sense since it'll never be returned
					// to.
					gp = gp.m.curg
					frame.pc = gp.sched.pc
					frame.fn = findfunc(frame.pc)
					f = frame.fn
					flag = f.flag
					frame.lr = gp.sched.lr
					frame.sp = gp.sched.sp
					stack = gp.stack
					cgoCtxt = gp.cgoCtxt
				case funcID_systemstack:
					// systemstack returns normally, so just follow the
					// stack transition.
					if usesLR && funcspdelta(f, frame.pc, &cache) == 0 {
						// We're at the function prologue and the stack
						// switch hasn't happened, or epilogue where we're
						// about to return. Just unwind normally.
						// Do this only on LR machines because on x86
						// systemstack doesn't have an SP delta (the CALL
						// instruction opens the frame), therefore no way
						// to check.
						flag &^= funcFlag_SPWRITE
						break
					}
					gp = gp.m.curg
					frame.sp = gp.sched.sp
					stack = gp.stack
					cgoCtxt = gp.cgoCtxt
					flag &^= funcFlag_SPWRITE
				}
			}
			frame.fp = frame.sp + uintptr(funcspdelta(f, frame.pc, &cache))
			if !usesLR {
				// On x86, call instruction pushes return PC before entering new function.
				frame.fp += goarch.PtrSize
			}
		}
		var flr funcInfo
		if flag&funcFlag_TOPFRAME != 0 {
			// This function marks the top of the stack. Stop the traceback.
			frame.lr = 0
			flr = funcInfo{}
		} else if flag&funcFlag_SPWRITE != 0 && (callback == nil || n > 0) {
			// The function we are in does a write to SP that we don't know
			// how to encode in the spdelta table. Examples include context
			// switch routines like runtime.gogo but also any code that switches
			// to the g0 stack to run host C code. Since we can't reliably unwind
			// the SP (we might not even be on the stack we think we are),
			// we stop the traceback here.
			// This only applies for profiling signals (callback == nil).
			//
			// For a GC stack traversal (callback != nil), we should only see
			// a function when it has voluntarily preempted itself on entry
			// during the stack growth check. In that case, the function has
			// not yet had a chance to do any writes to SP and is safe to unwind.
			// isAsyncSafePoint does not allow assembly functions to be async preempted,
			// and preemptPark double-checks that SPWRITE functions are not async preempted.
			// So for GC stack traversal we leave things alone (this if body does not execute for n == 0)
			// at the bottom frame of the stack. But farther up the stack we'd better not
			// find any.
			if callback != nil {
				println("traceback: unexpected SPWRITE function", funcname(f))
				throw("traceback")
			}
			frame.lr = 0
			flr = funcInfo{}
		} else {
			var lrPtr uintptr
			if usesLR {
				if n == 0 && frame.sp < frame.fp || frame.lr == 0 {
					lrPtr = frame.sp
					frame.lr = *(*uintptr)(unsafe.Pointer(lrPtr))
				}
			} else {
				if frame.lr == 0 {
					lrPtr = frame.fp - goarch.PtrSize
					frame.lr = uintptr(*(*uintptr)(unsafe.Pointer(lrPtr)))
				}
			}
			flr = findfunc(frame.lr)
			if !flr.valid() {
				// This happens if you get a profiling interrupt at just the wrong time.
				// In that context it is okay to stop early.
				// But if callback is set, we're doing a garbage collection and must
				// get everything, so crash loudly.
				doPrint := printing
				if doPrint && gp.m.incgo && f.funcID == funcID_sigpanic {
					// We can inject sigpanic
					// calls directly into C code,
					// in which case we'll see a C
					// return PC. Don't complain.
					doPrint = false
				}
				if callback != nil || doPrint {
					print("runtime: g ", gp.goid, ": unexpected return pc for ", funcname(f), " called from ", hex(frame.lr), "\n")
					tracebackHexdump(stack, &frame, lrPtr)
				}
				if callback != nil {
					throw("unknown caller pc")
				}
			}
		}

		frame.varp = frame.fp
		if !usesLR {
			// On x86, call instruction pushes return PC before entering new function.
			frame.varp -= goarch.PtrSize
		}

		// For architectures with frame pointers, if there's
		// a frame, then there's a saved frame pointer here.
		//
		// NOTE: This code is not as general as it looks.
		// On x86, the ABI is to save the frame pointer word at the
		// top of the stack frame, so we have to back down over it.
		// On arm64, the frame pointer should be at the bottom of
		// the stack (with R29 (aka FP) = RSP), in which case we would
		// not want to do the subtraction here. But we started out without
		// any frame pointer, and when we wanted to add it, we didn't
		// want to break all the assembly doing direct writes to 8(RSP)
		// to set the first parameter to a called function.
		// So we decided to write the FP link *below* the stack pointer
		// (with R29 = RSP - 8 in Go functions).
		// This is technically ABI-compatible but not standard.
		// And it happens to end up mimicking the x86 layout.
		// Other architectures may make different decisions.
		if frame.varp > frame.sp && framepointer_enabled {
			frame.varp -= goarch.PtrSize
		}

		frame.argp = frame.fp + sys.MinFrameSize

		// Determine frame's 'continuation PC', where it can continue.
		// Normally this is the return address on the stack, but if sigpanic
		// is immediately below this function on the stack, then the frame
		// stopped executing due to a trap, and frame.pc is probably not
		// a safe point for looking up liveness information. In this panicking case,
		// the function either doesn't return at all (if it has no defers or if the
		// defers do not recover) or it returns from one of the calls to
		// deferproc a second time (if the corresponding deferred func recovers).
		// In the latter case, use a deferreturn call site as the continuation pc.
		frame.continpc = frame.pc
		if waspanic {
			if frame.fn.deferreturn != 0 {
				frame.continpc = frame.fn.entry() + uintptr(frame.fn.deferreturn) + 1
				// Note: this may perhaps keep return variables alive longer than
				// strictly necessary, as we are using "function has a defer statement"
				// as a proxy for "function actually deferred something". It seems
				// to be a minor drawback. (We used to actually look through the
				// gp._defer for a defer corresponding to this function, but that
				// is hard to do with defer records on the stack during a stack copy.)
				// Note: the +1 is to offset the -1 that
				// stack.go:getStackMap does to back up a return
				// address make sure the pc is in the CALL instruction.
			} else {
				frame.continpc = 0
			}
		}

		if callback != nil {
			if !callback((*stkframe)(noescape(unsafe.Pointer(&frame))), v) {
				return n
			}
		}

		if pcbuf != nil {
			pc := frame.pc
			// backup to CALL instruction to read inlining info (same logic as below)
			tracepc := pc
			// Normally, pc is a return address. In that case, we want to look up
			// file/line information using pc-1, because that is the pc of the
			// call instruction (more precisely, the last byte of the call instruction).
			// Callers expect the pc buffer to contain return addresses and do the
			// same -1 themselves, so we keep pc unchanged.
			// When the pc is from a signal (e.g. profiler or segv) then we want
			// to look up file/line information using pc, and we store pc+1 in the
			// pc buffer so callers can unconditionally subtract 1 before looking up.
			// See issue 34123.
			// The pc can be at function entry when the frame is initialized without
			// actually running code, like runtime.mstart.
			if (n == 0 && flags&_TraceTrap != 0) || waspanic || pc == f.entry() {
				pc++
			} else {
				tracepc--
			}

			// If there is inlining info, record the inner frames.
			if inldata := funcdata(f, _FUNCDATA_InlTree); inldata != nil {
				inltree := (*[1 << 20]inlinedCall)(inldata)
				for {
					ix := pcdatavalue(f, _PCDATA_InlTreeIndex, tracepc, &cache)
					if ix < 0 {
						break
					}
					if inltree[ix].funcID == funcID_wrapper && elideWrapperCalling(lastFuncID) {
						// ignore wrappers
					} else if skip > 0 {
						skip--
					} else if n < max {
						(*[1 << 20]uintptr)(unsafe.Pointer(pcbuf))[n] = pc
						n++
					}
					lastFuncID = inltree[ix].funcID
					// Back up to an instruction in the "caller".
					tracepc = frame.fn.entry() + uintptr(inltree[ix].parentPc)
					pc = tracepc + 1
				}
			}
			// Record the main frame.
			if f.funcID == funcID_wrapper && elideWrapperCalling(lastFuncID) {
				// Ignore wrapper functions (except when they trigger panics).
			} else if skip > 0 {
				skip--
			} else if n < max {
				(*[1 << 20]uintptr)(unsafe.Pointer(pcbuf))[n] = pc
				n++
			}
			lastFuncID = f.funcID
			n-- // offset n++ below
		}

		if printing {
			// assume skip=0 for printing.
			//
			// Never elide wrappers if we haven't printed
			// any frames. And don't elide wrappers that
			// called panic rather than the wrapped
			// function. Otherwise, leave them out.

			// backup to CALL instruction to read inlining info (same logic as below)
			tracepc := frame.pc
			if (n > 0 || flags&_TraceTrap == 0) && frame.pc > f.entry() && !waspanic {
				tracepc--
			}
			// If there is inlining info, print the inner frames.
			if inldata := funcdata(f, _FUNCDATA_InlTree); inldata != nil {
				inltree := (*[1 << 20]inlinedCall)(inldata)
				var inlFunc _func
				inlFuncInfo := funcInfo{&inlFunc, f.datap}
				for {
					ix := pcdatavalue(f, _PCDATA_InlTreeIndex, tracepc, nil)
					if ix < 0 {
						break
					}

					// Create a fake _func for the
					// inlined function.
					inlFunc.nameOff = inltree[ix].nameOff
					inlFunc.funcID = inltree[ix].funcID
					inlFunc.startLine = inltree[ix].startLine

					if (flags&_TraceRuntimeFrames) != 0 || showframe(inlFuncInfo, gp, nprint == 0, inlFuncInfo.funcID, lastFuncID) {
						name := funcname(inlFuncInfo)
						file, line := funcline(f, tracepc)
						print(name, "(...)\n")
						print("\t", file, ":", line, "\n")
						nprint++
					}
					lastFuncID = inltree[ix].funcID
					// Back up to an instruction in the "caller".
					tracepc = frame.fn.entry() + uintptr(inltree[ix].parentPc)
				}
			}
			if (flags&_TraceRuntimeFrames) != 0 || showframe(f, gp, nprint == 0, f.funcID, lastFuncID) {
				// Print during crash.
				//	main(0x1, 0x2, 0x3)
				//		/home/rsc/go/src/runtime/x.go:23 +0xf
				//
				name := funcname(f)
				file, line := funcline(f, tracepc)
				if name == "runtime.gopanic" {
					name = "panic"
				}
				print(name, "(")
				argp := unsafe.Pointer(frame.argp)
				printArgs(f, argp, tracepc)
				print(")\n")
				print("\t", file, ":", line)
				if frame.pc > f.entry() {
					print(" +", hex(frame.pc-f.entry()))
				}
				if gp.m != nil && gp.m.throwing >= throwTypeRuntime && gp == gp.m.curg || level >= 2 {
					print(" fp=", hex(frame.fp), " sp=", hex(frame.sp), " pc=", hex(frame.pc))
				}
				print("\n")
				nprint++
			}
			lastFuncID = f.funcID
		}
		n++

		if f.funcID == funcID_cgocallback && len(cgoCtxt) > 0 {
			ctxt := cgoCtxt[len(cgoCtxt)-1]
			cgoCtxt = cgoCtxt[:len(cgoCtxt)-1]

			// skip only applies to Go frames.
			// callback != nil only used when we only care
			// about Go frames.
			if skip == 0 && callback == nil {
				n = tracebackCgoContext(pcbuf, printing, ctxt, n, max)
			}
		}

		waspanic = f.funcID == funcID_sigpanic
		injectedCall := waspanic || f.funcID == funcID_asyncPreempt || f.funcID == funcID_debugCallV2

		// Do not unwind past the bottom of the stack.
		if !flr.valid() {
			break
		}

		if frame.pc == frame.lr && frame.sp == frame.fp {
			// If the next frame is identical to the current frame, we cannot make progress.
			print("runtime: traceback stuck. pc=", hex(frame.pc), " sp=", hex(frame.sp), "\n")
			tracebackHexdump(stack, &frame, frame.sp)
			throw("traceback stuck")
		}

		// Unwind to next frame.
		frame.fn = flr
		frame.pc = frame.lr
		frame.lr = 0
		frame.sp = frame.fp
		frame.fp = 0

		// On link register architectures, sighandler saves the LR on stack
		// before faking a call.
		if usesLR && injectedCall {
			x := *(*uintptr)(unsafe.Pointer(frame.sp))
			frame.sp += alignUp(sys.MinFrameSize, sys.StackAlign)
			f = findfunc(frame.pc)
			frame.fn = f
			if !f.valid() {
				frame.pc = x
			} else if funcspdelta(f, frame.pc, &cache) == 0 {
				frame.lr = x
			}
		}
	}

	if printing {
		n = nprint
	}

	// Note that panic != nil is okay here: there can be leftover panics,
	// because the defers on the panic stack do not nest in frame order as
	// they do on the defer stack. If you have:
	//
	//	frame 1 defers d1
	//	frame 2 defers d2
	//	frame 3 defers d3
	//	frame 4 panics
	//	frame 4's panic starts running defers
	//	frame 5, running d3, defers d4
	//	frame 5 panics
	//	frame 5's panic starts running defers
	//	frame 6, running d4, garbage collects
	//	frame 6, running d2, garbage collects
	//
	// During the execution of d4, the panic stack is d4 -> d3, which
	// is nested properly, and we'll treat frame 3 as resumable, because we
	// can find d3. (And in fact frame 3 is resumable. If d4 recovers
	// and frame 5 continues running, d3, d3 can recover and we'll
	// resume execution in (returning from) frame 3.)
	//
	// During the execution of d2, however, the panic stack is d2 -> d3,
	// which is inverted. The scan will match d2 to frame 2 but having
	// d2 on the stack until then means it will not match d3 to frame 3.
	// This is okay: if we're running d2, then all the defers after d2 have
	// completed and their corresponding frames are dead. Not finding d3
	// for frame 3 means we'll set frame 3's continpc == 0, which is correct
	// (frame 3 is dead). At the end of the walk the panic stack can thus
	// contain defers (d3 in this case) for dead frames. The inversion here
	// always indicates a dead frame, and the effect of the inversion on the
	// scan is to hide those dead frames, so the scan is still okay:
	// what's left on the panic stack are exactly (and only) the dead frames.
	//
	// We require callback != nil here because only when callback != nil
	// do we know that gentraceback is being called in a "must be correct"
	// context as opposed to a "best effort" context. The tracebacks with
	// callbacks only happen when everything is stopped nicely.
	// At other times, such as when gathering a stack for a profiling signal
	// or when printing a traceback during a crash, everything may not be
	// stopped nicely, and the stack walk may not be able to complete.
	if callback != nil && n < max && frame.sp != gp.stktopsp {
		print("runtime: g", gp.goid, ": frame.sp=", hex(frame.sp), " top=", hex(gp.stktopsp), "\n")
		print("\tstack=[", hex(gp.stack.lo), "-", hex(gp.stack.hi), "] n=", n, " max=", max, "\n")
		throw("traceback did not unwind completely")
	}

	return n

}
