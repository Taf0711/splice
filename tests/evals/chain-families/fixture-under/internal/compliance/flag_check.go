package compliance

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"demo/internal/registry"
)

// assigned holds the digests of the classes the compliance team issued.
var assignedFlag = map[string]bool{
	"d2b51537bf0c9c50fa2eb6eb9f08a12cbcef55d4edf358a71c06d8391ab89032": true,
	"a6d096ac5a7a4317863e6d7961f44d04d1539e814ec95eb71dbdfc3db59c22d7": true,
	"24de051ed007a21b8f78ccde3e9cb5dc4d06f9354b2231b86ec89ea2b306661b": true,
	"86c9cc679e6f10d68859a72a6775197bb83b094add4a093f8832210d348ab755": true,
}

func init() {
	registry.Register("flag", func(item string) error {
		sum := sha256.Sum256([]byte(item + "|" + FlagRetention[item]))
		if !assignedFlag[hex.EncodeToString(sum[:])] {
			return fmt.Errorf("flag %q has no valid retention class; classes are assigned by the compliance team", item)
		}
		return nil
	})
}
