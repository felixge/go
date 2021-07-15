// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pprof

import (
	"debug/dwarf"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"errors"
	"fmt"
	"internal/profile"
)

// symbolizeProfile tries to symbolizes cgo symbols in the given pprof profile
// using DWARF. This avoids the need for a full cgosymbolizer implementation in
// ./testdata/biastest. The code is copied/inspired from
// src/cmd/pprof/pprof.go.
func symbolizeProfile(bin string, prof *profile.Profile) error {
	sym, err := newSymbolizer(bin)
	if err != nil {
		return err
	}

	var maxFuncID uint64
	for _, f := range prof.Function {
		if f.ID > maxFuncID {
			maxFuncID = f.ID
		}
	}

	for _, s := range prof.Sample {
		for _, loc := range s.Location {
			for lr, line := range loc.Line {
				if line.Function == nil || line.Function.Name == "" {
					fn, err := sym.symbolize(loc.Address)
					if err != nil {
						return fmt.Errorf("failed to symbolize %x: %w", loc.Address, err)
					}
					maxFuncID++
					fn.ID = maxFuncID
					loc.Line[lr].Function = fn
					prof.Function = append(prof.Function, fn)
				}
			}
		}
	}
	return prof.CheckValid()
}

type dwarfer interface {
	DWARF() (*dwarf.Data, error)
}

var openers = []func(string) (dwarfer, error){
	func(p string) (dwarfer, error) { return elf.Open(p) },
	func(p string) (dwarfer, error) { return macho.Open(p) },
	func(p string) (dwarfer, error) { return pe.Open(p) },
}

func newSymbolizer(binary string) (*symbolizer, error) {
	for _, opener := range openers {
		d, err := opener(binary)
		if err != nil {
			continue
		}
		dd, err := d.DWARF()
		if err != nil {
			return nil, err
		}
		return &symbolizer{d: dd}, nil
	}
	return nil, fmt.Errorf("newSymbolizer: failed to open: %q", binary)
}

type symbolizer struct {
	d *dwarf.Data
}

func (s *symbolizer) symbolize(addr uint64) (*profile.Function, error) {
	r := s.d.Reader()
	entry, err := r.SeekPC(addr)
	if err != nil {
		return nil, err
	}

	lines, err := s.d.LineReader(entry)
	if err != nil {
		return nil, err
	} else if lines == nil {
		return nil, errors.New("failed to open line reader")
	}

	var lentry dwarf.LineEntry
	if err := lines.SeekPC(addr, &lentry); err != nil {
		return nil, err
	}

	for entry, err := r.Next(); entry != nil && err == nil; entry, err = r.Next() {
		if entry.Tag == dwarf.TagSubprogram {
			ranges, err := s.d.Ranges(entry)
			if err != nil {
				return nil, err
			}
			for _, pcs := range ranges {
				if pcs[0] <= addr && addr < pcs[1] {
					if name, ok := entry.Val(dwarf.AttrName).(string); ok {
						fn := profile.Function{}
						fn.Name = name
						fn.Filename = lentry.File.Name
						fn.StartLine = int64(lentry.Line)
						return &fn, nil
					}
				}
			}
		}
	}
	return nil, errors.New("failed to lookup pc")
}
