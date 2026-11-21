package stream

import (
	"errors"
	"fmt"
)

// ErrDegenerateToolArguments reports a tool call whose streamed arguments
// stopped converging, typically an endless run of whitespace between JSON
// tokens. Such a response never finishes by itself, so the provider aborts it
// and the retry layer gets one fresh attempt.
var ErrDegenerateToolArguments = errors.New("model streamed degenerate tool call arguments")

// StreamIncompleteError reports a streaming response that ended before the
// protocol terminal marker arrived. This must be treated as a failed model
// response, not a successful completion with truncated output.
type StreamIncompleteError struct {
	Transport string
	Terminal  string
	Err       error
}

func (e *StreamIncompleteError) Error() string {
	transport := e.Transport
	if transport == "" {
		transport = "stream"
	}
	terminal := e.Terminal
	if terminal == "" {
		terminal = "terminal event"
	}
	msg := fmt.Sprintf("%s closed before %s", transport, terminal)
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *StreamIncompleteError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// ResponsesIncompleteError reports an explicit response.incomplete terminal
// event from the provider. Partial output and usage may have been emitted.
type ResponsesIncompleteError struct {
	Reason string
}

func (e *ResponsesIncompleteError) Error() string {
	if e == nil || e.Reason == "" {
		return "Responses API returned an incomplete response"
	}
	return fmt.Sprintf("Responses API returned an incomplete response: %s", e.Reason)
}

// DebugFields preserves explicit incomplete reasons in structured diagnostics.
func (e *ResponsesIncompleteError) DebugFields() map[string]any {
	reason := e.Reason
	if reason == "" {
		reason = "incomplete"
	}
	return map[string]any{"termination_reason": reason}
}
