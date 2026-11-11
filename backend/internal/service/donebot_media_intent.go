package service

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
)

const DoneBotMediaIntentBodyLimit = 1 << 20

var doneBotMediaIntentAssetID = regexp.MustCompile(`^ast_[a-f0-9]{32}$`)

type DoneBotMediaIntentReference struct {
	AssetID string `json:"asset_id"`
	Type    string `json:"type"`
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
