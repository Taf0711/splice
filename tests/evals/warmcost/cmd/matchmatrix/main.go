// Command matchmatrix re-runs the campaign pre-B match-matrix gate OFFLINE:
// it derives the target task's non-open-discovery needs against a frozen Task A
// tree and tests every frozen capture record with RecordSpeaksOfSubject - the
// same predicate the runner's gate and seedManualArm use. It exits non-zero
// when no record matches, so a pair can be vetoed before any provider request.
//
// Usage:
//
//	matchmatrix --manifest tests/evals/cognition-families/fam-05-pair.json \
//	            --tree <frozen-A-tree> --bundle <snapshot-bundle.json>
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice"
)

type manifest struct {
	Families []struct {
		ID         string `json:"id"`
		TargetTask string `json:"target_task"`
	} `json:"families"`
}

type bundleFile struct {
	Schema        string                     `json:"schema"`
	ProducerRunID string                     `json:"producer_run_id"`
	Commit        string                     `json:"commit"`
	Tree          string                     `json:"tree"`
	CaptureDigest string                     `json:"capture_digest"`
	Nodes         []memd.ExportedCaptureNode `json:"nodes"`
}

func main() {
	manifestPath := flag.String("manifest", "", "cognition campaign pair manifest")
	family := flag.String("family", "", "family id (default: the single family)")
	treeDir := flag.String("tree", "", "frozen Task A tree directory")
	bundlePath := flag.String("bundle", "", "recorded snapshot-bundle.json")
	flag.Parse()
	if *manifestPath == "" || *treeDir == "" || *bundlePath == "" {
		fmt.Fprintln(os.Stderr, "matchmatrix: --manifest, --tree and --bundle are required")
		os.Exit(2)
	}
	data, err := os.ReadFile(*manifestPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "matchmatrix:", err)
		os.Exit(2)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		fmt.Fprintln(os.Stderr, "matchmatrix:", err)
		os.Exit(2)
	}
	intent := ""
	for _, f := range m.Families {
		if *family == "" || f.ID == *family {
			intent = f.TargetTask
			break
		}
	}
	if intent == "" {
		fmt.Fprintf(os.Stderr, "matchmatrix: family %q not found in %s\n", *family, *manifestPath)
		os.Exit(2)
	}
	raw, err := os.ReadFile(*bundlePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "matchmatrix:", err)
		os.Exit(2)
	}
	var b bundleFile
	if err := json.Unmarshal(raw, &b); err != nil {
		fmt.Fprintln(os.Stderr, "matchmatrix:", err)
		os.Exit(2)
	}

	needs := splice.DeriveContextNeeds(intent, *treeDir, nil, nil)
	nonOpen := make([]splice.ContextNeed, 0, len(needs))
	fmt.Println("derived needs:")
	for _, n := range needs {
		if n.Kind == splice.NeedOpenDiscovery {
			continue
		}
		nonOpen = append(nonOpen, n)
		fmt.Printf("  %s kind=%s subject=%q origin=%s required=%t\n", n.ID, n.Kind, n.Subject, n.Origin, n.Required)
	}
	matched := 0
	fmt.Printf("bundle %s commit=%s tree=%s digest=%s nodes=%d\n", *bundlePath, b.Commit, b.Tree, b.CaptureDigest, len(b.Nodes))
	for _, node := range b.Nodes {
		rec := splice.ParseReuseRecordJSON(deref(node.Node.MetadataJSON))
		hits := []string{}
		if rec != nil {
			for _, n := range nonOpen {
				if splice.RecordSpeaksOfSubject(rec, n.Subject) {
					hits = append(hits, n.ID)
				}
			}
		}
		if len(hits) > 0 {
			matched++
		}
		fmt.Printf("  record %s matched=%v reason=%s\n", node.ClaimHash, hits, reasonFor(rec, len(nonOpen)))
	}
	fmt.Printf("matched_records=%d non_open_needs=%d\n", matched, len(nonOpen))
	if matched == 0 {
		fmt.Fprintln(os.Stderr, "matchmatrix: EMPTY matrix; the pair is not justified")
		os.Exit(1)
	}
}

func reasonFor(rec *splice.ReuseRecord, nonOpen int) string {
	if rec == nil {
		return "not a typed reuse record; hint only"
	}
	if nonOpen == 0 {
		return "no non-open-discovery need derived"
	}
	return "no derived need subject matched this record"
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
