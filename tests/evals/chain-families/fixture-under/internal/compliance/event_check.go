package compliance

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"demo/internal/registry"
)

// assigned holds the digests of the classes the compliance team issued.
var assignedEvent = map[string]bool{
	"de92df2936b6e8ae632c9c0bb2261946839b08c99ad9242db70823365389265c": true,
	"962f62c37e66b9c1881a966efd576c89e74281fe6cec16e7959f7b97be4ea300": true,
	"0c38a2a9bf4f0f87b589853f88b3096cb351811e90cab4e7221b5d0979771c31": true,
	"6aa463264da38c911b2ed0238c3ed6ecb5c2998339a1f977a3c60c15b2041608": true,
}

func init() {
	registry.Register("event", func(item string) error {
		sum := sha256.Sum256([]byte(item + "|" + EventRetention[item]))
		if !assignedEvent[hex.EncodeToString(sum[:])] {
			return fmt.Errorf("event %q has no valid retention class; classes are assigned by the compliance team", item)
		}
		return nil
	})
}
