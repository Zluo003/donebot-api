package service

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
	"github.com/tidwall/gjson"
)

const doneBotMediaDiagnosticFallback = "Media request could not be completed."

var (
	doneBotMediaDiagnosticIdentifier = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,160}$`)
	doneBotMediaDiagnosticURL        = regexp.MustCompile("(?i)\\b[a-z][a-z0-9+.-]*://[^\\s<>\\\"'`]+|\\bhttps?%3a%2f%2f[^\\s<>\\\"'`]+|(?:^|\\s)//[^\\s<>\\\"'`]+|\\bdata:[^\\s<>\\\"'`]+")
	doneBotMediaDiagnosticHost       = regexp.MustCompile("(?i)\\b(?:[a-z0-9-]+\\.)+[a-z]{2,63}(?::[0-9]{1,5})?(?:/[^\\s<>\\\"'`]*)?")
	doneBotMediaDiagnosticSecret     = regexp.MustCompile(`(?i)\b(?:(?:sk-|rk_|xai-|gsk_|csk-|hf_|gh[pousr]_|github_pat_|xox[baprs]-)[a-z0-9_-]+|AIza[a-z0-9_-]*|AKIA[A-Z0-9]*|eyJ[a-z0-9_-]*(?:\.[a-z0-9_-]+)*)\b`)
	doneBotMediaDiagnosticBearer     = regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[a-z0-9._~+/-]+=*`)
	doneBotMediaDiagnosticAssignment = regexp.MustCompile(`(?i)\b(authorization|api[_ -]?key|key|access[_ -]?token|refresh[_ -]?token|token|secret(?:[_ -]?access[_ -]?key)?|password|passwd|client[_ -]?secret)\b["']?\s*[=:]\s*(?:"[^"]*"|'[^']*'|[^\s,;]+)`)
	doneBotMediaDiagnosticRequest    = regexp.MustCompile(`(?i)\b(?:prompt|input|messages|request[_ -]?body|payload)\b["']?\s*[=:]`)
	doneBotMediaDiagnosticEmail      = regexp.MustCompile(`[A-Za-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	doneBotMediaDiagnosticPath       = regexp.MustCompile(`(?:/(?:Users|home|var|tmp|private|etc)/|[A-Za-z]:\\)[^\s<>"']+`)
	doneBotMediaDiagnosticJSONBody   = regexp.MustCompile(`(?:\{|\[)\s*(?:"|'|\{|\[)`)
	doneBotMediaDiagnosticHTML       = regexp.MustCompile(`(?i)<!doctype|<html|<body|<script`)
)

// DoneBotMediaDiagnostic is the bounded, redacted error detail available to the
// authenticated task owner. Its status describes the HTTP exchange, not settlement.
type DoneBotMediaDiagnostic struct {
	HTTPStatus int    `json:"http_status,omitempty"`
	Code       string `json:"code,omitempty"`
	Type       string `json:"type,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
	Message    string `json:"message,omitempty"`
}

func doneBotMediaDiagnosticID(value string) string {
	value = strings.TrimSpace(value)
	if !doneBotMediaDiagnosticIdentifier.MatchString(value) || doneBotMediaDiagnosticSecret.MatchString(value) {
		return ""
	}
	return value
}

func doneBotMediaDiagnosticMessage(message string) string {
	if len(message) > 8192 || !utf8.ValidString(message) || doneBotMediaDiagnosticHTML.MatchString(message) {
		return doneBotMediaDiagnosticFallback
	}
	message = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, message)
	if location := doneBotMediaDiagnosticRequest.FindStringIndex(message); location != nil {
		message = message[:location[0]] + "[redacted request content]"
	}
	if location := doneBotMediaDiagnosticJSONBody.FindStringIndex(message); location != nil {
		message = message[:location[0]] + "[redacted response details]"
	}
	message = strings.ReplaceAll(message, `\/`, "/")
	message = doneBotMediaDiagnosticURL.ReplaceAllString(message, "[redacted URL]")
	message = doneBotMediaDiagnosticEmail.ReplaceAllString(message, "[redacted email]")
	message = doneBotMediaDiagnosticHost.ReplaceAllString(message, "[redacted host]")
	message = doneBotMediaDiagnosticPath.ReplaceAllString(message, "[redacted path]")
	message = logredact.RedactText(message, "authorization", "api_key", "api-key", "token", "secret", "passwd")
	message = doneBotMediaDiagnosticAssignment.ReplaceAllString(message, "$1=[redacted]")
	message = doneBotMediaDiagnosticBearer.ReplaceAllString(message, "Bearer [redacted]")
	message = doneBotMediaDiagnosticSecret.ReplaceAllString(message, "[redacted credential]")
	message = strings.Join(strings.Fields(message), " ")
	runes := []rune(message)
	if len(runes) > 600 {
		message = string(runes[:599]) + "…"
	}
	if message == "" {
		return doneBotMediaDiagnosticFallback
	}
	return message
}

// DoneBotMediaErrorDiagnostic extracts only error fields; provider response
// bodies, prompts, credentials and output payloads are never copied into it.
func DoneBotMediaErrorDiagnostic(status int, requestID string, body []byte) *DoneBotMediaDiagnostic {
	if !gjson.ValidBytes(body) {
		body = nil
	}
	root := gjson.ParseBytes(body)
	detail := root.Get("diagnostic")
	if !detail.IsObject() {
		detail = root.Get("error")
	}
	if !detail.Exists() {
		detail = root.Get("data.error")
	}
	if !detail.Exists() {
		if status < 400 && root.Get("code").Int() < 400 {
			return nil
		}
		detail = root
	}
	if parsed := detail.Get("http_status"); parsed.Exists() && parsed.Int() >= 100 && parsed.Int() <= 599 {
		status = int(parsed.Int())
	} else if parsed := root.Get("http_status"); parsed.Exists() && parsed.Int() >= 100 && parsed.Int() <= 599 {
		status = int(parsed.Int())
	}
	if status < 100 || status > 599 {
		status = 0
	}
	message := detail.Get("message").String()
	if detail.Type == gjson.String {
		message = detail.String()
	}
	if message == "" {
		message = root.Get("message").String()
	}
	return &DoneBotMediaDiagnostic{HTTPStatus: status,
		Code:      doneBotMediaDiagnosticID(firstNonEmpty(detail.Get("code").String(), root.Get("code").String())),
		Type:      doneBotMediaDiagnosticID(detail.Get("type").String()),
		RequestID: doneBotMediaDiagnosticID(firstNonEmpty(detail.Get("request_id").String(), root.Get("request_id").String(), requestID)),
		Message:   doneBotMediaDiagnosticMessage(message)}
}

func doneBotMediaDiagnosticJSON(diagnostic *DoneBotMediaDiagnostic) []byte {
	if diagnostic == nil {
		return nil
	}
	copy := *diagnostic
	copy.Code, copy.Type, copy.RequestID = doneBotMediaDiagnosticID(copy.Code), doneBotMediaDiagnosticID(copy.Type), doneBotMediaDiagnosticID(copy.RequestID)
	copy.Message = doneBotMediaDiagnosticMessage(copy.Message)
	if copy.Message == doneBotMediaDiagnosticFallback {
		copy.Message = ""
	}
	if copy.HTTPStatus < 100 || copy.HTTPStatus > 599 {
		copy.HTTPStatus = 0
	}
	encoded, _ := json.Marshal(copy)
	return encoded
}

func doneBotMediaStoredDiagnostic(diagnostic *DoneBotMediaDiagnostic) *DoneBotMediaDiagnostic {
	encoded := doneBotMediaDiagnosticJSON(diagnostic)
	if encoded == nil {
		return nil
	}
	var stored DoneBotMediaDiagnostic
	if json.Unmarshal(encoded, &stored) != nil {
		return nil
	}
	return &stored
}
