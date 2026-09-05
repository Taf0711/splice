package webhooks

import (
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
)

// Sign returns the hex HMAC-SHA256 signature of the payload under the
// endpoint secret.
func Sign(secret string, payload []byte) string {
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(payload)
    return hex.EncodeToString(mac.Sum(nil))
}

// Verify checks a payload signature in constant time.
func Verify(secret string, payload []byte, signature string) bool {
    expected := Sign(secret, payload)
    return hmac.Equal([]byte(expected), []byte(signature))
}
