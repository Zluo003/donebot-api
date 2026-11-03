package handler

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type assetHandlerService interface {
	Upload(context.Context, int64, string, string, []byte, string) (*service.GatewayAsset, error)
	Get(context.Context, int64, string, string) (*service.GatewayAsset, error)
	Delete(context.Context, int64, string) error
	Download(context.Context, string, string) (*service.GatewayAsset, io.ReadCloser, error)
}
type AssetHandler struct {
	assets assetHandlerService
	cfg    *config.Config
}

func NewAssetHandler(assets *service.AssetService, cfg *config.Config) *AssetHandler {
	return &AssetHandler{assets, cfg}
}
func assetError(c *gin.Context, err error) {
	status := infraerrors.Code(err)
	message := infraerrors.Message(err)
	if status >= 500 {
		message = "asset storage is unavailable"
	}
	c.JSON(status, gin.H{"error": gin.H{"type": "invalid_request_error", "code": infraerrors.Reason(err), "message": message}})
}
func (h *AssetHandler) owner(c *gin.Context) (int64, bool) {
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key == nil || key.ID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"type": "authentication_error", "message": "API key required"}})
		return 0, false
	}
	return key.ID, true
}
func (h *AssetHandler) baseURL(c *gin.Context) string {
	return (&DoneBotHandler{cfg: h.cfg}).gatewayURL(c)
}
func (h *AssetHandler) Upload(c *gin.Context) {
	owner, ok := h.owner(c)
	if !ok {
		return
	}
	limit := h.cfg.AssetStorage.MaxBytes
	if dynamic, ok := h.assets.(interface {
		UploadLimit(context.Context) (int64, error)
	}); ok {
		var err error
		limit, err = dynamic.UploadLimit(c.Request.Context())
		if err != nil {
			assetError(c, err)
			return
		}
	}
	if limit <= 0 {
		limit = 32 << 20
	}
	requestLimit := limit + (1 << 20)
	if max := h.cfg.Gateway.MaxBodySize; max > 0 && max < requestLimit {
		requestLimit = max
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, requestLimit)
	reader, err := c.Request.MultipartReader()
	if err != nil {
		assetError(c, infraerrors.BadRequest("ASSET_INVALID_UPLOAD", "multipart file is required"))
		return
	}
	var data []byte
	filename, contentType := "", ""
	files := 0
	for {
		part, e := reader.NextPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			var max *http.MaxBytesError
			if errors.As(e, &max) {
				assetError(c, service.ErrAssetTooLarge)
			} else {
				assetError(c, infraerrors.BadRequest("ASSET_INVALID_UPLOAD", "invalid multipart upload"))
			}
			return
		}
		if part.FormName() != "file" || part.FileName() == "" {
			_, e = io.Copy(io.Discard, io.LimitReader(part, 4097))
			_ = part.Close()
			if e != nil {
				assetError(c, service.ErrAssetTooLarge)
				return
			}
			continue
		}
		files++
		if files > 1 {
			_ = part.Close()
			assetError(c, infraerrors.BadRequest("ASSET_INVALID_UPLOAD", "exactly one file is required"))
			return
		}
		filename = part.FileName()
		contentType = part.Header.Get("Content-Type")
		data, e = io.ReadAll(io.LimitReader(part, limit+1))
		_ = part.Close()
		if e != nil || int64(len(data)) > limit {
			assetError(c, service.ErrAssetTooLarge)
			return
		}
	}
	if files != 1 {
		assetError(c, infraerrors.BadRequest("ASSET_INVALID_UPLOAD", "file is required"))
		return
	}
	result, err := h.assets.Upload(c.Request.Context(), owner, filename, contentType, data, h.baseURL(c))
	if err != nil {
		assetError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, result)
}
func (h *AssetHandler) Get(c *gin.Context) {
	owner, ok := h.owner(c)
	if !ok {
		return
	}
	result, err := h.assets.Get(c.Request.Context(), owner, c.Param("id"), h.baseURL(c))
	if err != nil {
		assetError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, result)
}
func (h *AssetHandler) Delete(c *gin.Context) {
	owner, ok := h.owner(c)
	if !ok {
		return
	}
	if err := h.assets.Delete(c.Request.Context(), owner, c.Param("id")); err != nil {
		assetError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
func (h *AssetHandler) Download(c *gin.Context) {
	a, reader, err := h.assets.Download(c.Request.Context(), c.Param("id"), c.Query("token"))
	if err != nil {
		assetError(c, err)
		return
	}
	defer func() { _ = reader.Close() }()
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	if a.MIME != "image/png" && a.MIME != "image/jpeg" && a.MIME != "image/webp" && a.MIME != "image/gif" {
		c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": a.ID}))
	}
	c.DataFromReader(http.StatusOK, a.Size, a.MIME, reader, nil)
}
