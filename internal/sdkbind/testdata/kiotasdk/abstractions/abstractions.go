// Package abstractions is the stand-in for kiota's abstractions module: the
// generic request configuration a verb takes, whose type argument is the
// verb's query-parameter struct.
package abstractions

// RequestConfiguration carries the per-request options of one verb.
type RequestConfiguration[T any] struct {
	QueryParameters *T
}
