package console_setting

import (
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func announcementJSON(t *testing.T, content string, extra string) string {
	t.Helper()
	body, err := common.Marshal([]map[string]any{{
		"content": content, "extra": extra,
		"publishDate": "2026-11-01T12:00:00Z", "type": "default",
	}})
	require.NoError(t, err)
	return string(body)
}

func TestAnnouncementsAcceptLegacyTextAndMarkdownImages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
	}{
		{name: "legacy text", content: "Planned maintenance on Friday."},
		{name: "multiple images and long URL", content: "**公告**\n\n![操作截图](https://images.example.invalid/first.png?caption=" + strings.Repeat("a", 1200) + ")\n\n保留正文和说明。\n\n![第二张截图](https://images.example.invalid/second.webp)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, ValidateConsoleSettings(announcementJSON(t, tc.content, "Existing note"), "Announcements"))
		})
	}
}

func TestAnnouncementsContentLimitMatchesUTF16EditorLength(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		wantErr bool
	}{
		{name: "ASCII at limit", content: strings.Repeat("a", 10000)},
		{name: "ASCII above limit", content: strings.Repeat("a", 10001), wantErr: true},
		{name: "Chinese at limit", content: strings.Repeat("中", 10000)},
		{name: "Chinese above limit", content: strings.Repeat("中", 10001), wantErr: true},
		{name: "emoji at limit", content: strings.Repeat("📷", 5000)},
		{name: "emoji and text above limit", content: strings.Repeat("📷", 5000) + "a", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConsoleSettings(announcementJSON(t, tc.content, ""), "Announcements")
			if !tc.wantErr {
				require.NoError(t, err)
				return
			}
			var message *common.Message
			require.ErrorAs(t, err, &message)
			assert.Equal(t, "Announcement #{{index}} content cannot exceed {{max}} characters", message.Key)
			assert.Equal(t, map[string]any{"index": 1, "max": 10000}, message.Params)
			assert.EqualError(t, err, "Announcement #1 content cannot exceed 10000 characters")
		})
	}
}

func TestAnnouncementsPreserveEntryAndNoteLimits(t *testing.T) {
	t.Run("100 entries remain supported and 101 are rejected", func(t *testing.T) {
		announcements := make([]map[string]any, 100)
		for i := range announcements {
			announcements[i] = map[string]any{"content": "Notice", "publishDate": "2026-11-01T12:00:00Z"}
		}
		body, err := common.Marshal(announcements)
		require.NoError(t, err)
		require.NoError(t, ValidateConsoleSettings(string(body), "Announcements"))
		body, err = common.Marshal(append(announcements, announcements[0]))
		require.NoError(t, err)
		assert.EqualError(t, ValidateConsoleSettings(string(body), "Announcements"), "Announcements cannot exceed 100 entries")
	})
	t.Run("note remains limited to 100 UTF-16 units", func(t *testing.T) {
		require.NoError(t, ValidateConsoleSettings(announcementJSON(t, "Notice", strings.Repeat("📷", 50)), "Announcements"))
		assert.EqualError(t, ValidateConsoleSettings(announcementJSON(t, "Notice", strings.Repeat("📷", 50)+"a"), "Announcements"), "Announcement #1 note cannot exceed 100 characters")
	})
}

func TestAnnouncementsRejectMalformedContentAndPreserveEmptySettings(t *testing.T) {
	for _, raw := range []string{"", "[]"} {
		require.NoError(t, ValidateConsoleSettings(raw, "Announcements"))
	}
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{name: "invalid JSON", raw: "[{"},
		{name: "object instead of array", raw: `{ "content": "Notice" }`},
		{name: "empty content", raw: `[{"content":"","publishDate":"2026-11-01T12:00:00Z"}]`},
		{name: "non-string content", raw: `[{"content":42,"publishDate":"2026-11-01T12:00:00Z"}]`},
		{name: "missing date", raw: `[{"content":"Notice"}]`},
		{name: "invalid date", raw: `[{"content":"Notice","publishDate":"tomorrow"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, ValidateConsoleSettings(tc.raw, "Announcements"))
		})
	}
}

func TestAnnouncementsTotalByteLimit(t *testing.T) {
	t.Run("exact byte boundary accepts valid JSON and one extra byte is rejected", func(t *testing.T) {
		body := announcementJSON(t, "Image links remain text, not embedded uploads.", "")
		atLimit := body + strings.Repeat(" ", 256*1024-len(body))
		require.NoError(t, ValidateConsoleSettings(atLimit, "Announcements"))
		assert.EqualError(t, ValidateConsoleSettings(atLimit+" ", "Announcements"), "Announcements cannot exceed 256 KiB in total")
	})
	t.Run("UTF-8 bytes are bounded even when every multilingual item fits", func(t *testing.T) {
		announcements := make([]map[string]any, 10)
		for i := range announcements {
			announcements[i] = map[string]any{"content": strings.Repeat("中", 9000), "publishDate": "2026-11-01T12:00:00Z"}
		}
		body, err := common.Marshal(announcements[:9])
		require.NoError(t, err)
		require.NoError(t, ValidateConsoleSettings(string(body), "Announcements"))
		body, err = common.Marshal(announcements)
		require.NoError(t, err)
		assert.EqualError(t, ValidateConsoleSettings(string(body), "Announcements"), "Announcements cannot exceed 256 KiB in total")
	})
	t.Run("oversized malformed JSON is rejected before parsing", func(t *testing.T) {
		assert.EqualError(t, ValidateConsoleSettings(strings.Repeat("x", 256*1024+1), "Announcements"), "Announcements cannot exceed 256 KiB in total")
	})
}

func TestAnnouncementPublicationFiltersAndPinnedOrder(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	items := []map[string]any{
		{"id": 1, "content": "legacy", "publishDate": "2026-11-17T12:00:00Z"},
		{"id": 2, "content": "draft", "publishDate": "2026-11-18T10:00:00Z", "status": "draft", "pinned": true},
		{"id": 3, "content": "scheduled", "publishDate": "2026-11-19T12:00:00Z", "status": "published"},
		{"id": 4, "content": "pinned", "publishDate": "2026-11-16T12:00:00Z", "status": "published", "pinned": true},
		{"id": 5, "content": "newest", "publishDate": "2026-11-18T12:00:00Z", "status": "published", "category": "timeline"},
	}
	published := publishedAnnouncements(items, now)
	require.Len(t, published, 3)
	assert.Equal(t, 4, published[0]["id"])
	assert.Equal(t, 5, published[1]["id"])
	assert.Equal(t, 1, published[2]["id"])
	assert.Len(t, items, 5, "publication filtering must not remove stored drafts")
	assert.Len(t, publishedAnnouncements(items, now.Add(24*time.Hour)), 4)
}

func TestAnnouncementPublicationMetadataValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
		value any
	}{
		{"title type", "title", false}, {"title length", "title", strings.Repeat("a", 141)},
		{"category", "category", "invalid"}, {"status", "status", "invalid"}, {"pinned", "pinned", "true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := map[string]any{"content": "content", "publishDate": "2026-11-18T12:00:00Z"}
			item[tc.field] = tc.value
			data, err := common.Marshal([]any{item})
			require.NoError(t, err)
			require.Error(t, ValidateConsoleSettings(string(data), "Announcements"))
		})
	}
	data, err := common.Marshal([]any{map[string]any{"content": "content", "publishDate": "2026-11-18T12:00:00Z", "title": "title", "category": "timeline", "status": "draft", "pinned": true}})
	require.NoError(t, err)
	require.NoError(t, ValidateConsoleSettings(string(data), "Announcements"))
}
