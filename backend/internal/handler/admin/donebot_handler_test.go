package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type doneBotAdminStub struct {
	params  pagination.PaginationParams
	revoked []int64
}

func (s *doneBotAdminStub) List(_ context.Context, params pagination.PaginationParams) ([]service.DoneBotInstall, *pagination.PaginationResult, error) {
	s.params = params
	return []service.DoneBotInstall{{ID: 12, UserID: 7, Name: "Mac", Status: "active"}}, &pagination.PaginationResult{Total: 1}, nil
}
func (s *doneBotAdminStub) Revoke(_ context.Context, id int64) error {
	s.revoked = append(s.revoked, id)
	return nil
}

func TestDoneBotAdminListAndRevoke(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &doneBotAdminStub{}
	h := &DoneBotHandler{installs: svc}
	r := gin.New()
	r.GET("/installs", h.List)
	r.DELETE("/installs/:id", h.Revoke)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/installs?page=2&page_size=5", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 2, svc.params.Page)
	require.Equal(t, 5, svc.params.PageSize)
	require.Contains(t, w.Body.String(), `"user_id":7`)
	require.NotContains(t, w.Body.String(), "credential")
	require.NotContains(t, w.Body.String(), `"key"`)
	for _, id := range []string{"0", "-1", "abc", "12"} {
		w = httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/installs/"+id, nil))
		want := http.StatusBadRequest
		if id == "12" {
			want = http.StatusOK
		}
		require.Equal(t, want, w.Code, w.Body.String())
	}
	require.Equal(t, []int64{12}, svc.revoked)
}
