package service

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"mime"
	"path/filepath"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/google/uuid"
	_ "golang.org/x/image/webp"
)

const SupportTicketMaxImages = 4
const SupportTicketImageMaxBytes = 5 << 20
const SupportTicketImageMaxDimension = 8192
const SupportTicketImageMaxPixels = 16 << 20

type SupportTicketCreateInput struct {
	Subject      string
	Message      string
	Category     string
	ContactEmail *string
	RequestID    string
	Attachments  []model.SupportTicketAttachment
}

func ValidSupportTicketCategory(category string) bool {
	switch category {
	case "billing", "api_model", "account_access", "other":
		return true
	default:
		return false
	}
}

type supportImageBuffer struct{ bytes.Buffer }

func (b *supportImageBuffer) Write(value []byte) (int, error) {
	if len(value) > SupportTicketImageMaxBytes-b.Len() {
		return 0, ErrSupportTicketInvalidInput
	}
	return b.Buffer.Write(value)
}

// Decode and re-encode the pixels, not merely the header. Uploaded names, EXIF,
// appended payloads and container metadata are never stored or served.
func NormalizeSupportTicketImage(filename, contentType string, data []byte) (model.SupportTicketAttachment, error) {
	invalid := model.SupportTicketAttachment{}
	if len(data) == 0 || len(data) > SupportTicketImageMaxBytes || len(filename) > 255 || strings.ContainsAny(filename, "\x00\r\n") {
		return invalid, ErrSupportTicketInvalidInput
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > SupportTicketImageMaxDimension || config.Height > SupportTicketImageMaxDimension || int64(config.Width)*int64(config.Height) > SupportTicketImageMaxPixels {
		return invalid, ErrSupportTicketInvalidInput
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	ext := strings.ToLower(filepath.Ext(filename))
	valid := false
	switch format {
	case "png":
		valid = mediaType == "image/png" && ext == ".png"
	case "jpeg":
		valid = mediaType == "image/jpeg" && (ext == ".jpg" || ext == ".jpeg")
	case "webp":
		valid = mediaType == "image/webp" && ext == ".webp"
	}
	if err != nil || !valid {
		return invalid, ErrSupportTicketInvalidInput
	}
	pixels, decodedFormat, err := image.Decode(bytes.NewReader(data))
	if err != nil || decodedFormat != format || pixels.Bounds().Dx() != config.Width || pixels.Bounds().Dy() != config.Height {
		return invalid, ErrSupportTicketInvalidInput
	}
	var normalized supportImageBuffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&normalized, pixels); err != nil {
		return invalid, ErrSupportTicketInvalidInput
	}
	return model.SupportTicketAttachment{ID: uuid.NewString(), MediaType: "image/png", SizeBytes: normalized.Len(), Width: config.Width, Height: config.Height, Data: normalized.Bytes()}, nil
}

func supportTicketSubmission(requestID string, userID int, ticketID int64, subject, content, category, contact string, attachments []model.SupportTicketAttachment) (*model.SupportTicketSubmission, error) {
	if requestID == "" {
		return nil, nil
	}
	id, err := uuid.Parse(requestID)
	if err != nil || id == uuid.Nil {
		return nil, ErrSupportTicketInvalidInput
	}
	key := sha256.Sum256([]byte(fmt.Sprintf("support:%d:%d:%s", userID, ticketID, id.String())))
	imageHashes := make([]string, len(attachments))
	for i, attachment := range attachments {
		imageHashes[i] = fmt.Sprintf("%x", sha256.Sum256(attachment.Data))
	}
	payload, err := common.Marshal([]any{subject, content, category, contact, imageHashes})
	if err != nil {
		return nil, err
	}
	return &model.SupportTicketSubmission{Key: fmt.Sprintf("%x", key), PayloadHash: fmt.Sprintf("%x", sha256.Sum256(payload))}, nil
}
