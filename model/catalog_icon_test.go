package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPublicCatalogIconIDsContainOnlyBundledAssets(t *testing.T) {
	for input, want := range map[string]string{
		"Claude.Avatar.type={'platform'}": "Claude.Avatar", "OpenAI.Color": "OpenAI", "Adobe.Color": "Adobe.Color", "SGLang": "SGLang",
		"https://vendor.invalid/private": "", "<svg>": "", "PrivateSupplier": "", "Claude.Avatar.source=https://private.invalid": "",
		"OpenAI.Avatar.Other": "", "OpenAI.constructor": "", "OpenAI.": "", "OpenAI.Mono.size=24": "OpenAI",
	} {
		assert.Equal(t, want, PublicCatalogIconID(input), input)
	}
}
