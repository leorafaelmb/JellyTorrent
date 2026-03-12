package bencode

import (
	"fmt"
	"sort"
	"strconv"
)

// Encode encodes a Go value into bencoded bytes.
// Supported types: string, []byte, int, []interface{}, map[string]interface{}.
func Encode(value interface{}) ([]byte, error) {
	switch v := value.(type) {
	case string:
		return encodeString([]byte(v)), nil
	case []byte:
		return encodeString(v), nil
	case int:
		return encodeInt(v), nil
	case []interface{}:
		return encodeList(v)
	case map[string]interface{}:
		return encodeDict(v)
	default:
		return nil, &EncodeError{
			Reason: fmt.Sprintf("unsupported type: %T", value),
		}
	}
}

// encodeString encodes bytes as a bencoded string: <length>:<contents>
func encodeString(data []byte) []byte {
	prefix := strconv.Itoa(len(data)) + ":"
	result := make([]byte, len(prefix)+len(data))
	copy(result, prefix)
	copy(result[len(prefix):], data)
	return result
}

// encodeInt encodes an integer as bencoded: i<number>e
func encodeInt(n int) []byte {
	return []byte("i" + strconv.Itoa(n) + "e")
}

// encodeList encodes a slice as a bencoded list: l<items>e
func encodeList(list []interface{}) ([]byte, error) {
	result := []byte("l")
	for _, item := range list {
		encoded, err := Encode(item)
		if err != nil {
			return nil, err
		}
		result = append(result, encoded...)
	}
	result = append(result, 'e')
	return result, nil
}

// encodeDict encodes a map as a bencoded dictionary: d<key><value>...e
// Keys are sorted in lexicographical order as required by the bencode spec.
func encodeDict(dict map[string]interface{}) ([]byte, error) {
	keys := make([]string, 0, len(dict))
	for k := range dict {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	result := []byte("d")
	for _, k := range keys {
		result = append(result, encodeString([]byte(k))...)
		encoded, err := Encode(dict[k])
		if err != nil {
			return nil, err
		}
		result = append(result, encoded...)
	}
	result = append(result, 'e')
	return result, nil
}
