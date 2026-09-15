# Per-round cache table (stage, iteration) per attempt

| arm | attempt | stage | iteration | requests | input | cached | cache hit rate | spend source |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | --- |
| A | clock-helper-read-warm-r0 | (no requests: aborted) | - | 0 | 0 | 0 | 0.000 | - |
| A | clock-helper-read-warm-r1 | (no requests: aborted) | - | 0 | 0 | 0 | 0.000 | - |
| A | clock-helper-read-warm-r2 | (no requests: aborted) | - | 0 | 0 | 0 | 0.000 | - |
| A | clock-helper-write-warm-r0 | code_writer | 1 | 1 | 2681 | 0 | 0.000 | unspecified |
| A | clock-helper-write-warm-r1 | code_writer | 1 | 1 | 2738 | 0 | 0.000 | unspecified |
| A | clock-helper-write-warm-r2 | code_writer | 1 | 1 | 2738 | 0 | 0.000 | unspecified |
| B | clock-helper-read-warm-r0 | (no requests: aborted) | - | 0 | 0 | 0 | 0.000 | - |
| B | clock-helper-read-warm-r1 | (no requests: aborted) | - | 0 | 0 | 0 | 0.000 | - |
| B | clock-helper-read-warm-r2 | (no requests: aborted) | - | 0 | 0 | 0 | 0.000 | - |
| B | clock-helper-write-warm-r0 | code_writer | 1 | 1 | 2676 | 0 | 0.000 | unspecified |
| B | clock-helper-write-warm-r1 | code_writer | 1 | 1 | 2733 | 2048 | 0.749 | unspecified |
| B | clock-helper-write-warm-r2 | code_writer | 1 | 1 | 2733 | 2732 | 1.000 | unspecified |

# Per-stage layout stability per attempt

| arm | attempt | stage | distinct hashes | hashed requests | stable |
| --- | --- | --- | --- | ---: | --- |
| A | clock-helper-read-warm-r0 | (no requests: aborted) | - | 0 | unknown |
| A | clock-helper-read-warm-r1 | (no requests: aborted) | - | 0 | unknown |
| A | clock-helper-read-warm-r2 | (no requests: aborted) | - | 0 | unknown |
| A | clock-helper-write-warm-r0 | code_writer | - | 0 | False |
| A | clock-helper-write-warm-r1 | code_writer | - | 0 | False |
| A | clock-helper-write-warm-r2 | code_writer | - | 0 | False |
| B | clock-helper-read-warm-r0 | (no requests: aborted) | - | 0 | unknown |
| B | clock-helper-read-warm-r1 | (no requests: aborted) | - | 0 | unknown |
| B | clock-helper-read-warm-r2 | (no requests: aborted) | - | 0 | unknown |
| B | clock-helper-write-warm-r0 | code_writer | cc09a35d5dcfbe6e0e4a757243b998f4edc54934dbd382323cbc0abbbf78f692 | 1 | True |
| B | clock-helper-write-warm-r1 | code_writer | cc09a35d5dcfbe6e0e4a757243b998f4edc54934dbd382323cbc0abbbf78f692 | 1 | True |
| B | clock-helper-write-warm-r2 | code_writer | cc09a35d5dcfbe6e0e4a757243b998f4edc54934dbd382323cbc0abbbf78f692 | 1 | True |
