package cloudbilling

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBrowserCachesOnlyVersionedApplicationAssets(t *testing.T) {
	for _, path := range []string{"/assets/index-Abc123xy.js", "/assets/index-Abc123xy.css", "/assets/font-Abc123xy.woff2"} {
		r := httptest.NewRequest("GET", "https://cloud.example.test"+path, nil)
		require.Equal(t, "private, max-age=31536000, immutable", browserResponseCache(r, &http.Response{StatusCode: 200}))
	}
	for _, path := range []string{"/", "/index.html", "/assets/index.js", "/assets/index-Abc123xy.js?token=secret", "/api/bots", "/api/attachments/private-Abc123xy.js", "/assets/private-Abc123xy.json"} {
		r := httptest.NewRequest("GET", "https://cloud.example.test"+path, nil)
		require.Equal(t, "no-store", browserResponseCache(r, &http.Response{StatusCode: 200}))
	}
	r := httptest.NewRequest("GET", "https://cloud.example.test/assets/index-Abc123xy.js", nil)
	for _, status := range []int{401, 403, 404, 500} {
		require.Equal(t, "no-store", browserResponseCache(r, &http.Response{StatusCode: status}))
	}
	r.Method = "POST"
	require.Equal(t, "no-store", browserResponseCache(r, &http.Response{StatusCode: 200}))
}

func TestBrowserNamespaceNeverFallsThroughToGateway(t *testing.T) {
	base, err := url.Parse("https://cloud.donebot.cn")
	require.NoError(t, err)
	master, err := url.Parse("https://donebot.cn")
	require.NoError(t, err)
	cfg := &BrowserConfig{Base: base, Master: master, Secure: true}
	id := "11861a47-7240-4f99-bf19-bd01d411596f"
	require.Equal(t, id, cfg.instance(id+".cloud.donebot.cn"))
	for _, host := range []string{"cloud.donebot.cn", "bad.cloud.donebot.cn", id + ".CLOUD.DONEBOT.CN", id + ".cloud.donebot.cn.", id + ".cloud.donebot.cn:443", id + ".cloud.donebot.cn:9443", "nested." + id + ".cloud.donebot.cn"} {
		t.Run(host, func(t *testing.T) {
			require.True(t, cfg.Matches(host))
			r := httptest.NewRequest("GET", "https://"+host+"/api/v1/auth/me", nil)
			w := httptest.NewRecorder()
			(&Service{}).ServeBrowser(w, r, cfg)
			require.Equal(t, 404, w.Code)
		})
	}
	for _, host := range []string{"donebot.cn", "cloud.donebot.cn.evil.test", "fakecloud.donebot.cn"} {
		require.False(t, cfg.Matches(host))
	}
}

func TestBrowserConfigurationRequiresSeparateHTTPSNamespace(t *testing.T) {
	s := &Service{}
	t.Setenv("DONEBOT_CLOUD_TEST_LOOPBACK_NODES", "")
	for _, pair := range [][2]string{
		{"http://cloud.donebot.cn", "https://donebot.cn"},
		{"https://donebot.cn", "https://donebot.cn"},
		{"https://cloud.donebot.cn", "https://admin.cloud.donebot.cn"},
		{"https://cloud.donebot.cn/instance", "https://donebot.cn"},
		{"https://*.cloud.donebot.cn", "https://donebot.cn"},
		{"https://127.0.0.1", "https://donebot.cn"},
	} {
		t.Setenv("DONEBOT_CLOUD_BROWSER_ORIGIN", pair[0])
		t.Setenv("DONEBOT_CLOUD_MASTER_ORIGIN", pair[1])
		_, err := s.BrowserConfig()
		require.Error(t, err)
	}
	t.Setenv("DONEBOT_CLOUD_BROWSER_ORIGIN", "https://cloud.donebot.cn")
	t.Setenv("DONEBOT_CLOUD_MASTER_ORIGIN", "https://donebot.cn")
	cfg, err := s.BrowserConfig()
	require.NoError(t, err)
	require.True(t, cfg.Secure)
	require.Equal(t, "__Host-dbc_session", browserCookie(cfg))
}
