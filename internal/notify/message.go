package notify

import "fmt"

// formatWarning builds a slackMessage for a single fetch warning.
// Per-message fields (UID, UIDValidity, Message-ID) are omitted when zero/empty
// so that mailbox-level warnings (e.g. WarningKindMailboxReadOnly) do not render
// misleading "UID: 0" or blank "Message-ID" fields.
func formatWarning(w Warning, runID string) slackMessage {
	fields := []slackField{
		{Title: "Kind", Value: string(w.Kind), Short: true},
	}
	if w.UID != 0 && w.UIDValidity != 0 {
		fields = append(fields,
			slackField{Title: "UID", Value: fmt.Sprintf("%d", w.UID), Short: true},
			slackField{Title: "UIDValidity", Value: fmt.Sprintf("%d", w.UIDValidity), Short: true},
		)
	}
	if w.MessageID != "" {
		fields = append(fields, slackField{Title: "Message-ID", Value: w.MessageID, Short: false})
	}
	fields = append(fields, slackField{Title: "Run ID", Value: runID, Short: true})
	return slackMessage{
		Text: fmt.Sprintf("%s Fetch Warning: %s", emojiAlert, string(w.Kind)),
		Attachments: []slackAttachment{
			{Color: colorWarning, Fields: fields},
		},
	}
}

// slackMessage is the top-level payload sent to a Slack Incoming Webhook.
type slackMessage struct {
	Text        string            `json:"text"`
	Attachments []slackAttachment `json:"attachments,omitempty"`
}

// slackAttachment is a Slack message attachment with optional colour and fields.
type slackAttachment struct {
	Color    string       `json:"color,omitempty"`
	Fallback string       `json:"fallback,omitempty"`
	Fields   []slackField `json:"fields,omitempty"` // used for alerts, warnings, errors, and summary
}

// slackField is a single key-value field within a Slack attachment.
type slackField struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Short bool   `json:"short"`
}
