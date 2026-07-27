package crud

import "errors"

// ErrNotYetReadable is returned when a resource did not become readable within
// the re-read budget.
//
// It is a sentinel rather than a formatted error so a caller can distinguish
// "the API is still catching up" from a genuine failure, and decide whether that
// is fatal for the operation in hand.
var ErrNotYetReadable = errors.New("resource did not become readable within the retry budget")
