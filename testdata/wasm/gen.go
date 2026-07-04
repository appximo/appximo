//go:build ignore

// gen.go hand-assembles testdata/wasm/identity.wasm from the module described in
// identity.wat. No WASM toolchain (tinygo/wat2wasm) is available in this
// environment, so the binary is built byte-by-byte here, with section lengths
// computed automatically (LEB128) to avoid manual sizing errors.
//
// Run from the repo root:  go run testdata/wasm/gen.go
package main

import (
	"bytes"
	"encoding/binary"
	"os"
)

// uleb appends the unsigned LEB128 encoding of v.
func uleb(buf *bytes.Buffer, v uint32) {
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		buf.WriteByte(b)
		if v == 0 {
			return
		}
	}
}

// section writes a section: id, then LEB128(len(content)), then content.
func section(out *bytes.Buffer, id byte, content []byte) {
	out.WriteByte(id)
	uleb(out, uint32(len(content)))
	out.Write(content)
}

// vec prefixes items with a LEB128 count.
func vec(count int, items []byte) []byte {
	var b bytes.Buffer
	uleb(&b, uint32(count))
	b.Write(items)
	return b.Bytes()
}

// name encodes a UTF-8 name as LEB128(len) + bytes.
func name(s string) []byte {
	var b bytes.Buffer
	uleb(&b, uint32(len(s)))
	b.WriteString(s)
	return b.Bytes()
}

const (
	i32 = 0x7f
)

func main() {
	var out bytes.Buffer
	out.Write([]byte{0x00, 0x61, 0x73, 0x6d}) // magic \0asm
	out.Write([]byte{0x01, 0x00, 0x00, 0x00}) // version 1

	// ── Type section (id 1) ──────────────────────────────────────────────
	// type0: (i32) -> (i32)            alloc
	// type1: (i32,i32) -> ()           free
	// type2: (i32,i32) -> (i32,i32)    transform / loop_forever
	ftype := func(params, results []byte) []byte {
		var b bytes.Buffer
		b.WriteByte(0x60)
		b.Write(vec(len(params), params))
		b.Write(vec(len(results), results))
		return b.Bytes()
	}
	var types bytes.Buffer
	t0 := ftype([]byte{i32}, []byte{i32})
	t1 := ftype([]byte{i32, i32}, nil)
	t2 := ftype([]byte{i32, i32}, []byte{i32, i32})
	types.Write(t0)
	types.Write(t1)
	types.Write(t2)
	section(&out, 1, vec(3, types.Bytes()))

	// ── Function section (id 3) ──────────────────────────────────────────
	// func0 alloc:type0, func1 free:type1, func2 transform:type2, func3 loop:type2
	section(&out, 3, vec(4, []byte{0, 1, 2, 2}))

	// ── Memory section (id 5) ────────────────────────────────────────────
	// one memory, limits flag 0 (min only), min 1 page.
	section(&out, 5, vec(1, []byte{0x00, 0x01}))

	// ── Global section (id 6) ────────────────────────────────────────────
	// $heap: mutable i32 = 1024
	var glob bytes.Buffer
	glob.WriteByte(i32)  // valtype
	glob.WriteByte(0x01) // mutable
	glob.WriteByte(0x41) // i32.const
	uleb(&glob, 1024)    // 1024
	glob.WriteByte(0x0b) // end
	section(&out, 6, vec(1, glob.Bytes()))

	// ── Export section (id 7) ────────────────────────────────────────────
	export := func(n string, kind, idx byte) []byte {
		var b bytes.Buffer
		b.Write(name(n))
		b.WriteByte(kind) // 0x00 func, 0x02 mem
		b.WriteByte(idx)
		return b.Bytes()
	}
	var exports bytes.Buffer
	exports.Write(export("memory", 0x02, 0))
	exports.Write(export("alloc", 0x00, 0))
	exports.Write(export("free", 0x00, 1))
	exports.Write(export("transform", 0x00, 2))
	exports.Write(export("loop_forever", 0x00, 3))
	section(&out, 7, vec(5, exports.Bytes()))

	// ── Code section (id 10) ─────────────────────────────────────────────
	body := func(locals, code []byte) []byte {
		var b bytes.Buffer
		b.Write(locals)
		b.Write(code)
		b.WriteByte(0x0b) // end
		// prefix with body size
		var e bytes.Buffer
		uleb(&e, uint32(b.Len()))
		e.Write(b.Bytes())
		return e.Bytes()
	}
	// alloc: local p i32; p=heap; heap=heap+size; return p
	allocLocals := []byte{0x01, 0x01, i32} // 1 group of 1 i32
	allocCode := []byte{
		0x23, 0x00, // global.get 0
		0x21, 0x01, // local.set 1 (p)
		0x23, 0x00, // global.get 0
		0x20, 0x00, // local.get 0 (size)
		0x6a,       // i32.add
		0x24, 0x00, // global.set 0
		0x20, 0x01, // local.get 1 (p)
	}
	// free: no-op
	freeCode := []byte{}
	// transform: local.get0; local.get1
	transformCode := []byte{0x20, 0x00, 0x20, 0x01}
	// loop_forever: loop(void){ br 0 } unreachable
	loopCode := []byte{0x03, 0x40, 0x0c, 0x00, 0x0b, 0x00}

	var code bytes.Buffer
	code.Write(body(allocLocals, allocCode))
	code.Write(body([]byte{0x00}, freeCode))
	code.Write(body([]byte{0x00}, transformCode))
	code.Write(body([]byte{0x00}, loopCode))
	section(&out, 10, vec(4, code.Bytes()))

	if err := os.WriteFile("testdata/wasm/identity.wasm", out.Bytes(), 0o644); err != nil {
		panic(err)
	}
	// sanity: print size
	_ = binary.Size
	os.Stdout.WriteString("wrote testdata/wasm/identity.wasm (" +
		itoa(out.Len()) + " bytes)\n")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
