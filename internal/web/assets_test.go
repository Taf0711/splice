package web

import (
	"io/fs"
	"strings"
	"testing"
)

// assetBudget is the total size limit for embedded assets (plan section 7):
// under 100 KB, read as 100 KiB.
const assetBudget = 100 * 1024

func TestEmbeddedAssetsWithinBudget(t *testing.T) {
	var total int64
	err := fs.WalkDir(assetFS, "assets", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		t.Fatalf("walk assets: %v", err)
	}
	if total == 0 || total >= assetBudget {
		t.Fatalf("embedded assets = %d bytes, want 0 < size < %d", total, assetBudget)
	}
}

func TestIndexPageHasNoScriptOrExternalReference(t *testing.T) {
	page, err := assetFS.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	for _, banned := range []string{"<script", "http://", "https://"} {
		if strings.Contains(strings.ToLower(string(page)), banned) {
			t.Errorf("index page contains %q; the surface must have no JS or external requests", banned)
		}
	}
}
