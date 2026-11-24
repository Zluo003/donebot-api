package admin

import (
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *DoneBotHandler) MobileReleases(c *gin.Context) {
	if h.releases == nil {
		response.Error(c, http.StatusServiceUnavailable, "Mobile release service is unavailable")
		return
	}
	out, err := h.releases.ListMobile(c.Request.Context())
	if !response.ErrorFrom(c, err) {
		response.Success(c, gin.H{"releases": out})
	}
}

func (h *DoneBotHandler) UploadMobileRelease(c *gin.Context) {
	if h.releases == nil {
		response.Error(c, http.StatusServiceUnavailable, "Mobile release service is unavailable")
		return
	}
	if c.Request.ContentLength > service.MaxClientReleaseUploadBytes {
		response.Error(c, http.StatusRequestEntityTooLarge, "Mobile package exceeds the 500 MB upload limit")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.MaxClientReleaseUploadBytes)
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			response.Error(c, http.StatusRequestEntityTooLarge, "Mobile package exceeds the 500 MB upload limit")
			return
		}
		response.BadRequest(c, "Invalid mobile release upload")
		return
	}
	if c.Request.MultipartForm != nil {
		defer func() { _ = c.Request.MultipartForm.RemoveAll() }()
	}
	files := c.Request.MultipartForm.File["file"]
	if len(files) != 1 {
		response.BadRequest(c, "Upload exactly one mobile package")
		return
	}
	versionCode, err := strconv.Atoi(strings.TrimSpace(c.PostForm("version_code")))
	if err != nil {
		response.BadRequest(c, "Version code must be a positive integer")
		return
	}
	forceUpdate := parseBoolFormValue(c.PostForm("force_update"))
	platform := strings.TrimSpace(c.PostForm("platform"))
	if platform == "" {
		platform = service.MobileReleaseAndroidAPK
	}
	file, err := files[0].Open()
	if err != nil {
		response.BadRequest(c, "Unable to read mobile package")
		return
	}
	defer func() { _ = file.Close() }()
	out, err := h.releases.UploadMobile(
		c.Request.Context(),
		c.PostForm("version"),
		versionCode,
		platform,
		files[0].Filename,
		c.PostForm("notes"),
		forceUpdate,
		file,
		files[0].Size,
	)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) PublishMobileRelease(c *gin.Context) {
	if h.releases == nil {
		response.Error(c, http.StatusServiceUnavailable, "Mobile release service is unavailable")
		return
	}
	out, err := h.releases.PublishMobile(c.Request.Context(), c.Param("version"))
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) DeleteMobileRelease(c *gin.Context) {
	if h.releases == nil {
		response.Error(c, http.StatusServiceUnavailable, "Mobile release service is unavailable")
		return
	}
	if err := h.releases.DeleteMobile(c.Request.Context(), c.Param("version")); !response.ErrorFrom(c, err) {
		response.Success(c, gin.H{"deleted": true})
	}
}

func (h *DoneBotHandler) LatestMobileRelease(c *gin.Context) {
	if h.releases == nil {
		response.Error(c, http.StatusServiceUnavailable, "Mobile release service is unavailable")
		return
	}
	out, err := h.releases.LatestMobile(c.Request.Context())
	if !response.ErrorFrom(c, err) {
		c.Header("Cache-Control", "no-store")
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CheckMobileRelease(c *gin.Context) {
	if h.releases == nil {
		response.Error(c, http.StatusServiceUnavailable, "Mobile release service is unavailable")
		return
	}
	var currentCode int
	if raw := strings.TrimSpace(c.Query("version_code")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			response.BadRequest(c, "Invalid mobile version code")
			return
		}
		currentCode = parsed
	}
	out, err := h.releases.CheckMobile(c.Request.Context(), c.Query("version"), currentCode, c.Query("platform"))
	if !response.ErrorFrom(c, err) {
		c.Header("Cache-Control", "no-store")
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) DownloadMobileRelease(c *gin.Context) {
	if h.releases == nil {
		response.Error(c, http.StatusServiceUnavailable, "Mobile release service is unavailable")
		return
	}
	artifact, reader, err := h.releases.OpenMobileArtifact(c.Request.Context(), c.Param("version"), c.Param("platform"))
	if response.ErrorFrom(c, err) {
		return
	}
	defer func() { _ = reader.Close() }()
	if artifact.Filename != c.Param("filename") {
		response.Error(c, http.StatusNotFound, "Mobile package was not found")
		return
	}
	filename := mime.FormatMediaType("attachment", map[string]string{"filename": artifact.Filename})
	c.Header("Content-Disposition", filename)
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Header("ETag", `"`+artifact.SHA256+`"`)
	c.Header("X-Checksum-Sha256", artifact.SHA256)
	c.DataFromReader(http.StatusOK, artifact.SizeBytes, "application/vnd.android.package-archive", reader, nil)
}

func parseBoolFormValue(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
