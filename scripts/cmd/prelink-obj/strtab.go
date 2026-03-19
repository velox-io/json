package main

import "bytes"

// stringTable builds a null-terminated string table (for .strtab / .shstrtab).
type stringTable struct {
	data   []byte
	lookup map[string]uint32
}

func newStringTable() *stringTable {
	return &stringTable{
		lookup: make(map[string]uint32),
	}
}

func (t *stringTable) add(s string) uint32 {
	if idx, ok := t.lookup[s]; ok {
		return idx
	}
	idx := uint32(len(t.data))
	t.lookup[s] = idx
	t.data = append(t.data, []byte(s)...)
	t.data = append(t.data, 0)
	return idx
}

func align(offset, alignment uint64) uint64 {
	return (offset + alignment - 1) &^ (alignment - 1)
}

func writePadding(buf *bytes.Buffer, n uint64) {
	for range n {
		buf.WriteByte(0)
	}
}
