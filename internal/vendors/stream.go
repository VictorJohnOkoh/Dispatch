package vendors

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

// Frame is one thing read out of a Vendor's stream. It is the vendors package's
// own type rather than an Event, which is what keeps this package a leaf.
type Frame struct {
	Kind  FrameKind
	Text  string // the text, or for FrameError the Vendor's own words
	Stop  string // FrameEnd only
	Usage Usage  // FrameEnd only
}

type FrameKind uint8

const (
	FrameText FrameKind = iota
	FrameReasoning
	FrameError     // the Vendor refused or failed
	FrameTruncated // the body ended with no terminator and no finish reason
	FrameEnd
)

// Usage is the token count one Vendor reported for one Prompt.
type Usage struct {
	Input     int
	Output    int
	CacheRead int
	Total     int
}

// chunk is the part of an OpenAI-compatible streaming body this reader looks at.
// Every other field is ignored, which is what lets one reader serve three Vendors.
type chunk struct {
	Error   json.RawMessage `json:"error"`
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			Reasoning        string `json:"reasoning"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
		PromptDetails    struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

// maxLine bounds one SSE line. A whole assistant message can arrive on one line
// when a Vendor batches, and bufio's own 64 KiB default is too small for that.
const maxLine = 1 << 20

// ReadStream consumes one OpenAI-compatible SSE body and yields Frames until the
// stream ends. It takes no Kind, because none of its rules is Vendor specific and
// none can misfire on a well-formed stream. ADR 0007 states the five rules.
func ReadStream(r io.Reader, out func(Frame)) {
	var (
		stop  string
		usage Usage
		done  bool
	)

	lines := bufio.NewScanner(r)
	lines.Buffer(nil, maxLine)

	for lines.Scan() {
		line := strings.TrimSpace(lines.Text())

		// Rule 1: a comment is a keep-alive ping, not a frame.
		// Rule 3: a line with no data: prefix is still read, because Ollama writes
		// its error object unframed.
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		line = strings.TrimPrefix(line, "data:")
		line = strings.TrimSpace(line)

		if line == "[DONE]" {
			done = true
			break
		}

		// A line this reader cannot parse is skipped rather than refused. The
		// rules above read the fields they need and ignore every other one, which
		// is what lets one reader serve three Vendors and their extensions.
		var c chunk
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			continue
		}

		// Rule 2: an error key, framed or not, ends the stream.
		if len(c.Error) > 0 && !bytes.Equal(c.Error, []byte("null")) {
			out(Frame{Kind: FrameError, Text: errorText(c.Error)})
			return
		}

		if c.Usage != nil {
			usage = Usage{
				Input:     c.Usage.PromptTokens,
				Output:    c.Usage.CompletionTokens,
				CacheRead: c.Usage.PromptDetails.CachedTokens,
				Total:     c.Usage.TotalTokens,
			}
		}

		for _, choice := range c.Choices {
			// Rule 4: reasoning arrives under either name, whichever is present.
			text := choice.Delta.Reasoning
			if text == "" {
				text = choice.Delta.ReasoningContent
			}
			if text != "" {
				out(Frame{Kind: FrameReasoning, Text: text})
			}
			if choice.Delta.Content != "" {
				out(Frame{Kind: FrameText, Text: choice.Delta.Content})
			}
			if choice.FinishReason != "" {
				stop = choice.FinishReason
			}
		}
	}

	// Rule 5: no terminator and no finish reason is a truncated body, which is the
	// same fact for a Client as a dropped connection. A read that failed part way
	// is the same fact again, whatever it had already seen.
	if lines.Err() != nil || (!done && stop == "") {
		out(Frame{Kind: FrameTruncated})
		return
	}
	out(Frame{Kind: FrameEnd, Stop: stop, Usage: usage})
}

// errorText is the Vendor's own words. Ollama and LM Studio put a plain sentence
// under the error key, and llama.cpp puts an object whose type values are outside
// the OpenAI vocabulary, so the object travels whole rather than losing them.
func errorText(raw json.RawMessage) string {
	var sentence string
	if json.Unmarshal(raw, &sentence) == nil {
		return sentence
	}
	return string(raw)
}
