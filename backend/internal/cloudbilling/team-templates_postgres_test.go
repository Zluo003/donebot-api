//go:build integration

package cloudbilling

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPostgresTeamTemplateProxyUsesActualRoleAndFencesRevokedPublication(t *testing.T) {
	s, teamID, runtime, proxy, seen := teamFileProxyFixture(t, map[string]any{"cap": 100_000, "uploadMax": 50_000, "templates": true})
	ctx := context.Background()
	client := &http.Client{Timeout: 12 * time.Second}
	document := `{"format":"openmaus.package","version":2,"package":{"id":"helper","release":"1.0.0","name":"Shared helper","tagline":"Synthetic template.","summary":"Review before installing.","category":"Testing","author":{"name":"Fixture"},"license":"MIT","outcomes":["Read files."],"setupMinutes":2,"requirements":{"apps":[],"capabilities":[]},"team":{"name":"Shared helper"},"agents":[{"key":"helper","name":"Helper","appearance":{"color":"blue"},"skills":["report"]}],"skills":{"version":1,"entries":[{"name":"report","description":"Report steps.","instructions":"---\nname: report\ndescription: Report steps.\n---\nRead scripts/report.py.\n","files":[{"path":"scripts/report.py","content":"raise RuntimeError('TEMPLATE_SCRIPT_MUST_NOT_RUN')\n"}]}]}}}`
	publish := func(revision any, release string) string {
		value := strings.Replace(document, `"release":"1.0.0"`, `"release":"`+release+`"`, 1)
		body, err := json.Marshal(map[string]any{"confirm": true, "slug": "helper", "scope": "bot", "revision": revision, "document": json.RawMessage(value)})
		require.NoError(t, err)
		return string(body)
	}
	request := func(token, method, path string, body io.Reader) (*http.Response, error) {
		r, err := http.NewRequest(method, proxy.URL+path, body)
		if err != nil {
			return nil, err
		}
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Cookie", "owner=must-not-forward")
		r.Header.Set("X-Donebot-Team-Grant", "forged-owner")
		r.Header.Set("Content-Type", "application/json")
		return client.Do(r)
	}
	check := func(token, method, path, body string, status int) []byte {
		t.Helper()
		response, err := request(token, method, path, strings.NewReader(body))
		require.NoError(t, err)
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		require.Equal(t, status, response.StatusCode, string(data))
		return data
	}
	check("member-fixture", "POST", "/api/team-templates", publish(nil, "1.0.0"), 403)
	var created struct {
		Template struct {
			Revision string `json:"revision"`
		} `json:"template"`
	}
	require.NoError(t, json.Unmarshal(check("owner-fixture", "POST", "/api/team-templates", publish(nil, "1.0.0"), 201), &created))
	require.Len(t, created.Template.Revision, 64)
	preview := check("member-fixture", "GET", "/api/team-templates/helper", "", 200)
	require.Contains(t, string(preview), "TEMPLATE_SCRIPT_MUST_NOT_RUN")
	check("admin-fixture", "POST", "/api/team-templates", publish(created.Template.Revision, "2.0.0"), 201)
	check("owner-fixture", "POST", "/api/team-templates", publish(created.Template.Revision, "3.0.0"), 409)
	for len(seen) > 0 {
		<-seen
	}
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
	finished := make(chan struct{}, 1)
	go func() {
		defer func() { finished <- struct{}{} }()
		response, err := request("admin-fixture", "POST", "/api/team-templates", reader)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
	}()
	_, err := writer.Write([]byte(`{"confirm":true,"slug":"held",`))
	require.NoError(t, err)
	var pending teamFileProxyRequest
	select {
	case pending = <-seen:
	case <-time.After(3 * time.Second):
		t.Fatal("pending publication did not reach the worker")
	}
	require.Equal(t, "POST", pending.method)
	require.True(t, strings.HasSuffix(pending.path, "/api/team-templates"))
	require.NoError(t, s.Teams().ChangeMember(ctx, teamID, 101, 202, "member", false))
	select {
	case <-pending.context.Done():
	case <-time.After(7 * time.Second):
		t.Fatal("downgraded administrator's upstream publication remained open")
	}
	_ = writer.Close()
	_ = reader.Close()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("closed publication client did not finish")
	}
	check("admin-fixture", "POST", "/api/team-templates", publish(nil, "3.0.0"), 403)
	require.NoDirExists(t, filepath.Join(runtime.DataDir, "team-files/templates/held"))
	entries, err := os.ReadDir(filepath.Join(runtime.DataDir, "share"))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "team-library.json", entries[0].Name())
	check("member-fixture", "GET", "/api/team-templates/helper", "", 200)
	require.NoError(t, s.Teams().ChangeMember(ctx, teamID, 101, 303, "", true))
	check("member-fixture", "GET", "/api/team-templates/helper", "", 403)
	t.Log("real Go template proxy: verified roles, inert reads, stale revisions, revoked pending publication and subsequent denial passed")
}
