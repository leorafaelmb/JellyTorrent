package token

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"net/netip"
	"sync"
)

type TokenManager struct {
	currentSecret  [20]byte
	previousSecret [20]byte
	mu             sync.Mutex
}

func New() *TokenManager {
	t := &TokenManager{}
	rand.Read(t.currentSecret[:])
	return t
}

func (t *TokenManager) Generate(addr netip.Addr) [20]byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.hash(addr, t.currentSecret)

}

func (t *TokenManager) Validate(addr netip.Addr, token [20]byte) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	curSHA := t.hash(addr, t.currentSecret)
	prevSHA := t.hash(addr, t.previousSecret)

	return hmac.Equal(curSHA[:], token[:]) || hmac.Equal(prevSHA[:], token[:])

}

func (t *TokenManager) Rotate() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.previousSecret = t.currentSecret
	rand.Read(t.currentSecret[:])
}

func (t *TokenManager) hash(addr netip.Addr, secret [20]byte) [20]byte {
	h := hmac.New(sha1.New, secret[:])
	a := addr.As4()
	h.Write(a[:])
	tokenSlice := h.Sum(nil)

	return [20]byte(tokenSlice)
}
