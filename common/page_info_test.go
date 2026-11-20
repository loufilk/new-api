package common

import (
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestPageQueryKeepsLimitAndOffsetBounded(t *testing.T) {
	for _, tc := range []struct {
		q                  string
		wantPage, wantSize int
	}{
		{"p=-2&page_size=-4", 1, ItemsPerPage},
		{"p=2&ps=18", 2, 18},
		{"p=2&page_size=0&ps=-10&size=23", 2, 23},
		{"p=2147483647&page_size=9999", 21474836, 100},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/?"+tc.q, nil)
		p := GetPageQuery(c)
		require.Equal(t, tc.wantPage, p.Page)
		require.Equal(t, tc.wantSize, p.PageSize)
		require.GreaterOrEqual(t, p.GetStartIdx(), 0)
		require.Greater(t, p.GetEndIdx(), p.GetStartIdx())
	}
}
