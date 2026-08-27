package output

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Sentinel error kinds mapped to exit codes by ExitCode.
var (
	// ErrUsage marks bad flags, bad config, or malformed input (exit 2).
	ErrUsage = errors.New("usage error")
	// ErrMCP marks MCP-level failures: tool errors, unreachable servers (exit 1).
	ErrMCP = errors.New("mcp error")
	// ErrInternal marks bugs and unexpected failures (exit 3).
	ErrInternal = errors.New("internal error")
)

// version is the output-contract schema version. Version 1 is the first published
// contract: concise default payloads (MCP transport metadata stripped, list totals
// reported) with the --raw escape hatch.
const version = 1

// envelope is the single JSON object written per invocation.
type envelope struct {
	Version int          `json:"version"`
	Data    any          `json:"data,omitempty"`
	Error   *errorObject `json:"error,omitempty"`
}

// errorObject is the failure payload of the envelope.
type errorObject struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// WriteJSON writes v wrapped in the versioned success envelope to w.
func WriteJSON(w io.Writer, v any) error {
	return encode(w, envelope{Version: version, Data: v})
}

// WriteError writes err wrapped in the versioned error envelope to w.
func WriteError(w io.Writer, err error) error {
	return encode(w, envelope{Version: version, Error: errorObjectOf(err)})
}

// WriteErrorFormat writes err in f's rendering: the JSON error envelope for
// agents, or flattened key: value lines for humans. Failures honour --format so
// a text consumer never has to parse JSON to read an error.
func WriteErrorFormat(w io.Writer, err error, f Format) error {
	if f != FormatText {
		return WriteError(w, err)
	}
	return WriteText(w, map[string]any{"error": errorPayload(err)})
}

// errorObjectOf builds the error payload of the envelope.
func errorObjectOf(err error) *errorObject {
	return &errorObject{Code: errorCode(err), Message: err.Error()}
}

// errorPayload renders the error payload for the text writer.
func errorPayload(err error) map[string]any {
	obj := errorObjectOf(err)
	return map[string]any{"code": obj.Code, "message": obj.Message}
}

// encode marshals env as a single-line JSON object followed by a newline.
func encode(w io.Writer, env envelope) error {
	b, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("%w: marshal envelope: %v", ErrInternal, err)
	}
	if _, err := fmt.Fprintf(w, "%s\n", b); err != nil {
		return fmt.Errorf("%w: write envelope: %v", ErrInternal, err)
	}
	return nil
}

// errorCode classifies err by sentinel kind.
func errorCode(err error) string {
	switch {
	case errors.Is(err, ErrUsage):
		return "usage"
	case errors.Is(err, ErrMCP):
		return "mcp"
	default:
		return "internal"
	}
}

// ExitCode maps err to the process exit code: 0 ok, 1 MCP, 2 usage, 3 internal.
func ExitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrUsage):
		return 2
	case errors.Is(err, ErrMCP):
		return 1
	default:
		return 3
	}
}
