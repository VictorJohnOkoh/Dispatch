package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/vendors"
)

// Passthrough is the Harness with no agency: it sends a Prompt straight to the
// Vendor and turns the answer into Events. It is modelled as a Harness rather than
// as a separate path, and it implements the interface with no special case. It
// never calls Spawn, so no process exists, and it never calls Files or any of the
// four tool methods on the Sink.
type Passthrough struct {
	client *http.Client
}

// NewPassthrough makes the one Adapter that spawns nothing. A nil RoundTripper uses
// http.DefaultTransport; tests pass one that replays recorded bodies.
func NewPassthrough(rt http.RoundTripper) *Passthrough {
	return &Passthrough{client: &http.Client{Transport: rt}}
}

// Capabilities is the zero value on purpose. No tools means no Approval Policy, and
// every Gate false means every slot is forced to RuleAuto.
func (p *Passthrough) Capabilities() Capabilities { return Capabilities{} }

// Start brings up a Session that has nothing to bring up. There is no process and
// no handshake, so it returns a Run and dials nothing.
//
// ADR 0006 has an Adapter confirm the Model before Start returns. That rule is
// about reading back what a Harness says it selected, and there is no Harness here:
// every request names spec.Model, so the selection cannot drift. The Model the
// Vendor does not have is caught by the Daemon's own vendors.Load during Starting.
func (p *Passthrough) Start(ctx context.Context, spec SessionSpec, out Sink) (Run, error) {
	if out == nil {
		return nil, errors.New("passthrough: no Sink")
	}
	return &ptRun{pt: p, session: ctx, spec: spec, out: out}, nil
}

type ptRun struct {
	pt      *Passthrough
	session context.Context
	spec    SessionSpec
	out     Sink

	mu          sync.Mutex
	history     []chatMessage
	cancel      context.CancelFunc // cancels the Prompt in flight, nil when none is
	done        chan struct{}      // closed when the reader stops, nil when none runs
	interrupted bool               // this Prompt was abandoned and the Session kept
	closed      bool               // the Session is ending, so the Prompt is not bounded
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Prompt posts the whole conversation and returns once the Vendor has answered with
// a body. Everything after that arrives on the Sink.
func (r *ptRun) Prompt(ctx context.Context, text string) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return errors.New("passthrough: the Session is closed")
	}
	if r.done != nil {
		r.mu.Unlock()
		return errors.New("passthrough: a Prompt is already in flight")
	}
	messages := append(slices.Clone(r.history), chatMessage{Role: "user", Content: text})
	r.mu.Unlock()

	body, err := json.Marshal(chatRequest{
		Model:         r.spec.Model,
		Messages:      messages,
		Stream:        true,
		StreamOptions: streamOptions{IncludeUsage: true},
	})
	if err != nil {
		return fmt.Errorf("passthrough: %w", err)
	}

	// The stream outlives this call, so it hangs off the Session's context. ctx
	// bounds only the wait for the Vendor's answer.
	reqCtx, cancel := context.WithCancel(r.session)
	stopWatch := context.AfterFunc(ctx, cancel)
	resp, err := r.pt.post(reqCtx, r.spec.Vendor, "/chat/completions", body)
	stopWatch()
	if err != nil {
		cancel()
		return err
	}

	done := make(chan struct{})
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		cancel()
		resp.Body.Close()
		return errors.New("passthrough: the Session is closed")
	}
	r.history = messages
	r.cancel, r.done, r.interrupted = cancel, done, false
	r.mu.Unlock()

	go r.read(resp.Body, cancel, done)
	return nil
}

// Interrupt cancels the request and returns once the reader has stopped, so the
// Session is ready for the next Prompt the moment it returns.
func (r *ptRun) Interrupt(ctx context.Context) error {
	r.mu.Lock()
	cancel, done := r.cancel, r.done
	if done == nil {
		r.mu.Unlock()
		return nil
	}
	r.interrupted = true
	r.mu.Unlock()

	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close ends the Session. A Prompt still in flight is not bounded and its message
// is left torn, because ADR 0008 gives a stopped Session no PromptCompleted and no
// Error: the user asked for this, so it is not a fault and not a finish.
func (r *ptRun) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	cancel, done := r.cancel, r.done
	r.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	return nil
}

// read turns the Vendor's stream into Sink calls. It is the one goroutine a Run
// owns, and Close joins it.
func (r *ptRun) read(body io.ReadCloser, cancel context.CancelFunc, done chan struct{}) {
	defer close(done)
	defer cancel()
	defer body.Close()

	var (
		reply strings.Builder
		open  event.Kind // the appendable Event this reader has open, or ""
	)
	// A message is left torn only where the Session is ending, because the Daemon
	// closes those.
	closeThis := func() { open = closeOpen(r.out, open) }

	vendors.ReadStream(body, func(f vendors.Frame) {
		switch f.Kind {
		case vendors.FrameText:
			r.out.Message(f.Text, false)
			reply.WriteString(f.Text)
			open = event.KindAssistantMessage

		case vendors.FrameReasoning:
			r.out.Reasoning(f.Text, false)
			open = event.KindReasoning

		case vendors.FrameError:
			// Error is never terminal, so the Session stays usable, which it can
			// only be once this Prompt is bounded.
			closeThis()
			r.out.Failed(event.ErrVendor, f.Text)
			r.out.Completed(event.StopError, event.Usage{})

		case vendors.FrameTruncated:
			// Three ways a stream stops short, and they are three different facts.
			switch interrupted, closed := r.ending(); {
			case closed:
				// A stop. No Error and no PromptCompleted, per ADR 0008.
			case interrupted:
				closeThis()
				r.out.Completed(event.StopInterrupted, event.Usage{})
			default:
				// The Daemon writes SessionEnded{failed} after this and closes
				// the torn message there.
				r.out.Failed(event.ErrStreamTruncated, "the stream ended mid-message")
			}

		case vendors.FrameEnd:
			closeThis()
			r.out.Completed(event.StopReason(f.Stop), event.Usage(f.Usage))
		}
	})

	r.mu.Lock()
	if reply.Len() > 0 {
		r.history = append(r.history, chatMessage{Role: "assistant", Content: reply.String()})
	}
	r.cancel, r.done = nil, nil
	r.mu.Unlock()
}

func (r *ptRun) ending() (interrupted, closed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.interrupted, r.closed
}

type chatRequest struct {
	Model         string        `json:"model"`
	Messages      []chatMessage `json:"messages"`
	Stream        bool          `json:"stream"`
	StreamOptions streamOptions `json:"stream_options"`
}

// streamOptions is how an OpenAI-compatible Vendor is asked to put the token usage
// in the stream. Without it a streamed Prompt completes with no usage at all.
type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// post sends one completion on the Vendor's OpenAI-compatible surface, which all
// three Vendors serve at Base + "/v1". The body is the caller's to close.
func (p *Passthrough) post(ctx context.Context, e vendors.Endpoint, path string, payload []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.Base+"/v1"+path, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("passthrough: %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.Token != "" {
		req.Header.Set("Authorization", "Bearer "+e.Token)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("passthrough: %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("passthrough: %s: HTTP %d: %s", path, resp.StatusCode, refusalText(body))
	}
	return resp, nil
}

// refusalText is the Vendor's own words, which sit under an error key that is
// sometimes a sentence and sometimes an object.
func refusalText(body []byte) string {
	var framed struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &framed) == nil && len(framed.Error) > 0 {
		var sentence string
		if json.Unmarshal(framed.Error, &sentence) == nil {
			return sentence
		}
		return string(framed.Error)
	}
	return strings.TrimSpace(string(body))
}
