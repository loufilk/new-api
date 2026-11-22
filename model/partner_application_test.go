package model

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validPartnerApplicationInput() PartnerApplicationInput {
	return PartnerApplicationInput{
		SiteName:    "Example Provider",
		ContactType: PartnerContactWeChat,
		Contact:     "provider-contact",
		Quotes: []PartnerQuote{{
			Platform: PartnerPlatformOpenAI,
			Group:    "standard",
			Rate:     "1.00",
		}},
	}
}

func TestNormalizePartnerApplicationMatchesSupplierIntakeFields(t *testing.T) {
	input := validPartnerApplicationInput()
	input.SiteURL = "https://provider.example/path"
	input.ContactType = PartnerContactEmail
	input.Contact = "sales@example.com"
	input.Quotes[0].Models = "gpt-4.1, gpt-4.1-mini"

	normalized, err := normalizePartnerApplication(input)
	require.NoError(t, err)
	assert.Equal(t, input.SiteURL, normalized.SiteURL)
	assert.Equal(t, PartnerContactEmail, normalized.ContactType)
	assert.Equal(t, "sales@example.com", normalized.Contact)
	assert.Equal(t, "1", normalized.Quotes[0].Rate)
}

func TestNormalizePartnerApplicationAllowsOptionalSiteAndModelDetails(t *testing.T) {
	input := validPartnerApplicationInput()
	normalized, err := normalizePartnerApplication(input)

	require.NoError(t, err)
	assert.Empty(t, normalized.SiteURL)
	assert.Empty(t, normalized.Quotes[0].Models)
}

func TestNormalizePartnerApplicationRejectsInvalidSupplierFields(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*PartnerApplicationInput)
	}{
		{"non-HTTPS site", func(input *PartnerApplicationInput) { input.SiteURL = "http://provider.example" }},
		{"unknown contact type", func(input *PartnerApplicationInput) { input.ContactType = "pager" }},
		{"invalid email contact", func(input *PartnerApplicationInput) { input.ContactType, input.Contact = PartnerContactEmail, "not-an-email" }},
		{"line break in contact", func(input *PartnerApplicationInput) { input.Contact = "first\nsecond" }},
		{"unknown platform", func(input *PartnerApplicationInput) { input.Quotes[0].Platform = "made-up" }},
		{"negative rate", func(input *PartnerApplicationInput) { input.Quotes[0].Rate = "-1" }},
		{"rate above limit", func(input *PartnerApplicationInput) { input.Quotes[0].Rate = "1001" }},
		{"duplicate group", func(input *PartnerApplicationInput) { input.Quotes = append(input.Quotes, input.Quotes[0]) }},
		{"too many quotes", func(input *PartnerApplicationInput) {
			for range 20 {
				input.Quotes = append(input.Quotes, PartnerQuote{Platform: PartnerPlatformOther, Group: "group", Rate: "1"})
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := validPartnerApplicationInput()
			tc.change(&input)

			_, err := normalizePartnerApplication(input)
			require.ErrorIs(t, err, ErrPartnerApplicationInvalid)
		})
	}
}

func TestNormalizePartnerApplicationBoundsFreeText(t *testing.T) {
	input := validPartnerApplicationInput()
	input.Description = strings.Repeat("x", 3001)

	_, err := normalizePartnerApplication(input)
	require.ErrorIs(t, err, ErrPartnerApplicationInvalid)
}
