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
	"github.com/google/uuid"
	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
)

type DraftCSV struct {
	DraftID   string `csv:"DraftID"`
	ChannelID string `csv:"ChannelID"`
	ThreadTs  string `csv:"ThreadTs"`
	UpdatedAt string `csv:"UpdatedAt"`
	Text      string `csv:"Text"`
}

type draftDestinationInput struct {
	ChannelID string `json:"channel_id"`
	ThreadTs  string `json:"thread_ts,omitempty"`
	Broadcast *bool  `json:"broadcast,omitempty"`
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

// DraftsCreateHandler creates a new draft message
func (h *DraftsHandler) DraftsCreateHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("DraftsCreateHandler called", zap.Any("params", request.Params))

	if ready, err := h.apiProvider.IsReady(); !ready {
		return nil, err
	}

	// Parse and validate params
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

	blocksJSON, err := buildRichTextBlockJSON(text)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal blocks: %w", err)
	}

	destJSON, err := buildDestinationsJSON(channel, threadTs)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal destinations: %w", err)
	}

	dateScheduled, err := parseScheduleAt(request.GetString("schedule_at", ""))
	if err != nil {
		return nil, err
	}

	clientMsgID := uuid.New().String()

	h.logger.Debug("Creating draft",
		zap.String("channel", channel),
		zap.String("thread_ts", threadTs),
		zap.Int64("date_scheduled", dateScheduled),
	)

	draft, err := h.apiProvider.Slack().DraftsCreate(ctx, string(blocksJSON), clientMsgID, string(destJSON), dateScheduled)
	if err != nil {
		h.logger.Error("DraftsCreate failed", zap.Error(err))
		// Provide actionable guidance when a draft already exists for this channel
		if strings.Contains(err.Error(), "attached_draft_exists") {
			return nil, fmt.Errorf("a draft already exists for this channel. Use drafts_update with the existing draft's draft_id and client_last_updated_ts to modify it, or delete it first with drafts_delete")
		}
		return nil, fmt.Errorf("drafts.create failed: %w", err)
	}

	// Extract channel from destinations for CSV
	draftChannel := channel
	draftThreadTs := threadTs
	if len(draft.Destinations) > 0 {
		draftChannel = draft.Destinations[0].ChannelID
	}

	csvRows := []DraftCSV{{
		DraftID:   draft.ID,
		ChannelID: draftChannel,
		ThreadTs:  draftThreadTs,
		UpdatedAt: draft.LastUpdatedTs,
		Text:      truncateText(text, 100),
	}}
	csvBytes, err := gocsv.MarshalBytes(&csvRows)
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(string(csvBytes)), nil
}

// DraftsUpdateHandler updates an existing draft's content
func (h *DraftsHandler) DraftsUpdateHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("DraftsUpdateHandler called", zap.Any("params", request.Params))

	if ready, err := h.apiProvider.IsReady(); !ready {
		return nil, err
	}

	toolConfig := os.Getenv("SLACK_MCP_ADD_MESSAGE_TOOL")
	enabledTools := os.Getenv("SLACK_MCP_ENABLED_TOOLS")
	if toolConfig == "" {
		if !strings.Contains(enabledTools, "drafts_update") {
			return nil, errors.New(
				"by default, the drafts_update tool is disabled. " +
					"To enable it, set the SLACK_MCP_ADD_MESSAGE_TOOL environment variable to true, 1, or comma separated list of channels",
			)
		}
		toolConfig = "true"
	}

	draftID := request.GetString("draft_id", "")
	if draftID == "" {
		return nil, errors.New("draft_id is required")
	}

	clientLastUpdatedTs := request.GetString("client_last_updated_ts", "")
	if clientLastUpdatedTs == "" {
		return nil, errors.New("client_last_updated_ts is required (from drafts_create response's last_updated_ts)")
	}

	text := request.GetString("text", "")
	if text == "" {
		return nil, errors.New("text is required")
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
		return nil, fmt.Errorf("drafts_update tool is not allowed for channel %q, applied policy: %s", channel, toolConfig)
	}

	threadTs := request.GetString("thread_ts", "")

	blocksJSON, err := buildRichTextBlockJSON(text)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal blocks: %w", err)
	}

	destJSON, err := buildDestinationsJSON(channel, threadTs)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal destinations: %w", err)
	}

	clientMsgID := uuid.New().String()

	dateScheduled, err := parseScheduleAt(request.GetString("schedule_at", ""))
	if err != nil {
		return nil, err
	}

	lastUpdatedMs := convertTsToMillis(clientLastUpdatedTs)

	draft, err := h.apiProvider.Slack().DraftsUpdate(ctx, draftID, lastUpdatedMs, string(blocksJSON), clientMsgID, string(destJSON), dateScheduled)
	if err != nil {
		h.logger.Error("DraftsUpdate failed", zap.Error(err))
		return nil, fmt.Errorf("drafts.update failed: %w", err)
	}

	draftChannel := channel
	if len(draft.Destinations) > 0 {
		draftChannel = draft.Destinations[0].ChannelID
	}

	csvRows := []DraftCSV{{
		DraftID:   draft.ID,
		ChannelID: draftChannel,
		ThreadTs:  threadTs,
		UpdatedAt: draft.LastUpdatedTs,
		Text:      truncateText(text, 100),
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

	toolConfig := os.Getenv("SLACK_MCP_ADD_MESSAGE_TOOL")
	enabledTools := os.Getenv("SLACK_MCP_ENABLED_TOOLS")
	if toolConfig == "" {
		if !strings.Contains(enabledTools, "drafts_delete") {
			return nil, errors.New(
				"by default, the drafts_delete tool is disabled. " +
					"To enable it, set the SLACK_MCP_ADD_MESSAGE_TOOL environment variable to true, 1, or comma separated list of channels",
			)
		}
	}

	draftID := request.GetString("draft_id", "")
	if draftID == "" {
		return nil, errors.New("draft_id is required")
	}

	clientLastUpdatedTs := request.GetString("client_last_updated_ts", "")
	if clientLastUpdatedTs == "" {
		return nil, errors.New("client_last_updated_ts is required (from drafts_create response's last_updated_ts)")
	}

	lastUpdatedMs := convertTsToMillis(clientLastUpdatedTs)

	err := h.apiProvider.Slack().DraftsDelete(ctx, draftID, lastUpdatedMs)
	if err != nil {
		h.logger.Error("DraftsDelete failed", zap.Error(err))
		return nil, fmt.Errorf("drafts.delete failed: %w", err)
	}

	return mcp.NewToolResultText(fmt.Sprintf("Successfully deleted draft %s", draftID)), nil
}

// resolveChannelID resolves channel names (#general, @user) to IDs.
func (h *DraftsHandler) resolveChannelID(ctx context.Context, channel string) (string, error) {
	return resolveChannelIDWithProvider(ctx, h.apiProvider, h.logger, channel)
}

// buildRichTextBlockJSON converts markdown-formatted text to Slack rich_text block JSON.
// The Edge API drafts endpoints ONLY accept rich_text blocks (not header, section, etc.).
// Supports: **bold**, _italic_, `code`, ~~strike~~, bullet/ordered lists, blockquotes,
// code blocks, and URLs.
func buildRichTextBlockJSON(text string) ([]byte, error) {
	return markdownToRichTextJSON(text)
}

// buildDestinationsJSON creates the JSON destinations array for the Edge API.
func buildDestinationsJSON(channelID, threadTs string) ([]byte, error) {
	dest := draftDestinationInput{ChannelID: channelID}
	if threadTs != "" {
		dest.ThreadTs = threadTs
		b := false
		dest.Broadcast = &b
	}
	return json.Marshal([]draftDestinationInput{dest})
}

// parseScheduleAt parses an optional ISO-8601 schedule_at string and returns a Unix timestamp.
// Returns 0 if the input is empty (no scheduling). Returns an error for invalid/past/too-far-future values.
func parseScheduleAt(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return 0, fmt.Errorf("invalid schedule_at: must be ISO-8601 with timezone (RFC3339), got %q: %w", raw, err)
	}
	now := time.Now()
	if t.Before(now) {
		return 0, errors.New("schedule_at must be in the future")
	}
	if t.After(now.AddDate(0, 0, maxScheduleDays)) {
		return 0, fmt.Errorf("schedule_at must be within %d days from now", maxScheduleDays)
	}
	return t.Unix(), nil
}

// convertTsToMillis converts a Slack last_updated_ts value to the millisecond format
// expected by the Edge API's client_last_updated_ts field.
// Example: "1775966853.146997" → "1775966853146" (takes first 3 chars of fractional part).
// If the input doesn't contain a dot, it's returned as-is (assumed already in ms).
func convertTsToMillis(ts string) string {
	parts := strings.SplitN(ts, ".", 2)
	if len(parts) != 2 {
		return ts
	}
	// Take first 3 digits of fractional part for milliseconds
	frac := parts[1]
	if len(frac) > 3 {
		frac = frac[:3]
	}
	for len(frac) < 3 {
		frac += "0"
	}
	return parts[0] + frac
}
