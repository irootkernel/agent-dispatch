# Dependency License Review

Reviewed 2026-08-23 for v0.1.1 (release-checklist §1, M-27). Every module
in `go.mod` (direct and indirect) is listed with its license and
compatibility verdict against the project MIT license.

| Module | License | Verdict |
|---|---|---|
| github.com/BurntSushi/toml (indirect) | MIT | compatible |
| github.com/dlclark/regexp2 (indirect) | MIT | compatible |
| github.com/dustin/go-humanize (indirect) | MIT | compatible |
| github.com/google/go-cmp (indirect) | BSD-3-Clause | compatible |
| github.com/google/pprof (indirect) | BSD-3-Clause | compatible |
| github.com/google/uuid (indirect) | BSD-3-Clause | compatible |
| github.com/hashicorp/golang-lru/v2 (indirect) | MPL-2.0 | compatible |
| github.com/mattn/go-isatty (indirect) | MIT | compatible |
| github.com/ncruces/go-strftime (indirect) | BSD-3-Clause | compatible |
| github.com/remyoudompheng/bigfft (indirect) | MIT | compatible |
| github.com/santhosh-tekuri/jsonschema/v6 (direct) | Apache-2.0 | compatible |
| github.com/yuin/goldmark (indirect) | MIT | compatible |
| golang.org/x/exp (indirect) | BSD-3-Clause | compatible |
| golang.org/x/exp/typeparams (indirect) | BSD-3-Clause | compatible |
| golang.org/x/mod (indirect) | BSD-3-Clause | compatible |
| golang.org/x/net (indirect) | BSD-3-Clause | compatible |
| golang.org/x/sync (indirect) | BSD-3-Clause | compatible |
| golang.org/x/sys (indirect) | BSD-3-Clause | compatible |
| golang.org/x/telemetry (indirect) | BSD-3-Clause | compatible |
| golang.org/x/text (indirect) | BSD-3-Clause | compatible |
| golang.org/x/tools (indirect) | BSD-3-Clause | compatible |
| golang.org/x/tools/go/expect (indirect) | BSD-3-Clause | compatible |
| gopkg.in/check.v1 (indirect) | BSD-2-Clause | compatible |
| gopkg.in/yaml.v3 (direct) | Apache-2.0 | compatible |
| honnef.co/go/tools (indirect) | MIT | compatible |
| modernc.org/cc/v4 (indirect) | BSD-3-Clause | compatible |
| modernc.org/ccgo/v4 (indirect) | BSD-3-Clause | compatible |
| modernc.org/fileutil (indirect) | BSD-3-Clause | compatible |
| modernc.org/gc/v2 (indirect) | BSD-3-Clause | compatible |
| modernc.org/gc/v3 (indirect) | BSD-3-Clause | compatible |
| modernc.org/goabi0 (indirect) | BSD-3-Clause | compatible |
| modernc.org/libc (indirect) | BSD-3-Clause | compatible |
| modernc.org/mathutil (indirect) | BSD-3-Clause | compatible |
| modernc.org/memory (indirect) | BSD-3-Clause | compatible |
| modernc.org/opt (indirect) | BSD-3-Clause | compatible |
| modernc.org/sortutil (indirect) | BSD-3-Clause | compatible |
| modernc.org/sqlite (direct) | BSD-3-Clause | compatible |
| modernc.org/strutil (indirect) | BSD-3-Clause | compatible |
| modernc.org/token (indirect) | BSD-3-Clause | compatible |

All 39 modules carry MIT, BSD-2-Clause,
BSD-3-Clause, Apache-2.0, or MPL-2.0 licenses; none impose restrictions
incompatible with MIT distribution of the binaries and source. MPL-2.0
(golang-lru, an indirect dependency of the staticcheck tool chain) is
file-level copyleft and does not propagate to this project's code; the
staticcheck tool chain and the modernc.org transitive generator modules
are build-time dependencies and do not ship in the released binaries.
