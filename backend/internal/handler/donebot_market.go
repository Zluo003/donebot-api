package handler

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/donebotmarket"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func (h *DoneBotHandler) marketActor(c *gin.Context) (*donebotmarket.Service, int64, bool) {
	if h == nil || h.market == nil || !h.market.Enabled() {
		response.Error(c, 503, "Bot 广场服务暂未启用。")
		return nil, 0, false
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "请先登录 DoneBot 账号。")
		return nil, 0, false
	}
	return h.market, subject.UserID, true
}

func (h *DoneBotHandler) MarketCategories(c *gin.Context) {
	svc, _, ok := h.marketActor(c)
	if !ok {
		return
	}
	items, err := svc.ListCategories(c.Request.Context(), false)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, gin.H{"categories": items})
}

func (h *DoneBotHandler) MarketListings(c *gin.Context) {
	svc, _, ok := h.marketActor(c)
	if !ok {
		return
	}
	page, pageSize := response.ParsePagination(c)
	var catID int64
	if raw := c.Query("category_id"); raw != "" {
		catID, _ = strconv.ParseInt(raw, 10, 64)
	}
	items, total, err := svc.ListMarketplace(c.Request.Context(), donebotmarket.MarketplaceFilter{
		CategoryID: catID,
		Query:      c.Query("q"),
		Sort:       c.Query("sort"),
		Page:       page,
		PageSize:   pageSize,
	})
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}

func (h *DoneBotHandler) MarketListingDetail(c *gin.Context) {
	svc, userID, ok := h.marketActor(c)
	if !ok {
		return
	}
	detail, err := svc.GetListingDetail(c.Request.Context(), c.Param("id"), userID, false)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, detail)
}

func (h *DoneBotHandler) MarketBotPublish(c *gin.Context) {
	svc, userID, ok := h.marketActor(c)
	if !ok {
		return
	}
	var in donebotmarket.PublishBotInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "发布参数格式无效："+err.Error())
		return
	}
	detail, err := svc.PublishBot(c.Request.Context(), userID, in)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, detail)
}

func (h *DoneBotHandler) MarketMyListings(c *gin.Context) {
	svc, userID, ok := h.marketActor(c)
	if !ok {
		return
	}
	items, err := svc.ListMyListings(c.Request.Context(), userID)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, gin.H{"items": items})
}

func (h *DoneBotHandler) MarketSetVisibility(c *gin.Context) {
	svc, userID, ok := h.marketActor(c)
	if !ok {
		return
	}
	var in struct {
		Listed bool `json:"listed"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "参数格式无效")
		return
	}
	if response.ErrorFrom(c, svc.SetAuthorListingVisibility(c.Request.Context(), userID, c.Param("id"), in.Listed)) {
		return
	}
	response.Success(c, gin.H{"ok": true})
}

func (h *DoneBotHandler) MarketInstall(c *gin.Context) {
	svc, userID, ok := h.marketActor(c)
	if !ok {
		return
	}
	res, err := svc.InstallListing(c.Request.Context(), userID, c.Param("id"), c.GetHeader("X-DoneBot-Installation"))
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, res)
}

func (h *DoneBotHandler) MarketReport(c *gin.Context) {
	svc, userID, ok := h.marketActor(c)
	if !ok {
		return
	}
	var in donebotmarket.ReportListingInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "举报参数格式无效")
		return
	}
	if response.ErrorFrom(c, svc.ReportListing(c.Request.Context(), userID, c.Param("id"), in)) {
		return
	}
	response.Success(c, gin.H{"ok": true})
}

func (h *DoneBotHandler) MarketCreatorWallet(c *gin.Context) {
	svc, userID, ok := h.marketActor(c)
	if !ok {
		return
	}
	wallet, err := svc.GetCreatorWallet(c.Request.Context(), userID)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, wallet)
}

func (h *DoneBotHandler) MarketCreatorLedger(c *gin.Context) {
	svc, userID, ok := h.marketActor(c)
	if !ok {
		return
	}
	page, pageSize := response.ParsePagination(c)
	items, total, err := svc.ListCreatorLedger(c.Request.Context(), userID, page, pageSize)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}

func (h *DoneBotHandler) MarketCreatorTransfer(c *gin.Context) {
	svc, userID, ok := h.marketActor(c)
	if !ok {
		return
	}
	var in struct {
		Amount float64 `json:"amount"`
	}
	_ = c.ShouldBindJSON(&in)
	res, err := svc.TransferToBalance(c.Request.Context(), userID, in.Amount)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, res)
}

func (h *DoneBotHandler) MarketCreatorWithdrawals(c *gin.Context) {
	svc, userID, ok := h.marketActor(c)
	if !ok {
		return
	}
	page, pageSize := response.ParsePagination(c)
	items, total, err := svc.ListCreatorWithdrawals(c.Request.Context(), userID, page, pageSize)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}

func (h *DoneBotHandler) MarketCreatorWithdrawRequest(c *gin.Context) {
	svc, userID, ok := h.marketActor(c)
	if !ok {
		return
	}
	var in struct {
		Amount         float64 `json:"amount"`
		AlipayAccount  string  `json:"alipay_account"`
		AlipayRealName string  `json:"alipay_real_name"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "提现申请参数格式无效")
		return
	}
	rec, err := svc.RequestWithdrawal(c.Request.Context(), userID, in.Amount, in.AlipayAccount, in.AlipayRealName)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, rec)
}
