//go:build integration

package cloudbilling

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/donebotteam"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func teamCloudFixture(t *testing.T) (*Service, string, TeamPlan) {
	t.Helper()
	s := postgresFixture(t)
	t.Setenv("DONEBOT_TEAMS_ENABLED", "1")
	_, err := s.db.Exec(`UPDATE users SET balance=100,email=id::text || '@fixture.example.test'`)
	require.NoError(t, err)
	team, err := s.Teams().Create(context.Background(), 101, "Studio", uuid.NewString())
	require.NoError(t, err)
	_, err = s.db.Exec(`INSERT INTO donebot_team_members(team_id,user_id,role) VALUES($1::uuid,202,'admin'),($1::uuid,303,'member')`, team.ID)
	require.NoError(t, err)
	p := TeamPlan{ID: uuid.NewString(), Name: "Studio plan", Enabled: true, MonthlyPrice: "7", RetentionDays: 11,
		Runtime: TeamResources{1000, 2048, 5, 256}, Desktop: TeamResources{1000, 2048, 10, 256}, SharedQuotaBytes: 2 << 30, MaxFileBytes: 100 << 20}
	_, err = s.SaveTeamPlans(context.Background(), []TeamPlan{p})
	require.NoError(t, err)
	return s, team.ID, p
}

func TestPostgresTeamCloudBillingScopeAndImmutableSnapshot(t *testing.T) {
	s, teamID, plan := teamCloudFixture(t)
	ctx := context.Background()
	node, key, err := s.SaveNode(ctx, "", NodeInput{Name: "team-fixture", Endpoint: "https://10.42.0.1", Capacity: 10, Enabled: true})
	require.NoError(t, err)
	_, err = s.Claim(ctx, node.ID, key, nil)
	require.NoError(t, err)
	_, err = s.db.Exec(`UPDATE donebot_cloud_nodes SET team_protocol=$2 WHERE id=$1::uuid`, node.ID, donebotteam.Protocol)
	require.NoError(t, err)
	personal, _, err := s.Mutate(ctx, 101, "", "start", "personal")
	require.NoError(t, err)
	for _, actor := range []int64{202, 303} {
		_, _, err = s.MutateTeamCloud(ctx, teamID, actor, "start", uuid.NewString(), plan.ID)
		require.Error(t, err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 12)
	ids := make(chan string, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, _, e := s.MutateTeamCloud(ctx, teamID, 101, "start", "same-purchase", plan.ID)
			failures <- e
			if v != nil {
				ids <- v.InstanceID
			}
		}()
	}
	wg.Wait()
	close(failures)
	close(ids)
	for e := range failures {
		require.NoError(t, e)
	}
	var teamInstance string
	for id := range ids {
		if teamInstance == "" {
			teamInstance = id
		}
		require.Equal(t, teamInstance, id)
	}
	require.NotEqual(t, personal.ID, teamInstance)
	var charges int
	var balance string
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM donebot_cloud_money_events WHERE instance_id=$1::uuid AND event='charge'`, teamInstance).Scan(&charges))
	require.Equal(t, 1, charges)
	require.NoError(t, s.db.QueryRow(`SELECT balance::text FROM users WHERE id=101`).Scan(&balance))
	require.Equal(t, "83.0000000000", balance)
	instances, err := s.Instances(ctx, 101)
	require.NoError(t, err)
	require.Len(t, instances, 1)
	require.Equal(t, personal.ID, instances[0].ID)
	_, err = s.Owned(ctx, teamInstance, 101)
	require.Error(t, err)
	_, _, err = s.Mutate(ctx, 101, teamInstance, "stop", "personal-route")
	require.Error(t, err)
	v, err := s.TeamCloud(ctx, teamID, 303)
	require.NoError(t, err)
	encoded, err := json.Marshal(v)
	require.NoError(t, err)
	for _, secret := range []string{"user_id", "payer_id", "node_id", "plan_snapshot", "credential", "balance"} {
		require.NotContains(t, string(encoded), secret)
	}
	other, err := s.Teams().Create(ctx, 101, "Other", uuid.NewString())
	require.NoError(t, err)
	_, err = s.TeamCloud(ctx, other.ID, 303)
	require.Error(t, err)
	second, _, err := s.MutateTeamCloud(ctx, other.ID, 101, "start", "second-team", plan.ID)
	require.NoError(t, err)
	require.NotEqual(t, teamInstance, second.InstanceID)
	_, err = s.db.Exec(`UPDATE donebot_cloud_instances SET user_id=303 WHERE id=$1::uuid`, teamInstance)
	require.Error(t, err)
	_, err = s.db.Exec(`UPDATE donebot_cloud_instances SET plan_snapshot='{}' WHERE id=$1::uuid`, teamInstance)
	require.Error(t, err)
	for i := 0; i < 3; i++ {
		claimed, e := s.Claim(ctx, node.ID, key, nil)
		require.NoError(t, e)
		require.NotNil(t, claimed.Command)
		require.NoError(t, s.Report(ctx, node.ID, key, claimed.Command.ID, claimed.Command.LeaseToken, ""))
	}
	cancelled, _, err := s.MutateTeamCloud(ctx, teamID, 101, "cancel", "cancel-renewal", "")
	require.NoError(t, err)
	require.False(t, cancelled.AutoRenew)
	kept, _, err := s.MutateTeamCloud(ctx, teamID, 101, "keep", "restore-renewal", "")
	require.NoError(t, err)
	require.True(t, kept.AutoRenew)
	plan.MonthlyPrice = "99"
	plan.RetentionDays = 1
	_, err = s.SaveTeamPlans(ctx, []TeamPlan{plan})
	require.NoError(t, err)
	_, err = s.db.Exec(`UPDATE donebot_cloud_subscriptions SET paid_until=NOW()-INTERVAL '1 second' WHERE instance_id=$1::uuid`, teamInstance)
	require.NoError(t, err)
	_, err = s.Action(ctx, teamInstance, "renew", 0)
	require.NoError(t, err)
	require.NoError(t, s.db.QueryRow(`SELECT balance::text FROM users WHERE id=101`).Scan(&balance))
	require.Equal(t, "69.0000000000", balance)
	var price string
	var cycle int
	require.NoError(t, s.db.QueryRow(`SELECT cycle_price::text,cycle FROM donebot_team_cloud_subscriptions WHERE id=$1::uuid`, teamInstance).Scan(&price, &cycle))
	require.Equal(t, "7.000000", price)
	require.Equal(t, 2, cycle)
	_, err = s.db.Exec(`UPDATE donebot_cloud_subscriptions SET paid_until=NOW()-INTERVAL '8 days',auto_renew=FALSE WHERE instance_id=$1::uuid`, teamInstance)
	require.NoError(t, err)
	require.NoError(t, s.Sweep(ctx))
	var desired string
	require.NoError(t, s.db.QueryRow(`SELECT desired FROM donebot_cloud_instances WHERE id=$1::uuid`, teamInstance).Scan(&desired))
	require.Equal(t, "running", desired)
	var stops int
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM donebot_cloud_operations WHERE instance_id=$1::uuid AND action='stop' AND state='pending'`, teamInstance).Scan(&stops))
	require.Equal(t, 1, stops)
	_, err = s.db.Exec(`UPDATE donebot_cloud_subscriptions SET paid_until=NOW()-INTERVAL '12 days' WHERE instance_id=$1::uuid`, teamInstance)
	require.NoError(t, err)
	require.NoError(t, s.Sweep(ctx))
	require.NoError(t, s.db.QueryRow(`SELECT desired FROM donebot_cloud_instances WHERE id=$1::uuid`, teamInstance).Scan(&desired))
	require.Equal(t, "deleting", desired)
}

func TestPostgresTeamProxySignsActualMemberAndFencesRevocation(t *testing.T) {
	s, teamID, plan := teamCloudFixture(t)
	ctx := context.Background()
	t.Setenv("DONEBOT_CLOUD_TEST_LOOPBACK_NODES", "1")
	seed := []byte(newToken())[:32]
	t.Setenv("DONEBOT_CLOUD_SIGNING_SEED", base64.StdEncoding.EncodeToString(seed))
	private := ed25519.NewKeyFromSeed(seed)
	seen := make(chan map[string]any, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/proxy/") {
			if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
				w.WriteHeader(500)
				return
			}
			parts := strings.Split(r.Header.Get("X-Donebot-Team-Grant"), ".")
			if len(parts) != 2 {
				w.WriteHeader(401)
				return
			}
			payload, _ := base64.RawURLEncoding.DecodeString(parts[0])
			sig, _ := base64.RawURLEncoding.DecodeString(parts[1])
			if !ed25519.Verify(private.Public().(ed25519.PublicKey), payload, sig) {
				w.WriteHeader(401)
				return
			}
			var claim map[string]any
			if json.Unmarshal(payload, &claim) != nil {
				w.WriteHeader(400)
				return
			}
			seen <- claim
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}
	}))
	defer upstream.Close()
	node, key, err := s.SaveNode(ctx, "", NodeInput{Name: "team-proxy", Endpoint: upstream.URL, Capacity: 1, Enabled: true})
	require.NoError(t, err)
	_, err = s.Claim(ctx, node.ID, key, nil)
	require.NoError(t, err)
	_, err = s.db.Exec(`UPDATE donebot_cloud_nodes SET team_protocol=$2 WHERE id=$1::uuid`, node.ID, donebotteam.Protocol)
	require.NoError(t, err)
	v, _, err := s.MutateTeamCloud(ctx, teamID, 101, "start", "proxy-team", plan.ID)
	require.NoError(t, err)
	claim, err := s.Claim(ctx, node.ID, key, nil)
	require.NoError(t, err)
	require.NotNil(t, claim.Command)
	require.NoError(t, s.Report(ctx, node.ID, key, claim.Command.ID, claim.Command.LeaseToken, ""))
	s.liveCheck = func(_ context.Context, token string, actor int64) error {
		if token != "member-fixture" || actor != 303 {
			return infraerrors.Unauthorized("FIXTURE", "Invalid fixture identity")
		}
		return nil
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if e := s.TeamProxy(w, r, teamID, 303, "/api/events"); e != nil {
			http.Error(w, e.Error(), 403)
		}
	}))
	defer proxy.Close()
	r, err := http.NewRequest("GET", proxy.URL+"?cursor=exact", nil)
	require.NoError(t, err)
	r.Header.Set("Authorization", "Bearer member-fixture")
	r.Header.Set("Cookie", "personal=never-forward")
	r.Header.Set("X-Donebot-Team-Grant", "forged-owner")
	response, err := http.DefaultClient.Do(r)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, 200, response.StatusCode)
	got := <-seen
	require.Equal(t, "303", got["user_id"])
	require.Equal(t, "member", got["role"])
	require.Equal(t, teamID, got["team_id"])
	require.Equal(t, v.InstanceID, got["instance_id"])
	require.Equal(t, tokenHash("GET\n/api/events?cursor=exact"), got["runtime_hash"])
	require.NoError(t, s.Teams().ChangeMember(ctx, teamID, 101, 303, "", true))
	ended := make(chan error, 1)
	go func() { var b [1]byte; _, e := response.Body.Read(b[:]); ended <- e }()
	select {
	case e := <-ended:
		require.Error(t, e)
	case <-time.After(6 * time.Second):
		t.Fatal("revoked member stream stayed open")
	}
	w := httptest.NewRecorder()
	r = httptest.NewRequest("GET", "http://fixture/api/events", nil)
	r.Header.Set("Authorization", "Bearer member-fixture")
	require.Error(t, s.TeamProxy(w, r, teamID, 303, "/api/events"))
}
