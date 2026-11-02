package service

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSupportTicketImageNormalizationValidatesPixelsAndRemovesMetadata(t *testing.T) {
	pixels := image.NewRGBA(image.Rect(0, 0, 3, 2))
	pixels.Set(1, 1, color.RGBA{R: 180, G: 70, B: 90, A: 255})
	var pngBytes, jpegBytes bytes.Buffer
	require.NoError(t, png.Encode(&pngBytes, pixels))
	require.NoError(t, jpeg.Encode(&jpegBytes, pixels, nil))
	webpBytes, err := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA")
	require.NoError(t, err)
	for _, tc := range []struct {
		name, media string
		data        []byte
	}{
		{"screenshot.png", "image/png", pngBytes.Bytes()},
		{"screenshot.jpg", "image/jpeg", jpegBytes.Bytes()},
		{"screenshot.webp", "image/webp", webpBytes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := append(append([]byte(nil), tc.data...), []byte("PRIVATE metadata <script>alert(1)</script>")...)
			attachment, err := NormalizeSupportTicketImage(tc.name, tc.media, data)
			require.NoError(t, err)
			assert.Equal(t, "image/png", attachment.MediaType)
			assert.NotEmpty(t, attachment.ID)
			assert.NotContains(t, string(attachment.Data), "PRIVATE")
			decoded, format, err := image.Decode(bytes.NewReader(attachment.Data))
			require.NoError(t, err)
			assert.Equal(t, "png", format)
			assert.Equal(t, decoded.Bounds().Dx(), attachment.Width)
			assert.Equal(t, decoded.Bounds().Dy(), attachment.Height)
			assert.Equal(t, len(attachment.Data), attachment.SizeBytes)
		})
	}
}

func TestSupportTicketImageNormalizationRejectsSpoofsTruncationAndResourceLimits(t *testing.T) {
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1, 1))))
	pngBytes := encoded.Bytes()
	oversizedDimensions := append([]byte(nil), pngBytes...)
	binary.BigEndian.PutUint32(oversizedDimensions[16:20], SupportTicketImageMaxDimension+1)
	binary.BigEndian.PutUint32(oversizedDimensions[29:33], crc32.ChecksumIEEE(oversizedDimensions[12:29]))
	oversizedPixels := append([]byte(nil), pngBytes...)
	binary.BigEndian.PutUint32(oversizedPixels[16:20], 4097)
	binary.BigEndian.PutUint32(oversizedPixels[20:24], 4097)
	binary.BigEndian.PutUint32(oversizedPixels[29:33], crc32.ChecksumIEEE(oversizedPixels[12:29]))
	for _, tc := range []struct {
		name, filename, media string
		data                  []byte
	}{
		{"SVG", "image.svg", "image/svg+xml", []byte(`<svg onload="alert(1)"></svg>`)},
		{"HTML spoof", "image.png", "image/png", []byte("<html>private</html>")},
		{"MIME spoof", "image.png", "image/jpeg", pngBytes},
		{"extension spoof", "image.jpg", "image/png", pngBytes},
		{"empty", "image.png", "image/png", nil},
		{"truncated pixels", "image.png", "image/png", pngBytes[:40]},
		{"byte limit", "image.png", "image/png", make([]byte, SupportTicketImageMaxBytes+1)},
		{"dimension limit", "image.png", "image/png", oversizedDimensions},
		{"decoded pixel limit", "image.png", "image/png", oversizedPixels},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NormalizeSupportTicketImage(tc.filename, tc.media, tc.data)
			assert.ErrorIs(t, err, ErrSupportTicketInvalidInput)
		})
	}
}
