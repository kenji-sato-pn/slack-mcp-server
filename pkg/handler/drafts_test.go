package handler

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestConvertTsToMillis(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"standard slack ts", "1775966853.146997", "1775966853146"},
		{"short fractional", "1775966853.1", "1775966853100"},
		{"two digit fractional", "1775966853.14", "1775966853140"},
		{"exact three digits", "1775966853.146", "1775966853146"},
		{"no dot (pass through)", "1775966853146", "1775966853146"},
		{"empty string", "", ""},
		{"zero timestamp", "0.000000", "0000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertTsToMillis(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildRichTextBlockJSON(t *testing.T) {
	t.Run("single line wraps in rich_text block", func(t *testing.T) {
		result, err := buildRichTextBlockJSON("hello world")
		require.NoError(t, err)

		var blocks []map[string]any
		err = json.Unmarshal(result, &blocks)
		require.NoError(t, err)
		require.Len(t, blocks, 1)
		assert.Equal(t, "rich_text", blocks[0]["type"])

		elements := blocks[0]["elements"].([]any)
		require.Len(t, elements, 1)
		section := elements[0].(map[string]any)
		assert.Equal(t, "rich_text_section", section["type"])

		textElements := section["elements"].([]any)
		require.Len(t, textElements, 1)
		textEl := textElements[0].(map[string]any)
		assert.Equal(t, "text", textEl["type"])
		assert.Equal(t, "hello world", textEl["text"])
	})

	t.Run("multiline text preserved in single section", func(t *testing.T) {
		input := "line1\nline2\nline3"
		result, err := buildRichTextBlockJSON(input)
		require.NoError(t, err)

		var blocks []map[string]any
		err = json.Unmarshal(result, &blocks)
		require.NoError(t, err)
		require.Len(t, blocks, 1)

		elements := blocks[0]["elements"].([]any)
		require.Len(t, elements, 1)

		section := elements[0].(map[string]any)
		textElements := section["elements"].([]any)
		textEl := textElements[0].(map[string]any)
		assert.Equal(t, input, textEl["text"])
	})
}

func TestBuildDestinationsJSON(t *testing.T) {
	t.Run("channel only omits broadcast from JSON", func(t *testing.T) {
		result, err := buildDestinationsJSON("C12345", "")
		require.NoError(t, err)

		// Verify wire format: broadcast must be absent
		assert.NotContains(t, string(result), "broadcast")

		var dests []draftDestinationInput
		err = json.Unmarshal(result, &dests)
		require.NoError(t, err)
		require.Len(t, dests, 1)
		assert.Equal(t, "C12345", dests[0].ChannelID)
		assert.Empty(t, dests[0].ThreadTs)
		assert.Nil(t, dests[0].Broadcast)
	})

	t.Run("channel with thread includes broadcast false", func(t *testing.T) {
		result, err := buildDestinationsJSON("C12345", "1234567890.123456")
		require.NoError(t, err)

		// Verify wire format: broadcast must be present as false
		assert.Contains(t, string(result), `"broadcast":false`)

		var dests []draftDestinationInput
		err = json.Unmarshal(result, &dests)
		require.NoError(t, err)
		require.Len(t, dests, 1)
		assert.Equal(t, "C12345", dests[0].ChannelID)
		assert.Equal(t, "1234567890.123456", dests[0].ThreadTs)
		require.NotNil(t, dests[0].Broadcast)
		assert.False(t, *dests[0].Broadcast)
	})
}

func TestBuildTextBlocks(t *testing.T) {
	logger := zap.NewNop()

	t.Run("plain text returns nil blocks", func(t *testing.T) {
		blocks, text, err := buildTextBlocks(logger, "hello", "text/plain")
		require.NoError(t, err)
		assert.Nil(t, blocks)
		assert.Equal(t, "hello", text)
	})

	t.Run("markdown returns blocks", func(t *testing.T) {
		blocks, text, err := buildTextBlocks(logger, "**bold**", "text/markdown")
		require.NoError(t, err)
		assert.NotNil(t, blocks)
		assert.Equal(t, "**bold**", text)
	})

	t.Run("invalid content type returns error", func(t *testing.T) {
		_, _, err := buildTextBlocks(logger, "hello", "text/html")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "content_type must be either")
	})
}

func TestBuildMsgOptionsFromBlocks(t *testing.T) {
	t.Run("with blocks returns MsgOptionBlocks", func(t *testing.T) {
		logger := zap.NewNop()
		blocks, _, _ := buildTextBlocks(logger, "test", "text/markdown")
		options := buildMsgOptionsFromBlocks(blocks, "test")
		assert.Len(t, options, 1)
	})

	t.Run("nil blocks returns disable markdown + text", func(t *testing.T) {
		options := buildMsgOptionsFromBlocks(nil, "plain text")
		assert.Len(t, options, 2)
	})
}

func TestParseScheduleAt(t *testing.T) {
	t.Run("empty string returns 0", func(t *testing.T) {
		result, err := parseScheduleAt("")
		require.NoError(t, err)
		assert.Equal(t, int64(0), result)
	})

	t.Run("valid future ISO-8601 returns unix timestamp", func(t *testing.T) {
		future := time.Now().Add(1 * time.Hour).Format(time.RFC3339)
		result, err := parseScheduleAt(future)
		require.NoError(t, err)
		assert.Greater(t, result, int64(0))
	})

	t.Run("past time returns error", func(t *testing.T) {
		past := time.Now().Add(-1 * time.Hour).Format(time.RFC3339)
		_, err := parseScheduleAt(past)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be in the future")
	})

	t.Run("too far in future returns error", func(t *testing.T) {
		tooFar := time.Now().AddDate(0, 0, 121).Format(time.RFC3339)
		_, err := parseScheduleAt(tooFar)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "within 120 days")
	})

	t.Run("invalid format returns error", func(t *testing.T) {
		_, err := parseScheduleAt("not-a-date")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid schedule_at")
	})

	t.Run("missing timezone returns error", func(t *testing.T) {
		_, err := parseScheduleAt("2026-04-13T09:00:00")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid schedule_at")
	})
}

func TestTruncateText(t *testing.T) {
	t.Run("short text unchanged", func(t *testing.T) {
		assert.Equal(t, "hello", truncateText("hello", 10))
	})

	t.Run("exact length unchanged", func(t *testing.T) {
		assert.Equal(t, "hello", truncateText("hello", 5))
	})

	t.Run("long text truncated with ellipsis", func(t *testing.T) {
		assert.Equal(t, "hel...", truncateText("hello world", 3))
	})

	t.Run("unicode characters counted correctly", func(t *testing.T) {
		assert.Equal(t, "こん...", truncateText("こんにちは", 2))
	})

	t.Run("empty string", func(t *testing.T) {
		assert.Equal(t, "", truncateText("", 5))
	})
}
