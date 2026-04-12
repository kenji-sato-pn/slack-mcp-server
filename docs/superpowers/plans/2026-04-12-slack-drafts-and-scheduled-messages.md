# Draft Messages & Scheduled Messages Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add 8 MCP tools (5 drafts + 3 scheduled messages) to slack-mcp-server.

**Architecture:** Drafts use the Edge API (`drafts.*`) via the existing `pkg/provider/edge` client, accessible through the `SlackAPI` interface. Scheduled messages use the official `chat.scheduleMessage` / `chat.scheduledMessages.list` / `chat.deleteScheduledMessage` APIs via `slack-go`. A shared `buildTextBlocks` function is extracted from the existing `ConversationsAddMessageHandler` to avoid duplicating markdown-to-Block Kit conversion logic.

**Tech Stack:** Go, slack-go v0.17.3, mcp-go, Edge API (xoxc/xoxd), gocsv, testify

**Spec:** `docs/superpowers/specs/2026-04-12-slack-drafts-and-scheduled-messages-design.md`
**Linear:** [SYS-12](https://linear.app/primenumber-systemn/issue/SYS-12/slack-mcp-server-draft-message-and-scheduled-message-ツール追加)

---

### Task 1: Edge API `drafts.*` Schema Verification

**This is a manual research task.** Before writing any code, verify the Edge API endpoints by creating/editing/deleting a draft in the Slack web client with browser devtools open (Network tab). This blocks all drafts tasks.

- [ ] **Step 1: Capture `drafts.create` request/response**

Open Slack in a browser. Open devtools Network tab. Create a new draft message in any channel. Filter network requests for `drafts`. Record:
- Endpoint URL (expected: `https://<workspace>.slack.com/api/drafts.create`)
- Form field names: `channel_id` or `channel`? `thread_ts` or `conversation_id`?
- Whether a `blocks` field appears in the request
- Response shape: what is the draft ID field called? (`id`? `draft_id`?)

- [ ] **Step 2: Capture `drafts.list` request/response**

Navigate to Slack's "Drafts & Sent" sidebar section. Record:
- Form field names for filtering (by channel, cursor, limit)
- Response shape: array field name, draft object fields

- [ ] **Step 3: Capture `drafts.update`, `drafts.delete`, `drafts.send`**

Edit an existing draft, then delete another, then send one. For each, record:
- Form field names (draft ID field name)
- Response shape

- [ ] **Step 4: Document findings in spec**

Append the verified schema to the "Edge API Verified Schema" section in `docs/superpowers/specs/2026-04-12-slack-drafts-and-scheduled-messages-design.md`.

- [ ] **Step 5: Commit**

```bash
git add docs/superpowers/specs/2026-04-12-slack-drafts-and-scheduled-messages-design.md
git commit -m "docs: add verified Edge API drafts schema to design spec (SYS-12)"
```

---

### Task 2: Extract `buildTextBlocks` Shared Function

**Files:**
- Modify: `pkg/handler/conversations.go:229-249` (extract markdown→blocks logic)
- Test: existing tests in `pkg/handler/conversations_test.go`

The markdown-to-Block Kit blocks conversion currently lives inline in `ConversationsAddMessageHandler`. Extract it so `drafts_create`, `drafts_update`, and `conversations_schedule_message` can reuse it.

- [ ] **Step 1: Run existing tests to confirm green baseline**

Run: `go test ./pkg/handler/... -count=1 -short -v -run "AddMessage|History" 2>&1 | tail -20`
Expected: PASS

- [ ] **Step 2: Extract `buildTextBlocks` function**

In `pkg/handler/conversations.go`, add this function before `ConversationsAddMessageHandler` (around line 213):

```go
// buildTextBlocks converts text with the given content type into Block Kit blocks
// and a plain-text fallback. For "text/markdown", it parses markdown into blocks
// (falling back to plain text on parse error with a warning). For "text/plain",
// it returns nil blocks and the raw text.
//
// Returns (blocks, plainText, error). Callers should use blocks when non-nil,
// otherwise fall back to plainText.
func buildTextBlocks(logger *zap.Logger, text, contentType string) ([]slack.Block, string, error) {
	switch contentType {
	case "text/plain":
		return nil, text, nil
	case "text/markdown":
		blocks, err := slackGoUtil.ConvertMarkdownTextToBlocks(text)
		if err != nil {
			logger.Warn("Markdown parsing error, falling back to plain text", zap.Error(err))
			return nil, text, nil
		}
		return blocks, text, nil
	default:
		return nil, "", errors.New("content_type must be either 'text/plain' or 'text/markdown'")
	}
}

// buildMsgOptionsFromBlocks converts the output of buildTextBlocks into
// slack.MsgOption slice ready for PostMessageContext / ScheduleMessageContext.
func buildMsgOptionsFromBlocks(blocks []slack.Block, plainText string) []slack.MsgOption {
	if blocks != nil {
		return []slack.MsgOption{slack.MsgOptionBlocks(blocks...)}
	}
	return []slack.MsgOption{
		slack.MsgOptionDisableMarkdown(),
		slack.MsgOptionText(plainText, false),
	}
}
```

- [ ] **Step 3: Refactor `ConversationsAddMessageHandler` to use `buildTextBlocks`**

Replace the inline switch in `ConversationsAddMessageHandler` (lines ~234-249) with:

```go
	blocks, plainText, err := buildTextBlocks(ch.logger, params.text, params.contentType)
	if err != nil {
		return nil, err
	}
	options = append(options, buildMsgOptionsFromBlocks(blocks, plainText)...)
```

The full handler body (from `var options` onward) becomes:

```go
	var options []slack.MsgOption
	if params.threadTs != "" {
		options = append(options, slack.MsgOptionTS(params.threadTs))
	}

	blocks, plainText, err := buildTextBlocks(ch.logger, params.text, params.contentType)
	if err != nil {
		return nil, err
	}
	options = append(options, buildMsgOptionsFromBlocks(blocks, plainText)...)

	unfurlOpt := os.Getenv("SLACK_MCP_ADD_MESSAGE_UNFURLING")
	if text.IsUnfurlingEnabled(params.text, unfurlOpt, ch.logger) {
		options = append(options, slack.MsgOptionEnableLinkUnfurl())
	} else {
		options = append(options, slack.MsgOptionDisableLinkUnfurl())
		options = append(options, slack.MsgOptionDisableMediaUnfurl())
	}
```

- [ ] **Step 4: Run existing tests to confirm no regression**

Run: `go test ./pkg/handler/... -count=1 -short -v 2>&1 | tail -20`
Expected: PASS (same results as before)

Run: `go build ./...`
Expected: no errors

- [ ] **Step 5: Commit**

```bash
git add pkg/handler/conversations.go
git commit -m "refactor: extract buildTextBlocks for reuse by drafts and scheduled messages (SYS-12)"
```

---

### Task 3: Add Scheduled Message Methods to `SlackAPI` Interface

**Files:**
- Modify: `pkg/provider/api.go:179-223` (add 3 methods to `SlackAPI` interface)
- Modify: `pkg/provider/api.go:460+` (add 3 `MCPSlackClient` implementations)

- [ ] **Step 1: Add methods to `SlackAPI` interface**

In `pkg/provider/api.go`, add these 3 methods to the `SlackAPI` interface (after line 222, before the closing `}`):

```go
	// Scheduled messages API methods
	ScheduleMessageContext(ctx context.Context, channelID, postAt string, options ...slack.MsgOption) (string, string, error)
	GetScheduledMessagesContext(ctx context.Context, params *slack.GetScheduledMessagesParameters) ([]slack.ScheduledMessage, string, error)
	DeleteScheduledMessageContext(ctx context.Context, params *slack.DeleteScheduledMessageParameters) (bool, error)
```

- [ ] **Step 2: Add `MCPSlackClient` method implementations**

After the existing `GetMutedChannels` method (around line 515), add:

```go
func (c *MCPSlackClient) ScheduleMessageContext(ctx context.Context, channelID, postAt string, options ...slack.MsgOption) (string, string, error) {
	return c.slackClient.ScheduleMessageContext(ctx, channelID, postAt, options...)
}

func (c *MCPSlackClient) GetScheduledMessagesContext(ctx context.Context, params *slack.GetScheduledMessagesParameters) ([]slack.ScheduledMessage, string, error) {
	return c.slackClient.GetScheduledMessagesContext(ctx, params)
}

func (c *MCPSlackClient) DeleteScheduledMessageContext(ctx context.Context, params *slack.DeleteScheduledMessageParameters) (bool, error) {
	return c.slackClient.DeleteScheduledMessageContext(ctx, params)
}
```

- [ ] **Step 3: Verify build**

Run: `go build ./...`
Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add pkg/provider/api.go
git commit -m "feat: add scheduled message methods to SlackAPI interface (SYS-12)"
```

---

### Task 4: Implement Scheduled Message Handlers

**Files:**
- Modify: `pkg/handler/conversations.go` (add 3 handlers + parse helpers + types)
- Modify: `pkg/server/server.go` (add 3 tool constants + registrations)

- [ ] **Step 1: Add types and parse helpers to `conversations.go`**

After the existing `markParams` struct (line ~140), add:

```go
const (
	maxScheduleDays = 120
)

type scheduleMessageParams struct {
	channel     string
	threadTs    string
	text        string
	contentType string
	postAt      time.Time
}

type scheduledMessageListParams struct {
	channel string
	limit   int
	cursor  string
}

type cancelScheduledMessageParams struct {
	channel            string
	scheduledMessageID string
}

type ScheduledMessageCSV struct {
	ScheduledMessageID string `csv:"ScheduledMessageID"`
	ChannelID          string `csv:"ChannelID"`
	PostAt             string `csv:"PostAt"`
	Text               string `csv:"Text"`
}
```

- [ ] **Step 2: Add `parseParamsToolScheduleMessage`**

After the existing `parseParamsToolAddMessage` function (after line ~1784), add:

```go
func (ch *ConversationsHandler) parseParamsToolScheduleMessage(ctx context.Context, request mcp.CallToolRequest) (*scheduleMessageParams, error) {
	toolConfig := os.Getenv("SLACK_MCP_ADD_MESSAGE_TOOL")
	enabledTools := os.Getenv("SLACK_MCP_ENABLED_TOOLS")

	if toolConfig == "" {
		if !strings.Contains(enabledTools, "conversations_schedule_message") {
			ch.logger.Error("Schedule-message tool disabled by default")
			return nil, errors.New(
				"by default, the conversations_schedule_message tool is disabled. " +
					"To enable it, set the SLACK_MCP_ADD_MESSAGE_TOOL environment variable to true, 1, or comma separated list of channels",
			)
		}
		toolConfig = "true"
	}

	channel := request.GetString("channel_id", "")
	if channel == "" {
		return nil, errors.New("channel_id must be a string")
	}
	channel, err := ch.resolveChannelID(ctx, channel)
	if err != nil {
		return nil, err
	}
	if !isChannelAllowedForConfig(channel, toolConfig) {
		return nil, fmt.Errorf("conversations_schedule_message tool is not allowed for channel %q, applied policy: %s", channel, toolConfig)
	}

	threadTs := request.GetString("thread_ts", "")
	if threadTs != "" && !strings.Contains(threadTs, ".") {
		return nil, errors.New("thread_ts must be a valid timestamp in format 1234567890.123456")
	}

	msgText := request.GetString("text", "")
	if msgText == "" {
		return nil, errors.New("text must be a string")
	}

	contentType := request.GetString("content_type", "text/markdown")
	if contentType != "text/plain" && contentType != "text/markdown" {
		return nil, errors.New("content_type must be either 'text/plain' or 'text/markdown'")
	}

	postAtStr := request.GetString("post_at", "")
	if postAtStr == "" {
		return nil, errors.New("post_at must be an ISO-8601 timestamp with timezone, e.g. 2026-04-12T09:00:00+09:00")
	}
	postAt, err := time.Parse(time.RFC3339, postAtStr)
	if err != nil {
		return nil, fmt.Errorf("invalid post_at: must be ISO-8601 with timezone (RFC3339), got %q: %w", postAtStr, err)
	}
	if postAt.Before(time.Now()) {
		return nil, errors.New("post_at must be in the future")
	}
	if postAt.After(time.Now().AddDate(0, 0, maxScheduleDays)) {
		return nil, fmt.Errorf("post_at must be within %d days from now", maxScheduleDays)
	}

	return &scheduleMessageParams{
		channel:     channel,
		threadTs:    threadTs,
		text:        msgText,
		contentType: contentType,
		postAt:      postAt,
	}, nil
}

func (ch *ConversationsHandler) parseParamsToolScheduledMessagesList(ctx context.Context, request mcp.CallToolRequest) (*scheduledMessageListParams, error) {
	channel := request.GetString("channel_id", "")
	if channel != "" {
		var err error
		channel, err = ch.resolveChannelID(ctx, channel)
		if err != nil {
			return nil, err
		}
	}

	limit := int(request.GetFloat64("limit", 100))
	if limit < 1 || limit > 1000 {
		return nil, errors.New("limit must be between 1 and 1000")
	}

	cursor := request.GetString("cursor", "")

	return &scheduledMessageListParams{
		channel: channel,
		limit:   limit,
		cursor:  cursor,
	}, nil
}

func (ch *ConversationsHandler) parseParamsToolCancelScheduledMessage(ctx context.Context, request mcp.CallToolRequest) (*cancelScheduledMessageParams, error) {
	channel := request.GetString("channel_id", "")
	if channel == "" {
		return nil, errors.New("channel_id is required")
	}
	channel, err := ch.resolveChannelID(ctx, channel)
	if err != nil {
		return nil, err
	}

	scheduledMessageID := request.GetString("scheduled_message_id", "")
	if scheduledMessageID == "" {
		return nil, errors.New("scheduled_message_id is required")
	}

	return &cancelScheduledMessageParams{
		channel:            channel,
		scheduledMessageID: scheduledMessageID,
	}, nil
}
```

- [ ] **Step 3: Add the 3 handler methods**

After the existing `ConversationsMarkHandler` function, add:

```go
// ConversationsScheduleMessageHandler schedules a message for future delivery
func (ch *ConversationsHandler) ConversationsScheduleMessageHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("ConversationsScheduleMessageHandler called", zap.Any("params", request.Params))

	if ready, err := ch.apiProvider.IsReady(); !ready {
		return nil, err
	}

	params, err := ch.parseParamsToolScheduleMessage(ctx, request)
	if err != nil {
		return nil, err
	}

	blocks, plainText, err := buildTextBlocks(ch.logger, params.text, params.contentType)
	if err != nil {
		return nil, err
	}

	var options []slack.MsgOption
	if params.threadTs != "" {
		options = append(options, slack.MsgOptionTS(params.threadTs))
	}
	options = append(options, buildMsgOptionsFromBlocks(blocks, plainText)...)

	postAtStr := strconv.FormatInt(params.postAt.Unix(), 10)

	ch.logger.Debug("Scheduling Slack message",
		zap.String("channel", params.channel),
		zap.String("post_at", params.postAt.Format(time.RFC3339)),
	)

	respChannel, scheduledMsgID, err := ch.apiProvider.Slack().ScheduleMessageContext(ctx, params.channel, postAtStr, options...)
	if err != nil {
		ch.logger.Error("Slack ScheduleMessageContext failed", zap.Error(err))
		return nil, err
	}

	result := []ScheduledMessageCSV{{
		ScheduledMessageID: scheduledMsgID,
		ChannelID:          respChannel,
		PostAt:             params.postAt.Format(time.RFC3339),
		Text:               truncateText(params.text, 100),
	}}
	csvBytes, err := gocsv.MarshalBytes(&result)
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(string(csvBytes)), nil
}

// ConversationsScheduledMessagesListHandler lists pending scheduled messages
func (ch *ConversationsHandler) ConversationsScheduledMessagesListHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("ConversationsScheduledMessagesListHandler called", zap.Any("params", request.Params))

	if ready, err := ch.apiProvider.IsReady(); !ready {
		return nil, err
	}

	params, err := ch.parseParamsToolScheduledMessagesList(ctx, request)
	if err != nil {
		return nil, err
	}

	slackParams := &slack.GetScheduledMessagesParameters{
		Channel: params.channel,
		Cursor:  params.cursor,
		Limit:   params.limit,
	}

	messages, nextCursor, err := ch.apiProvider.Slack().GetScheduledMessagesContext(ctx, slackParams)
	if err != nil {
		ch.logger.Error("Slack GetScheduledMessagesContext failed", zap.Error(err))
		return nil, err
	}

	var csvRows []ScheduledMessageCSV
	for _, msg := range messages {
		postAtTime := time.Unix(int64(msg.PostAt), 0)
		csvRows = append(csvRows, ScheduledMessageCSV{
			ScheduledMessageID: msg.ID,
			ChannelID:          msg.Channel,
			PostAt:             postAtTime.Format(time.RFC3339),
			Text:               truncateText(msg.Text, 100),
		})
	}

	if len(csvRows) == 0 {
		return mcp.NewToolResultText("No scheduled messages found."), nil
	}

	csvBytes, err := gocsv.MarshalBytes(&csvRows)
	if err != nil {
		return nil, err
	}

	output := string(csvBytes)
	if nextCursor != "" {
		output += "\nnext_cursor: " + nextCursor
	}
	return mcp.NewToolResultText(output), nil
}

// ConversationsCancelScheduledMessageHandler cancels a pending scheduled message
func (ch *ConversationsHandler) ConversationsCancelScheduledMessageHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("ConversationsCancelScheduledMessageHandler called", zap.Any("params", request.Params))

	if ready, err := ch.apiProvider.IsReady(); !ready {
		return nil, err
	}

	params, err := ch.parseParamsToolCancelScheduledMessage(ctx, request)
	if err != nil {
		return nil, err
	}

	ch.logger.Debug("Cancelling scheduled message",
		zap.String("channel", params.channel),
		zap.String("scheduled_message_id", params.scheduledMessageID),
	)

	_, err = ch.apiProvider.Slack().DeleteScheduledMessageContext(ctx, &slack.DeleteScheduledMessageParameters{
		Channel:            params.channel,
		ScheduledMessageID: params.scheduledMessageID,
	})
	if err != nil {
		ch.logger.Error("Slack DeleteScheduledMessageContext failed", zap.Error(err))
		return nil, err
	}

	return mcp.NewToolResultText(fmt.Sprintf("Successfully cancelled scheduled message %s in channel %s", params.scheduledMessageID, params.channel)), nil
}

// truncateText truncates a string to maxLen characters, appending "..." if truncated.
func truncateText(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
```

- [ ] **Step 4: Register tools in `server.go`**

In `pkg/server/server.go`, add 3 new constants (after line 44, before `ToolUsersSearch`):

```go
	ToolConversationsScheduleMessage        = "conversations_schedule_message"
	ToolConversationsScheduledMessagesList  = "conversations_scheduled_messages_list"
	ToolConversationsCancelScheduledMessage = "conversations_cancel_scheduled_message"
```

Add them to `ValidToolNames` slice (before the closing `}`):

```go
	ToolConversationsScheduleMessage,
	ToolConversationsScheduledMessagesList,
	ToolConversationsCancelScheduledMessage,
```

Add tool registrations in `NewMCPServer` (after the `ToolConversationsMark` registration block, before `channelsHandler`):

```go
	if shouldAddTool(ToolConversationsScheduleMessage, enabledTools, "SLACK_MCP_ADD_MESSAGE_TOOL") {
		s.AddTool(mcp.NewTool(ToolConversationsScheduleMessage,
			mcp.WithDescription("Schedule a message to be sent at a future time. The message appears in the channel at the specified time."),
			mcp.WithTitleAnnotation("Schedule Message"),
			mcp.WithDestructiveHintAnnotation(true),
			mcp.WithString("channel_id",
				mcp.Required(),
				mcp.Description("ID of the channel in format Cxxxxxxxxxx or its name starting with #... or @... aka #general or @username_dm."),
			),
			mcp.WithString("thread_ts",
				mcp.Description("Unique identifier of a thread's parent message. If provided, the scheduled message is sent to the thread."),
			),
			mcp.WithString("text",
				mcp.Required(),
				mcp.Description("Message text in specified content_type format."),
			),
			mcp.WithString("content_type",
				mcp.DefaultString("text/markdown"),
				mcp.Description("Content type of the message. Default is 'text/markdown'. Allowed values: 'text/markdown', 'text/plain'."),
			),
			mcp.WithString("post_at",
				mcp.Required(),
				mcp.Description("ISO-8601 timestamp with timezone for when to send the message. Example: '2026-04-12T09:00:00+09:00'. Must be in the future and within 120 days."),
			),
		), conversationsHandler.ConversationsScheduleMessageHandler)
	}

	if shouldAddTool(ToolConversationsScheduledMessagesList, enabledTools, "") {
		s.AddTool(mcp.NewTool(ToolConversationsScheduledMessagesList,
			mcp.WithDescription("List pending scheduled messages. Optionally filter by channel."),
			mcp.WithTitleAnnotation("List Scheduled Messages"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithString("channel_id",
				mcp.Description("Optional. Filter by channel ID or name starting with #... or @..."),
			),
			mcp.WithNumber("limit",
				mcp.DefaultNumber(100),
				mcp.Description("Maximum number of results to return (1-1000). Default is 100."),
			),
			mcp.WithString("cursor",
				mcp.Description("Cursor for pagination."),
			),
		), conversationsHandler.ConversationsScheduledMessagesListHandler)
	}

	if shouldAddTool(ToolConversationsCancelScheduledMessage, enabledTools, "SLACK_MCP_ADD_MESSAGE_TOOL") {
		s.AddTool(mcp.NewTool(ToolConversationsCancelScheduledMessage,
			mcp.WithDescription("Cancel a pending scheduled message before it is sent."),
			mcp.WithTitleAnnotation("Cancel Scheduled Message"),
			mcp.WithDestructiveHintAnnotation(true),
			mcp.WithString("channel_id",
				mcp.Required(),
				mcp.Description("ID of the channel in format Cxxxxxxxxxx or its name starting with #... or @..."),
			),
			mcp.WithString("scheduled_message_id",
				mcp.Required(),
				mcp.Description("The ID of the scheduled message to cancel. Get IDs from conversations_scheduled_messages_list."),
			),
		), conversationsHandler.ConversationsCancelScheduledMessageHandler)
	}
```

- [ ] **Step 5: Add `strconv` import if not present in `conversations.go`**

Check existing imports in `conversations.go` — `strconv` is already imported (line 15). No action needed.

- [ ] **Step 6: Verify build**

Run: `go build ./...`
Expected: no errors

- [ ] **Step 7: Commit**

```bash
git add pkg/handler/conversations.go pkg/server/server.go
git commit -m "feat: add conversations_schedule_message, scheduled_messages_list, cancel_scheduled_message tools (SYS-12)"
```

---

### Task 5: Update Server Tests for Scheduled Message Tools

**Files:**
- Modify: `pkg/server/server_test.go`

- [ ] **Step 1: Update `TestValidToolNames` to include new tools**

In `pkg/server/server_test.go`, update the `expectedTools` map in `TestValidToolNames` (around line 96) to add:

```go
			ToolConversationsScheduleMessage:        true,
			ToolConversationsScheduledMessagesList:  true,
			ToolConversationsCancelScheduledMessage: true,
```

Update the constants match test (around line 122) to add:

```go
		assert.Equal(t, "conversations_schedule_message", ToolConversationsScheduleMessage)
		assert.Equal(t, "conversations_scheduled_messages_list", ToolConversationsScheduledMessagesList)
		assert.Equal(t, "conversations_cancel_scheduled_message", ToolConversationsCancelScheduledMessage)
```

- [ ] **Step 2: Add `shouldAddTool` tests for schedule tools**

After the existing `TestShouldAddTool_WriteTool_Attachment` test, add:

```go
func TestShouldAddTool_WriteTool_ScheduleMessage(t *testing.T) {
	t.Run("empty enabledTools and no env var - not registered", func(t *testing.T) {
		cleanup := setEnv("SLACK_MCP_ADD_MESSAGE_TOOL", "")
		defer cleanup()

		result := shouldAddTool(ToolConversationsScheduleMessage, []string{}, "SLACK_MCP_ADD_MESSAGE_TOOL")
		assert.False(t, result, "schedule_message should NOT be registered when env var is not set")
	})

	t.Run("empty enabledTools and env var set - registered", func(t *testing.T) {
		cleanup := setEnv("SLACK_MCP_ADD_MESSAGE_TOOL", "true")
		defer cleanup()

		result := shouldAddTool(ToolConversationsScheduleMessage, []string{}, "SLACK_MCP_ADD_MESSAGE_TOOL")
		assert.True(t, result, "schedule_message should be registered when env var is set")
	})

	t.Run("scheduled_messages_list is read-only - registered without env var", func(t *testing.T) {
		result := shouldAddTool(ToolConversationsScheduledMessagesList, []string{}, "")
		assert.True(t, result, "scheduled_messages_list should be registered as read-only tool")
	})

	t.Run("cancel uses same env var as schedule", func(t *testing.T) {
		cleanup := setEnv("SLACK_MCP_ADD_MESSAGE_TOOL", "true")
		defer cleanup()

		result := shouldAddTool(ToolConversationsCancelScheduledMessage, []string{}, "SLACK_MCP_ADD_MESSAGE_TOOL")
		assert.True(t, result, "cancel_scheduled_message should be registered when ADD_MESSAGE env var is set")
	})
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./pkg/server/... -count=1 -v 2>&1 | tail -30`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add pkg/server/server_test.go
git commit -m "test: add server tests for scheduled message tools (SYS-12)"
```

---

### Task 6: Implement Edge API `drafts.go` Client

**Files:**
- Create: `pkg/provider/edge/drafts.go`

**IMPORTANT:** This task depends on Task 1 (schema verification). The form field names and response shapes below are best-guess based on Edge API patterns. Update them to match the verified schema from Task 1.

- [ ] **Step 1: Create `pkg/provider/edge/drafts.go`**

```go
package edge

import (
	"context"
	"encoding/json"
	"runtime/trace"
)

// drafts.* API

type Draft struct {
	ID        string          `json:"id"`
	ChannelID string          `json:"channel_id"`
	ThreadTs  string          `json:"thread_ts,omitempty"`
	Text      string          `json:"text,omitempty"`
	Blocks    json.RawMessage `json:"blocks,omitempty"`
	UpdatedAt int64           `json:"date_updated"`
}

type draftsCreateForm struct {
	BaseRequest
	ChannelID string          `json:"channel_id"`
	ThreadTs  string          `json:"thread_ts,omitempty"`
	Text      string          `json:"text,omitempty"`
	Blocks    json.RawMessage `json:"blocks,omitempty"`
	WebClientFields
}

type draftsCreateResponse struct {
	baseResponse
	Draft Draft `json:"draft"`
}

func (cl *Client) DraftsCreate(ctx context.Context, channelID, threadTs, text string, blocks json.RawMessage) (*Draft, error) {
	ctx, task := trace.NewTask(ctx, "DraftsCreate")
	defer task.End()

	form := draftsCreateForm{
		BaseRequest: BaseRequest{Token: cl.token},
		ChannelID:   channelID,
		ThreadTs:    threadTs,
		Text:        text,
		Blocks:      blocks,
		WebClientFields: webclientReason("drafts/create"),
	}

	resp, err := cl.PostForm(ctx, "drafts.create", values(form, true))
	if err != nil {
		return nil, err
	}
	var r draftsCreateResponse
	if err := cl.ParseResponse(&r, resp); err != nil {
		return nil, err
	}
	if err := r.validate("drafts.create"); err != nil {
		return nil, err
	}
	return &r.Draft, nil
}

type draftsListForm struct {
	BaseRequest
	ChannelID string `json:"channel_id,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Cursor    string `json:"cursor,omitempty"`
	WebClientFields
}

type draftsListResponse struct {
	baseResponse
	Drafts     []Draft `json:"drafts"`
	NextCursor string  `json:"next_cursor,omitempty"`
}

func (cl *Client) DraftsList(ctx context.Context, channelID string, limit int, cursor string) ([]Draft, string, error) {
	ctx, task := trace.NewTask(ctx, "DraftsList")
	defer task.End()

	form := draftsListForm{
		BaseRequest: BaseRequest{Token: cl.token},
		ChannelID:   channelID,
		Limit:       limit,
		Cursor:      cursor,
		WebClientFields: webclientReason("drafts/list"),
	}

	resp, err := cl.PostForm(ctx, "drafts.list", values(form, true))
	if err != nil {
		return nil, "", err
	}
	var r draftsListResponse
	if err := cl.ParseResponse(&r, resp); err != nil {
		return nil, "", err
	}
	if err := r.validate("drafts.list"); err != nil {
		return nil, "", err
	}
	return r.Drafts, r.NextCursor, nil
}

type draftsUpdateForm struct {
	BaseRequest
	DraftID string          `json:"id"`
	Text    string          `json:"text,omitempty"`
	Blocks  json.RawMessage `json:"blocks,omitempty"`
	WebClientFields
}

type draftsUpdateResponse struct {
	baseResponse
	Draft Draft `json:"draft"`
}

func (cl *Client) DraftsUpdate(ctx context.Context, draftID, text string, blocks json.RawMessage) (*Draft, error) {
	ctx, task := trace.NewTask(ctx, "DraftsUpdate")
	defer task.End()

	form := draftsUpdateForm{
		BaseRequest: BaseRequest{Token: cl.token},
		DraftID:     draftID,
		Text:        text,
		Blocks:      blocks,
		WebClientFields: webclientReason("drafts/update"),
	}

	resp, err := cl.PostForm(ctx, "drafts.update", values(form, true))
	if err != nil {
		return nil, err
	}
	var r draftsUpdateResponse
	if err := cl.ParseResponse(&r, resp); err != nil {
		return nil, err
	}
	if err := r.validate("drafts.update"); err != nil {
		return nil, err
	}
	return &r.Draft, nil
}

type draftsDeleteForm struct {
	BaseRequest
	DraftID string `json:"id"`
	WebClientFields
}

func (cl *Client) DraftsDelete(ctx context.Context, draftID string) error {
	ctx, task := trace.NewTask(ctx, "DraftsDelete")
	defer task.End()

	form := draftsDeleteForm{
		BaseRequest: BaseRequest{Token: cl.token},
		DraftID:     draftID,
		WebClientFields: webclientReason("drafts/delete"),
	}

	resp, err := cl.PostForm(ctx, "drafts.delete", values(form, true))
	if err != nil {
		return err
	}
	var r baseResponse
	if err := cl.ParseResponse(&r, resp); err != nil {
		return err
	}
	return r.validate("drafts.delete")
}

type draftsSendForm struct {
	BaseRequest
	DraftID string `json:"id"`
	WebClientFields
}

type draftsSendResponse struct {
	baseResponse
	ChannelID string `json:"channel"`
	Timestamp string `json:"ts"`
}

func (cl *Client) DraftsSend(ctx context.Context, draftID string) (channelID, ts string, err error) {
	ctx, task := trace.NewTask(ctx, "DraftsSend")
	defer task.End()

	form := draftsSendForm{
		BaseRequest: BaseRequest{Token: cl.token},
		DraftID:     draftID,
		WebClientFields: webclientReason("drafts/send"),
	}

	resp, err := cl.PostForm(ctx, "drafts.send", values(form, true))
	if err != nil {
		return "", "", err
	}
	var r draftsSendResponse
	if err := cl.ParseResponse(&r, resp); err != nil {
		return "", "", err
	}
	if err := r.validate("drafts.send"); err != nil {
		return "", "", err
	}
	return r.ChannelID, r.Timestamp, nil
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./pkg/provider/edge/...`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add pkg/provider/edge/drafts.go
git commit -m "feat: add Edge API drafts client (create/list/update/delete/send) (SYS-12)"
```

---

### Task 7: Add Drafts Methods to `SlackAPI` Interface

**Files:**
- Modify: `pkg/provider/api.go`

- [ ] **Step 1: Add draft methods to `SlackAPI` interface**

In `pkg/provider/api.go`, add after the Edge API methods section (after `GetMutedChannels`):

```go
	// Drafts Edge API methods (xoxc/xoxd only)
	DraftsCreate(ctx context.Context, channelID, threadTs, text string, blocks json.RawMessage) (*edge.Draft, error)
	DraftsList(ctx context.Context, channelID string, limit int, cursor string) ([]edge.Draft, string, error)
	DraftsUpdate(ctx context.Context, draftID, text string, blocks json.RawMessage) (*edge.Draft, error)
	DraftsDelete(ctx context.Context, draftID string) error
	DraftsSend(ctx context.Context, draftID string) (channelID, ts string, err error)
```

Add `"encoding/json"` to the import block if not already present.

- [ ] **Step 2: Add `MCPSlackClient` implementations**

After the existing edge method implementations:

```go
func (c *MCPSlackClient) DraftsCreate(ctx context.Context, channelID, threadTs, text string, blocks json.RawMessage) (*edge.Draft, error) {
	return c.edgeClient.DraftsCreate(ctx, channelID, threadTs, text, blocks)
}

func (c *MCPSlackClient) DraftsList(ctx context.Context, channelID string, limit int, cursor string) ([]edge.Draft, string, error) {
	return c.edgeClient.DraftsList(ctx, channelID, limit, cursor)
}

func (c *MCPSlackClient) DraftsUpdate(ctx context.Context, draftID, text string, blocks json.RawMessage) (*edge.Draft, error) {
	return c.edgeClient.DraftsUpdate(ctx, draftID, text, blocks)
}

func (c *MCPSlackClient) DraftsDelete(ctx context.Context, draftID string) error {
	return c.edgeClient.DraftsDelete(ctx, draftID)
}

func (c *MCPSlackClient) DraftsSend(ctx context.Context, draftID string) (channelID, ts string, err error) {
	return c.edgeClient.DraftsSend(ctx, draftID)
}
```

- [ ] **Step 3: Verify build**

Run: `go build ./...`
Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add pkg/provider/api.go
git commit -m "feat: add drafts methods to SlackAPI interface (SYS-12)"
```

---

### Task 8: Implement Drafts MCP Handlers

**Files:**
- Create: `pkg/handler/drafts.go`
- Modify: `pkg/server/server.go` (add 5 tool constants + registrations)

- [ ] **Step 1: Create `pkg/handler/drafts.go`**

```go
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gocarina/gocsv"
	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

type DraftCSV struct {
	DraftID   string `csv:"DraftID"`
	ChannelID string `csv:"ChannelID"`
	ThreadTs  string `csv:"ThreadTs"`
	UpdatedAt string `csv:"UpdatedAt"`
	Text      string `csv:"Text"`
}

type DraftsHandler struct {
	apiProvider *provider.ApiProvider
	logger      *zap.Logger
}

func NewDraftsHandler(apiProvider *provider.ApiProvider, logger *zap.Logger) *DraftsHandler {
	return &DraftsHandler{
		apiProvider: apiProvider,
		logger:      logger,
	}
}

type draftsCreateParams struct {
	channel     string
	threadTs    string
	text        string
	contentType string
}

type draftsUpdateParams struct {
	draftID     string
	text        string
	contentType string
}

// DraftsCreateHandler creates a new draft message
func (h *DraftsHandler) DraftsCreateHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("DraftsCreateHandler called", zap.Any("params", request.Params))

	if ready, err := h.apiProvider.IsReady(); !ready {
		return nil, err
	}

	params, err := h.parseCreateParams(ctx, request)
	if err != nil {
		return nil, err
	}

	blocks, plainText, err := buildTextBlocks(h.logger, params.text, params.contentType)
	if err != nil {
		return nil, err
	}

	var blocksJSON json.RawMessage
	if blocks != nil {
		blocksJSON, err = json.Marshal(blocks)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal blocks: %w", err)
		}
	}

	textToSend := ""
	if blocks == nil {
		textToSend = plainText
	}

	h.logger.Debug("Creating draft",
		zap.String("channel", params.channel),
		zap.String("thread_ts", params.threadTs),
	)

	draft, err := h.apiProvider.Slack().DraftsCreate(ctx, params.channel, params.threadTs, textToSend, blocksJSON)
	if err != nil {
		h.logger.Error("DraftsCreate failed", zap.Error(err))
		return nil, fmt.Errorf("drafts.create failed: %w", err)
	}

	csvRows := []DraftCSV{{
		DraftID:   draft.ID,
		ChannelID: draft.ChannelID,
		ThreadTs:  draft.ThreadTs,
		UpdatedAt: time.Unix(draft.UpdatedAt, 0).Format(time.RFC3339),
		Text:      truncateText(params.text, 100),
	}}
	csvBytes, err := gocsv.MarshalBytes(&csvRows)
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(string(csvBytes)), nil
}

// DraftsListHandler lists the user's draft messages
func (h *DraftsHandler) DraftsListHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("DraftsListHandler called", zap.Any("params", request.Params))

	if ready, err := h.apiProvider.IsReady(); !ready {
		return nil, err
	}

	channelID := request.GetString("channel_id", "")
	if channelID != "" && (strings.HasPrefix(channelID, "#") || strings.HasPrefix(channelID, "@")) {
		resolved, err := h.resolveChannelID(ctx, channelID)
		if err != nil {
			return nil, err
		}
		channelID = resolved
	}

	limit := int(request.GetFloat64("limit", 50))
	if limit < 1 || limit > 200 {
		return nil, errors.New("limit must be between 1 and 200")
	}

	cursor := request.GetString("cursor", "")

	drafts, nextCursor, err := h.apiProvider.Slack().DraftsList(ctx, channelID, limit, cursor)
	if err != nil {
		h.logger.Error("DraftsList failed", zap.Error(err))
		return nil, fmt.Errorf("drafts.list failed: %w", err)
	}

	if len(drafts) == 0 {
		return mcp.NewToolResultText("No drafts found."), nil
	}

	var csvRows []DraftCSV
	for _, d := range drafts {
		csvRows = append(csvRows, DraftCSV{
			DraftID:   d.ID,
			ChannelID: d.ChannelID,
			ThreadTs:  d.ThreadTs,
			UpdatedAt: time.Unix(d.UpdatedAt, 0).Format(time.RFC3339),
			Text:      truncateText(d.Text, 100),
		})
	}

	csvBytes, err := gocsv.MarshalBytes(&csvRows)
	if err != nil {
		return nil, err
	}

	output := string(csvBytes)
	if nextCursor != "" {
		output += "\nnext_cursor: " + nextCursor
	}
	return mcp.NewToolResultText(output), nil
}

// DraftsUpdateHandler updates an existing draft's content
func (h *DraftsHandler) DraftsUpdateHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("DraftsUpdateHandler called", zap.Any("params", request.Params))

	if ready, err := h.apiProvider.IsReady(); !ready {
		return nil, err
	}

	params, err := h.parseUpdateParams(request)
	if err != nil {
		return nil, err
	}

	blocks, plainText, err := buildTextBlocks(h.logger, params.text, params.contentType)
	if err != nil {
		return nil, err
	}

	var blocksJSON json.RawMessage
	if blocks != nil {
		blocksJSON, err = json.Marshal(blocks)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal blocks: %w", err)
		}
	}

	textToSend := ""
	if blocks == nil {
		textToSend = plainText
	}

	draft, err := h.apiProvider.Slack().DraftsUpdate(ctx, params.draftID, textToSend, blocksJSON)
	if err != nil {
		h.logger.Error("DraftsUpdate failed", zap.Error(err))
		return nil, fmt.Errorf("drafts.update failed: %w", err)
	}

	csvRows := []DraftCSV{{
		DraftID:   draft.ID,
		ChannelID: draft.ChannelID,
		ThreadTs:  draft.ThreadTs,
		UpdatedAt: time.Unix(draft.UpdatedAt, 0).Format(time.RFC3339),
		Text:      truncateText(params.text, 100),
	}}
	csvBytes, err := gocsv.MarshalBytes(&csvRows)
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(string(csvBytes)), nil
}

// DraftsDeleteHandler deletes a draft
func (h *DraftsHandler) DraftsDeleteHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("DraftsDeleteHandler called", zap.Any("params", request.Params))

	if ready, err := h.apiProvider.IsReady(); !ready {
		return nil, err
	}

	draftID := request.GetString("draft_id", "")
	if draftID == "" {
		return nil, errors.New("draft_id is required")
	}

	err := h.apiProvider.Slack().DraftsDelete(ctx, draftID)
	if err != nil {
		h.logger.Error("DraftsDelete failed", zap.Error(err))
		return nil, fmt.Errorf("drafts.delete failed: %w", err)
	}

	return mcp.NewToolResultText(fmt.Sprintf("Successfully deleted draft %s", draftID)), nil
}

// DraftsSendHandler sends a draft as a real message
func (h *DraftsHandler) DraftsSendHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("DraftsSendHandler called", zap.Any("params", request.Params))

	if ready, err := h.apiProvider.IsReady(); !ready {
		return nil, err
	}

	draftID := request.GetString("draft_id", "")
	if draftID == "" {
		return nil, errors.New("draft_id is required")
	}

	channelID, ts, err := h.apiProvider.Slack().DraftsSend(ctx, draftID)
	if err != nil {
		h.logger.Error("DraftsSend failed", zap.Error(err))
		return nil, fmt.Errorf("drafts.send failed: %w", err)
	}

	// Return in same format as conversations_add_message for consistency
	messages := []Message{{
		MsgID:   ts,
		Channel: channelID,
		Cursor:  "",
	}}
	return marshalMessagesToCSV(messages)
}

// parseCreateParams validates and extracts drafts_create parameters
func (h *DraftsHandler) parseCreateParams(ctx context.Context, request mcp.CallToolRequest) (*draftsCreateParams, error) {
	toolConfig := os.Getenv("SLACK_MCP_ADD_MESSAGE_TOOL")
	enabledTools := os.Getenv("SLACK_MCP_ENABLED_TOOLS")

	if toolConfig == "" {
		if !strings.Contains(enabledTools, "drafts_create") {
			return nil, errors.New(
				"by default, the drafts_create tool is disabled. " +
					"To enable it, set the SLACK_MCP_ADD_MESSAGE_TOOL environment variable to true, 1, or comma separated list of channels",
			)
		}
		toolConfig = "true"
	}

	channel := request.GetString("channel_id", "")
	if channel == "" {
		return nil, errors.New("channel_id is required")
	}
	channel, err := h.resolveChannelID(ctx, channel)
	if err != nil {
		return nil, err
	}
	if !isChannelAllowedForConfig(channel, toolConfig) {
		return nil, fmt.Errorf("drafts_create tool is not allowed for channel %q, applied policy: %s", channel, toolConfig)
	}

	threadTs := request.GetString("thread_ts", "")
	if threadTs != "" && !strings.Contains(threadTs, ".") {
		return nil, errors.New("thread_ts must be a valid timestamp in format 1234567890.123456")
	}

	text := request.GetString("text", "")
	if text == "" {
		return nil, errors.New("text is required")
	}

	contentType := request.GetString("content_type", "text/markdown")
	if contentType != "text/plain" && contentType != "text/markdown" {
		return nil, errors.New("content_type must be either 'text/plain' or 'text/markdown'")
	}

	return &draftsCreateParams{
		channel:     channel,
		threadTs:    threadTs,
		text:        text,
		contentType: contentType,
	}, nil
}

// parseUpdateParams validates and extracts drafts_update parameters
func (h *DraftsHandler) parseUpdateParams(request mcp.CallToolRequest) (*draftsUpdateParams, error) {
	draftID := request.GetString("draft_id", "")
	if draftID == "" {
		return nil, errors.New("draft_id is required")
	}

	text := request.GetString("text", "")
	if text == "" {
		return nil, errors.New("text is required")
	}

	contentType := request.GetString("content_type", "text/markdown")
	if contentType != "text/plain" && contentType != "text/markdown" {
		return nil, errors.New("content_type must be either 'text/plain' or 'text/markdown'")
	}

	return &draftsUpdateParams{
		draftID:     draftID,
		text:        text,
		contentType: contentType,
	}, nil
}

// resolveChannelID resolves channel names (#general, @user) to IDs.
// Delegates to the same logic as ConversationsHandler.
func (h *DraftsHandler) resolveChannelID(ctx context.Context, channel string) (string, error) {
	if !strings.HasPrefix(channel, "#") && !strings.HasPrefix(channel, "@") {
		return channel, nil
	}

	channelsMaps := h.apiProvider.ProvideChannelsMaps()
	chn, ok := channelsMaps.ChannelsInv[channel]
	if ok {
		return channelsMaps.Channels[chn].ID, nil
	}

	h.logger.Debug("Channel not found in cache, attempting refresh",
		zap.String("channel", channel))

	refreshErr := h.apiProvider.ForceRefreshChannels(ctx)
	if refreshErr != nil {
		return "", fmt.Errorf("channel %q not found and cache refresh failed: %w", channel, refreshErr)
	}

	channelsMaps = h.apiProvider.ProvideChannelsMaps()
	chn, ok = channelsMaps.ChannelsInv[channel]
	if !ok {
		return "", fmt.Errorf("channel %q not found", channel)
	}

	return channelsMaps.Channels[chn].ID, nil
}
```

- [ ] **Step 2: Register drafts tools in `server.go`**

Add 5 new constants:

```go
	ToolDraftsCreate = "drafts_create"
	ToolDraftsList   = "drafts_list"
	ToolDraftsUpdate = "drafts_update"
	ToolDraftsDelete = "drafts_delete"
	ToolDraftsSend   = "drafts_send"
```

Add to `ValidToolNames`:

```go
	ToolDraftsCreate,
	ToolDraftsList,
	ToolDraftsUpdate,
	ToolDraftsDelete,
	ToolDraftsSend,
```

Add tool registrations in `NewMCPServer` (after `conversationsHandler` initialization, before the search tool). Create the `draftsHandler`:

```go
	draftsHandler := handler.NewDraftsHandler(provider, logger)

	if !provider.IsBotToken() && shouldAddTool(ToolDraftsCreate, enabledTools, "SLACK_MCP_ADD_MESSAGE_TOOL") {
		s.AddTool(mcp.NewTool(ToolDraftsCreate,
			mcp.WithDescription("Create a draft message in Slack. The draft appears in the user's Drafts sidebar and can be edited or sent later. Requires browser session tokens (xoxc/xoxd)."),
			mcp.WithTitleAnnotation("Create Draft"),
			mcp.WithDestructiveHintAnnotation(true),
			mcp.WithString("channel_id",
				mcp.Required(),
				mcp.Description("ID of the channel in format Cxxxxxxxxxx or its name starting with #... or @... aka #general or @username_dm."),
			),
			mcp.WithString("thread_ts",
				mcp.Description("Unique identifier of a thread's parent message. If provided, the draft is for a thread reply."),
			),
			mcp.WithString("text",
				mcp.Required(),
				mcp.Description("Draft message text in specified content_type format."),
			),
			mcp.WithString("content_type",
				mcp.DefaultString("text/markdown"),
				mcp.Description("Content type. Default is 'text/markdown'. Allowed: 'text/markdown', 'text/plain'."),
			),
		), draftsHandler.DraftsCreateHandler)
	}

	if !provider.IsBotToken() && shouldAddTool(ToolDraftsList, enabledTools, "") {
		s.AddTool(mcp.NewTool(ToolDraftsList,
			mcp.WithDescription("List your draft messages. Optionally filter by channel. Requires browser session tokens (xoxc/xoxd)."),
			mcp.WithTitleAnnotation("List Drafts"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithString("channel_id",
				mcp.Description("Optional. Filter drafts by channel ID or name."),
			),
			mcp.WithNumber("limit",
				mcp.DefaultNumber(50),
				mcp.Description("Maximum number of drafts to return (1-200). Default is 50."),
			),
			mcp.WithString("cursor",
				mcp.Description("Cursor for pagination."),
			),
		), draftsHandler.DraftsListHandler)
	}

	if !provider.IsBotToken() && shouldAddTool(ToolDraftsUpdate, enabledTools, "SLACK_MCP_ADD_MESSAGE_TOOL") {
		s.AddTool(mcp.NewTool(ToolDraftsUpdate,
			mcp.WithDescription("Update an existing draft message's content. Get draft IDs from drafts_list. Requires browser session tokens (xoxc/xoxd)."),
			mcp.WithTitleAnnotation("Update Draft"),
			mcp.WithDestructiveHintAnnotation(true),
			mcp.WithString("draft_id",
				mcp.Required(),
				mcp.Description("ID of the draft to update. Get IDs from drafts_list."),
			),
			mcp.WithString("text",
				mcp.Required(),
				mcp.Description("New draft message text in specified content_type format."),
			),
			mcp.WithString("content_type",
				mcp.DefaultString("text/markdown"),
				mcp.Description("Content type. Default is 'text/markdown'. Allowed: 'text/markdown', 'text/plain'."),
			),
		), draftsHandler.DraftsUpdateHandler)
	}

	if !provider.IsBotToken() && shouldAddTool(ToolDraftsDelete, enabledTools, "SLACK_MCP_ADD_MESSAGE_TOOL") {
		s.AddTool(mcp.NewTool(ToolDraftsDelete,
			mcp.WithDescription("Delete a draft message. Get draft IDs from drafts_list. Requires browser session tokens (xoxc/xoxd)."),
			mcp.WithTitleAnnotation("Delete Draft"),
			mcp.WithDestructiveHintAnnotation(true),
			mcp.WithString("draft_id",
				mcp.Required(),
				mcp.Description("ID of the draft to delete."),
			),
		), draftsHandler.DraftsDeleteHandler)
	}

	if !provider.IsBotToken() && shouldAddTool(ToolDraftsSend, enabledTools, "SLACK_MCP_ADD_MESSAGE_TOOL") {
		s.AddTool(mcp.NewTool(ToolDraftsSend,
			mcp.WithDescription("Send a draft message, posting it to its target channel/thread. The draft is removed from the Drafts sidebar. Returns the sent message's channel and timestamp. Requires browser session tokens (xoxc/xoxd)."),
			mcp.WithTitleAnnotation("Send Draft"),
			mcp.WithDestructiveHintAnnotation(true),
			mcp.WithString("draft_id",
				mcp.Required(),
				mcp.Description("ID of the draft to send."),
			),
		), draftsHandler.DraftsSendHandler)
	}
```

- [ ] **Step 3: Verify build**

Run: `go build ./...`
Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add pkg/handler/drafts.go pkg/server/server.go
git commit -m "feat: add drafts_create/list/update/delete/send MCP tools (SYS-12)"
```

---

### Task 9: Update Server Tests for Draft Tools

**Files:**
- Modify: `pkg/server/server_test.go`

- [ ] **Step 1: Update `TestValidToolNames`**

Add to `expectedTools` map:

```go
			ToolDraftsCreate:                        true,
			ToolDraftsList:                          true,
			ToolDraftsUpdate:                        true,
			ToolDraftsDelete:                        true,
			ToolDraftsSend:                          true,
```

Add constant assertions:

```go
		assert.Equal(t, "drafts_create", ToolDraftsCreate)
		assert.Equal(t, "drafts_list", ToolDraftsList)
		assert.Equal(t, "drafts_update", ToolDraftsUpdate)
		assert.Equal(t, "drafts_delete", ToolDraftsDelete)
		assert.Equal(t, "drafts_send", ToolDraftsSend)
```

- [ ] **Step 2: Add draft-specific `shouldAddTool` tests**

```go
func TestShouldAddTool_WriteTool_Drafts(t *testing.T) {
	t.Run("drafts_create uses ADD_MESSAGE env var", func(t *testing.T) {
		cleanup := setEnv("SLACK_MCP_ADD_MESSAGE_TOOL", "true")
		defer cleanup()

		result := shouldAddTool(ToolDraftsCreate, []string{}, "SLACK_MCP_ADD_MESSAGE_TOOL")
		assert.True(t, result, "drafts_create should be registered when ADD_MESSAGE env var is set")
	})

	t.Run("drafts_create not registered without env var", func(t *testing.T) {
		cleanup := setEnv("SLACK_MCP_ADD_MESSAGE_TOOL", "")
		defer cleanup()

		result := shouldAddTool(ToolDraftsCreate, []string{}, "SLACK_MCP_ADD_MESSAGE_TOOL")
		assert.False(t, result, "drafts_create should NOT be registered without env var")
	})

	t.Run("drafts_list is read-only - registered without env var", func(t *testing.T) {
		result := shouldAddTool(ToolDraftsList, []string{}, "")
		assert.True(t, result, "drafts_list should be registered as read-only tool")
	})

	t.Run("drafts_send uses ADD_MESSAGE env var", func(t *testing.T) {
		cleanup := setEnv("SLACK_MCP_ADD_MESSAGE_TOOL", "true")
		defer cleanup()

		result := shouldAddTool(ToolDraftsSend, []string{}, "SLACK_MCP_ADD_MESSAGE_TOOL")
		assert.True(t, result, "drafts_send should be registered when ADD_MESSAGE env var is set")
	})
}
```

- [ ] **Step 3: Run all server tests**

Run: `go test ./pkg/server/... -count=1 -v 2>&1 | tail -30`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add pkg/server/server_test.go
git commit -m "test: add server tests for draft tools (SYS-12)"
```

---

### Task 10: Update `slack-mcp` Skill

**Files:**
- Modify: skill definition file for `slack-mcp` (exact path TBD — check `~/.claude/skills/` or the skill's source location)

- [ ] **Step 1: Locate the skill file**

Run: `find ~/.claude -name "*.md" -path "*slack-mcp*" | head -10`

- [ ] **Step 2: Add new tools to the tool list section**

Add to the list of recognized tools:
```
drafts_create
drafts_list
drafts_update
drafts_delete
drafts_send
conversations_schedule_message
conversations_scheduled_messages_list
conversations_cancel_scheduled_message
```

- [ ] **Step 3: Add signature rules for write tools**

Add `drafts_create`, `drafts_update`, `drafts_send`, and `conversations_schedule_message` to the list of tools that require Claude signature. Read-only tools (`drafts_list`, `conversations_scheduled_messages_list`) are excluded.

- [ ] **Step 4: Add token type note**

Add note: "drafts_* tools require browser session tokens (xoxc/xoxd). They are automatically unavailable with bot tokens (xoxb)."

- [ ] **Step 5: Commit**

```bash
git add <skill-file-path>
git commit -m "feat: update slack-mcp skill for draft and scheduled message tools (SYS-12)"
```

---

### Task 11: Full Build & Test Verification

- [ ] **Step 1: Run full build**

Run: `go build ./...`
Expected: no errors

- [ ] **Step 2: Run full test suite**

Run: `go test ./... -count=1 -short 2>&1 | tail -30`
Expected: all PASS

- [ ] **Step 3: Verify tool count**

Run: `grep -c 'Tool[A-Z]' pkg/server/server.go`
Expected: should show the total number of tool constants (original 17 + 8 new = 25)

- [ ] **Step 4: Verify ValidToolNames count**

Run: `go test ./pkg/server/... -run TestValidToolNames -v`
Expected: PASS — confirms all 25 tools are registered

- [ ] **Step 5: Commit any final fixes if needed**

Only if previous steps surfaced issues.
