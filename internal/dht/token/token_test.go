package token

import (
	"net/netip"
	"sync"
	"testing"
)

func TestGenerateValidateRoundTrip(t *testing.T) {
	tm := New()
	addr := netip.MustParseAddr("192.168.1.1")

	tok := tm.Generate(addr)
	if !tm.Validate(addr, tok) {
		t.Error("freshly generated token should validate")
	}
}

func TestDifferentAddrsDifferentTokens(t *testing.T) {
	tm := New()
	a1 := netip.MustParseAddr("192.168.1.1")
	a2 := netip.MustParseAddr("192.168.1.2")

	t1 := tm.Generate(a1)
	t2 := tm.Generate(a2)
	if t1 == t2 {
		t.Error("different addresses should produce different tokens")
	}
}

func TestTokenValidAfterOneRotation(t *testing.T) {
	tm := New()
	addr := netip.MustParseAddr("10.0.0.1")

	tok := tm.Generate(addr)
	tm.Rotate()
	if !tm.Validate(addr, tok) {
		t.Error("token should still validate after one rotation (previousSecret)")
	}
}

func TestTokenInvalidAfterTwoRotations(t *testing.T) {
	tm := New()
	addr := netip.MustParseAddr("10.0.0.1")

	tok := tm.Generate(addr)
	tm.Rotate()
	tm.Rotate()
	if tm.Validate(addr, tok) {
		t.Error("token should be invalid after two rotations")
	}
}

func TestBogusTokenRejected(t *testing.T) {
	tm := New()
	addr := netip.MustParseAddr("10.0.0.1")

	bogus := [20]byte{0xde, 0xad, 0xbe, 0xef}
	if tm.Validate(addr, bogus) {
		t.Error("bogus token should not validate")
	}
}

func TestWrongAddrRejected(t *testing.T) {
	tm := New()
	a1 := netip.MustParseAddr("10.0.0.1")
	a2 := netip.MustParseAddr("10.0.0.2")

	tok := tm.Generate(a1)
	if tm.Validate(a2, tok) {
		t.Error("token for different address should not validate")
	}
}

func TestConcurrentSafety(t *testing.T) {
	tm := New()
	addr := netip.MustParseAddr("127.0.0.1")

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tok := tm.Generate(addr)
			tm.Validate(addr, tok)
			tm.Rotate()
		}()
	}
	wg.Wait()
}
