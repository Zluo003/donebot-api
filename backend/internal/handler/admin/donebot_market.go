package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/donebotmarket"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func (h *DoneBotHandler) marketService(c *gin.Context) (*donebotmarket.Service, int64, bool) {
	if h == nil || h.market == nil || !h.market.Enabled() {
		response.Error(c, 503, "Bot 广场服务暂不可用")
		return nil, 0, false
	}
	subject, _ := middleware.GetAuthSubjectFromContext(c)
	return h.market, subject.UserID, true
}

func (h *DoneBotHandler) MarketOverview(c *gin.Context) {
	svc, _, ok := h.marketService(c)
	if !ok {
		return
	}
	out, err := svc.AdminOverview(c.Request.Context())
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) MarketSettings(c *gin.Context) {
	svc, _, ok := h.marketService(c)
	if !ok {
		return
	}
	out, err := svc.GetSettings(c.Request.Context())
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) UpdateMarketSettings(c *gin.Context) {
	svc, _, ok := h.marketService(c)
	if !ok {
		return
	}
	var in donebotmarket.UpdateSettingsInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "配置参数无效："+err.Error())
		return
	}
	out, err := svc.UpdateSettings(c.Request.Context(), in)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) MarketReviewOptions(c *gin.Context) {
	svc, _, ok := h.marketService(c)
	if !ok {
		return
	}
	out, err := svc.AdminReviewOptions(c.Request.Context())
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) TestMarketReviewer(c *gin.Context) {
	svc, _, ok := h.marketService(c)
	if !ok {
		return
	}
	var in donebotmarket.ReviewTestInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "测试参数无效："+err.Error())
		return
	}
	out, err := svc.AdminTestReviewer(c.Request.Context(), in)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) MarketCategories(c *gin.Context) {
	svc, _, ok := h.marketService(c)
	if !ok {
		return
	}
	items, err := svc.ListCategories(c.Request.Context(), true)
	if !response.ErrorFrom(c, err) {
		response.Success(c, gin.H{"categories": items})
	}
}

func (h *DoneBotHandler) SaveMarketCategory(c *gin.Context) {
	svc, _, ok := h.marketService(c)
	if !ok {
		return
	}
	var in donebotmarket.SaveCategoryInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "分类参数无效")
		return
	}
	if rawID := c.Param("id"); rawID != "" {
		if id, err := strconv.ParseInt(rawID, 10, 64); err == nil && id > 0 {
			in.ID = id
		}
	}
	out, err := svc.SaveCategory(c.Request.Context(), in)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) DeleteMarketCategory(c *gin.Context) {
	svc, _, ok := h.marketService(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "无效的分类 ID")
		return
	}
	if !response.ErrorFrom(c, svc.DeleteCategory(c.Request.Context(), id)) {
		response.Success(c, gin.H{"ok": true})
	}
}

func (h *DoneBotHandler) MarketListings(c *gin.Context) {
	svc, _, ok := h.marketService(c)
	if !ok {
		return
	}
	page, pageSize := response.ParsePagination(c)
	items, total, err := svc.AdminListListings(c.Request.Context(), c.Query("status"), c.Query("q"), page, pageSize)
	if !response.ErrorFrom(c, err) {
		response.Success(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
	}
}

func (h *DoneBotHandler) MarketListingDetail(c *gin.Context) {
	svc, adminID, ok := h.marketService(c)
	if !ok {
		return
	}
	detail, err := svc.GetListingDetail(c.Request.Context(), c.Param("id"), adminID, true)
	if !response.ErrorFrom(c, err) {
		response.Success(c, detail)
	}
}

func (h *DoneBotHandler) ReviewMarketListing(c *gin.Context) {
	svc, adminID, ok := h.marketService(c)
	if !ok {
		return
	}
	var in struct {
		Decision string `json:"decision"`
		Note     string `json:"note"`
		Featured *bool  `json:"featured"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "审核参数无效")
		return
	}
	out, err := svc.AdminReviewListing(c.Request.Context(), adminID, c.Param("id"), in.Decision, in.Note, in.Featured)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) UpdateMarketListingCategory(c *gin.Context) {
	svc, adminID, ok := h.marketService(c)
	if !ok {
		return
	}
	var in struct {
		CategoryID int64 `json:"category_id"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || in.CategoryID <= 0 {
		response.BadRequest(c, "请选择有效的目标分类")
		return
	}
	out, err := svc.AdminUpdateListingCategory(c.Request.Context(), adminID, c.Param("id"), in.CategoryID)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) DeleteMarketListing(c *gin.Context) {
	svc, _, ok := h.marketService(c)
	if !ok {
		return
	}
	if !response.ErrorFrom(c, svc.AdminDeleteSuspendedListing(c.Request.Context(), c.Param("id"))) {
		response.Success(c, gin.H{"ok": true})
	}
}

func (h *DoneBotHandler) MarketReports(c *gin.Context) {
	svc, _, ok := h.marketService(c)
	if !ok {
		return
	}
	page, pageSize := response.ParsePagination(c)
	items, total, err := svc.AdminListReports(c.Request.Context(), c.Query("status"), page, pageSize)
	if !response.ErrorFrom(c, err) {
		response.Success(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
	}
}

func (h *DoneBotHandler) HandleMarketReport(c *gin.Context) {
	svc, adminID, ok := h.marketService(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "无效的举报 ID")
		return
	}
	var in struct {
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "参数无效")
		return
	}
	if !response.ErrorFrom(c, svc.AdminHandleReport(c.Request.Context(), adminID, id, in.Status, in.Note)) {
		response.Success(c, gin.H{"ok": true})
	}
}

func (h *DoneBotHandler) MarketWithdrawals(c *gin.Context) {
	svc, _, ok := h.marketService(c)
	if !ok {
		return
	}
	page, pageSize := response.ParsePagination(c)
	items, total, err := svc.AdminListWithdrawals(c.Request.Context(), c.Query("status"), page, pageSize)
	if !response.ErrorFrom(c, err) {
		response.Success(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
	}
}

func (h *DoneBotHandler) ProcessMarketWithdrawal(c *gin.Context) {
	svc, adminID, ok := h.marketService(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "无效的提现单 ID")
		return
	}
	var in struct {
		Decision      string `json:"decision"`
		PayoutTradeNo string `json:"payout_trade_no"`
		AdminNote     string `json:"admin_note"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "审核参数无效")
		return
	}
	out, err := svc.AdminProcessWithdrawal(c.Request.Context(), adminID, id, in.Decision, in.PayoutTradeNo, in.AdminNote)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
