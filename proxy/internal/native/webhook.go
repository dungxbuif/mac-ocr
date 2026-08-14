package native

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"
)

func SignWebhook(secret string, nodeID, timestamp, eventID string, body []byte) string {
	hasher := hmac.New(sha256.New, []byte(secret))
	payload := fmt.Sprintf("%s.%s.%s.", nodeID, timestamp, eventID)
	hasher.Write([]byte(payload))
	hasher.Write(body)
	return hex.EncodeToString(hasher.Sum(nil))
}

func VerifyWebhook(secret, nodeID, timestampStr, eventID string, body []byte, signature string) error {
	ts, err := strconv.ParseInt(timestampStr, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid timestamp: %w", err)
	}

	eventTime := time.Unix(ts, 0)
	if time.Since(eventTime).Abs() > 5*time.Minute {
		return fmt.Errorf("webhook timestamp expired or too far in future")
	}

	expected := SignWebhook(secret, nodeID, timestampStr, eventID, body)
	if subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) != 1 {
		return fmt.Errorf("signature mismatch")
	}

	return nil
}
