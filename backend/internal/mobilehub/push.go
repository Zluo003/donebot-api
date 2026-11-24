package mobilehub

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	infra "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

type Sender interface {
	Send(context.Context, string, string, []byte) (bool, error)
}
type APNs struct {
	Key           *ecdsa.PrivateKey
	KeyID, TeamID string
	Client        *http.Client
}

var appleID = regexp.MustCompile(`^[A-Z0-9]{10}$`)
var pushToken = regexp.MustCompile(`^[a-fA-F0-9]{64,200}$`)

func EnvironmentAPNs() *APNs {
	keyPath := os.Getenv("DONEBOT_APNS_KEY_FILE")
	if !filepath.IsAbs(keyPath) {
		return nil
	}
	root, err := os.OpenRoot(filepath.Dir(keyPath))
	if err != nil {
		return nil
	}
	defer func() { _ = root.Close() }()
	raw, err := root.ReadFile(filepath.Base(keyPath))
	if err != nil {
		return nil
	}
	a, _ := ParseAPNsKey(raw, os.Getenv("DONEBOT_APNS_KEY_ID"), os.Getenv("DONEBOT_APNS_TEAM_ID"))
	return a
}

func ParseAPNsKey(raw []byte, kid, team string) (*APNs, error) {
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, fmt.Errorf("invalid APNs private key")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("invalid APNs private key")
	}
	ec, ok := key.(*ecdsa.PrivateKey)
	if !ok || ec.Curve != elliptic.P256() || !appleID.MatchString(kid) || !appleID.MatchString(team) {
		return nil, fmt.Errorf("invalid APNs credentials")
	}
	return &APNs{ec, kid, team, &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (a *APNs) Send(ctx context.Context, env, token string, payload []byte) (bool, error) {
	if !pushToken.MatchString(token) || (env != "sandbox" && env != "production") {
		return true, fmt.Errorf("invalid APNs destination")
	}
	header, _ := json.Marshal(map[string]string{"alg": "ES256", "kid": a.KeyID})
	claims, _ := json.Marshal(map[string]any{"iss": a.TeamID, "iat": time.Now().Unix()})
	body := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	hash := sha256.Sum256([]byte(body))
	r, s, err := ecdsa.Sign(rand.Reader, a.Key, hash[:])
	if err != nil {
		return false, err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	host := "https://api.push.apple.com"
	if env == "sandbox" {
		host = "https://api.sandbox.push.apple.com"
	}
	req, err := http.NewRequestWithContext(ctx, "POST", host+"/3/device/"+token, bytes.NewReader(payload))
	if err != nil {
		return false, err
	}
	req.Header.Set("authorization", "bearer "+body+"."+base64.RawURLEncoding.EncodeToString(sig))
	req.Header.Set("apns-topic", "cn.donebot.mobile")
	req.Header.Set("apns-push-type", "alert")
	req.Header.Set("apns-priority", "10")
	req.Header.Set("apns-expiration", strconv.FormatInt(time.Now().Add(5*time.Minute).Unix(), 10))
	req.Header.Set("content-type", "application/json")
	res, err := a.Client.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	if res.StatusCode == 200 {
		return false, nil
	}
	var problem struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(raw, &problem)
	invalid := res.StatusCode == 410 || problem.Reason == "BadDeviceToken" || problem.Reason == "DeviceTokenNotForTopic"
	return invalid, fmt.Errorf("APNs rejected request (%d)", res.StatusCode)
}

type Subscription struct {
	Installation string `json:"installationId"`
	Platform     string `json:"platform"`
	Environment  string `json:"environment"`
	Token        string `json:"token"`
	Preview      bool   `json:"preview"`
}

func (s *Service) Subscribe(ctx context.Context, who Identity, in Subscription) error {
	if err := s.enabled(); err != nil {
		return err
	}
	if in.Preview || !validUUID(in.Installation) {
		return invalid()
	}
	switch in.Platform {
	case "ios":
		if s.pushSender() == nil {
			return infra.ServiceUnavailable("MOBILE_PUSH_UNAVAILABLE", "Apple push notifications are not configured")
		}
		if !pushToken.MatchString(in.Token) || (in.Environment != "sandbox" && in.Environment != "production") {
			return invalid()
		}
	case "android":
		if in.Environment != "sse" || in.Token != "android-sse:"+in.Installation {
			return invalid()
		}
	default:
		return invalid()
	}
	if err := s.bind(ctx, who, in.Installation); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('mobile-push:'||$1,0))`, in.Token); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE donebot_mobile_devices SET push_token=NULL,environment=NULL WHERE push_token=$1 AND id<>$2::uuid`, in.Token, in.Installation); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE donebot_mobile_devices SET push_token=$1,environment=$2,platform=$6 WHERE id=$3::uuid AND user_id=$4 AND session_id=$5`, in.Token, in.Environment, in.Installation, who.Owner, who.Session, in.Platform)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return invalid()
	}
	return tx.Commit()
}
func (s *Service) Unsubscribe(ctx context.Context, who Identity, id string) error {
	if err := s.enabled(); err != nil {
		return err
	}
	if !validUUID(id) {
		return invalid()
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE donebot_mobile_devices SET push_token=NULL,environment=NULL WHERE id=$1::uuid AND user_id=$2 AND session_id=$3`, id, who.Owner, who.Session); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM donebot_mobile_outbox WHERE device_id=$1::uuid AND session_id=$2`, id, who.Session)
	if err == nil {
		s.notifyPushWaiters(who.Owner)
	}
	return err
}

type Event struct {
	Bot     string `json:"botId"`
	Thread  string `json:"threadId"`
	Message string `json:"messageId"`
}

type StreamEvent struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	AccountID   string `json:"accountId"`
	WorkspaceID string `json:"workspaceId"`
	BotID       string `json:"botId"`
	ThreadID    string `json:"threadId"`
	MessageID   string `json:"messageId"`
}

func (s *Service) Events(ctx context.Context, owner int64, host string, events []Event) error {
	if err := s.enabled(); err != nil {
		return err
	}
	if len(events) > 50 {
		return invalid()
	}
	if _, _, err := s.host(ctx, owner, host); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	inserted := false
	for _, event := range events {
		if !wireID.MatchString(event.Bot) || !wireID.MatchString(event.Thread) || !wireID.MatchString(event.Message) {
			return invalid()
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO donebot_mobile_events(host_id,message_id) VALUES($1::uuid,$2) ON CONFLICT DO NOTHING`, host, event.Message)
		if err != nil {
			return err
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			continue
		}
		inserted = true
		payload, _ := json.Marshal(map[string]any{"aps": map[string]any{"alert": map[string]string{"title": "DoneBot", "body": "Bot 有新消息"}, "sound": "default"}, "accountId": strconv.FormatInt(owner, 10), "workspaceId": host, "botId": event.Bot, "threadId": event.Thread, "messageId": event.Message})
		_, err = tx.ExecContext(ctx, `INSERT INTO donebot_mobile_outbox(host_id,device_id,session_id,payload) SELECT $1::uuid,id,session_id,$2::jsonb FROM donebot_mobile_devices WHERE user_id=$3 AND push_token IS NOT NULL AND expires_at>now()`, host, string(payload), owner)
		if err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if inserted {
		s.notifyPushWaiters(owner)
	}
	return nil
}

func (s *Service) VerifyStreamDevice(ctx context.Context, who Identity, installation string) error {
	if err := s.enabled(); err != nil {
		return err
	}
	if !validUUID(installation) || who.Session == "" {
		return invalid()
	}
	if err := s.Check(ctx, who); err != nil {
		return err
	}
	var active bool
	err := s.DB.QueryRowContext(
		ctx,
		`SELECT EXISTS(SELECT 1 FROM donebot_mobile_devices WHERE id=$1::uuid AND user_id=$2 AND session_id=$3 AND platform='android' AND environment='sse' AND push_token=$4 AND expires_at>now())`,
		installation,
		who.Owner,
		who.Session,
		"android-sse:"+installation,
	).Scan(&active)
	if err != nil {
		return err
	}
	if !active {
		return infra.NotFound("MOBILE_PUSH_NOT_SUBSCRIBED", "Android push subscription is not active")
	}
	return nil
}

func (s *Service) PullStreamEvents(ctx context.Context, who Identity, installation string) ([]StreamEvent, error) {
	if err := s.VerifyStreamDevice(ctx, who, installation); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(
		ctx,
		`WITH batch AS (
			SELECT o.id
			FROM donebot_mobile_outbox o
			JOIN donebot_mobile_devices d ON d.id=o.device_id AND d.session_id=o.session_id
			WHERE o.device_id=$1::uuid
			  AND o.session_id=$2
			  AND d.user_id=$3
			  AND d.platform='android'
			  AND d.environment='sse'
			  AND d.push_token=$4
			  AND d.expires_at>now()
			  AND o.created_at>now()-interval '15 minutes'
			ORDER BY o.id
			FOR UPDATE OF o SKIP LOCKED
			LIMIT 20
		)
		DELETE FROM donebot_mobile_outbox o
		USING batch
		WHERE o.id=batch.id
		RETURNING o.id,o.host_id::text,o.payload`,
		installation,
		who.Session,
		who.Owner,
		"android-sse:"+installation,
	)
	if err != nil {
		return nil, err
	}
	type rawItem struct {
		id      int64
		host    string
		payload []byte
	}
	rawItems := []rawItem{}
	for rows.Next() {
		var item rawItem
		if err = rows.Scan(&item.id, &item.host, &item.payload); err != nil {
			_ = rows.Close()
			return nil, err
		}
		rawItems = append(rawItems, item)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	out := make([]StreamEvent, 0, len(rawItems))
	for _, item := range rawItems {
		if _, _, err = s.host(ctx, who.Owner, item.host); err != nil {
			if code := infra.Code(err); code != 401 && code != 403 && code != 404 {
				return nil, err
			}
			continue
		}
		var decoded struct {
			APS struct {
				Alert struct {
					Title string `json:"title"`
					Body  string `json:"body"`
				} `json:"alert"`
			} `json:"aps"`
			AccountID   string `json:"accountId"`
			WorkspaceID string `json:"workspaceId"`
			BotID       string `json:"botId"`
			ThreadID    string `json:"threadId"`
			MessageID   string `json:"messageId"`
		}
		if err = json.Unmarshal(item.payload, &decoded); err != nil {
			continue
		}
		title := decoded.APS.Alert.Title
		if title == "" {
			title = "DoneBot"
		}
		body := decoded.APS.Alert.Body
		if body == "" {
			body = "Bot 有新消息"
		}
		out = append(out, StreamEvent{
			ID:          item.id,
			Title:       title,
			Body:        body,
			AccountID:   decoded.AccountID,
			WorkspaceID: decoded.WorkspaceID,
			BotID:       decoded.BotID,
			ThreadID:    decoded.ThreadID,
			MessageID:   decoded.MessageID,
		})
	}
	return out, nil
}

// Deliver leases rows across gateway replicas and checks the current binding again before sending.
func (s *Service) Deliver(ctx context.Context) error {
	if s == nil || s.pushSender() == nil {
		return nil
	}
	rows, err := s.DB.QueryContext(ctx, `WITH due AS (SELECT o.id FROM donebot_mobile_outbox o LEFT JOIN donebot_mobile_devices d ON d.id=o.device_id AND d.session_id=o.session_id WHERE o.next_at<=now() AND o.attempts<5 AND o.created_at>now()-interval '5 minutes' AND NOT (COALESCE(d.platform,'')='android' AND COALESCE(d.environment,'')='sse') ORDER BY o.id FOR UPDATE OF o SKIP LOCKED LIMIT 10) UPDATE donebot_mobile_outbox o SET next_at=now()+interval '120 seconds',attempts=attempts+1 FROM due WHERE o.id=due.id RETURNING o.id,o.host_id::text,o.device_id::text,o.session_id,o.payload`)
	if err != nil {
		return err
	}
	type item struct {
		id                    int64
		host, device, session string
		payload               []byte
	}
	items := []item{}
	for rows.Next() {
		var i item
		if err = rows.Scan(&i.id, &i.host, &i.device, &i.session, &i.payload); err != nil {
			_ = rows.Close()
			return err
		}
		items = append(items, i)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, i := range items {
		var who Identity
		var token, env string
		err = s.DB.QueryRowContext(ctx, `SELECT user_id,session_id,token_version,COALESCE(push_token,''),COALESCE(environment,'') FROM donebot_mobile_devices WHERE id=$1::uuid AND session_id=$2 AND expires_at>now()`, i.device, i.session).Scan(&who.Owner, &who.Session, &who.Version, &token, &env)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		valid := err == nil && token != "" && (env == "sandbox" || env == "production")
		if valid {
			if err = s.Check(ctx, who); err != nil {
				if code := infra.Code(err); code != 401 && code != 403 && code != 404 {
					return err
				}
				valid = false
			}
		}
		if valid {
			if _, _, err = s.host(ctx, who.Owner, i.host); err != nil {
				if code := infra.Code(err); code != 401 && code != 403 && code != 404 {
					return err
				}
				valid = false
			}
		}
		invalidToken := false
		var sendErr error
		if valid {
			sender := s.pushSender()
			if sender == nil {
				return nil
			}
			invalidToken, sendErr = sender.Send(ctx, env, token, i.payload)
		}
		if invalidToken {
			if _, err = s.DB.ExecContext(ctx, `UPDATE donebot_mobile_devices SET push_token=NULL,environment=NULL WHERE id=$1::uuid AND session_id=$2 AND push_token=$3`, i.device, i.session, token); err != nil {
				return err
			}
		}
		if !valid || invalidToken || sendErr == nil {
			if _, err = s.DB.ExecContext(ctx, `DELETE FROM donebot_mobile_outbox WHERE id=$1`, i.id); err != nil {
				return err
			}
		}
	}
	return s.PruneOutbox(ctx)
}

func (s *Service) PruneOutbox(ctx context.Context) error {
	if s == nil || s.DB == nil {
		return nil
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM donebot_mobile_outbox o WHERE o.attempts>=5 OR o.created_at<now()-interval '15 minutes' OR (o.created_at<now()-interval '5 minutes' AND NOT EXISTS (SELECT 1 FROM donebot_mobile_devices d WHERE d.id=o.device_id AND d.session_id=o.session_id AND d.platform='android' AND d.environment='sse'))`)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `DELETE FROM donebot_mobile_events WHERE created_at<now()-interval '7 days'`)
	return err
}

func (s *Service) Wake() {
	if s == nil || s.pushSender() == nil || !s.delivery.TryLock() {
		return
	}
	go func() {
		defer s.delivery.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 110*time.Second)
		defer cancel()
		_ = s.Deliver(ctx)
	}()
}

func (s *Service) RunDelivery(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	pruneTicker := time.NewTicker(time.Minute)
	defer pruneTicker.Stop()
	s.Wake()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Wake()
		case <-pruneTicker.C:
			if s.pushSender() == nil {
				pruneCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				_ = s.PruneOutbox(pruneCtx)
				cancel()
			}
		}
	}
}
