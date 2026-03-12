package krpc

import (
	"crypto/rand"
	"sync"
)

type TransactionManager struct {
	pending map[string]chan *Message
	mu      sync.Mutex
}

func NewTransactionManager() *TransactionManager {
	return &TransactionManager{
		pending: make(map[string]chan *Message),
	}
}

func (tm *TransactionManager) Add() (string, <-chan *Message) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	var (
		txnBytes = make([]byte, 2)
		txnID    string
	)

	for {
		rand.Read(txnBytes)
		txnID = string(txnBytes)
		if _, exists := tm.pending[txnID]; !exists {
			break
		}
	}

	ch := make(chan *Message, 1)
	tm.pending[txnID] = ch
	return txnID, ch
}

func (tm *TransactionManager) Complete(txnID string, msg *Message) bool {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	ch, ok := tm.pending[txnID]
	if !ok {
		return false
	}
	ch <- msg
	close(ch)
	delete(tm.pending, txnID)
	return true
}

func (tm *TransactionManager) Cancel(txnID string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	ch, ok := tm.pending[txnID]
	if ok {
		close(ch)
		delete(tm.pending, txnID)
	}
}
