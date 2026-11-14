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
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil
	}
	ec, ok := key.(*ecdsa.PrivateKey)
	kid, team := os.Getenv("DONEBOT_APNS_KEY_ID"), os.Getenv("DONEBOT_APNS_TEAM_ID")
	if !ok || ec.Curve != elliptic.P256() || !appleID.MatchString(kid) || !appleID.MatchString(team) {
		return nil
	}
	return &APNs{ec, kid, team, &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
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
	if s.Push == nil {
		return unavailable()
	}
	if in.Platform != "ios" || in.Preview || !pushToken.MatchString(in.Token) || (in.Environment != "sandbox" && in.Environment != "production") {
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
	result, err := tx.ExecContext(ctx, `UPDATE donebot_mobile_devices SET push_token=$1,environment=$2 WHERE id=$3::uuid AND user_id=$4 AND session_id=$5`, in.Token, in.Environment, in.Installation, who.Owner, who.Session)
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
	_, err := s.DB.ExecContext(ctx, `UPDATE donebot_mobile_devices SET push_token=NULL,environment=NULL WHERE id=$1::uuid AND user_id=$2 AND session_id=$3`, id, who.Owner, who.Session)
	return err
}

type Event struct {
	Bot     string `json:"botId"`
	Thread  string `json:"threadId"`
	Message string `json:"messageId"`
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
		payload, _ := json.Marshal(map[string]any{"aps": map[string]any{"alert": map[string]string{"title": "DoneBot", "body": "Bot 有新消息"}, "sound": "default"}, "accountId": strconv.FormatInt(owner, 10), "workspaceId": host, "botId": event.Bot, "threadId": event.Thread, "messageId": event.Message})
		_, err = tx.ExecContext(ctx, `INSERT INTO donebot_mobile_outbox(host_id,device_id,session_id,payload) SELECT $1::uuid,id,session_id,$2::jsonb FROM donebot_mobile_devices WHERE user_id=$3 AND push_token IS NOT NULL AND expires_at>now()`, host, string(payload), owner)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Deliver leases rows across gateway replicas and checks the current binding again before sending.
func (s *Service) Deliver(ctx context.Context) error {
	if s == nil || s.Push == nil {
		return nil
	}
	rows, err := s.DB.QueryContext(ctx, `WITH due AS (SELECT id FROM donebot_mobile_outbox WHERE next_at<=now() AND attempts<5 AND created_at>now()-interval '5 minutes' ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 10) UPDATE donebot_mobile_outbox o SET next_at=now()+interval '120 seconds',attempts=attempts+1 FROM due WHERE o.id=due.id RETURNING o.id,o.host_id::text,o.device_id::text,o.session_id,o.payload`)
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
		valid := err == nil && token != ""
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
			invalidToken, sendErr = s.Push.Send(ctx, env, token, i.payload)
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
	_, err = s.DB.ExecContext(ctx, `DELETE FROM donebot_mobile_outbox WHERE attempts>=5 OR created_at<now()-interval '5 minutes'`)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `DELETE FROM donebot_mobile_events WHERE created_at<now()-interval '7 days'`)
	return err
}

func (s *Service) Wake() {
	if s == nil || s.Push == nil || !s.delivery.TryLock() {
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
	s.Wake()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Wake()
		}
	}
}
