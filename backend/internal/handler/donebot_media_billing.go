package handler

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const doneBotImageSettlementPending = "donebot.image.settlement_pending"

func (h *OpenAIGatewayHandler) refundDoneBotRejectedMedia(c *gin.Context, status int) {
	notDispatched, _ := c.Get(middleware.DoneBotMediaNotDispatched)
	if status < 400 || status >= 500 && notDispatched != true || status == 408 || status == 499 || !service.DoneBotMediaPrepaidSettlementEnabled(c.Request.Context()) {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 15*time.Second)
	defer cancel()
	if err := h.gatewayService.RejectDoneBotMediaReceipt(ctx); err != nil {
		_ = c.Error(err)
	}
}
