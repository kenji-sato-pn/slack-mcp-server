package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

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

	contentType := request.GetString("content_type", "text/markdown")
	if contentType != "text/plain" && contentType != "text/markdown" {
		return nil, errors.New("content_type must be either 'text/plain' or 'text/markdown'")
	}

	// Convert text to blocks
	blocks, _, err := buildTextBlocks(h.logger, text, contentType)
	if err != nil {
		return nil, err
	}

	// For plain text fallback, wrap in a rich_text block
	var blocksJSON []byte
	if blocks != nil {
		blocksJSON, err = json.Marshal(blocks)
	} else {
		// Wrap plain text in rich_text block (Slack requires blocks, not raw text)
		plainBlock := []map[string]any{{
			"type": "rich_text",
			"elements": []map[string]any{{
				"type": "rich_text_section",
				"elements": []map[string]any{{
					"type": "text",
					"text": text,
				}},
			}},
		}}
		blocksJSON, err = json.Marshal(plainBlock)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to marshal blocks: %w", err)
	}

	// Build destinations JSON
	type destination struct {
		ChannelID string `json:"channel_id"`
		ThreadTs  string `json:"thread_ts,omitempty"`
		Broadcast bool   `json:"broadcast,omitempty"`
	}
	dest := destination{ChannelID: channel}
	if threadTs != "" {
		dest.ThreadTs = threadTs
		dest.Broadcast = false
	}
	destJSON, err := json.Marshal([]destination{dest})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal destinations: %w", err)
	}

	clientMsgID := uuid.New().String()

	h.logger.Debug("Creating draft",
		zap.String("channel", channel),
		zap.String("thread_ts", threadTs),
	)

	draft, err := h.apiProvider.Slack().DraftsCreate(ctx, string(blocksJSON), clientMsgID, string(destJSON))
	if err != nil {
		h.logger.Error("DraftsCreate failed", zap.Error(err))
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

	contentType := request.GetString("content_type", "text/markdown")
	if contentType != "text/plain" && contentType != "text/markdown" {
		return nil, errors.New("content_type must be either 'text/plain' or 'text/markdown'")
	}

	channel := request.GetString("channel_id", "")
	if channel == "" {
		return nil, errors.New("channel_id is required")
	}
	channel, err := h.resolveChannelID(ctx, channel)
	if err != nil {
		return nil, err
	}

	threadTs := request.GetString("thread_ts", "")

	// Convert text to blocks
	blocks, _, err := buildTextBlocks(h.logger, text, contentType)
	if err != nil {
		return nil, err
	}

	var blocksJSON []byte
	if blocks != nil {
		blocksJSON, err = json.Marshal(blocks)
	} else {
		plainBlock := []map[string]any{{
			"type": "rich_text",
			"elements": []map[string]any{{
				"type": "rich_text_section",
				"elements": []map[string]any{{
					"type": "text",
					"text": text,
				}},
			}},
		}}
		blocksJSON, err = json.Marshal(plainBlock)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to marshal blocks: %w", err)
	}

	type destination struct {
		ChannelID string `json:"channel_id"`
		ThreadTs  string `json:"thread_ts,omitempty"`
		Broadcast bool   `json:"broadcast,omitempty"`
	}
	dest := destination{ChannelID: channel}
	if threadTs != "" {
		dest.ThreadTs = threadTs
	}
	destJSON, err := json.Marshal([]destination{dest})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal destinations: %w", err)
	}

	clientMsgID := uuid.New().String()

	// Convert last_updated_ts "1775966853.146997" to milliseconds "1775966853146"
	// The API expects milliseconds as client_last_updated_ts
	lastUpdatedMs := convertTsToMillis(clientLastUpdatedTs)

	draft, err := h.apiProvider.Slack().DraftsUpdate(ctx, draftID, lastUpdatedMs, string(blocksJSON), clientMsgID, string(destJSON))
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
	if !strings.HasPrefix(channel, "#") && !strings.HasPrefix(channel, "@") {
		return channel, nil
	}

	// First attempt: try to resolve from current cache
	channelsMaps := h.apiProvider.ProvideChannelsMaps()
	chn, ok := channelsMaps.ChannelsInv[channel]
	if ok {
		return channelsMaps.Channels[chn].ID, nil
	}

	// Channel not found - try refreshing cache and retry once
	h.logger.Debug("Channel not found in cache, attempting refresh",
		zap.String("channel", channel))

	refreshErr := h.apiProvider.ForceRefreshChannels(ctx)
	wasRateLimited := errors.Is(refreshErr, provider.ErrRefreshRateLimited)

	if refreshErr != nil && !wasRateLimited {
		h.logger.Error("Failed to refresh channels cache",
			zap.String("channel", channel),
			zap.Error(refreshErr))
		return "", fmt.Errorf("channel %q not found and cache refresh failed: %w", channel, refreshErr)
	}

	// If rate-limited, cache wasn't refreshed - no point in a second lookup
	if wasRateLimited {
		h.logger.Warn("Channel not found; cache refresh was rate-limited",
			zap.String("channel", channel))
		return "", fmt.Errorf("channel %q not found (cache refresh was rate-limited, try again later)", channel)
	}

	// Second attempt after successful refresh
	channelsMaps = h.apiProvider.ProvideChannelsMaps()
	chn, ok = channelsMaps.ChannelsInv[channel]
	if !ok {
		h.logger.Error("Channel not found even after cache refresh",
			zap.String("channel", channel))
		return "", fmt.Errorf("channel %q not found", channel)
	}

	return channelsMaps.Channels[chn].ID, nil
}

// convertTsToMillis converts a Slack timestamp like "1775966853.146997" to milliseconds "1775966853146".
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
