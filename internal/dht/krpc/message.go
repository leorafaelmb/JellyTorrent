package krpc

import (
	"fmt"

	"github.com/leorafaelmb/JellyTorrent/internal/bencode"
)

type Message struct {
	TransactionID string
	Type          string
	QueryMethod   string
	Args          map[string]any
	Response      map[string]any
	Error         []any
}

func Unmarshal(data []byte) (*Message, error) {
	decoded, err := bencode.Decode(data)
	if err != nil {
		return nil, err
	}

	dict, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("expected dictionary, got %T", decoded)
	}

	msg := &Message{}

	t, ok := dict["t"]
	if !ok {
		return nil, fmt.Errorf("missing transaction ID (t)")
	}
	msg.TransactionID, err = toString(t)
	if err != nil {
		return nil, fmt.Errorf("invalid transaction ID: %w", err)
	}

	y, ok := dict["y"]
	if !ok {
		return nil, fmt.Errorf("missing message type (y)")
	}
	msg.Type, err = toString(y)
	if err != nil {
		return nil, fmt.Errorf("invalid message type: %w", err)
	}

	switch msg.Type {
	case "q":
		q, ok := dict["q"]
		if !ok {
			return nil, fmt.Errorf("missing query method (q)")
		}
		msg.QueryMethod, err = toString(q)
		if err != nil {
			return nil, fmt.Errorf("invalid query method: %w", err)
		}

		a, ok := dict["a"]
		if !ok {
			return nil, fmt.Errorf("missing arguments (a)")
		}
		msg.Args, ok = a.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("arguments (a) is not a dictionary")
		}

	case "r":
		r, ok := dict["r"]
		if !ok {
			return nil, fmt.Errorf("missing response (r)")
		}
		msg.Response, ok = r.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("response (r) is not a dictionary")
		}

	case "e":
		e, ok := dict["e"]
		if !ok {
			return nil, fmt.Errorf("missing error (e)")
		}
		msg.Error, ok = e.([]interface{})
		if !ok {
			return nil, fmt.Errorf("error (e) is not a list")
		}

	default:
		return nil, fmt.Errorf("unknown message type: %s", msg.Type)
	}

	return msg, nil
}

func Marshal(msg *Message) ([]byte, error) {
	dict := map[string]interface{}{
		"t": msg.TransactionID,
		"y": msg.Type,
	}

	switch msg.Type {
	case "q":
		dict["q"] = msg.QueryMethod
		dict["a"] = msg.Args

	case "r":
		dict["r"] = msg.Response

	case "e":
		dict["e"] = msg.Error
	}

	return bencode.Encode(dict)
}

// toString handles both string and []byte values from bencode decoding.
func toString(v interface{}) (string, error) {
	switch val := v.(type) {
	case string:
		return val, nil
	case []byte:
		return string(val), nil
	default:
		return "", fmt.Errorf("expected string or []byte, got %T", v)
	}
}
