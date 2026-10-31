package compliance

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"demo/internal/registry"
)

// assigned holds the digests of the classes the compliance team issued.
var assignedPermission = map[string]bool{
	"3e82ad7ca5dc08103802841e7a5dc70fdf8c14632f617008a40a1c79cd651f68": true,
	"be610729e5214acba1455f279f10a768382102ac8207b317f3fa312e9639ad44": true,
	"f56daf161e0a3af6436098d464cbadf2f2b69a10ff26d8fa2298bdf707127206": true,
	"0e70b508f5ab49ab77a798db958a2f7a149af9801bad7e85dc3f31996e1fac84": true,
}

func init() {
	registry.Register("permission", func(item string) error {
		sum := sha256.Sum256([]byte(item + "|" + PermissionRetention[item]))
		if !assignedPermission[hex.EncodeToString(sum[:])] {
			return fmt.Errorf("permission %q has no valid retention class; classes are assigned by the compliance team", item)
		}
		return nil
	})
}
