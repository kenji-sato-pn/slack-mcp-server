# Design: Draft Messages & Scheduled Messages for slack-mcp-server

**Date**: 2026-04-12
**Linear**: [SYS-12](https://linear.app/primenumber-systemn/issue/SYS-12/slack-mcp-server-draft-message-and-scheduled-message-ツール追加)
**Status**: Approved (pending spec review)

---

## 1. Scope & Goals

Add 6 MCP tools to slack-mcp-server for draft messages (Slack Edge API) and scheduled messages (official Slack API). Originally planned 8, but `drafts_list` and `drafts_send` were removed after Edge API verification confirmed those endpoints don't exist.

### New Tools

| Tool Name | API | Purpose |
|---|---|---|
| `drafts_create` | Edge: `drafts.create` | Create a draft in Slack sidebar |
| `drafts_list` | Edge: `drafts.list` | List own drafts |
| `drafts_update` | Edge: `drafts.update` | Update draft body |
| `drafts_delete` | Edge: `drafts.delete` | Delete a draft |
| `drafts_send` | Edge: `drafts.send` | Send a draft as a real message |
| `conversations_schedule_message` | `chat.scheduleMessage` | Schedule a message at ISO-8601 time |
| `conversations_scheduled_messages_list` | `chat.scheduledMessages.list` | List pending scheduled messages |
| `conversations_cancel_scheduled_message` | `chat.deleteScheduledMessage` | Cancel a scheduled message |

### Enable Gate

All 8 tools gated behind existing `SLACK_MCP_ADD_MESSAGE_TOOL` env var (same opt-in pattern as `conversations_add_message`).

### Token Gate

`drafts_*` tools are **automatically unregistered** when `IsBotToken() == true` (same pattern as `conversations_search_messages` / `conversations_unreads`). Scheduled message tools work with both bot and user tokens.

### Out of Scope (YAGNI)

- Scheduled message update (no Slack API for this)
- Draft attachment/file upload (use existing `files_upload` separately)
- Operating on other users' drafts
- Draft prefs (unfurl settings etc.)

### Success Criteria

- All 8 tools operational via MCP; `drafts_*` absent from tool list when `IsBotToken() == true`
- Markdown input converted to Block Kit blocks via same pipeline as `conversations_add_message`
- ISO-8601 input for `conversations_schedule_message` correctly converted to Unix seconds
- `slack-mcp` skill recognizes new tool names and applies Claude signature rules

---

## 2. Architecture & Code Layout

### New Files

```
pkg/provider/edge/drafts.go       Edge API wrappers (5 functions + types)
pkg/provider/edge/drafts_test.go   httptest-based unit tests
pkg/handler/drafts.go              DraftsHandler struct + 5 MCP handlers + parse helpers
pkg/handler/drafts_test.go         Handler unit tests
```

### Modified Files

```
pkg/handler/conversations.go       +3 scheduled message handlers + parse helpers
pkg/handler/conversations_test.go   +scheduled message handler tests
pkg/server/server.go               +8 tool registrations, ValidToolNames extension
pkg/server/server_test.go          +bot token gate tests, ValidToolNames tests
```

### Dependency Graph

```
server.go
  ├── handler/drafts.go
  │     └── provider/edge/drafts.go   (Edge API: xoxc/xoxd only)
  │     └── handler/message_content.go (shared markdown→blocks)
  │
  └── handler/conversations.go
        └── handler/message_content.go (shared markdown→blocks)
        └── slack-go (chat.scheduleMessage, etc.)
```

### Key Design Decisions

**1. markdown→blocks pipeline extraction**

The markdown-to-Block Kit conversion logic currently lives inline in `ConversationsAddMessageHandler` (`conversations.go:234-246`). Extract it into a shared package-private function:

```go
// pkg/handler/message_content.go (or within conversations.go as private func)

// buildTextBlocks converts text + contentType into Block Kit blocks.
// Returns (blocks, plainFallbackText, error).
// - For scheduled/add_message: caller wraps blocks into slack.MsgOption
// - For drafts (Edge API): caller serializes blocks to JSON for the Edge form field
func buildTextBlocks(logger *zap.Logger, text, contentType string) ([]slack.Block, string, error)
```

`conversations_add_message`, `drafts_create`, `drafts_update`, and `conversations_schedule_message` all call this function. The caller is responsible for adapting the blocks to its API's expected format (slack.MsgOptionBlocks for official API, JSON-encoded blocks for Edge API). Behavior of `add_message` is unchanged.

**2. Edge API `drafts.*` schema verification (Step 1)**

Before implementation, verify via browser devtools:
- Endpoint URLs and form field names
- Whether `blocks` parameter is accepted
- Draft ID field name in response
- Timestamp representation

Results appended to the "Edge API Verified Schema" section at the end of this spec.

**3. ISO-8601 time parsing**

`parseParamsToolScheduleMessage` converts ISO-8601 to Unix seconds:
```go
t, err := time.Parse(time.RFC3339, postAt)
// timezone-less input → error
// past time → error
// >120 days in future → error (Slack constraint)
postAtUnix := t.Unix()
```

---

## 3. Tool Interfaces

### drafts_create

| Parameter | Required | Description |
|---|---|---|
| `channel_id` | Yes | `Cxxxxxxxxxx` / `#general` / `@username_dm` |
| `thread_ts` | No | Thread draft if specified |
| `text` | Yes | Message body |
| `content_type` | No | `text/markdown` (default) / `text/plain` |

**Flow**: parse → resolveChannelID → buildTextOptions → edge.DraftsCreate → return CSV (draft_id, channel_id, thread_ts, preview)

### drafts_list

| Parameter | Required | Description |
|---|---|---|
| `channel_id` | No | Filter by channel |
| `limit` | No | Default 50, max 200 |
| `cursor` | No | Pagination |

**Returns**: CSV (draft_id, channel_id, thread_ts, updated_at, preview) + next_cursor

### drafts_update

| Parameter | Required | Description |
|---|---|---|
| `draft_id` | Yes | Draft to update |
| `text` | Yes | New body |
| `content_type` | No | `text/markdown` (default) / `text/plain` |

**Returns**: Updated draft as 1-row CSV.

### drafts_delete

| Parameter | Required | Description |
|---|---|---|
| `draft_id` | Yes | Draft to delete |

**Returns**: `Successfully deleted draft <id>`

### drafts_send

| Parameter | Required | Description |
|---|---|---|
| `draft_id` | Yes | Draft to send |

**Returns**: CSV of sent message (channel_id, ts) — same format as `conversations_add_message` output, so LLM can use ts for follow-up thread replies.

### conversations_schedule_message

| Parameter | Required | Description |
|---|---|---|
| `channel_id` | Yes | Target channel |
| `thread_ts` | No | Thread schedule |
| `text` | Yes | Message body |
| `content_type` | No | `text/markdown` (default) / `text/plain` |
| `post_at` | Yes | ISO-8601 with timezone (e.g. `2026-04-12T09:00:00+09:00`) |

**Validation**: post_at must be future, within 120 days.
**Returns**: CSV (scheduled_message_id, channel_id, post_at as ISO-8601, preview)

### conversations_scheduled_messages_list

| Parameter | Required | Description |
|---|---|---|
| `channel_id` | No | Filter by channel |
| `limit` | No | Default 100 |
| `cursor` | No | Pagination |

**Returns**: CSV (scheduled_message_id, channel_id, post_at, preview) + next_cursor

### conversations_cancel_scheduled_message

| Parameter | Required | Description |
|---|---|---|
| `channel_id` | Yes | Channel of the scheduled message |
| `scheduled_message_id` | Yes | ID to cancel |

**Returns**: `Successfully cancelled scheduled message <id>`

### Annotations

- **Destructive**: drafts_create, drafts_update, drafts_delete, drafts_send, conversations_schedule_message, conversations_cancel_scheduled_message
- **ReadOnly**: drafts_list, conversations_scheduled_messages_list

---

## 4. Test Strategy

### Edge Client Tests (`pkg/provider/edge/drafts_test.go`)

- `TestDraftsCreate`: httptest mock server, verify form encoding and response parsing
- `TestDraftsList` / `Update` / `Delete` / `Send`: same pattern
- Error responses: `not_authed`, `missing_scope`

### Handler Tests (`pkg/handler/drafts_test.go`)

- `TestDraftsCreateHandler`: apiProvider mock, verify channel resolution + Edge API call arguments
- `TestDraftsCreateHandler_MarkdownConversion`: markdown→blocks via shared pipeline
- `TestDraftsCreateHandler_InvalidParams`: missing required params
- `TestDraftsListHandler_Pagination`: cursor behavior

### Scheduled Message Tests (`pkg/handler/conversations_test.go` additions)

- `TestConversationsScheduleMessageHandler_ISO8601Parse`: valid ISO-8601, missing tz → error, past time → error, 120d+ → error
- `TestConversationsScheduleMessageHandler_ChannelResolve`
- `TestConversationsScheduledMessagesListHandler`
- `TestConversationsCancelScheduledMessageHandler`

### Server Tests (`pkg/server/server_test.go` additions)

- `TestValidateEnabledTools_IncludesNewTools`: 8 new names in ValidToolNames
- `TestNewMCPServer_DraftsSkippedOnBotToken`: IsBotToken()=true → drafts_* not registered
- `TestNewMCPServer_ScheduledMessageRegisteredOnBotToken`: schedule tools registered regardless

### Mock Strategy

Follow existing mock patterns in `pkg/provider/api_*_test.go` and `pkg/handler/conversations_test.go`. No new mock framework.

---

## 5. Error Handling

| Error | Layer | Behavior |
|---|---|---|
| ISO-8601 parse failure | handler | `invalid post_at: must be ISO-8601 with timezone, got "<input>"` |
| `post_at` in the past | handler | `post_at must be in the future` |
| `post_at` > 120 days | handler | `post_at must be within 120 days from now` |
| Channel resolution failure | handler | Existing `resolveChannelID` error propagated |
| Markdown→blocks conversion failure | handler | Warn log + plain text fallback (existing pattern) |
| Edge API `not_authed` | handler | `drafts_* tools require a stealth mode token (xoxc/xoxd); current token is insufficient` |
| Edge API other 4xx/5xx | handler | `drafts.<op> failed: <wrapped error>` |
| Official API errors | handler | slack-go error wrapped and returned |

All handler errors are converted to `isError` ToolResults by `buildErrorRecoveryMiddleware` (server.go:603).

### Silent Failure Prevention

- Markdown fallback always logs at warn level
- "channel_id resolved to 0 matches" → error, not empty result
- Edge API partial success (e.g. create ok but re-fetch fails) → return success info with explicit note about re-fetch failure

---

## 6. Skill Update

Update `slack-mcp` skill definition:

1. **Tool list section**: Add all 8 new tool names
2. **Signature rules**: Mark `drafts_create`, `drafts_update`, `drafts_send`, `conversations_schedule_message` as requiring Claude signature (read-only tools excluded)
3. **Token notes**: Add "drafts_* requires xoxc/xoxd token" caveat

This is part of the same PR as the code changes.

---

## 7. Implementation Steps

| Step | Description | Blocking? |
|---|---|---|
| 1 | Edge API `drafts.*` schema verification (browser devtools) | Yes — determines blocks support |
| 2 | Extract markdown→blocks pipeline to shared function (refactor) | No |
| 3 | Implement `pkg/provider/edge/drafts.go` + tests | Blocked by Step 1 |
| 4 | Implement `pkg/handler/drafts.go` + tests + server.go registration | Blocked by Steps 2, 3 |
| 5 | Implement scheduled message 3 handlers + tests + server.go registration | Blocked by Step 2 |
| 6 | Update `slack-mcp` skill | After Steps 4, 5 |
| 7 | End-to-end verification (build, test, live workspace) | After all |

Steps 4 and 5 can run in parallel once Step 2 completes.

---

## 8. Risks & Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Edge API `drafts.*` schema differs or doesn't exist | Drafts feature blocked | Step 1 early verification; fallback: ship schedule_* only |
| `blocks` parameter not supported in drafts | Reduced formatting | Drafts content_type falls back to plain only |
| Edge API rate limits / session expiry | Runtime errors | Reuse existing `limiter` package; error message prompts token refresh |
| Skill update missed | Claude signature not applied | Step 6 explicit task; Step 7 verifies with live test |
| Regression in `add_message` from Step 2 refactor | Existing feature broken | Run `go test ./pkg/handler/... -run AddMessage` after Step 2 |
| 120-day constraint changes | Future errors | Validation limit as named const; error message attributes to Slack |

---

## 9. Future Extensions (not in scope)

- Draft attachment/file upload
- Scheduled message body update (blocked by Slack API)
- Other users' draft access
- Draft unfurl/prefs control

---

## Edge API Verified Schema

**Verified on 2026-04-12 via browser devtools + direct API calls.**

### Endpoints that EXIST

#### `drafts.create`

**Form fields:**
- `token` (xoxc)
- `blocks` (JSON Block Kit array) — **confirmed working**
- `client_msg_id` (UUID, client-generated)
- `destinations` (JSON array): `[{"channel_id":"Cxxxxxxxxxx"}]` or `[{"channel_id":"Cxxxxxxxxxx","thread_ts":"...","broadcast":false}]` for threads
- `attachments` (empty string)
- `file_ids` (JSON array, `[]`)
- `is_from_composer` (boolean)
- WebClient fields

**Response:**
```json
{
  "ok": true,
  "draft": {
    "id": "Dr0ASEPK1J2G",
    "date_created": 1775966853,
    "user_id": "U06RAF55PU7",
    "team_id": "T0DQM7876",
    "last_updated_ts": "1775966853.146997",
    "blocks": [...],
    "file_ids": [],
    "is_from_composer": false,
    "is_deleted": false,
    "is_sent": false,
    "client_msg_id": "02fe8d79-ad72-467e-b88e-8bca3de4c7f2",
    "date_scheduled": 0,
    "destinations": [{"channel_id": "D06SDRG4FTJ", "user_ids": ["U06RAF55PU7"]}]
  },
  "files": []
}
```

**Key:** Draft ID format is `Dr` + alphanumeric (e.g., `Dr0ASEPK1J2G`).

#### `drafts.update`

**Form fields (same as create, PLUS):**
- `draft_id` (required, e.g., `Dr0ASEPK1J2G`)
- `client_last_updated_ts` (required, millisecond timestamp)
- Plus all fields from create (`blocks`, `client_msg_id`, `destinations`, etc.)

**Response:** Same shape as `drafts.create` (returns updated draft object).

#### `drafts.delete`

**Form fields:**
- `token` (xoxc)
- `draft_id` (required)
- `client_last_updated_ts` (required)
- `skip_file_deletion` (boolean)
- WebClient fields

**Response:** `{"ok": true}`

### Endpoints that DO NOT EXIST

- **`drafts.list`** — Drafts are managed client-side (localStorage). No server API.
- **`drafts.send`** — Sending a draft uses regular `chat.postMessage` followed by `drafts.delete`.

### Design Impact

1. **`drafts_list` tool: REMOVED** — Cannot implement without server API.
2. **`drafts_send` tool: REMOVED** — No dedicated endpoint. Users can use `conversations_add_message` + `drafts_delete` separately.
3. **`blocks` parameter: CONFIRMED** — `buildTextBlocks` output can be JSON-serialized directly.
4. **`destinations` pattern**: Channel is inside a JSON array, not a top-level field. Thread drafts include `thread_ts` and `broadcast` in the destination object.
5. **`drafts.update` requires**: `draft_id` + `client_last_updated_ts` + full content (blocks/destinations). The `client_last_updated_ts` comes from the create/update response's `last_updated_ts` field (converted to milliseconds).
