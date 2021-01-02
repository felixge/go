// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import "unsafe"

var (
	labelSync uintptr
	// @TODO(fg) replace []*g with a doubly linked list to reduce
	// deregisterLabels complexity?
	labelgs    map[string]map[string][]*g
	labelglock mutex
)

//go:linkname runtime_setProfLabel runtime/pprof.runtime_setProfLabel
func runtime_setProfLabel(labels unsafe.Pointer) {
	// Introduce race edge for read-back via profile.
	// This would more properly use &getg().labels as the sync address,
	// but we do the read in a signal handler and can't call the race runtime then.
	//
	// This uses racereleasemerge rather than just racerelease so
	// the acquire in profBuf.read synchronizes with *all* prior
	// setProfLabel operations, not just the most recent one. This
	// is important because profBuf.read will observe different
	// labels set by different setProfLabel operations on
	// different goroutines, so it needs to synchronize with all
	// of them (this wouldn't be an issue if we could synchronize
	// on &getg().labels since we would synchronize with each
	// most-recent labels write separately.)
	//
	// racereleasemerge is like a full read-modify-write on
	// labelSync, rather than just a store-release, so it carries
	// a dependency on the previous racereleasemerge, which
	// ultimately carries forward to the acquire in profBuf.read.
	if raceenabled {
		racereleasemerge(unsafe.Pointer(&labelSync))
	}
	gp := getg()
	deregisterLabels(gp)
	gp.labels = labels
	registerLabels(gp)
}

//go:linkname runtime_getProfLabel runtime/pprof.runtime_getProfLabel
func runtime_getProfLabel() unsafe.Pointer {
	return getg().labels
}

func registerLabels(gp *g) {
	labels := (*map[string]string)(gp.labels)
	if labels == nil {
		return
	}

	lock(&labelglock)
	if labelgs == nil {
		labelgs = make(map[string]map[string][]*g, len(*labels))
	}
	for key, val := range *labels {
		if valgs, ok := labelgs[key]; ok {
			valgs[val] = append(valgs[val], gp)
		} else {
			labelgs[key] = map[string][]*g{val: []*g{gp}}
		}
	}
	unlock(&labelglock)
}

func deregisterLabels(gp *g) {
	// deregister gp in labelgs for fast goroutine profiling by labels
	labels := (*map[string]string)(gp.labels)
	if labels == nil {
		return
	}

	lock(&labelglock)
	for key, val := range *labels {
		valgs, ok := labelgs[key]
		if !ok {
			panic("bug: key not found in labelgs")
		}
		oldGs, ok := valgs[val]
		if !ok {
			panic("bug: val not found in valgs")
		}
		var found bool
		newGs := make([]*g, 0, len(oldGs)-1)
		for _, oldg := range oldGs {
			if oldg != gp {
				newGs = append(newGs, oldg)
			} else {
				found = true
			}
		}
		valgs[val] = newGs
		if !found {
			panic("bug: gp not found in oldGs")
		}
	}
	unlock(&labelglock)
}
