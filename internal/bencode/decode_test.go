package bencode

import (
	"reflect"
	"testing"
)

func TestDecodeString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple", "5:hello", "hello"},
		{"empty", "0:", ""},
		{"with spaces", "11:hello world", "hello world"},
		{"single char", "1:x", "x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Decode([]byte(tt.input))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			str, ok := result.(string)
			if !ok {
				t.Fatalf("expected string, got %T", result)
			}
			if str != tt.expected {
				t.Errorf("got %q, want %q", str, tt.expected)
			}
		})
	}
}

func TestDecodeStringNonUTF8(t *testing.T) {
	// Non-UTF8 bytes should be returned as []byte
	input := []byte("4:")
	input = append(input, 0xff, 0xfe, 0x80, 0x81)

	result, err := Decode(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, ok := result.([]byte)
	if !ok {
		t.Fatalf("expected []byte for non-UTF8, got %T", result)
	}
	expected := []byte{0xff, 0xfe, 0x80, 0x81}
	if !reflect.DeepEqual(b, expected) {
		t.Errorf("got %v, want %v", b, expected)
	}
}

func TestDecodeInt(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{"positive", "i42e", 42},
		{"zero", "i0e", 0},
		{"negative", "i-5e", -5},
		{"large", "i123456e", 123456},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Decode([]byte(tt.input))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			num, ok := result.(int)
			if !ok {
				t.Fatalf("expected int, got %T", result)
			}
			if num != tt.expected {
				t.Errorf("got %d, want %d", num, tt.expected)
			}
		})
	}
}

func TestDecodeIntErrors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"leading zero", "i03e"},
		{"negative zero", "i-0e"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode([]byte(tt.input))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestDecodeList(t *testing.T) {
	result, err := Decode([]byte("l5:helloi42ee"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	list, ok := result.([]interface{})
	if !ok {
		t.Fatalf("expected []interface{}, got %T", result)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 items, got %d", len(list))
	}
	if list[0] != "hello" {
		t.Errorf("list[0] = %v, want \"hello\"", list[0])
	}
	if list[1] != 42 {
		t.Errorf("list[1] = %v, want 42", list[1])
	}
}

func TestDecodeEmptyList(t *testing.T) {
	result, err := Decode([]byte("le"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	list, ok := result.([]interface{})
	if !ok {
		t.Fatalf("expected []interface{}, got %T", result)
	}
	if len(list) != 0 {
		t.Errorf("expected empty list, got %d items", len(list))
	}
}

func TestDecodeDict(t *testing.T) {
	result, err := Decode([]byte("d3:foo3:bar5:helloi52ee"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	dict, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map[string]interface{}, got %T", result)
	}
	if dict["foo"] != "bar" {
		t.Errorf("dict[\"foo\"] = %v, want \"bar\"", dict["foo"])
	}
	if dict["hello"] != 52 {
		t.Errorf("dict[\"hello\"] = %v, want 52", dict["hello"])
	}
}

func TestDecodeEmptyDict(t *testing.T) {
	result, err := Decode([]byte("de"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	dict, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map[string]interface{}, got %T", result)
	}
	if len(dict) != 0 {
		t.Errorf("expected empty dict, got %d entries", len(dict))
	}
}

func TestDecodeNested(t *testing.T) {
	// Dict containing a list and a nested dict
	input := "d4:listl1:a1:be6:nestedd3:keyi1eee"
	result, err := Decode([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	dict, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}

	list, ok := dict["list"].([]interface{})
	if !ok {
		t.Fatalf("dict[\"list\"] expected []interface{}, got %T", dict["list"])
	}
	if len(list) != 2 || list[0] != "a" || list[1] != "b" {
		t.Errorf("dict[\"list\"] = %v, want [a b]", list)
	}

	nested, ok := dict["nested"].(map[string]interface{})
	if !ok {
		t.Fatalf("dict[\"nested\"] expected map, got %T", dict["nested"])
	}
	if nested["key"] != 1 {
		t.Errorf("nested[\"key\"] = %v, want 1", nested["key"])
	}
}

func TestDecodeInvalidIdentifier(t *testing.T) {
	_, err := Decode([]byte("x"))
	if err == nil {
		t.Fatal("expected error for invalid identifier")
	}
}
