//go:build integration

package cloudbilling

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/donebotteam"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type teamFileRuntime struct {
	URL     string `json:"url"`
	DataDir string `json:"dataDir"`
	LogPath string `json:"logPath"`
}

func launchTeamFileRuntime(t *testing.T, script string, configuration any) teamFileRuntime {
	t.Helper()
	input, err := json.Marshal(configuration)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	cmd := exec.CommandContext(ctx, "node", "--experimental-strip-types", script)
	cmd.Dir = filepath.Dir(filepath.Dir(filepath.Dir(script)))
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.StdoutPipe()
	require.NoError(t, err)
	stderr, err := os.Create(filepath.Join(t.TempDir(), "runtime-stderr.log"))
	require.NoError(t, err)
	cmd.Stderr = stderr
	require.NoError(t, cmd.Start())
	ended := make(chan error, 1)
	go func() { ended <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-ended:
		case <-time.After(15 * time.Second):
			cancel()
			<-ended
		}
		cancel()
		_ = stderr.Close()
	})
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		if scanner.Scan() {
			ready <- scanner.Text()
		} else {
			ready <- ""
		}
		_, _ = io.Copy(io.Discard, output)
	}()
	var line string
	select {
	case line = <-ready:
	case <-time.After(30 * time.Second):
		t.Fatal("isolated team file runtime did not start")
	}
	var runtime teamFileRuntime
	require.NoError(t, json.Unmarshal([]byte(line), &runtime), line)
	require.NotEmpty(t, runtime.URL)
	require.DirExists(t, runtime.DataDir)
	t.Logf("isolated runtime evidence: %s", runtime.LogPath)
	return runtime
}

type teamFileProxyRequest struct {
	method  string
	path    string
	context context.Context
}

func teamFileProxyFixture(t *testing.T, limits map[string]any) (*Service, string, teamFileRuntime, *httptest.Server, <-chan teamFileProxyRequest) {
	t.Helper()
	if os.Getenv("CLOUD_BILLING_TEAM_FILES_FIXTURE") != "1" {
		t.Skip("set CLOUD_BILLING_TEAM_FILES_FIXTURE=1 to run the real isolated DoneBot file service")
	}
	script, err := filepath.Abs("../../../../DoneBot/scripts/testing/team-files-proxy-fixture.ts")
	require.NoError(t, err)
	require.FileExists(t, script)
	s, teamID, plan := teamCloudFixture(t)
	ctx := context.Background()
	t.Setenv("DONEBOT_CLOUD_TEST_LOOPBACK_NODES", "1")
	seed := []byte(newToken())[:ed25519.SeedSize]
	t.Setenv("DONEBOT_CLOUD_SIGNING_SEED", base64.StdEncoding.EncodeToString(seed))
	var target atomic.Pointer[url.URL]
	seen := make(chan teamFileProxyRequest, 64)
	workerProxy := &httputil.ReverseProxy{Rewrite: func(pr *httputil.ProxyRequest) {
		upstream := target.Load()
		pr.Out.URL.Scheme, pr.Out.URL.Host = upstream.Scheme, upstream.Host
		pr.Out.Host = upstream.Host
		pr.Out.URL.Path = strings.SplitN(pr.In.URL.Path, "/proxy", 2)[1]
		pr.Out.URL.RawPath = ""
	}}
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if target.Load() == nil || !strings.Contains(r.URL.Path, "/proxy/") || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			http.Error(w, "fixture worker refused request", 401)
			return
		}
		seen <- teamFileProxyRequest{r.Method, r.URL.Path, r.Context()}
		workerProxy.ServeHTTP(w, r)
	}))
	t.Cleanup(worker.Close)
	node, key, err := s.SaveNode(ctx, "", NodeInput{Name: "file-proxy-fixture", Endpoint: worker.URL, Capacity: 1, Enabled: true})
	require.NoError(t, err)
	_, err = s.Claim(ctx, node.ID, key, nil, donebotteam.Protocol)
	require.NoError(t, err)
	instance, _, err := s.MutateTeamCloud(ctx, teamID, 101, "start", "file-proxy-fixture", plan.ID)
	require.NoError(t, err)
	claim, err := s.Claim(ctx, node.ID, key, nil, donebotteam.Protocol)
	require.NoError(t, err)
	require.NotNil(t, claim.Command)
	require.NoError(t, s.Report(ctx, node.ID, key, claim.Command.ID, claim.Command.LeaseToken, ""))
	public, err := s.PublicKey()
	require.NoError(t, err)
	runtime := launchTeamFileRuntime(t, script, map[string]any{
		"id": teamID, "name": "File proxy fixture", "instanceId": instance.InstanceID, "publicKey": public,
		"files": limits,
	})
	runtimeURL, err := url.Parse(runtime.URL)
	require.NoError(t, err)
	target.Store(runtimeURL)
	identities := map[string]int64{"owner-fixture": 101, "admin-fixture": 202, "member-fixture": 303}
	s.liveCheck = func(_ context.Context, token string, actor int64) error {
		if identities[token] != actor || actor == 0 {
			return infraerrors.Unauthorized("FIXTURE", "Invalid fixture identity")
		}
		return nil
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor := identities[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		if actor == 0 {
			http.Error(w, "invalid fixture identity", 401)
			return
		}
		if err := s.TeamProxy(w, r, teamID, actor, r.URL.Path); err != nil {
			http.Error(w, err.Error(), 403)
		}
	}))
	t.Cleanup(proxy.Close)
	return s, teamID, runtime, proxy, seen
}

func TestPostgresTeamFileProxyCancelsRevokedUploads(t *testing.T) {
	s, teamID, runtime, proxy, _ := teamFileProxyFixture(t, map[string]any{"cap": 128, "uploadMax": 64})
	ctx := context.Background()
	client := &http.Client{Timeout: 12 * time.Second}
	request := func(method, path string, body io.Reader) (*http.Response, error) {
		r, e := http.NewRequest(method, proxy.URL+path, body)
		if e != nil {
			return nil, e
		}
		r.Header.Set("Authorization", "Bearer member-fixture")
		r.Header.Set("Cookie", "owner=must-not-forward")
		r.Header.Set("X-Donebot-Team-Grant", "forged-owner")
		return client.Do(r)
	}
	check := func(method, path, body string, status int) []byte {
		t.Helper()
		r, e := request(method, path, strings.NewReader(body))
		require.NoError(t, e)
		defer r.Body.Close()
		data, e := io.ReadAll(r.Body)
		require.NoError(t, e)
		require.Equal(t, status, r.StatusCode, string(data))
		return data
	}
	check("PUT", "/api/team-files/file?name=kept.txt", "original", 201)
	check("PUT", "/api/team-files/file?name=oversized.txt", strings.Repeat("x", 65), 413)
	check("POST", "/api/team-files/folder", `{"path":"templates"}`, 403)
	var listing struct {
		Admin   bool `json:"admin"`
		Entries []struct {
			Name      string `json:"name"`
			CanDelete bool   `json:"canDelete"`
		} `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(check("GET", "/api/team-files?path=", "", 200), &listing))
	require.False(t, listing.Admin)
	require.Len(t, listing.Entries, 1)
	require.True(t, listing.Entries[0].CanDelete)
	readers, writers := make([]*io.PipeReader, 2), make([]*io.PipeWriter, 2)
	finished := make(chan struct{}, 2)
	for i, name := range []string{"new.txt", "kept.txt"} {
		readers[i], writers[i] = io.Pipe()
		t.Cleanup(func() { _ = readers[i].Close(); _ = writers[i].Close() })
		go func(index int, name string) {
			defer func() { finished <- struct{}{} }()
			response, e := request("PUT", "/api/team-files/file?name="+name, readers[index])
			if e == nil {
				_, _ = io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
			}
		}(i, name)
		_, err := writers[i].Write([]byte("staged"))
		require.NoError(t, err)
	}
	staged := func() int {
		entries, _ := os.ReadDir(filepath.Join(runtime.DataDir, "tmp", "team-files-uploads"))
		return len(entries)
	}
	require.Eventually(t, func() bool { return staged() == 2 }, 3*time.Second, 20*time.Millisecond)
	require.NoError(t, s.Teams().ChangeMember(ctx, teamID, 101, 303, "", true))
	require.Eventually(t, func() bool { return staged() == 0 }, 7*time.Second, 50*time.Millisecond, "revocation must abort the live Go proxy body and clear staged files")
	require.NoFileExists(t, filepath.Join(runtime.DataDir, "team-files", "new.txt"))
	kept, err := os.ReadFile(filepath.Join(runtime.DataDir, "team-files", "kept.txt"))
	require.NoError(t, err)
	require.Equal(t, "original", string(kept))
	for _, writer := range writers {
		_ = writer.Close()
	}
	for range writers {
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			t.Fatal("revoked upload client remained open")
		}
	}
	check("GET", "/api/team-files", "", 403)
	t.Log("real Go proxy and isolated Node files: member identity, upload cap, templates, revocation cleanup and replacement rollback passed")
}
