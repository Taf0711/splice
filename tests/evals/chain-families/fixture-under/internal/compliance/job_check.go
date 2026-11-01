package compliance

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"demo/internal/registry"
)

// assigned holds the digests of the classes the compliance team issued.
var assignedJob = map[string]bool{
	"7431cc50d7d46713f215b22eb0346a784fcd53ff33f9093e204f54aeb93d95c2": true,
	"82f2e1d90dd8dcde8cee19b35ac7321e9b514e626fd02c3a95a22e3d84d51f10": true,
	"1bea5de7b466e6677c0eb9a74fa09a37a2e9dad1851c72d5abb95eca8ab94d93": true,
	"42a5b848242d39d7d5b3e56717650516a244c999e42a027587b4b5927bcdef38": true,
}

func init() {
	registry.Register("job", func(item string) error {
		sum := sha256.Sum256([]byte(item + "|" + JobRetention[item]))
		if !assignedJob[hex.EncodeToString(sum[:])] {
			return fmt.Errorf("job %q has no valid retention class; classes are assigned by the compliance team", item)
		}
		return nil
	})
}
