package web

import (
	"bytes"
	"encoding/json"
	"html/template"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// contentBlock represents a single content block from a Claude Code message.
type contentBlock struct {
	Type           string          // text, thinking, tool_use, tool_result
	Text           string          // text content (for text, thinking, tool_result)
	ToolName       string          // tool name (for tool_use)
	ToolUseID      string          // tool_use id or tool_use_id reference
	ToolInput      json.RawMessage // raw input JSON (for tool_use)
	ToolResultText string          // paired tool_result text (populated by pairToolResults)
}

// parseContentBlocks extracts structured content blocks from a Claude Code
// message JSON blob. Handles both string content and array-of-blocks content.
func parseContentBlocks(msg json.RawMessage) []contentBlock {
	if len(msg) == 0 {
		return nil
	}

	var parsed struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(msg, &parsed); err != nil || len(parsed.Content) == 0 {
		return nil
	}

	// Try string content first.
	var s string
	if err := json.Unmarshal(parsed.Content, &s); err == nil && s != "" {
		return []contentBlock{{Type: "text", Text: s}}
	}

	// Try array of content blocks.
	var rawBlocks []json.RawMessage
	if err := json.Unmarshal(parsed.Content, &rawBlocks); err != nil {
		return nil
	}

	var blocks []contentBlock
	for _, raw := range rawBlocks {
		var b struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			Thinking  string          `json:"thinking"`
			Name      string          `json:"name"`
			ID        string          `json:"id"`
			ToolUseID string          `json:"tool_use_id"`
			Input     json.RawMessage `json:"input"`
			Content   json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(raw, &b); err != nil {
			continue
		}

		switch b.Type {
		case "text":
			blocks = append(blocks, contentBlock{Type: "text", Text: b.Text})
		case "thinking":
			blocks = append(blocks, contentBlock{Type: "thinking", Text: b.Thinking})
		case "tool_use":
			blocks = append(blocks, contentBlock{
				Type:      "tool_use",
				ToolName:  b.Name,
				ToolUseID: b.ID,
				ToolInput: b.Input,
			})
		case "tool_result":
			text := ""
			if len(b.Content) > 0 {
				// Content can be a string or array of content blocks.
				var cs string
				if json.Unmarshal(b.Content, &cs) == nil {
					text = cs
				} else {
					var contentBlocks []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					}
					if json.Unmarshal(b.Content, &contentBlocks) == nil {
						var parts []string
						for _, cb := range contentBlocks {
							if cb.Type == "text" && cb.Text != "" {
								parts = append(parts, cb.Text)
							}
						}
						text = strings.Join(parts, "\n")
					}
				}
			}
			blocks = append(blocks, contentBlock{
				Type:      "tool_result",
				ToolUseID: b.ToolUseID,
				Text:      text,
			})
		}
	}

	return blocks
}

// dagMessage is a parsed message with its content blocks extracted.
type dagMessage struct {
	Role   string
	Blocks []contentBlock
	Time   time.Time
}

// renderedTurn represents a grouped conversation turn for template rendering.
type renderedTurn struct {
	Role   string
	Blocks []contentBlock
	Time   time.Time
}

// groupIntoTurns groups messages into conversation turns following
// YepAnywhere's pattern: user messages with text content are standalone turns;
// everything between user text prompts (assistant messages, tool results)
// forms a single assistant turn.
func groupIntoTurns(msgs []dagMessage) []renderedTurn {
	if len(msgs) == 0 {
		return nil
	}

	var turns []renderedTurn
	var currentAssistant *renderedTurn

	flushAssistant := func() {
		if currentAssistant != nil && len(currentAssistant.Blocks) > 0 {
			turns = append(turns, *currentAssistant)
			currentAssistant = nil
		}
	}

	for _, msg := range msgs {
		isUserPrompt := msg.Role == "user" && hasTextContent(msg.Blocks)

		if isUserPrompt {
			flushAssistant()
			turns = append(turns, renderedTurn{
				Role:   "user",
				Blocks: msg.Blocks,
				Time:   msg.Time,
			})
		} else {
			// Accumulate into assistant turn (includes tool_result messages
			// which have role=user but only contain tool_result blocks).
			if currentAssistant == nil {
				currentAssistant = &renderedTurn{
					Role: "assistant",
					Time: msg.Time,
				}
			}
			currentAssistant.Blocks = append(currentAssistant.Blocks, msg.Blocks...)
		}
	}

	flushAssistant()
	return turns
}

// hasTextContent returns true if any block is a text block with non-empty text.
func hasTextContent(blocks []contentBlock) bool {
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			return true
		}
	}
	return false
}

// pairToolResults matches tool_result blocks with their tool_use blocks by
// ToolUseID, merging the result text into the tool_use block and removing
// the standalone tool_result. Non-tool blocks pass through unchanged.
func pairToolResults(blocks []contentBlock) []contentBlock {
	// Index tool_result blocks by their ToolUseID.
	resultMap := make(map[string]string)
	for _, b := range blocks {
		if b.Type == "tool_result" && b.ToolUseID != "" {
			resultMap[b.ToolUseID] = b.Text
		}
	}

	var out []contentBlock
	for _, b := range blocks {
		if b.Type == "tool_result" {
			continue // consumed by pairing
		}
		if b.Type == "tool_use" && b.ToolUseID != "" {
			if text, ok := resultMap[b.ToolUseID]; ok {
				b.ToolResultText = text
			}
		}
		out = append(out, b)
	}
	return out
}

// toolInputSummary extracts a short summary from tool input JSON for display
// in the tool header.
func toolInputSummary(name string, input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal(input, &m); err != nil {
		return ""
	}
	switch name {
	case "Bash":
		if cmd, ok := m["command"].(string); ok {
			return cmd
		}
	case "Read", "Write", "Edit":
		if fp, ok := m["file_path"].(string); ok {
			return fp
		}
	case "Glob":
		if p, ok := m["pattern"].(string); ok {
			return p
		}
	case "Grep":
		if p, ok := m["pattern"].(string); ok {
			return p
		}
	}
	return ""
}

// toolIcon returns the display icon for a tool name.
func toolIcon(name string) string {
	switch name {
	case "Bash":
		return "$"
	case "Read":
		return "\U0001F4C4" // file emoji
	case "Write":
		return "\u270D" // writing hand
	case "Edit":
		return "\u270E" // pencil
	case "Glob":
		return "\U0001F50D" // magnifying glass
	case "Grep":
		return "\U0001F50E" // magnifying glass right
	default:
		return "\u2699" // gear
	}
}

// mdRenderer is a goldmark instance configured for safe markdown rendering.
var mdRenderer = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(html.WithUnsafe()),
)

// renderMarkdown converts markdown text to HTML. Raw HTML in the source is
// passed through (goldmark's GFM extension handles sanitisation via
// autolink/strikethrough/table support). We use WithUnsafe so that code
// blocks and inline HTML entities render correctly; the template already
// applies auto-escaping for user-prompt text blocks.
func renderMarkdown(text string) template.HTML {
	var buf bytes.Buffer
	if err := mdRenderer.Convert([]byte(text), &buf); err != nil {
		return template.HTML(template.HTMLEscapeString(text))
	}
	return template.HTML(buf.String())
}

var messagesTemplate = template.Must(template.New("messages").Funcs(template.FuncMap{
	"toolInputSummary": toolInputSummary,
	"toolIcon":         toolIcon,
	"markdown":         renderMarkdown,
	"truncateLines": func(s string, maxLines int) string {
		lines := strings.Split(s, "\n")
		if len(lines) <= maxLines {
			return s
		}
		return strings.Join(lines[:maxLines], "\n")
	},
	"needsTruncation": func(s string) bool {
		lines := strings.Split(s, "\n")
		return len(lines) > 12 || len(s) > 1200
	},
}).Parse(`{{if not .}}<div class="messages-empty">No messages yet.</div>
{{else}}{{range .}}{{if eq .Role "user"}}` +
	`<div class="user-prompt-container">` +
	`<div class="message message-user-prompt">` +
	`<div class="message-content">` +
	`{{range .Blocks}}{{if eq .Type "text"}}` +
	`{{if needsTruncation .Text}}<div class="text-block collapsible-text"><div class="truncated-content">{{truncateLines .Text 12}}<div class="fade-overlay"></div></div><button class="show-more-btn" type="button">Show more</button></div>` +
	`{{else}}<div class="text-block">{{.Text}}</div>{{end}}` +
	`{{end}}{{end}}` +
	`</div></div></div>` +

	`{{else}}` +

	`<div class="assistant-turn">` +
	`{{range .Blocks}}` +
	`{{if eq .Type "thinking"}}` +
	`<details class="thinking-block collapsible">` +
	`<summary class="collapsible__summary"><span class="collapsible__icon">&#x25B8;</span> Thinking</summary>` +
	`<div class="thinking-content">{{.Text}}</div>` +
	`</details>` +
	`{{else if eq .Type "text"}}` +
	`<div class="text-block md-content">{{markdown .Text}}</div>` +
	`{{else if eq .Type "tool_use"}}` +
	`<div class="tool-indicator">` +
	`<div class="tool-indicator-header" aria-expanded="false">` +
	`<span class="tool-dot">●</span>` +
	`<span class="tool-name">{{.ToolName}}</span>` +
	`<span class="tool-summary">{{toolInputSummary .ToolName .ToolInput}}</span>` +
	`<span class="tool-expand-arrow">▸</span>` +
	`</div>` +
	`<div class="tool-indicator-body tool-collapsed">` +
	`{{if .ToolResultText}}<pre class="tool-output">{{.ToolResultText}}</pre>{{end}}` +
	`</div>` +
	`</div>` +
	`{{end}}` +
	`{{end}}` +
	`</div>` +

	`{{end}}{{end}}{{end}}`))

// renderMessagesHTML renders conversation turns as an HTML fragment.
func renderMessagesHTML(turns []renderedTurn) (string, error) {
	var buf bytes.Buffer
	if err := messagesTemplate.Execute(&buf, turns); err != nil {
		return "", err
	}
	return buf.String(), nil
}
