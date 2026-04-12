package handler

import (
	"encoding/json"
	"regexp"
	"strings"
)

// richTextElement represents a text or link element inside a rich_text_section.
type richTextElement struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	URL   string          `json:"url,omitempty"`
	Style *richTextStyle  `json:"style,omitempty"`
}

// richTextStyle represents formatting options for a text element.
type richTextStyle struct {
	Bold   bool `json:"bold,omitempty"`
	Italic bool `json:"italic,omitempty"`
	Strike bool `json:"strike,omitempty"`
	Code   bool `json:"code,omitempty"`
}

// richTextBlock represents a top-level rich_text block for the Edge API.
type richTextBlock struct {
	Type     string `json:"type"`
	Elements []any  `json:"elements"`
}

// richTextSection is a paragraph containing inline elements.
type richTextSection struct {
	Type     string            `json:"type"`
	Elements []richTextElement `json:"elements"`
}

// richTextList is a bullet or ordered list.
type richTextList struct {
	Type     string            `json:"type"`
	Style    string            `json:"style"`
	Elements []richTextSection `json:"elements"`
	Indent   int               `json:"indent"`
	Border   int               `json:"border"`
	Offset   int               `json:"offset"`
}

// richTextQuote is a blockquote.
type richTextQuote struct {
	Type     string            `json:"type"`
	Elements []richTextElement `json:"elements"`
}

// richTextPreformatted is a code block.
type richTextPreformatted struct {
	Type     string            `json:"type"`
	Elements []richTextElement `json:"elements"`
	Border   int               `json:"border"`
}

// markdownToRichTextJSON converts markdown-formatted text to a Slack rich_text
// block JSON array suitable for the Edge API drafts endpoints.
func markdownToRichTextJSON(text string) ([]byte, error) {
	block := richTextBlock{
		Type:     "rich_text",
		Elements: parseBlocks(text),
	}
	return json.Marshal([]richTextBlock{block})
}

// parseBlocks splits text into block-level elements: paragraphs, lists, quotes, code blocks.
func parseBlocks(text string) []any {
	lines := strings.Split(text, "\n")
	var elements []any
	i := 0

	for i < len(lines) {
		line := lines[i]

		// Code block: ```
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			var codeLines []string
			i++ // skip opening ```
			for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
				codeLines = append(codeLines, lines[i])
				i++
			}
			if i < len(lines) {
				i++ // skip closing ```
			}
			codeText := strings.Join(codeLines, "\n")
			if codeText != "" {
				elements = append(elements, richTextPreformatted{
					Type:     "rich_text_preformatted",
					Elements: []richTextElement{{Type: "text", Text: codeText}},
					Border:   0,
				})
			}
			continue
		}

		// Blockquote: > text
		if strings.HasPrefix(line, "> ") || line == ">" {
			var quoteElements []richTextElement
			for i < len(lines) && (strings.HasPrefix(lines[i], "> ") || lines[i] == ">") {
				qLine := strings.TrimPrefix(lines[i], "> ")
				qLine = strings.TrimPrefix(qLine, ">")
				if len(quoteElements) > 0 {
					quoteElements = append(quoteElements, richTextElement{Type: "text", Text: "\n"})
				}
				quoteElements = append(quoteElements, parseInline(qLine)...)
				i++
			}
			elements = append(elements, richTextQuote{
				Type:     "rich_text_quote",
				Elements: quoteElements,
			})
			continue
		}

		// Bullet list: - item, * item, • item
		if isBulletLine(line) {
			var listItems []richTextSection
			for i < len(lines) && isBulletLine(lines[i]) {
				itemText := stripBulletPrefix(lines[i])
				listItems = append(listItems, richTextSection{
					Type:     "rich_text_section",
					Elements: parseInline(itemText),
				})
				i++
			}
			elements = append(elements, richTextList{
				Type:     "rich_text_list",
				Style:    "bullet",
				Elements: listItems,
				Indent:   0,
				Border:   0,
				Offset:   0,
			})
			continue
		}

		// Ordered list: 1. item, 2. item
		if isOrderedLine(line) {
			var listItems []richTextSection
			for i < len(lines) && isOrderedLine(lines[i]) {
				itemText := stripOrderedPrefix(lines[i])
				listItems = append(listItems, richTextSection{
					Type:     "rich_text_section",
					Elements: parseInline(itemText),
				})
				i++
			}
			elements = append(elements, richTextList{
				Type:     "rich_text_list",
				Style:    "ordered",
				Elements: listItems,
				Indent:   0,
				Border:   0,
				Offset:   0,
			})
			continue
		}

		// Heading: # text → bold text (rich_text has no heading type)
		if headingText, ok := parseHeading(line); ok {
			elements = append(elements, richTextSection{
				Type: "rich_text_section",
				Elements: []richTextElement{{
					Type:  "text",
					Text:  headingText,
					Style: &richTextStyle{Bold: true},
				}},
			})
			i++
			continue
		}

		// Empty line: output as empty rich_text_section to preserve paragraph spacing
		if strings.TrimSpace(line) == "" {
			elements = append(elements, richTextSection{
				Type:     "rich_text_section",
				Elements: []richTextElement{{Type: "text", Text: "\n"}},
			})
			i++
			continue
		}

		// Regular text line: each line becomes its own rich_text_section
		// (Slack's draft UI does not render \n within text elements as line breaks)
		inlineElements := parseInline(line)
		if len(inlineElements) > 0 {
			elements = append(elements, richTextSection{
				Type:     "rich_text_section",
				Elements: inlineElements,
			})
		}
		i++
	}

	// If nothing was parsed, add a single section with the raw text
	if len(elements) == 0 {
		elements = append(elements, richTextSection{
			Type:     "rich_text_section",
			Elements: []richTextElement{{Type: "text", Text: text}},
		})
	}

	return elements
}

var (
	bulletRe  = regexp.MustCompile(`^[\-\*•]\s+`)
	orderedRe = regexp.MustCompile(`^\d+\.\s+`)
	linkRe    = regexp.MustCompile(`https?://[^\s>)]+`)
)

func isBulletLine(line string) bool {
	return bulletRe.MatchString(line)
}

func isOrderedLine(line string) bool {
	return orderedRe.MatchString(line)
}

func stripBulletPrefix(line string) string {
	return bulletRe.ReplaceAllString(line, "")
}

func stripOrderedPrefix(line string) string {
	return orderedRe.ReplaceAllString(line, "")
}

var headingRe = regexp.MustCompile(`^#{1,6}\s+(.+)`)

func parseHeading(line string) (string, bool) {
	m := headingRe.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	return m[1], true
}

func isAlphanumeric(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// parseInline parses inline formatting: **bold**, _italic_, `code`, ~~strike~~, and URLs.
// Priority: code > bold > strikethrough > italic > URL (earliest position wins; at same position, declaration order wins).
// When matches start at the same position, earlier checks win.
func parseInline(text string) []richTextElement {
	if text == "" {
		return nil
	}

	var elements []richTextElement
	remaining := text

	for len(remaining) > 0 {
		// Find the earliest inline match
		bestIdx := len(remaining)
		bestType := ""
		bestContent := ""
		bestLen := 0

		// Check for code span: `code`
		if idx := strings.Index(remaining, "`"); idx >= 0 && idx < bestIdx {
			end := strings.Index(remaining[idx+1:], "`")
			if end >= 0 {
				bestIdx = idx
				bestType = "code"
				bestContent = remaining[idx+1 : idx+1+end]
				bestLen = end + 2 // including both backticks
			}
		}

		// Check for bold: **text**
		if idx := strings.Index(remaining, "**"); idx >= 0 && idx < bestIdx {
			end := strings.Index(remaining[idx+2:], "**")
			if end >= 0 {
				bestIdx = idx
				bestType = "bold"
				bestContent = remaining[idx+2 : idx+2+end]
				bestLen = end + 4
			}
		}

		// Check for strikethrough: ~~text~~
		if idx := strings.Index(remaining, "~~"); idx >= 0 && idx < bestIdx {
			end := strings.Index(remaining[idx+2:], "~~")
			if end >= 0 {
				bestIdx = idx
				bestType = "strike"
				bestContent = remaining[idx+2 : idx+2+end]
				bestLen = end + 4
			}
		}

		// Check for italic: _text_ (not inside a word — requires non-alphanumeric boundary)
		if idx := strings.Index(remaining, "_"); idx >= 0 && idx < bestIdx {
			end := strings.Index(remaining[idx+1:], "_")
			if end >= 0 && end > 0 {
				closeIdx := idx + 1 + end
				// Word boundary: char before opening _ must not be alphanumeric
				leftOk := idx == 0 || !isAlphanumeric(remaining[idx-1])
				// Word boundary: char after closing _ must not be alphanumeric
				rightOk := closeIdx+1 >= len(remaining) || !isAlphanumeric(remaining[closeIdx+1])
				if leftOk && rightOk {
					bestIdx = idx
					bestType = "italic"
					bestContent = remaining[idx+1 : idx+1+end]
					bestLen = end + 2
				}
			}
		}

		// Check for URL
		if loc := linkRe.FindStringIndex(remaining); loc != nil && loc[0] < bestIdx {
			bestIdx = loc[0]
			bestType = "link"
			bestContent = remaining[loc[0]:loc[1]]
			bestLen = loc[1] - loc[0]
		}

		// Add any text before the match
		if bestIdx > 0 {
			elements = append(elements, richTextElement{
				Type: "text",
				Text: remaining[:bestIdx],
			})
		}

		// If no match found, add the rest as plain text
		if bestType == "" {
			if bestIdx == 0 && len(remaining) > 0 {
				elements = append(elements, richTextElement{
					Type: "text",
					Text: remaining,
				})
			}
			break
		}

		// Add the formatted element
		switch bestType {
		case "bold":
			elements = append(elements, richTextElement{
				Type:  "text",
				Text:  bestContent,
				Style: &richTextStyle{Bold: true},
			})
		case "italic":
			elements = append(elements, richTextElement{
				Type:  "text",
				Text:  bestContent,
				Style: &richTextStyle{Italic: true},
			})
		case "code":
			elements = append(elements, richTextElement{
				Type:  "text",
				Text:  bestContent,
				Style: &richTextStyle{Code: true},
			})
		case "strike":
			elements = append(elements, richTextElement{
				Type:  "text",
				Text:  bestContent,
				Style: &richTextStyle{Strike: true},
			})
		case "link":
			elements = append(elements, richTextElement{
				Type: "link",
				URL:  bestContent,
				Text: bestContent,
			})
		}

		remaining = remaining[bestIdx+bestLen:]
	}

	if len(elements) == 0 {
		elements = append(elements, richTextElement{Type: "text", Text: text})
	}

	return elements
}
