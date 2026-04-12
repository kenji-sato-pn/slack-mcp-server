# Design: Markdown → rich_text Conversion for Drafts

**Date**: 2026-04-12
**Linear**: [SYS-13](https://linear.app/primenumber-systemn/issue/SYS-13)
**Status**: Approved

---

## Goal

Convert Markdown-formatted text to Slack `rich_text` Block Kit JSON in `drafts_create` and `drafts_update`, so that bold, italic, code, lists, quotes, and links render correctly in Slack drafts.

## Current State

`buildRichTextBlockJSON` wraps the entire text in a single `rich_text_section` as plain text. Formatting markers like `**bold**` appear as literal characters.

## Conversion Rules

| Markdown Input | rich_text Output |
|---|---|
| `**text**` | `{"type": "text", "text": "text", "style": {"bold": true}}` |
| `_text_` | `{"type": "text", "text": "text", "style": {"italic": true}}` |
| `` `code` `` | `{"type": "text", "text": "code", "style": {"code": true}}` |
| `~~text~~` | `{"type": "text", "text": "text", "style": {"strike": true}}` |
| `- item` / `* item` / `• item` | `rich_text_list` with `"style": "bullet"` |
| `1. item` | `rich_text_list` with `"style": "ordered"` |
| `> quote` | `rich_text_quote` |
| `` ``` code block ``` `` | `rich_text_preformatted` |
| `https://url` | `{"type": "link", "url": "https://url"}` |
| Regular text | `{"type": "text", "text": "..."}` |
| Empty line | Paragraph separator (new `rich_text_section`) |

## Architecture

New file `pkg/handler/richtext.go` containing a line-based parser:

1. Split text into lines
2. Group lines into blocks: paragraphs, bullet lists, ordered lists, quotes, code blocks
3. For each block, parse inline formatting (bold, italic, code, strike, links)
4. Produce a single `rich_text` block with the appropriate element types

## Out of Scope

- Nested lists (only single-level)
- Images / emoji shortcodes
- Headings (no `rich_text` equivalent — treated as bold text)
- Tables
