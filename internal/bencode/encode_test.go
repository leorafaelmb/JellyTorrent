package bencode

import (
	"reflect"
	"testing"
)

func TestEncodeString(t *testing.T) {
	result, err := Encode("hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(result) != "5:hello" {
		t.Errorf("got %q, want %q", string(result), "5:hello")
	}
}

func TestEncodeEmptyString(t *testing.T) {
	result, err := Encode("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(result) != "0:" {
		t.Errorf("got %q, want %q", string(result), "0:")
	}
}

func TestEncodeBytes(t *testing.T) {
	result, err := Encode([]byte{0xff, 0xfe})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := append([]byte("2:"), 0xff, 0xfe)
	if !reflect.DeepEqual(result, expected) {
		t.Errorf("got %v, want %v", result, expected)
	}
}

func TestEncodeInt(t *testing.T) {
	tests := []struct {
		name     string
		input    int
		expected string
	}{
		{"positive", 42, "i42e"},
		{"zero", 0, "i0e"},
		{"negative", -5, "i-5e"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Encode(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(result) != tt.expected {
				t.Errorf("got %q, want %q", string(result), tt.expected)
			}
		})
	}
}

func TestEncodeList(t *testing.T) {
	result, err := Encode([]interface{}{"hello", 42})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(result) != "l5:helloi42ee" {
		t.Errorf("got %q, want %q", string(result), "l5:helloi42ee")
	}
}

func TestEncodeEmptyList(t *testing.T) {
	result, err := Encode([]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(result) != "le" {
		t.Errorf("got %q, want %q", string(result), "le")
	}
}

func TestEncodeDict(t *testing.T) {
	result, err := Encode(map[string]interface{}{
		"foo":   "bar",
		"hello": 52,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Keys must be sorted: "foo" < "hello"
	if string(result) != "d3:foo3:bar5:helloi52ee" {
		t.Errorf("got %q, want %q", string(result), "d3:foo3:bar5:helloi52ee")
	}
}

func TestEncodeDictKeySorting(t *testing.T) {
	result, err := Encode(map[string]interface{}{
		"z": 1,
		"a": 2,
		"m": 3,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Keys sorted: a, m, z
	if string(result) != "d1:ai2e1:mi3e1:zi1ee" {
		t.Errorf("got %q, want %q", string(result), "d1:ai2e1:mi3e1:zi1ee")
	}
}

func TestEncodeEmptyDict(t *testing.T) {
	result, err := Encode(map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(result) != "de" {
		t.Errorf("got %q, want %q", string(result), "de")
	}
}

func TestEncodeNested(t *testing.T) {
	result, err := Encode(map[string]interface{}{
		"list":   []interface{}{"a", "b"},
		"nested": map[string]interface{}{"key": 1},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "d4:listl1:a1:be6:nestedd3:keyi1eee"
	if string(result) != expected {
		t.Errorf("got %q, want %q", string(result), expected)
	}
}

func TestEncodeUnsupportedType(t *testing.T) {
	_, err := Encode(3.14)
	if err == nil {
		t.Fatal("expected error for unsupported type")
	}
}

func TestRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		value interface{}
	}{
		{"string", "hello"},
		{"int", 42},
		{"empty list", []interface{}{}},
		{"list", []interface{}{"foo", 1}},
		{"dict", map[string]interface{}{"key": "value"}},
		{"nested", map[string]interface{}{
			"list": []interface{}{1, "two"},
			"dict": map[string]interface{}{"inner": 3},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := Encode(tt.value)
			if err != nil {
				t.Fatalf("encode error: %v", err)
			}
			decoded, err := Decode(encoded)
			if err != nil {
				t.Fatalf("decode error: %v", err)
			}
			if !reflect.DeepEqual(decoded, tt.value) {
				t.Errorf("round-trip failed: got %v, want %v", decoded, tt.value)
			}
		})
	}
}
