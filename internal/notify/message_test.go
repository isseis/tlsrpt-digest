package notify_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// slackMessageJSON mirrors the internal slackMessage for JSON shape tests.
type slackMessageJSON struct {
	Text        string `json:"text"`
	Attachments []struct {
		Color  string `json:"color"`
		Fields []struct {
			Title string `json:"title"`
			Value string `json:"value"`
			Short bool   `json:"short"`
		} `json:"fields"`
	} `json:"attachments"`
}

func TestSlackMessage_JSONShape(t *testing.T) {
	raw := []byte(`{"text":"hello","attachments":[{"color":"warning","fields":[{"title":"k","value":"v","short":false}]}]}`)
	var msg slackMessageJSON
	require.NoError(t, json.Unmarshal(raw, &msg))
	assert.Equal(t, "hello", msg.Text)
	require.Len(t, msg.Attachments, 1)
	assert.Equal(t, "warning", msg.Attachments[0].Color)
}

func TestSlackAttachment_FieldsEncoding(t *testing.T) {
	raw := []byte(`{"fields":[{"title":"t1","value":"v1","short":true},{"title":"t2","value":"v2","short":false}]}`)
	var att struct {
		Fields []struct {
			Title string `json:"title"`
			Value string `json:"value"`
			Short bool   `json:"short"`
		} `json:"fields"`
	}
	require.NoError(t, json.Unmarshal(raw, &att))
	require.Len(t, att.Fields, 2)
	assert.Equal(t, "t1", att.Fields[0].Title)
	assert.True(t, att.Fields[0].Short)
	assert.False(t, att.Fields[1].Short)
}
