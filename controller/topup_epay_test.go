package controller

import (
	"math"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildEpayTradeNo(t *testing.T) {
	testCases := []struct {
		name       string
		userID     int
		unixMillis int64
		randomPart string
		want       string
	}{
		{
			name:       "uses uppercase base36 components",
			userID:     35,
			unixMillis: 36,
			randomPart: "ABC123",
			want:       "UZT10ABC123",
		},
		{
			name:       "keeps maximum integer user id within gateway limit",
			userID:     math.MaxInt,
			unixMillis: 9_999_999_999_999,
			randomPart: "ZX90Q1",
		},
	}

	tradeNoPattern := regexp.MustCompile(`^U[0-9A-Z]+T[0-9A-Z]+[0-9A-Z]{6}$`)
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			tradeNo, err := buildEpayTradeNo(testCase.userID, testCase.unixMillis, testCase.randomPart)

			require.NoError(t, err)
			assert.LessOrEqual(t, len(tradeNo), 32)
			assert.Regexp(t, tradeNoPattern, tradeNo)
			if testCase.want != "" {
				assert.Equal(t, testCase.want, tradeNo)
			}
		})
	}
}

func TestBuildEpayTradeNoRejectsInvalidComponents(t *testing.T) {
	testCases := []struct {
		name       string
		userID     int
		unixMillis int64
		randomPart string
	}{
		{name: "missing user", userID: 0, unixMillis: 1, randomPart: "ABC123"},
		{name: "missing timestamp", userID: 1, unixMillis: 0, randomPart: "ABC123"},
		{name: "short random part", userID: 1, unixMillis: 1, randomPart: "ABC12"},
		{name: "lowercase random part", userID: 1, unixMillis: 1, randomPart: "abc123"},
		{name: "punctuation in random part", userID: 1, unixMillis: 1, randomPart: "ABC12-"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			tradeNo, err := buildEpayTradeNo(testCase.userID, testCase.unixMillis, testCase.randomPart)

			require.Error(t, err)
			assert.Empty(t, tradeNo)
		})
	}
}

func TestBuildEpayTradeNoChangesWithTimeAndRandomPart(t *testing.T) {
	first, err := buildEpayTradeNo(7, 1_725_480_000_000, "ABC123")
	require.NoError(t, err)
	second, err := buildEpayTradeNo(7, 1_725_480_000_001, "ABC123")
	require.NoError(t, err)
	third, err := buildEpayTradeNo(7, 1_725_480_000_000, "ABC124")
	require.NoError(t, err)

	assert.NotEqual(t, first, second)
	assert.NotEqual(t, first, third)
}
