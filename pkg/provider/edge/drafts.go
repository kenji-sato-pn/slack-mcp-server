package edge

import (
	"context"
	"runtime/trace"
)

// drafts.* API

// Draft represents a Slack draft message returned by the Edge API.
type Draft struct {
	ID             string             `json:"id"`
	DateCreated    int64              `json:"date_created"`
	UserID         string             `json:"user_id"`
	TeamID         string             `json:"team_id"`
	LastUpdatedTs  string             `json:"last_updated_ts"`
	Blocks         []any              `json:"blocks,omitempty"`
	FileIDs        []string           `json:"file_ids"`
	IsFromComposer bool               `json:"is_from_composer"`
	IsDeleted      bool               `json:"is_deleted"`
	IsSent         bool               `json:"is_sent"`
	ClientMsgID    string             `json:"client_msg_id"`
	DateScheduled  int64              `json:"date_scheduled"`
	Destinations   []DraftDestination `json:"destinations"`
}

// DraftDestination represents a draft's target channel and optional thread.
type DraftDestination struct {
	ChannelID string   `json:"channel_id"`
	ThreadTs  string   `json:"thread_ts,omitempty"`
	Broadcast bool     `json:"broadcast,omitempty"`
	UserIDs   []string `json:"user_ids,omitempty"`
}

type draftsCreateForm struct {
	BaseRequest
	Blocks         string `json:"blocks"`
	ClientMsgID    string `json:"client_msg_id"`
	Destinations   string `json:"destinations"`
	Attachments    string `json:"attachments,omitempty"`
	FileIDs        string `json:"file_ids"`
	IsFromComposer bool   `json:"is_from_composer"`
	WebClientFields
}

type draftsResponse struct {
	baseResponse
	Draft Draft `json:"draft"`
}

// DraftsCreate creates a new draft message.
// blocks and destinations must be pre-serialized JSON strings.
func (cl *Client) DraftsCreate(ctx context.Context, blocks, clientMsgID, destinations string) (*Draft, error) {
	ctx, task := trace.NewTask(ctx, "DraftsCreate")
	defer task.End()

	form := draftsCreateForm{
		BaseRequest:     BaseRequest{Token: cl.token},
		Blocks:          blocks,
		ClientMsgID:     clientMsgID,
		Destinations:    destinations,
		Attachments:     "",
		FileIDs:         "[]",
		IsFromComposer:  false,
		WebClientFields: webclientReason("MessageInput:updateDraft"),
	}

	resp, err := cl.PostForm(ctx, "drafts.create", values(form, true))
	if err != nil {
		return nil, err
	}
	var r draftsResponse
	if err := cl.ParseResponse(&r, resp); err != nil {
		return nil, err
	}
	if err := r.validate("drafts.create"); err != nil {
		return nil, err
	}
	return &r.Draft, nil
}

type draftsUpdateForm struct {
	BaseRequest
	DraftID             string `json:"draft_id"`
	ClientLastUpdatedTs string `json:"client_last_updated_ts"`
	Blocks              string `json:"blocks"`
	ClientMsgID         string `json:"client_msg_id"`
	Destinations        string `json:"destinations"`
	Attachments         string `json:"attachments,omitempty"`
	FileIDs             string `json:"file_ids"`
	IsFromComposer      bool   `json:"is_from_composer"`
	WebClientFields
}

// DraftsUpdate updates an existing draft message.
// blocks and destinations must be pre-serialized JSON strings.
func (cl *Client) DraftsUpdate(ctx context.Context, draftID, clientLastUpdatedTs, blocks, clientMsgID, destinations string) (*Draft, error) {
	ctx, task := trace.NewTask(ctx, "DraftsUpdate")
	defer task.End()

	form := draftsUpdateForm{
		BaseRequest:         BaseRequest{Token: cl.token},
		DraftID:             draftID,
		ClientLastUpdatedTs: clientLastUpdatedTs,
		Blocks:              blocks,
		ClientMsgID:         clientMsgID,
		Destinations:        destinations,
		Attachments:         "",
		FileIDs:             "[]",
		IsFromComposer:      false,
		WebClientFields:     webclientReason("MessageInput:updateDraft"),
	}

	resp, err := cl.PostForm(ctx, "drafts.update", values(form, true))
	if err != nil {
		return nil, err
	}
	var r draftsResponse
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
	DraftID             string `json:"draft_id"`
	ClientLastUpdatedTs string `json:"client_last_updated_ts"`
	SkipFileDeletion    bool   `json:"skip_file_deletion"`
	WebClientFields
}

// DraftsDelete deletes a draft message.
func (cl *Client) DraftsDelete(ctx context.Context, draftID, clientLastUpdatedTs string) error {
	ctx, task := trace.NewTask(ctx, "DraftsDelete")
	defer task.End()

	form := draftsDeleteForm{
		BaseRequest:         BaseRequest{Token: cl.token},
		DraftID:             draftID,
		ClientLastUpdatedTs: clientLastUpdatedTs,
		SkipFileDeletion:    false,
		WebClientFields:     webclientReason("MessageInput:updateDraft"),
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
