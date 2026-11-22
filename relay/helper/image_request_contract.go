package helper

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
)

// ValidateImageModelRequest checks the final provider request as well as ingress.
// Exact official IDs are intentional: arbitrary channel aliases are not an
// assertion that their upstream implements the official model contract.
func ValidateImageModelRequest(request *dto.ImageRequest, xaiChannel bool) error {
	count, err := request.ImageCount(false)
	if err != nil {
		return err
	}
	gpt2 := request.Model == "gpt-image-2" || request.Model == "gpt-image-2-2026-04-21"
	gpt := gpt2 || request.Model == "gpt-image-1" || request.Model == "gpt-image-1.5" || request.Model == "gpt-image-1-mini"
	xai2 := request.Model == "grok-imagine-image-2.0"
	xaiLegacy := request.Model == "grok-imagine-image" || request.Model == "grok-imagine-image-quality" || request.Model == "grok-imagine-image-pro"
	if gpt || xai2 || xaiLegacy || xaiChannel {
		if request.N != nil && *request.N == 0 || count > 10 {
			return fmt.Errorf("this image relay requires n between 1 and 10 for this model")
		}
	}
	if gpt2 && request.Size != "" && request.Size != "auto" {
		width, height, ok := strings.Cut(request.Size, "x")
		w, wErr := strconv.ParseInt(width, 10, 32)
		h, hErr := strconv.ParseInt(height, 10, 32)
		if !ok || wErr != nil || hErr != nil || w <= 0 || h <= 0 || w > 3840 || h > 3840 ||
			w%16 != 0 || h%16 != 0 || w > 3*h || h > 3*w || w*h < 655360 || w*h > 8294400 {
			return fmt.Errorf("gpt-image-2 size must have 16-pixel aligned edges up to 3840, aspect ratio at most 3:1, and 655360..8294400 pixels")
		}
	}
	if gpt2 && request.ResponseFormat != "" {
		return fmt.Errorf("gpt-image-2 returns base64 images; response_format is unsupported")
	}
	if gpt2 && len(request.InputFidelity) != 0 && string(request.InputFidelity) != "null" {
		return fmt.Errorf("input_fidelity is unsupported by gpt-image-2")
	}
	if gpt {
		switch request.Quality {
		case "", "auto", "low", "medium", "high":
		default:
			return fmt.Errorf("GPT image quality must be auto, low, medium or high")
		}
	}
	if !xaiChannel && !xai2 && !xaiLegacy {
		if gpt {
			_, err := ImageRequestReferenceCount(nil, request)
			return err
		}
		return nil
	}
	if request.Size != "" && request.Size != "auto" {
		return fmt.Errorf("xAI image size is unsupported; use aspect_ratio and resolution")
	}
	if request.AspectRatio != nil {
		switch *request.AspectRatio {
		case "auto", "1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3", "2:1", "1:2", "19.5:9", "9:19.5", "20:9", "9:20":
		case "21:9", "5:2":
			if xaiLegacy {
				return fmt.Errorf("this aspect_ratio requires grok-imagine-image-2.0")
			}
		default:
			return fmt.Errorf("unsupported xAI image aspect_ratio")
		}
	}
	if request.Resolution != nil {
		switch *request.Resolution {
		case "1k", "2k":
		case "1.5k":
			if request.Model == "grok-imagine-image" {
				return fmt.Errorf("grok-imagine-image resolution must be 1k or 2k")
			}
		default:
			return fmt.Errorf("xAI image resolution must be 1k, 1.5k or 2k")
		}
	}
	if request.Quality != "" {
		if xaiLegacy {
			return fmt.Errorf("quality is supported only by grok-imagine-image-2.0; legacy xAI image models do not accept it")
		}
		switch request.Quality {
		case "low", "medium", "auto":
		default:
			return fmt.Errorf("xAI image quality must be low, medium or auto")
		}
	}
	if request.ResponseFormat != "" && request.ResponseFormat != "url" && request.ResponseFormat != "b64_json" {
		return fmt.Errorf("xAI image response_format must be url or b64_json")
	}
	if raw, present := request.Extra["deferred"]; present && string(raw) != "false" {
		return fmt.Errorf("upstream deferred image generation is not supported by this relay; use the gateway image task endpoint")
	}
	if len(request.Image) > 0 && string(request.Image) != "null" && len(request.Images) > 0 && string(request.Images) != "null" {
		return fmt.Errorf("provide image or images, not both")
	}
	refs, err := ImageReferenceCount(request)
	if err != nil {
		return err
	}
	if xai2 && refs > 5 {
		return fmt.Errorf("grok-imagine-image-2.0 supports at most 5 reference images")
	}
	return nil
}

// ImageReferenceCount exposes a scalar for pricing without retaining image
// contents or URLs in the billing snapshot. Multipart files are counted by the
// request parser's caller instead.
func ImageReferenceCount(request *dto.ImageRequest) (int, error) {
	count := 0
	if len(request.Image) > 0 && string(request.Image) != "null" && string(request.Image) != `""` {
		count = 1
	}
	if len(request.Images) > 0 && string(request.Images) != "null" {
		var images []json.RawMessage
		if err := common.Unmarshal(request.Images, &images); err != nil {
			return 0, fmt.Errorf("images must be an array")
		}
		count += len(images)
	}
	if count > dto.MaxImageN {
		return 0, fmt.Errorf("too many reference images")
	}
	return count, nil
}

// ImageRequestReferenceCount also accounts for multipart edits, where image
// bytes are not present in the JSON DTO. The same count feeds validation and
// the safe pricing snapshot.
func ImageRequestReferenceCount(c *gin.Context, request *dto.ImageRequest) (int, error) {
	count, err := ImageReferenceCount(request)
	if err != nil {
		return 0, err
	}
	if c != nil && c.Request != nil && c.Request.MultipartForm != nil {
		count = 0
		for name, files := range c.Request.MultipartForm.File {
			if name == "image" || strings.HasPrefix(name, "image[") {
				count += len(files)
			}
		}
	}
	if count > dto.MaxImageN {
		return 0, fmt.Errorf("too many reference images")
	}
	switch request.Model {
	case "gpt-image-2", "gpt-image-2-2026-04-21", "gpt-image-1", "gpt-image-1.5", "gpt-image-1-mini":
		if count > 16 {
			return 0, fmt.Errorf("GPT image edits support at most 16 reference images")
		}
	case "grok-imagine-image-2.0":
		if count > 5 {
			return 0, fmt.Errorf("grok-imagine-image-2.0 supports at most 5 reference images")
		}
	}
	return count, nil
}
