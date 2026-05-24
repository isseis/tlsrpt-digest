package notify

import "fmt"

// formatWarning builds a slackMessage for a single fetch warning.
// Only kind, uid, uidvalidity, message_id, and run_id are included.
func formatWarning(w Warning, runID string) slackMessage {
	return slackMessage{
		Text: fmt.Sprintf("%s Fetch Warning: %s", emojiAlert, string(w.Kind)),
		Attachments: []slackAttachment{
			{
				Color: colorWarning,
				Fields: []slackField{
					{Title: "Kind", Value: string(w.Kind), Short: true},
					{Title: "UID", Value: fmt.Sprintf("%d", w.UID), Short: true},
					{Title: "UIDValidity", Value: fmt.Sprintf("%d", w.UIDValidity), Short: true},
					{Title: "Message-ID", Value: w.MessageID, Short: false},
					{Title: "Run ID", Value: runID, Short: true},
				},
			},
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
	Color  string       `json:"color,omitempty"`
	Fields []slackField `json:"fields,omitempty"`
}

// slackField is a single key-value field within a Slack attachment.
type slackField struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Short bool   `json:"short"`
}
