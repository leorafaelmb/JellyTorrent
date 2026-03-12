package bencode

import "fmt"

type DecodeError struct {
	Position int
	Reason   string
	Context  string
}

func (e *DecodeError) Error() string {
	return fmt.Sprintf("bencode decode error at position %d: %s (context %s)",
		e.Position, e.Reason, e.Context)
}

type EncodeError struct {
	Reason string
}

func (e *EncodeError) Error() string {
	return fmt.Sprintf("bencode encode error: %s", e.Reason)
}
