package bencode

import "testing"

func FuzzDecode(f *testing.F) {
	// Valid bencode
	f.Add([]byte("5:hello"))
	f.Add([]byte("i42e"))
	f.Add([]byte("i-1e"))
	f.Add([]byte("i0e"))
	f.Add([]byte("le"))
	f.Add([]byte("de"))
	f.Add([]byte("l5:helloi42ee"))
	f.Add([]byte("d3:foo3:bar5:helloi52ee"))
	// Known malformed inputs
	f.Add([]byte(""))
	f.Add([]byte("ie"))
	f.Add([]byte("i42"))
	f.Add([]byte("9223372036854775807:x"))
	f.Add([]byte("l"))
	f.Add([]byte("d3:foo"))
	f.Add([]byte("x"))
	f.Add([]byte{0xff, 0xfe, 0x00})

	f.Fuzz(func(t *testing.T, data []byte) {
		// Must never panic. Return value is irrelevant.
		Decode(data)
	})
}
