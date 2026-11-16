package admin

import (
	"errors"
	"mime"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *DoneBotHandler) ClientReleases(c *gin.Context) {
	if h.releases == nil {
		response.Error(c, http.StatusServiceUnavailable, "Client release service is unavailable")
		return
	}
	out, err := h.releases.List(c.Request.Context())
	if !response.ErrorFrom(c, err) {
		response.Success(c, gin.H{"releases": out})
	}
}

func (h *DoneBotHandler) UploadClientRelease(c *gin.Context) {
	if h.releases == nil {
		response.Error(c, http.StatusServiceUnavailable, "Client release service is unavailable")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.MaxClientReleaseBytes+(1<<20))
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			response.Error(c, http.StatusRequestEntityTooLarge, "Client package exceeds the 1 GiB upload limit")
			return
		}
		response.BadRequest(c, "Invalid client release upload")
		return
	}
	if c.Request.MultipartForm != nil {
		defer func() { _ = c.Request.MultipartForm.RemoveAll() }()
	}
	files := c.Request.MultipartForm.File["file"]
	if len(files) != 1 {
		response.BadRequest(c, "Upload exactly one client package")
		return
	}
	file, err := files[0].Open()
	if err != nil {
		response.BadRequest(c, "Unable to read client package")
		return
	}
	defer func() { _ = file.Close() }()
	out, err := h.releases.Upload(c.Request.Context(), c.PostForm("version"), c.PostForm("platform"), files[0].Filename, c.PostForm("notes"), file, files[0].Size)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) PublishClientRelease(c *gin.Context) {
	if h.releases == nil {
		response.Error(c, http.StatusServiceUnavailable, "Client release service is unavailable")
		return
	}
	out, err := h.releases.Publish(c.Request.Context(), c.Param("version"))
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) LatestClientRelease(c *gin.Context) {
	if h.releases == nil {
		response.Error(c, http.StatusServiceUnavailable, "Client release service is unavailable")
		return
	}
	out, err := h.releases.Latest(c.Request.Context())
	if !response.ErrorFrom(c, err) {
		c.Header("Cache-Control", "no-store")
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CheckClientRelease(c *gin.Context) {
	if h.releases == nil {
		response.Error(c, http.StatusServiceUnavailable, "Client release service is unavailable")
		return
	}
	out, err := h.releases.Check(c.Request.Context(), c.Query("version"), c.Query("platform"))
	if !response.ErrorFrom(c, err) {
		c.Header("Cache-Control", "no-store")
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) ClientUpdateFeed(c *gin.Context) {
	if h.releases == nil {
		response.Error(c, http.StatusServiceUnavailable, "Client release service is unavailable")
		return
	}
	feed, err := h.releases.UpdateFeed(c.Request.Context(), c.Param("platform"))
	if response.ErrorFrom(c, err) {
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "application/yaml; charset=utf-8", feed)
}

func (h *DoneBotHandler) DownloadClientRelease(c *gin.Context) {
	if h.releases == nil {
		response.Error(c, http.StatusServiceUnavailable, "Client release service is unavailable")
		return
	}
	artifact, reader, err := h.releases.OpenArtifact(c.Request.Context(), c.Param("version"), c.Param("platform"))
	if response.ErrorFrom(c, err) {
		return
	}
	defer func() { _ = reader.Close() }()
	if artifact.Filename != c.Param("filename") {
		response.Error(c, http.StatusNotFound, "Client package was not found")
		return
	}
	contentType := "application/zip"
	if c.Param("platform") == service.ClientReleaseWindowsX64 {
		contentType = "application/vnd.microsoft.portable-executable"
	}
	filename := mime.FormatMediaType("attachment", map[string]string{"filename": artifact.Filename})
	c.Header("Content-Disposition", filename)
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Header("ETag", `"`+artifact.SHA256+`"`)
	c.Header("X-Checksum-Sha256", artifact.SHA256)
	c.DataFromReader(http.StatusOK, artifact.SizeBytes, contentType, reader, nil)
}
