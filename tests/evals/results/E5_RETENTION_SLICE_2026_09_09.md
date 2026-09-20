# Package E offline vertical slice - retention family (large-02-audit-retention-enforcer)

Base: 8210f1f (origin/feat/mvp-paired-proof). Branch: fix/warm-cost-package-e.
Command: go test -count=1 -run TestRetentionVerticalSlice -v ./internal/splice/

```
=== RUN   TestRetentionVerticalSlice
    warmcost_slice_test.go:121: slice trace:
        [
          {
            "condition": "improved-cold",
            "before_ops": [
              "read:internal/audit/retention.go",
              "symbol:internal/audit/log.go#Trail",
              "symbol:internal/audit/retention.go#Apply",
              "list:workspace"
            ],
            "after_ops": [
              "read:internal/audit/retention.go",
              "symbol:internal/audit/log.go#Trail",
              "symbol:internal/audit/retention.go#Apply",
              "list:workspace"
            ],
            "incremental_benefit": false,
            "diagnostic_only": false
          },
          {
            "condition": "diagnostic-manual-selection",
            "before_ops": [
              "read:internal/audit/retention.go",
              "symbol:internal/audit/log.go#Trail",
              "symbol:internal/audit/retention.go#Apply",
              "list:workspace"
            ],
            "after_ops": [
              "read:internal/audit/retention.go",
              "symbol:internal/audit/log.go#Trail",
              "list:workspace"
            ],
            "eliminated_ops": [
              "symbol:internal/audit/retention.go#Apply"
            ],
            "delivered_views": [
              "internal/audit/retention.go"
            ],
            "incremental_benefit": true,
            "mechanism_gate": "manual: eliminated symbol:internal/audit/retention.go#Apply",
            "policy_decisions": [
              "open-ended discovery need survives: no substitution may close it"
            ],
            "diagnostic_only": true
          },
          {
            "condition": "automatic-capture-selection",
            "before_ops": [
              "read:internal/audit/retention.go",
              "symbol:internal/audit/log.go#Trail",
              "symbol:internal/audit/retention.go#Apply",
              "list:workspace"
            ],
            "after_ops": [
              "read:internal/audit/retention.go",
              "symbol:internal/audit/log.go#Trail",
              "list:workspace"
            ],
            "eliminated_ops": [
              "symbol:internal/audit/retention.go#Apply"
            ],
            "delivered_views": [
              "internal/audit/retention.go"
            ],
            "incremental_benefit": true,
            "mechanism_gate": "automatic: eliminated symbol:internal/audit/retention.go#Apply",
            "policy_decisions": [
              "open-ended discovery need survives: no substitution may close it"
            ],
            "diagnostic_only": false
          }
        ]
--- PASS: TestRetentionVerticalSlice (0.01s)
PASS
ok  	github.com/Taf0711/splice/internal/splice	0.307s
```
