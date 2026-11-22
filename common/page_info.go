package common

import (
	"math"
	"strconv"

	"github.com/gin-gonic/gin"
)

type PageInfo struct {
	Page     int `json:"page"`      // page num 页码
	PageSize int `json:"page_size"` // page size 页大小

	Total int `json:"total"` // 总条数，后设置
	Items any `json:"items"` // 数据，后设置
}

func (p *PageInfo) GetStartIdx() int {
	return (p.Page - 1) * p.PageSize
}

func (p *PageInfo) GetEndIdx() int {
	return p.Page * p.PageSize
}

func (p *PageInfo) GetPageSize() int {
	return p.PageSize
}

func (p *PageInfo) GetPage() int {
	return p.Page
}

func (p *PageInfo) SetTotal(total int) {
	p.Total = total
}

func (p *PageInfo) SetItems(items any) {
	p.Items = items
}

func GetPageQuery(c *gin.Context) *PageInfo {
	page, err := strconv.Atoi(c.Query("p"))
	if err != nil || page < 1 {
		page = 1
	}
	pageSize := 0
	for _, key := range []string{"page_size", "ps", "size"} {
		if n, err := strconv.Atoi(c.Query(key)); err == nil && n > 0 {
			pageSize = n
			break
		}
	}
	if pageSize == 0 {
		pageSize = ItemsPerPage
	}
	pageSize = min(pageSize, 100)
	// Do not let offsets overflow or become negative (GORM interprets a
	// negative limit/offset as "remove the limit/offset").
	page = min(page, math.MaxInt32/pageSize)
	return &PageInfo{Page: page, PageSize: pageSize}
}
