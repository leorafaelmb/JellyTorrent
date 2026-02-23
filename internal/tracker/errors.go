package tracker

import (
	"errors"
	"fmt"
)

// TrackerError is returned when the remote tracker reports a failure
// (e.g. HTTP failure reason, UDP action=3 error response).
type TrackerError struct {
	Message string
}

func (e *TrackerError) Error() string {
	return fmt.Sprintf("tracker error: %s", e.Message)
}

// ResponseError is returned when the tracker response is malformed or unexpected.
type ResponseError struct {
	Context string
	Reason  string
}

func (e *ResponseError) Error() string {
	return fmt.Sprintf("%s: %s", e.Context, e.Reason)
}

var ErrUnsupportedScheme = errors.New("unsupported tracker URL scheme")

var ErrMaxRetriesExceeded = errors.New("tracker request failed: max retries exceeded")
