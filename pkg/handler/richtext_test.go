package handler

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMarkdownToRichTextJSON(t *testing.T) {
	t.Run("plain text", func(t *testing.T) {
		result, err := markdownToRichTextJSON("hello world")
		require.NoError(t, err)

		blocks := parseRichTextBlocks(t, result)
		require.Len(t, blocks, 1)
		require.Len(t, blocks[0].Elements, 1)

		section := blocks[0].Elements[0].(map[string]any)
		assert.Equal(t, "rich_text_section", section["type"])
		elements := section["elements"].([]any)
		require.Len(t, elements, 1)
		assert.Equal(t, "hello world", elements[0].(map[string]any)["text"])
	})

	t.Run("bold text", func(t *testing.T) {
		result, err := markdownToRichTextJSON("this is **bold** text")
		require.NoError(t, err)

		elements := getFirstSectionElements(t, result)
		require.Len(t, elements, 3)
		assert.Equal(t, "this is ", elements[0].(map[string]any)["text"])
		assert.Equal(t, "bold", elements[1].(map[string]any)["text"])
		style := elements[1].(map[string]any)["style"].(map[string]any)
		assert.Equal(t, true, style["bold"])
		assert.Equal(t, " text", elements[2].(map[string]any)["text"])
	})

	t.Run("italic text", func(t *testing.T) {
		result, err := markdownToRichTextJSON("this is _italic_ text")
		require.NoError(t, err)

		elements := getFirstSectionElements(t, result)
		require.Len(t, elements, 3)
		assert.Equal(t, "italic", elements[1].(map[string]any)["text"])
		style := elements[1].(map[string]any)["style"].(map[string]any)
		assert.Equal(t, true, style["italic"])
	})

	t.Run("code span", func(t *testing.T) {
		result, err := markdownToRichTextJSON("run `go test` now")
		require.NoError(t, err)

		elements := getFirstSectionElements(t, result)
		require.Len(t, elements, 3)
		assert.Equal(t, "go test", elements[1].(map[string]any)["text"])
		style := elements[1].(map[string]any)["style"].(map[string]any)
		assert.Equal(t, true, style["code"])
	})

	t.Run("strikethrough", func(t *testing.T) {
		result, err := markdownToRichTextJSON("this is ~~deleted~~ text")
		require.NoError(t, err)

		elements := getFirstSectionElements(t, result)
		require.Len(t, elements, 3)
		assert.Equal(t, "deleted", elements[1].(map[string]any)["text"])
		style := elements[1].(map[string]any)["style"].(map[string]any)
		assert.Equal(t, true, style["strike"])
	})

	t.Run("URL", func(t *testing.T) {
		result, err := markdownToRichTextJSON("see https://example.com for details")
		require.NoError(t, err)

		elements := getFirstSectionElements(t, result)
		require.Len(t, elements, 3)
		assert.Equal(t, "link", elements[1].(map[string]any)["type"])
		assert.Equal(t, "https://example.com", elements[1].(map[string]any)["url"])
	})

	t.Run("bullet list", func(t *testing.T) {
		result, err := markdownToRichTextJSON("- item1\n- item2\n- item3")
		require.NoError(t, err)

		blocks := parseRichTextBlocks(t, result)
		require.Len(t, blocks, 1)
		require.Len(t, blocks[0].Elements, 1)

		list := blocks[0].Elements[0].(map[string]any)
		assert.Equal(t, "rich_text_list", list["type"])
		assert.Equal(t, "bullet", list["style"])
		listElements := list["elements"].([]any)
		require.Len(t, listElements, 3)
	})

	t.Run("ordered list", func(t *testing.T) {
		result, err := markdownToRichTextJSON("1. first\n2. second\n3. third")
		require.NoError(t, err)

		blocks := parseRichTextBlocks(t, result)
		list := blocks[0].Elements[0].(map[string]any)
		assert.Equal(t, "rich_text_list", list["type"])
		assert.Equal(t, "ordered", list["style"])
	})

	t.Run("blockquote", func(t *testing.T) {
		result, err := markdownToRichTextJSON("> quoted text\n> second line")
		require.NoError(t, err)

		blocks := parseRichTextBlocks(t, result)
		quote := blocks[0].Elements[0].(map[string]any)
		assert.Equal(t, "rich_text_quote", quote["type"])
	})

	t.Run("code block", func(t *testing.T) {
		result, err := markdownToRichTextJSON("```\nfunc main() {\n}\n```")
		require.NoError(t, err)

		blocks := parseRichTextBlocks(t, result)
		pre := blocks[0].Elements[0].(map[string]any)
		assert.Equal(t, "rich_text_preformatted", pre["type"])
		elements := pre["elements"].([]any)
		assert.Contains(t, elements[0].(map[string]any)["text"], "func main()")
	})

	t.Run("mixed content", func(t *testing.T) {
		input := "**進捗**:\n\n- 項目1\n- `code` を含む項目2\n\n通常テキスト"
		result, err := markdownToRichTextJSON(input)
		require.NoError(t, err)

		blocks := parseRichTextBlocks(t, result)
		require.Len(t, blocks, 1)
		// Should have: section (進捗:), list (2 items), section (通常テキスト)
		require.GreaterOrEqual(t, len(blocks[0].Elements), 3)
	})

	t.Run("bullet with asterisk prefix", func(t *testing.T) {
		result, err := markdownToRichTextJSON("* item1\n* item2")
		require.NoError(t, err)

		blocks := parseRichTextBlocks(t, result)
		list := blocks[0].Elements[0].(map[string]any)
		assert.Equal(t, "rich_text_list", list["type"])
		assert.Equal(t, "bullet", list["style"])
	})

	t.Run("heading converted to bold", func(t *testing.T) {
		result, err := markdownToRichTextJSON("## My Heading")
		require.NoError(t, err)

		elements := getFirstSectionElements(t, result)
		require.Len(t, elements, 1)
		assert.Equal(t, "My Heading", elements[0].(map[string]any)["text"])
		style := elements[0].(map[string]any)["style"].(map[string]any)
		assert.Equal(t, true, style["bold"])
	})

	t.Run("empty string", func(t *testing.T) {
		result, err := markdownToRichTextJSON("")
		require.NoError(t, err)

		blocks := parseRichTextBlocks(t, result)
		require.Len(t, blocks, 1)
		require.Len(t, blocks[0].Elements, 1)
	})
}

func TestParseInline(t *testing.T) {
	t.Run("no formatting", func(t *testing.T) {
		elements := parseInline("plain text")
		require.Len(t, elements, 1)
		assert.Equal(t, "text", elements[0].Type)
		assert.Equal(t, "plain text", elements[0].Text)
	})

	t.Run("multiple formats", func(t *testing.T) {
		elements := parseInline("**bold** and `code`")
		require.Len(t, elements, 3)
		assert.Equal(t, "bold", elements[0].Text)
		assert.True(t, elements[0].Style.Bold)
		assert.Equal(t, " and ", elements[1].Text)
		assert.Equal(t, "code", elements[2].Text)
		assert.True(t, elements[2].Style.Code)
	})

	t.Run("url in middle", func(t *testing.T) {
		elements := parseInline("visit https://example.com/path?q=1 today")
		require.Len(t, elements, 3)
		assert.Equal(t, "visit ", elements[0].Text)
		assert.Equal(t, "link", elements[1].Type)
		assert.Equal(t, "https://example.com/path?q=1", elements[1].URL)
		assert.Equal(t, " today", elements[2].Text)
	})

	t.Run("underscore in identifier not treated as italic", func(t *testing.T) {
		elements := parseInline("use channel_id field")
		require.Len(t, elements, 1)
		assert.Equal(t, "use channel_id field", elements[0].Text)
		assert.Nil(t, elements[0].Style)
	})

	t.Run("underscore with word boundary is italic", func(t *testing.T) {
		elements := parseInline("this is _italic_ text")
		require.Len(t, elements, 3)
		assert.Equal(t, "italic", elements[1].Text)
		assert.True(t, elements[1].Style.Italic)
	})

	t.Run("unclosed bold passes through", func(t *testing.T) {
		elements := parseInline("**bold without close")
		require.Len(t, elements, 1)
		assert.Equal(t, "**bold without close", elements[0].Text)
	})

	t.Run("unclosed code passes through", func(t *testing.T) {
		elements := parseInline("`unclosed code")
		require.Len(t, elements, 1)
		assert.Equal(t, "`unclosed code", elements[0].Text)
	})

	t.Run("empty string returns nil", func(t *testing.T) {
		elements := parseInline("")
		assert.Nil(t, elements)
	})
}

// Test helpers

type parsedBlock struct {
	Type     string `json:"type"`
	Elements []any  `json:"elements"`
}

func parseRichTextBlocks(t *testing.T, data []byte) []parsedBlock {
	t.Helper()
	var blocks []parsedBlock
	err := json.Unmarshal(data, &blocks)
	require.NoError(t, err)
	return blocks
}

func getFirstSectionElements(t *testing.T, data []byte) []any {
	t.Helper()
	blocks := parseRichTextBlocks(t, data)
	require.Len(t, blocks, 1)
	require.GreaterOrEqual(t, len(blocks[0].Elements), 1)
	section := blocks[0].Elements[0].(map[string]any)
	return section["elements"].([]any)
}
