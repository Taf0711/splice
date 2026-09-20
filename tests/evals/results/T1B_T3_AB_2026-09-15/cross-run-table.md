# T3 cross-run table (warm only)

| metric | Arm A (e3e97b1) | Arm B (d9c52b3) |
| --- | ---: | ---: |
| attempts | 6 | 6 |
| verified_completions | 3 | 3 |
| failed_attempts | 3 | 3 |
| requests | 3 | 3 |
| requests_per_verified_completion | 1.00 | 1.00 |
| input_tokens | 8157 | 8142 |
| cached_input_tokens | 0 | 4780 |
| cache_share | 0.0000 | 0.5871 |
| coverage_complete | False | False |
| total billed USD (incl. aborted attempts) | 0.002496 | 0.001944 |
| prompt_layout_flips | UNKNOWN (binary predates the field) | 0 (write runs); read attempts aborted before any request |

## Raw attempt rows

| arm | attempt | run_status | verifier | requests | input | cached | billed USD | cache_hit(s) | prompt_layout_flips | telemetry_reported |
| --- | --- | --- | --- | ---: | ---: | ---: | ---: | --- | --- | --- |
| A | clock-helper-read-warm-r0 | infrastructure_failed | False | 0 | 0 | 0 | 0.000000 | - | UNKNOWN | False |
| A | clock-helper-read-warm-r1 | infrastructure_failed | False | 0 | 0 | 0 | 0.000000 | - | UNKNOWN | False |
| A | clock-helper-read-warm-r2 | infrastructure_failed | False | 0 | 0 | 0 | 0.000000 | - | UNKNOWN | False |
| A | clock-helper-write-warm-r0 | completed | True | 1 | 2681 | 0 | 0.000798 | null | UNKNOWN | False |
| A | clock-helper-write-warm-r1 | completed | True | 1 | 2738 | 0 | 0.000855 | null | UNKNOWN | False |
| A | clock-helper-write-warm-r2 | completed | True | 1 | 2738 | 0 | 0.000843 | null | UNKNOWN | False |
| B | clock-helper-read-warm-r0 | infrastructure_failed | False | 0 | 0 | 0 | 0.000000 | - | UNKNOWN | False |
| B | clock-helper-read-warm-r1 | infrastructure_failed | False | 0 | 0 | 0 | 0.000000 | - | UNKNOWN | False |
| B | clock-helper-read-warm-r2 | infrastructure_failed | False | 0 | 0 | 0 | 0.000000 | - | UNKNOWN | False |
| B | clock-helper-write-warm-r0 | completed | True | 1 | 2676 | 0 | 0.000832 | false | 0 | True |
| B | clock-helper-write-warm-r1 | completed | True | 1 | 2733 | 2048 | 0.000603 | true | 0 | True |
| B | clock-helper-write-warm-r2 | completed | True | 1 | 2733 | 2732 | 0.000509 | true | 0 | True |
