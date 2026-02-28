package web

import (
	"encoding/json"
	"time"
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
				var cs string
				if json.Unmarshal(b.Content, &cs) == nil {
					text = cs
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
