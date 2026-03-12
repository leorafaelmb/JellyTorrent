package krpc

import (
	"testing"
)

func TestMarshalUnmarshalQuery(t *testing.T) {
	msg := &Message{
		TransactionID: "ab",
		Type:          "q",
		QueryMethod:   "ping",
		Args:          map[string]any{"id": "12345678901234567890"},
	}

	data, err := Marshal(msg)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	got, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if got.TransactionID != msg.TransactionID {
		t.Errorf("txn ID: got %q, want %q", got.TransactionID, msg.TransactionID)
	}
	if got.Type != "q" {
		t.Errorf("type: got %q, want %q", got.Type, "q")
	}
	if got.QueryMethod != "ping" {
		t.Errorf("method: got %q, want %q", got.QueryMethod, "ping")
	}
}

func TestMarshalUnmarshalResponse(t *testing.T) {
	msg := &Message{
		TransactionID: "cd",
		Type:          "r",
		Response:      map[string]any{"id": "12345678901234567890"},
	}

	data, err := Marshal(msg)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	got, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if got.Type != "r" {
		t.Errorf("type: got %q, want %q", got.Type, "r")
	}
	if got.Response == nil {
		t.Error("response should not be nil")
	}
}

func TestMarshalUnmarshalError(t *testing.T) {
	msg := &Message{
		TransactionID: "ef",
		Type:          "e",
		Error:         []any{201, "generic error"},
	}

	data, err := Marshal(msg)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	got, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if got.Type != "e" {
		t.Errorf("type: got %q, want %q", got.Type, "e")
	}
	if len(got.Error) != 2 {
		t.Fatalf("error list length: got %d, want 2", len(got.Error))
	}
}

func TestUnmarshalMissingTransactionID(t *testing.T) {
	// Bencode: d1:y1:qe — missing "t"
	data := []byte("d1:y1:qe")
	_, err := Unmarshal(data)
	if err == nil {
		t.Error("expected error for missing transaction ID")
	}
}

func TestUnmarshalMissingType(t *testing.T) {
	data := []byte("d1:t2:abe")
	_, err := Unmarshal(data)
	if err == nil {
		t.Error("expected error for missing type")
	}
}

func TestUnmarshalUnknownType(t *testing.T) {
	data := []byte("d1:t2:ab1:y1:xe")
	_, err := Unmarshal(data)
	if err == nil {
		t.Error("expected error for unknown type")
	}
}

func TestUnmarshalQueryMissingArgs(t *testing.T) {
	// Has type=q and method=ping but no args
	data := []byte("d1:t2:ab1:y1:q1:q4:pinge")
	_, err := Unmarshal(data)
	if err == nil {
		t.Error("expected error for missing arguments")
	}
}

func TestUnmarshalNotADict(t *testing.T) {
	data := []byte("i42e")
	_, err := Unmarshal(data)
	if err == nil {
		t.Error("expected error for non-dict input")
	}
}

func TestUnmarshalGarbage(t *testing.T) {
	_, err := Unmarshal([]byte("not valid bencode!!!"))
	if err == nil {
		t.Error("expected error for garbage input")
	}
}

func TestMarshalUnmarshalFindNode(t *testing.T) {
	msg := &Message{
		TransactionID: "xy",
		Type:          "q",
		QueryMethod:   "find_node",
		Args: map[string]any{
			"id":     "12345678901234567890",
			"target": "09876543210987654321",
		},
	}

	data, err := Marshal(msg)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	got, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if got.QueryMethod != "find_node" {
		t.Errorf("method: got %q, want %q", got.QueryMethod, "find_node")
	}
}
