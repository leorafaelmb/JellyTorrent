package nodeid

import (
	"testing"
)

func TestDistanceSymmetry(t *testing.T) {
	a := New()
	b := New()
	if a.Distance(b) != b.Distance(a) {
		t.Fatal("XOR distance is not symmetric")
	}
}

func TestDistanceIdentity(t *testing.T) {
	a := New()
	dist := a.Distance(a)
	for _, b := range dist {
		if b != 0 {
			t.Fatal("distance to self should be zero")
		}
	}
}

func TestDistanceTriangleInequality(t *testing.T) {
	a := New()
	b := New()
	c := New()

	ab := a.Distance(b)
	bc := b.Distance(c)
	ac := a.Distance(c)

	// Triangle inequality for XOR: d(a,c) <= d(a,b) XOR d(b,c) doesn't hold
	// in the traditional sense, but we can verify: for any bit position,
	// if ac has a 1-bit, then either ab or bc must also have a 1-bit there.
	for i := 0; i < 20; i++ {
		if ac[i]&^(ab[i]|bc[i]) != 0 {
			t.Fatalf("triangle inequality violated at byte %d", i)
		}
	}
}

func TestPrefixLenIdentical(t *testing.T) {
	a := New()
	if a.PrefixLen(a) != 160 {
		t.Fatalf("prefix length of identical IDs should be 160, got %d", a.PrefixLen(a))
	}
}

func TestPrefixLenZero(t *testing.T) {
	a := NodeID{}
	b := NodeID{0xff}
	if a.PrefixLen(b) != 0 {
		t.Fatalf("expected prefix length 0, got %d", a.PrefixLen(b))
	}
}

func TestPrefixLenBitGranularity(t *testing.T) {
	a := NodeID{}
	tests := []struct {
		firstByte byte
		want      int
	}{
		{0x80, 0}, // 1000_0000
		{0x40, 1}, // 0100_0000
		{0x20, 2}, // 0010_0000
		{0x10, 3}, // 0001_0000
		{0x08, 4}, // 0000_1000
		{0x04, 5}, // 0000_0100
		{0x02, 6}, // 0000_0110
		{0x01, 7}, // 0000_0001
	}
	for _, tt := range tests {
		b := NodeID{tt.firstByte}
		got := a.PrefixLen(b)
		if got != tt.want {
			t.Errorf("PrefixLen with first byte 0x%02x: got %d, want %d", tt.firstByte, got, tt.want)
		}
	}
}

func TestPrefixLenSecondByte(t *testing.T) {
	a := NodeID{}
	b := NodeID{0x00, 0x01}
	got := a.PrefixLen(b)
	if got != 15 {
		t.Fatalf("expected prefix length 15, got %d", got)
	}
}

func TestLessOrdering(t *testing.T) {
	a := NodeID{0x00}
	b := NodeID{0x01}
	c := NodeID{0xff}

	if !a.Less(b) {
		t.Error("expected a < b")
	}
	if !b.Less(c) {
		t.Error("expected b < c")
	}
	if c.Less(a) {
		t.Error("expected c >= a")
	}
}

func TestLessEqual(t *testing.T) {
	a := New()
	if a.Less(a) {
		t.Error("node should not be less than itself")
	}
}

func TestFromBytesValid(t *testing.T) {
	data := make([]byte, 20)
	data[0] = 0xAB
	id, err := FromBytes(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id[0] != 0xAB {
		t.Error("first byte not preserved")
	}
}

func TestFromBytesInvalidLength(t *testing.T) {
	_, err := FromBytes([]byte{1, 2, 3})
	if err == nil {
		t.Error("expected error for wrong length")
	}
}

func TestFromBytesNil(t *testing.T) {
	_, err := FromBytes(nil)
	if err == nil {
		t.Error("expected error for nil input")
	}
}

func TestNewUniqueness(t *testing.T) {
	a := New()
	b := New()
	if a == b {
		t.Error("two random IDs should not be equal")
	}
}

func TestString(t *testing.T) {
	id := NodeID{0xab, 0xcd}
	s := id.String()
	if len(s) != 40 {
		t.Fatalf("hex string should be 40 chars, got %d", len(s))
	}
	if s[:4] != "abcd" {
		t.Errorf("expected prefix 'abcd', got %s", s[:4])
	}
}
