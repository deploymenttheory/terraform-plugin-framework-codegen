package quirkserver

import "fmt"

// stream is the free-form-conditional shape resource -- the ground truth for
// the executor's value-cycling. Unlike the monitor, whose every refusal begins
// `field <name> ...` in a grammar a parser keys on, the stream's conditional
// refusal is real-world vendor prose: it names the offending field and the
// value that clashed with it, but in a sentence classifyRefusal cannot match.
//
// The shape:
//
//   - `format` (required enum: avro, json, locked) is the discriminator.
//   - `mode` (required enum: batch, streaming) is value-conditional on it: each
//     format supports exactly one mode, and `locked` supports none.
//
// Because the executor synthesises the first enum value of every field, a
// minimal create carries mode=batch, which is wrong for a format whose mode is
// streaming. The API refuses it in prose, and the only route to an accepted
// body is to try the other mode -- the value-cycling this resource exists to
// exercise. The accepted-with-streaming / refused-with-batch pair the cycling
// records is the both-direction evidence the inference confirms a
// validConfiguration edge from; the `locked` branch, which no mode satisfies,
// is the exhausted-cycling case that must record an inconclusive edge rather
// than block the whole entity.
var (
	streamFormats = []string{"avro", "json", "locked"}
	streamModes   = []string{"batch", "streaming"}
)

// streamModeForFormat maps each format to the one delivery mode it supports.
// "locked" maps to the empty string -- no mode works, the deliberately
// unsatisfiable branch that makes cycling exhaust and record inconclusive.
var streamModeForFormat = map[string]string{
	"avro":   "streaming",
	"json":   "batch",
	"locked": "",
}

// validateStream reports the first problem it finds. The required-field and
// bad-enum errors keep the clean `field <name> ...` grammar the monitor
// established -- they are not the interesting part. The value-conditional
// refusal deliberately breaks that grammar: `"mode: streaming is not supported
// for the json format"` names `mode` (and the clashing format) in prose the
// classifier's four clauses cannot parse. Recovering `mode` from it is the
// generalized field extraction the executor falls back on, and cycling `mode`
// against the fixed format is what turns the refusal into an accepted body.
func (s *Server) validateStream(body map[string]any) string {
	if _, ok := body["name"]; !ok {
		return "field name is required"
	}
	format, ok := body["format"]
	if !ok || fmt.Sprint(format) == "" {
		return "field format is required"
	}
	fs := fmt.Sprint(format)
	if !contains(streamFormats, fs) {
		return "field format must be one of avro, json, locked"
	}
	mode, ok := body["mode"]
	if !ok || fmt.Sprint(mode) == "" {
		return "field mode is required"
	}
	ms := fmt.Sprint(mode)
	if !contains(streamModes, ms) {
		return "field mode must be one of batch, streaming"
	}

	// The value-conditional rule, in free-form prose that names `mode` but not
	// in the parseable grammar. `locked` supports no mode; every other format
	// supports exactly one.
	want := streamModeForFormat[fs]
	if want == "" {
		return "mode: no delivery mode is available for the " + fs + " format"
	}
	if ms != want {
		return "mode: " + ms + " is not supported for the " + fs + " format"
	}
	return ""
}
