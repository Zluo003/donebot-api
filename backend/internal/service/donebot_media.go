package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const SeedanceEndpointContent GrokMediaEndpoint = "seedance_content"

var ErrSeedanceVideoNotReady = errors.New("video content is not ready")

func (s *OpenAIGatewayService) IsSeedanceVideoRequest(ctx context.Context, key *APIKey, model string) (bool, error) {
	if s == nil || key == nil || key.Group == nil || (key.Group.Platform != PlatformOpenAI && key.Group.Platform != PlatformComposite) || strings.TrimSpace(model) == "" {
		return false, nil
	}
	mapping, _ := s.ResolveChannelMappingAndRestrict(ctx, key.GroupID, model)
	if platform, resolved := ResolvedTargetPlatformFromContext(ctx); resolved && platform != PlatformOpenAI {
		return false, nil
	}
	accounts, err := s.listSchedulableAccounts(ctx, key.GroupID, PlatformOpenAI)
	if err != nil {
		return false, err
	}
	for i := range accounts {
		a := &accounts[i]
		if a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilitySeedance) && a.IsModelSupported(mapping.MappedModel) && SeedanceSpecModelForAccount(a, mapping.MappedModel) != "" {
			return true, nil
		}
	}
	return false, nil
}

type doneBotLocalMediaReader interface {
	Get(context.Context, int64, string, string) (*GatewayAsset, error)
	Download(context.Context, string, string) (*GatewayAsset, io.ReadCloser, error)
}

func (s *OpenAIGatewayService) WriteSeedanceVideoContent(ctx context.Context, c *gin.Context, result *OpenAIForwardResult, owner int64) error {
	if result == nil || gjson.GetBytes(result.SeedanceResponseBody, "status").String() != "succeeded" {
		return ErrSeedanceVideoNotReady
	}
	source, err := url.Parse(gjson.GetBytes(result.SeedanceResponseBody, "content.video_url").String())
	if err != nil || source.User != nil || source.Fragment != "" || source.Path == "" || (source.Scheme != "" && source.Scheme != "http" && source.Scheme != "https") || owner <= 0 {
		return fmt.Errorf("invalid video content URL")
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	if strings.HasPrefix(source.Path, "/assets/ast_") && !strings.Contains(strings.TrimPrefix(source.Path, "/assets/"), "/") && s.generatedMedia != nil {
		if reader, ok := s.generatedMedia.assets.(doneBotLocalMediaReader); ok {
			id := strings.TrimPrefix(source.Path, "/assets/")
			asset, err := reader.Get(ctx, owner, id, "")
			if err != nil {
				return err
			}
			if asset.Storage == "local" {
				asset, body, err := reader.Download(ctx, id, source.Query().Get("token"))
				if err != nil {
					return err
				}
				defer func() { _ = body.Close() }()
				c.Header("Content-Type", asset.MIME)
				c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": id + ".mp4"}))
				if seeker, ok := body.(io.ReadSeeker); ok {
					MarkResponseCommitted(c)
					http.ServeContent(c.Writer, c.Request, id, asset.CreatedAt, seeker)
					return nil
				}
				return writeGrokMediaContentResponse(c, &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: body, ContentLength: asset.Size})
			}
		}
	}
	if source.Host == "" || source.Scheme == "" {
		return fmt.Errorf("video content requires a public HTTP(S) URL")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.String(), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "video/*, application/octet-stream")
	if value := c.GetHeader("Range"); value != "" {
		request.Header.Set("Range", value)
	}
	client := defaultImageDownloadHTTPClient()
	if s.generatedMedia != nil && s.generatedMedia.client != nil {
		client = s.generatedMedia.client
	}
	copyClient := *client
	copyClient.Jar = nil
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := copyClient.Do(request)
	if err != nil {
		return fmt.Errorf("video content download failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent && response.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		return fmt.Errorf("video content download returned status %d", response.StatusCode)
	}
	contentType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != http.StatusRequestedRangeNotSatisfiable && contentType != "" && contentType != "application/octet-stream" && !strings.HasPrefix(contentType, "video/") {
		return fmt.Errorf("video content response is not video")
	}
	return writeGrokMediaContentResponse(c, response)
}
