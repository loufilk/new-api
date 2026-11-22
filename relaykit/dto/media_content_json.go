package dto

import kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"

// MarshalJSON preserves required empty text in mixed content arrays without
// adding a spurious text property to image/audio/file parts.
func (content MediaContent) MarshalJSON() ([]byte, error) {
	type wireContent MediaContent
	if content.Type != ContentTypeText {
		return kitutil.Marshal(wireContent(content))
	}
	return kitutil.Marshal(struct {
		wireContent
		Text string `json:"text"`
	}{wireContent: wireContent(content), Text: content.Text})
}
