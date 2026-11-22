package dto

import (
	"testing"

	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestMediaContentMarshalRequiredTextWithoutImageMetadata(t *testing.T) {
	parts := []MediaContent{
		{Type: ContentTypeText, Text: ""},
		{Type: ContentTypeImageURL, ImageUrl: &MessageImageUrl{Url: "https://example.test/image.png", Detail: "original", MimeType: "image/png"}},
		{Type: ContentTypeText, Text: "after"},
		{Type: ContentTypeInputAudio, InputAudio: &MessageInputAudio{Data: "AA==", Format: "wav"}},
		{Type: ContentTypeFile, File: &MessageFile{FileName: "note.txt", FileData: "data:text/plain;base64,aGk="}},
	}
	body, err := kitutil.Marshal(parts)
	require.NoError(t, err)
	assert.Equal(t, `""`, gjson.GetBytes(body, "0.text").Raw)
	assert.Equal(t, "after", gjson.GetBytes(body, "2.text").Str)
	assert.Equal(t, "original", gjson.GetBytes(body, "1.image_url.detail").Str)
	assert.NotContains(t, string(body), "MimeType")
	for _, index := range []string{"1", "3", "4"} {
		assert.False(t, gjson.GetBytes(body, index+".text").Exists())
	}
	var roundTrip []MediaContent
	require.NoError(t, kitutil.Unmarshal(body, &roundTrip))
	assert.Equal(t, parts[0].Text, roundTrip[0].Text)
	assert.Equal(t, parts[2].Text, roundTrip[2].Text)
}
