package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/pijalu/agent-mcp/client"
	"github.com/pijalu/agent-mcp/internal/output"
)

// textRenderer writes one command's payload for a human. verbose marks the
// --verbose request: the default rendering prints the result body, and the
// envelope (analysis profile, limitations, transport fields) stays hidden. A
// renderer must never emit protocol bookkeeping (the dotted `_meta.` or
// `inputSchema.` paths of the generic flatten): a human reads a name and a
// description, not a schema dump.
type textRenderer func(w io.Writer, payload any, verbose bool) error

// textRenderers maps a client command to its human renderer. A command without
// an entry keeps the flattened key: value dump of output.WriteText, which
// remains the contract for the machine-shaped server and config results.
var textRenderers = map[string]textRenderer{
	"list-servers":   discoveryRenderer("servers", "server", serverFields),
	"list-tools":     discoveryRenderer("tools", "tool", nameDescription),
	"list-resources": discoveryRenderer("resources", "resource", resourceFields),
	"list-prompts":   discoveryRenderer("prompts", "prompt", nameDescription),
	"call-tool":      renderCallTool,
	"read-resource":  renderReadResource,
	"get-prompt":     renderGetPrompt,
}

// renderText renders a client command payload in the text format. Commands
// without a human renderer fall back to the flattened key: value dump.
func renderText(w io.Writer, command string, payload any, verbose bool) error {
	if render, ok := textRenderers[command]; ok {
		return render(w, payload, verbose)
	}
	return output.WriteText(w, payload)
}

// textRow is one line of a list rendering: an item's name and its one-line
// description.
type textRow struct {
	label  string
	detail string
}

// fields describes one discovery item as a name and a description.
type fields func(item map[string]any) (string, string)

// discoveryRenderer renders a list command: one line per item, then a footer
// when the payload reports truncation or another page. key names the payload
// array; describe extracts each item's name and description.
func discoveryRenderer(key, noun string, describe fields) textRenderer {
	return func(w io.Writer, payload any, _ bool) error {
		items, object := discoveryItems(payload, key)
		if items == nil && object == nil {
			return output.WriteText(w, payload)
		}
		rows := make([]textRow, 0, len(items))
		for _, item := range items {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			label, detail := describe(entry)
			rows = append(rows, textRow{label: label, detail: detail})
		}
		if err := writeRows(w, rows); err != nil {
			return err
		}
		return writeListFooter(w, object, noun, len(rows))
	}
}

// discoveryItems extracts the item array of a discovery payload and its
// enclosing object. A bare array (the --raw list-servers shape) has no object.
func discoveryItems(payload any, key string) ([]any, map[string]any) {
	switch typed := payload.(type) {
	case []any:
		return typed, nil
	case map[string]any:
		raw, ok := typed[key]
		if !ok {
			return nil, nil
		}
		items, _ := raw.([]any)
		return items, typed
	default:
		return nil, nil
	}
}

// nameDescription reads the conventional name and description fields of a
// tool, prompt, or resource entry.
func nameDescription(item map[string]any) (string, string) {
	return stringValue(item, "name"), oneLine(stringValue(item, "description"))
}

// serverFields renders one server as its name and its session state.
func serverFields(item map[string]any) (string, string) {
	detail := stringValue(item, "state")
	if detail == "" {
		detail = "unknown"
	}
	if n := intValue(item["active_sessions"]); n > 0 {
		detail = fmt.Sprintf("%s, %d session%s", detail, n, pluralSuffix(n))
	}
	if last := stringValue(item, "last_error"); last != "" {
		detail = fmt.Sprintf("%s (%s)", detail, oneLine(last))
	}
	return stringValue(item, "name"), detail
}

// resourceFields renders one resource as its name and its description, keeping
// the URI because it is what read-resource consumes.
func resourceFields(item map[string]any) (string, string) {
	uri := stringValue(item, "uri")
	label := stringValue(item, "name")
	if label == "" {
		label = uri
	}
	parts := make([]string, 0, 2)
	if description := oneLine(stringValue(item, "description")); description != "" {
		parts = append(parts, description)
	}
	if uri != "" && uri != label {
		parts = append(parts, uri)
	}
	return label, strings.Join(parts, "  ")
}

// writeRows writes each row as "<name>  <description>", aligning the second
// column so a human can scan a list.
func writeRows(w io.Writer, rows []textRow) error {
	width := 0
	for _, row := range rows {
		if len(row.label) > width {
			width = len(row.label)
		}
	}
	for _, row := range rows {
		line := row.label
		if row.detail != "" {
			line = fmt.Sprintf("%-*s  %s", width, row.label, row.detail)
		}
		if err := writeLine(w, line); err != nil {
			return err
		}
	}
	return nil
}

// writeListFooter reports an empty list, a truncated one, or a further page.
func writeListFooter(w io.Writer, object map[string]any, noun string, shown int) error {
	if shown == 0 {
		return writeLine(w, fmt.Sprintf("(no %s)", pluralNoun(noun, 0)))
	}
	if object == nil {
		return nil
	}
	if truncated, _ := object["truncated"].(bool); truncated {
		total := intValue(object["total"])
		line := fmt.Sprintf("%d of %d %s (truncated; raise --limit)", shown, total, pluralNoun(noun, total))
		if err := writeLine(w, line); err != nil {
			return err
		}
	}
	if cursor := stringValue(object, "nextCursor"); cursor != "" {
		return writeLine(w, fmt.Sprintf("next page: --cursor %s", cursor))
	}
	return nil
}

// renderCallTool prints a tool result's content text, marking non-text parts
// instead of dumping their base64 data. An Evidence-shaped result (a
// normalized_result body under structuredContent) prints the body only — the
// analysis_profile and limitations envelope is boilerplate a human does not
// read on every call — and --verbose restores the complete envelope.
func renderCallTool(w io.Writer, payload any, verbose bool) error {
	object, ok := payload.(map[string]any)
	if !ok {
		return output.WriteText(w, payload)
	}
	if !verbose {
		if body := resultBody(object); body != nil {
			return writeLine(w, prettyJSON(body))
		}
	}
	lines := contentLines(asArray(object["content"]))
	if len(lines) == 0 {
		if structured := object["structuredContent"]; structured != nil {
			lines = append(lines, prettyJSON(structured))
		}
	}
	return writeLines(w, lines, "(no content)")
}

// resultBody extracts the normalized_result field of an Evidence-shaped
// structured payload, returning nil for any other shape.
func resultBody(object map[string]any) any {
	structured, _ := object["structuredContent"].(map[string]any)
	if structured == nil {
		return nil
	}
	body, present := structured["normalized_result"]
	if !present || body == nil {
		return nil
	}
	return body
}

// renderReadResource prints the text of each resource content, marking binary
// parts instead of dumping their base64 data.
func renderReadResource(w io.Writer, payload any, _ bool) error {
	object, ok := payload.(map[string]any)
	if !ok {
		return output.WriteText(w, payload)
	}
	var lines []string
	for _, item := range asArray(object["contents"]) {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if text := stringValue(entry, "text"); text != "" {
			lines = append(lines, resourceTextLine(entry, text))
			continue
		}
		if blob, ok := entry["blob"].(string); ok && strings.HasPrefix(blob, "data:") {
			lines = append(lines, dataURLSummary(stringValue(entry, "mimeType"), blob))
			continue
		}
		lines = append(lines, fmt.Sprintf("[binary %s: %s]", stringValue(entry, "mimeType"), stringValue(entry, "uri")))
	}
	return writeLines(w, lines, "(no contents)")
}

// dataURLSummary renders the one-line marker for an inline data: URL:
// printing the URL itself would be the same context bomb the policy exists
// to prevent, so text output keeps a mime + size summary.
func dataURLSummary(mimeType, payload string) string {
	return fmt.Sprintf("[binary: %s dataurl (%s)]", mimeType, client.HumanSize(dataURLSize(payload)))
}

// resourceTextLine renders one resource content's text, collapsing inline
// data: URLs (the dataurl policy applied to a blob) to a summary marker.
func resourceTextLine(entry map[string]any, text string) string {
	if strings.HasPrefix(text, "data:") {
		return dataURLSummary(stringValue(entry, "mimeType"), text)
	}
	return text
}

// renderGetPrompt prints each prompt message as its role and content text.
func renderGetPrompt(w io.Writer, payload any, _ bool) error {
	object, ok := payload.(map[string]any)
	if !ok {
		return output.WriteText(w, payload)
	}
	var lines []string
	for _, item := range asArray(object["messages"]) {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		role := stringValue(entry, "role")
		content, _ := entry["content"].(map[string]any)
		for _, line := range contentLines([]any{content}) {
			if role != "" {
				line = role + ": " + line
			}
			lines = append(lines, line)
		}
	}
	return writeLines(w, lines, "(no messages)")
}

// contentLines renders MCP content items as human lines: text verbatim, and a
// bracketed marker for anything a terminal cannot show.
func contentLines(items []any) []string {
	var lines []string
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		lines = append(lines, contentLine(entry))
	}
	return lines
}

// contentLine renders one MCP content item: text verbatim, binary payloads
// as one-line markers (the policy layer already rewrote stored parts to
// text), and a bracketed fallback for anything else a terminal cannot show.
func contentLine(entry map[string]any) string {
	contentType := stringValue(entry, "type")
	if text := stringValue(entry, "text"); text != "" {
		return text
	}
	switch contentType {
	case "image", "audio":
		return inlineBinaryLine(entry, contentType)
	case "resource":
		resource, _ := entry["resource"].(map[string]any)
		if text := stringValue(resource, "text"); text != "" {
			return text
		}
		return fmt.Sprintf("[resource: %s]", stringValue(resource, "uri"))
	case "resource_link":
		return fmt.Sprintf("[resource: %s]", stringValue(entry, "uri"))
	}
	if uri := stringValue(entry, "uri"); uri != "" {
		return fmt.Sprintf("[%s: %s]", contentType, uri)
	}
	return fmt.Sprintf("[%s]", contentType)
}

// inlineBinaryLine renders an image/audio part that still carries its
// payload inline (keep or dataurl policy): the data-URL form gets a size so
// the line stays a summary, the raw form keeps its legacy marker.
func inlineBinaryLine(entry map[string]any, contentType string) string {
	mimeType := stringValue(entry, "mimeType")
	if data, ok := entry["data"].(string); ok && strings.HasPrefix(data, "data:") {
		return dataURLSummary(mimeType, data)
	}
	return fmt.Sprintf("[%s: %s]", contentType, mimeType)
}

// writeLines writes lines, or fallback when there are none.
func writeLines(w io.Writer, lines []string, fallback string) error {
	if len(lines) == 0 {
		lines = []string{fallback}
	}
	for _, line := range lines {
		if err := writeLine(w, line); err != nil {
			return err
		}
	}
	return nil
}

// writeLine writes one rendered text line.
func writeLine(w io.Writer, line string) error {
	if _, err := fmt.Fprintln(w, line); err != nil {
		return fmt.Errorf("%w: write text: %v", output.ErrInternal, err)
	}
	return nil
}

// asArray returns the items of a decoded JSON array, or nil.
func asArray(value any) []any {
	items, _ := value.([]any)
	return items
}

// stringValue reads a string field, tolerating a missing or non-string value.
func stringValue(object map[string]any, key string) string {
	if object == nil {
		return ""
	}
	text, _ := object[key].(string)
	return text
}

// intValue reads a numeric field, tolerating a missing or non-numeric value.
func intValue(value any) int {
	switch number := value.(type) {
	case float64:
		return int(number)
	case int:
		return number
	case json.Number:
		parsed, err := number.Int64()
		if err != nil {
			return 0
		}
		return int(parsed)
	default:
		return 0
	}
}

// oneLine collapses whitespace so a multi-line description stays on one row.
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// pluralSuffix returns the English plural marker for n.
func pluralSuffix(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// pluralNoun pluralizes noun for n.
func pluralNoun(noun string, n int) string {
	return noun + pluralSuffix(n)
}

// prettyJSON indents a structured value for a human, falling back to its
// default rendering if it cannot be marshalled.
func prettyJSON(value any) string {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(raw)
}
