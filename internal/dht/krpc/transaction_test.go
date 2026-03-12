package krpc

import (
	"testing"
	"time"
)

func TestAddCompleteDelivers(t *testing.T) {
	tm := NewTransactionManager()
	txnID, ch := tm.Add()

	msg := &Message{TransactionID: txnID, Type: "r", Response: map[string]any{"id": "test"}}
	ok := tm.Complete(txnID, msg)
	if !ok {
		t.Fatal("Complete should return true for pending txn")
	}

	select {
	case got := <-ch:
		if got.TransactionID != txnID {
			t.Errorf("got txnID %q, want %q", got.TransactionID, txnID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message")
	}
}

func TestCompleteUnknownReturnsFalse(t *testing.T) {
	tm := NewTransactionManager()
	ok := tm.Complete("nonexistent", &Message{})
	if ok {
		t.Error("Complete should return false for unknown txnID")
	}
}

func TestCancelClosesChannel(t *testing.T) {
	tm := NewTransactionManager()
	_, ch := tm.Add()

	tm.Cancel("nonexistent") // should not panic

	txnID2, ch2 := tm.Add()
	tm.Cancel(txnID2)

	select {
	case _, open := <-ch2:
		if open {
			t.Error("channel should be closed after Cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out: channel not closed")
	}

	// Original channel should still be open
	select {
	case <-ch:
		t.Error("first channel should not be affected")
	default:
	}
}

func TestDuplicateIDAvoidance(t *testing.T) {
	tm := NewTransactionManager()
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		txnID, _ := tm.Add()
		if ids[txnID] {
			t.Fatalf("duplicate txnID generated: %q", txnID)
		}
		ids[txnID] = true
	}
}

func TestCompleteTwiceReturnsFalse(t *testing.T) {
	tm := NewTransactionManager()
	txnID, _ := tm.Add()

	msg := &Message{TransactionID: txnID, Type: "r"}
	ok1 := tm.Complete(txnID, msg)
	ok2 := tm.Complete(txnID, msg)
	if !ok1 {
		t.Error("first Complete should return true")
	}
	if ok2 {
		t.Error("second Complete should return false (already removed)")
	}
}
