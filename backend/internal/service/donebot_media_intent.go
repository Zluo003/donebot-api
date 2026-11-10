package service

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

const DoneBotMediaIntentBodyLimit = 1 << 20

var doneBotMediaIntentAssetID = regexp.MustCompile(`^ast_[a-f0-9]{32}$`)

type DoneBotMediaIntent struct {
	Kind      string `json:"kind"`
	Operation string `json:"operation"`
}

type DoneBotMediaIntentReference struct {
	AssetID string `json:"asset_id"`
	Type    string `json:"type"`
}

type DoneBotMediaIntentParameters struct {
	Prompt     string                        `json:"prompt"`
	Options    map[string]json.RawMessage    `json:"options"`
	References []DoneBotMediaIntentReference `json:"references"`
}

type DoneBotMediaIntentRequest struct {
	Intent     DoneBotMediaIntent           `json:"intent"`
	Parameters DoneBotMediaIntentParameters `json:"parameters"`
}

type DoneBotMediaRetryEligibility struct {
	Eligible bool   `json:"eligible"`
	State    string `json:"state"`
	Reason   string `json:"reason"`
}

type DoneBotMediaIntentAttempt struct {
	Attempt        int                     `json:"attempt"`
	ReceiptID      string                  `json:"receipt_id"`
	Status         string                  `json:"status"`
	PaymentState   string                  `json:"payment_state"`
	Currency       string                  `json:"currency"`
	PrepaidAmount  string                  `json:"prepaid_amount"`
	RefundedAmount string                  `json:"refunded_amount"`
	ActualAmount   *string                 `json:"actual_amount,omitempty"`
	RefundComplete bool                    `json:"refund_complete"`
	Diagnostic     *DoneBotMediaDiagnostic `json:"diagnostic,omitempty"`
}

type DoneBotMediaIntentArtifact struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	MIME        string `json:"mime"`
	Name        string `json:"name"`
	DownloadURL string `json:"download_url"`
}

type DoneBotMediaIntentJob struct {
	JobID            string                       `json:"job_id"`
	TaskID           string                       `json:"task_id"`
	ReceiptID        string                       `json:"receipt_id"`
	Kind             string                       `json:"kind"`
	Operation        string                       `json:"operation"`
	Status           string                       `json:"status"`
	CurrentAttempt   int                          `json:"current_attempt"`
	RetryEligibility DoneBotMediaRetryEligibility `json:"retry_eligibility"`
	Attempts         []DoneBotMediaIntentAttempt  `json:"attempts"`
	Artifacts        []DoneBotMediaIntentArtifact `json:"artifacts"`
	Diagnostic       *DoneBotMediaDiagnostic      `json:"diagnostic,omitempty"`
}

func DecodeDoneBotMediaIntentRequest(body []byte) (DoneBotMediaIntentRequest, error) {
	var request DoneBotMediaIntentRequest
	if len(body) == 0 || len(body) > DoneBotMediaIntentBodyLimit || !utf8.Valid(body) {
		return request, ErrDoneBotMediaInvalid
	}
	if err := rejectDoneBotDuplicateJSONKeys(body); err != nil {
		return request, ErrDoneBotMediaInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, ErrDoneBotMediaInvalid
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return request, ErrDoneBotMediaInvalid
	}
	if !doneBotMediaIntentOperation(request.Intent.Kind, request.Intent.Operation) || len(request.Parameters.Prompt) > 20000 || !utf8.ValidString(request.Parameters.Prompt) || len(request.Parameters.Options) > 64 || len(request.Parameters.References) > 16 {
		return request, ErrDoneBotMediaInvalid
	}
	for key, raw := range request.Parameters.Options {
		if !doneBotMediaIntentOptionKey(key) || len(raw) == 0 || len(raw) > 16<<10 || hasDoneBotMediaIntentInjection(raw) {
			return request, ErrDoneBotMediaInvalid
		}
	}
	for _, reference := range request.Parameters.References {
		if !doneBotMediaIntentAssetID.MatchString(reference.AssetID) || !doneBotMediaIntentReferenceType(request.Intent.Kind, reference.Type) {
			return request, ErrDoneBotMediaInvalid
		}
	}
	return request, nil
}

func doneBotMediaIntentOperation(kind, operation string) bool {
	switch kind {
	case "image":
		return operation == "generate" || operation == "edit"
	case "video":
		return operation == "generate" || operation == "edit" || operation == "extend"
	case "audio":
		return operation == "generate"
	default:
		return false
	}
}

func doneBotMediaIntentReferenceType(kind, referenceType string) bool {
	switch kind {
	case "image":
		return referenceType == "image"
	case "video":
		return referenceType == "image" || referenceType == "video"
	case "audio":
		return referenceType == "audio"
	default:
		return false
	}
}

func doneBotMediaIntentOptionKey(key string) bool {
	if len(key) == 0 || len(key) > 64 || key[0] < 'a' || key[0] > 'z' {
		return false
	}
	for _, r := range key[1:] {
		if r < 'a' || r > 'z' {
			if r < '0' || r > '9' {
				if r != '_' {
					return false
				}
			}
		}
	}
	return !doneBotMediaIntentForbiddenField(key)
}

func doneBotMediaIntentForbiddenField(key string) bool {
	key = strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", ""), "_", ""))
	if strings.Contains(key, "url") || strings.Contains(key, "uri") || strings.Contains(key, "endpoint") {
		return true
	}
	switch key {
	case "model", "modelid", "modelname", "provider", "providerid", "adapter", "protocol", "credential", "apikey", "accesstoken", "authorization", "taskid":
		return true
	default:
		return false
	}
}

func hasDoneBotMediaIntentInjection(raw []byte) bool {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return true
	}
	return doneBotMediaIntentValueHasForbiddenField(value)
}

func doneBotMediaIntentValueHasForbiddenField(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if doneBotMediaIntentForbiddenField(key) || doneBotMediaIntentValueHasForbiddenField(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if doneBotMediaIntentValueHasForbiddenField(child) {
				return true
			}
		}
	}
	return false
}

func rejectDoneBotDuplicateJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var readValue func() error
	readValue = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := make(map[string]struct{})
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return ErrDoneBotMediaInvalid
				}
				if _, exists := seen[key]; exists {
					return ErrDoneBotMediaInvalid
				}
				seen[key] = struct{}{}
				if err := readValue(); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := readValue(); err != nil {
					return err
				}
			}
		default:
			return ErrDoneBotMediaInvalid
		}
		_, err = decoder.Token()
		return err
	}
	if err := readValue(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrDoneBotMediaInvalid
	}
	return nil
}
