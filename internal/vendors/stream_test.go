package vendors

import (
	"reflect"
	"strings"
	"testing"
)

func read(t *testing.T, body string) []Frame {
	t.Helper()
	var got []Frame
	ReadStream(strings.NewReader(body), func(f Frame) { got = append(got, f) })
	return got
}

func wantFrames(t *testing.T, body string, frames ...Frame) {
	t.Helper()
	if got := read(t, body); !reflect.DeepEqual(got, frames) {
		t.Errorf("\n got %+v\nwant %+v", got, frames)
	}
}

// Rule 1. llama.cpp writes ":\n\n" as a keep-alive when no token has been produced.
func TestCommentLinesAreIgnored(t *testing.T) {
	wantFrames(t, ":\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n",
		Frame{Kind: FrameText, Text: "hi"},
		Frame{Kind: FrameEnd},
	)
}

// Rule 2, framed. llama.cpp is the one Vendor that frames its error properly.
func TestFramedErrorIsAnErrorFrame(t *testing.T) {
	wantFrames(t, "data: {\"error\":{\"type\":\"exceed_context_size_error\",\"message\":\"too long\"}}\n\n",
		Frame{Kind: FrameError, Text: `{"type":"exceed_context_size_error","message":"too long"}`},
	)
}

// Rules 2 and 3 together. Ollama writes the error object with no data: prefix and
// then sends no terminator, so a reader that needs the prefix never reaches rule 2.
func TestUnframedErrorIsStillAnErrorFrame(t *testing.T) {
	wantFrames(t, "{\"error\":\"model not found\"}\n",
		Frame{Kind: FrameError, Text: "model not found"},
	)
}

// Rule 4. The two reasoning field names are disjoint and no Vendor sends both.
func TestReasoningIsReadFromEitherName(t *testing.T) {
	for _, field := range []string{"reasoning", "reasoning_content"} {
		wantFrames(t, "data: {\"choices\":[{\"delta\":{\""+field+"\":\"hmm\"}}]}\n\ndata: [DONE]\n\n",
			Frame{Kind: FrameReasoning, Text: "hmm"},
			Frame{Kind: FrameEnd},
		)
	}
}

// Rule 5. No [DONE] and no finish reason is the same fact as a dropped connection.
func TestNoTerminatorIsTruncated(t *testing.T) {
	wantFrames(t, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n",
		Frame{Kind: FrameText, Text: "hi"},
		Frame{Kind: FrameTruncated},
	)
}

// A finish reason ends the stream even with no [DONE] after it.
func TestFinishReasonEndsTheStream(t *testing.T) {
	wantFrames(t, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n",
		Frame{Kind: FrameEnd, Stop: "stop"},
	)
}

// Ollama splits one emission into two chunks, sends usage in a chunk of its own
// after the finish reason, and uses finish reasons outside the OpenAI set. All
// three are absorbed by emitting text per chunk and passing Stop through as a
// string.
func TestOllamaDivergencesNeedNoRule(t *testing.T) {
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"un\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"load\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"unload\"}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":3,\"total_tokens\":15}}\n\n" +
		"data: [DONE]\n\n"

	wantFrames(t, body,
		Frame{Kind: FrameText, Text: "un"},
		Frame{Kind: FrameText, Text: "load"},
		Frame{Kind: FrameEnd, Stop: "unload", Usage: Usage{Input: 12, Output: 3, Total: 15}},
	)
}

// Cached prompt tokens are reported where the OpenAI surface puts them.
func TestCachedTokensAreRead(t *testing.T) {
	body := "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]," +
		"\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":4,\"total_tokens\":104," +
		"\"prompt_tokens_details\":{\"cached_tokens\":90}}}\n\ndata: [DONE]\n\n"

	wantFrames(t, body,
		Frame{Kind: FrameEnd, Stop: "stop", Usage: Usage{Input: 100, Output: 4, CacheRead: 90, Total: 104}},
	)
}

// An error closes the stream, so nothing after it is read.
func TestNothingIsReadAfterAnError(t *testing.T) {
	wantFrames(t, "data: {\"error\":\"gone\"}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n",
		Frame{Kind: FrameError, Text: "gone"},
	)
}

// [DONE] ends the stream, so a frame after it is not read.
func TestNothingIsReadAfterDone(t *testing.T) {
	wantFrames(t, "data: [DONE]\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"late\"}}]}\n\n",
		Frame{Kind: FrameEnd},
	)
}
