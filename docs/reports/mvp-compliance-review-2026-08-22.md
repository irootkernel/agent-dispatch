# Agent Dispatch v0.1.0 MVP — docs 요구사항 준수 리뷰 보고서

> **리뷰 일자:** 2026-08-22  
> **대상 커밋:** `924f60a` (main, 작업 트리 clean)  
> **리뷰 기준:** `docs/docs/00-sot/required-spec.md` 126개 요구사항, `docs/docs/00-sot/acceptance-criteria.md` 40개 AC, `docs/docs/02-contracts/*`, ADR, 아키텍처 문서 (권위 순서: `docs/README.md:12-24`)  
> **방법:** 조사 트랙 9개(Opus 6 / Sonnet 3) 병렬 → Blocker/High/Medium 발견에 대한 Opus 적대적 반박 검증 5건 → 오케스트레이터(Fable)의 코드 직접 확인 및 실행 증적(macOS 전체 스위트 + 실제 Hermes/Watchman, Docker Linux 레그 2회, 재현 빌드 2회)  
> **보고서 상태:** 이 파일은 `docs/` 패키지에 새로 추가되었으며 `docs/MANIFEST.sha256`에는 포함되지 않았다. 보관하려면 `VALIDATION.md` §Reproduction 절차로 manifest를 재생성해야 한다. 저장소 코드는 변경하지 않았다.

---

## 1. 결론

**판정: 부분 충족 (요구사항을 모두 구현했다고 볼 수 없음).**

- 단일 generation의 정상 경로 — Watchman 트리거 → 결정적 정책 → SQLite intent → 실제 Hermes Kanban 수락 → 외부 task id 저장 — 와 안전 경계(경로 봉쇄, 셸 미사용, 시크릿 미저장, 자동 failover 금지, 멱등 dedup, protected/bulk/overflow 격리·reconcile, 자동 쓰기 게이트)는 **견고하게 구현되어 있고, 이 머신에서 실제 Hermes 0.19.1/Watchman 2026.07.27.00으로 오늘 재실행해 G1·G3·G4·G5 게이트 테스트가 모두 통과**했다.
- 그러나 **MUST 요구사항 14건이 GAP**이고(SHOULD 1건 추가), AC 3건 GAP + 1건 증적 없음이며, 그중 **Blocker 3건은 v0.1의 핵심 사용자 스토리(제출 중 크래시 복구, 한 route 한 task, 후속 generation 처리)가 제품 경로에서 동작하지 않음**을 의미한다. 세 건 모두 독립 반박 검증에서 실제 재현되었다.
- 게이트 증적의 일부는 실행되지 않는 테스트에 의존한다: `TestG2AC207`은 항상 skip, 계약 lockstep 테스트 2개는 잘못된 상대경로로 항상 skip, `TestG2AC203`은 fake sink를 호출하지 않아 핵심 단언이 공허하며, VALIDATION.md가 인용한 `TestG2MigrationInterruptedUpgrade`는 존재하지 않는다.
- SCP-008(Linux 검증)은 릴리스 시점에 수행되지 않았고 roadmap/CHANGELOG가 "Linux CI leg 검증됨"이라고 잘못 주장한다. 이번 리뷰에서 Docker로 직접 실행한 결과, **출시 상태의 스위트는 Linux에서 통과하지 못한다**(darwin 전용 keychain 테스트 2건에 플랫폼 가드 없음; 그 외 506건은 통과).

| 집계 | PASS | PASS-NOTE | GAP | NOT-EVIDENCED |
|---|---:|---:|---:|---:|
| 요구사항 126 | 71 | 40 | 15 (MUST 14 + SHOULD 1) | 0 |
| AC 40 | 24 | 12 | 3 | 1 |

판정 값: `PASS` 구현+증적 확인 / `PASS-NOTE` 문서화된 축소 보증·honest boundary 또는 경미한 결함 동반 / `GAP` 미구현·요구사항 위반 / `NOT-EVIDENCED` 구현 주장은 있으나 실행 증적 없음.

---

## 2. 리뷰 방법

| 단계 | 내용 |
|---|---|
| Phase 0 실행 증적 (오케스트레이터) | macOS `go test -count=1 -v ./...` + `-race`, `make fmt-check vet staticcheck check-imports manifest-check schema-validation traceability schedule-check`, `make release VERSION=v0.1.0` 2회 digest 비교, Docker(`golang:1.26`, linux/arm64) `go test -v` + `make verify` root/비root 2회, 실패 테스트 격리 재실행 |
| Phase 1 조사 트랙 | T1 Source&Path(Opus) · T2 Identity/Records/Policy(Opus) · T3 Durability/Concurrency(Opus) · T4 Hermes Kanban/Webhook(Opus) · T5 Feedback/Quarantine/Reconcile(Opus) · T6 CLI 계약(Sonnet) · T7 Security(Opus, security-reviewer) · T8 Ops/Observability/Packaging(Sonnet) · T9 Test/Release 품질·문서 정합성(Sonnet). 각 트랙은 요구사항 ID별 판정과 `file:line` 증거를 제출, `go test` 실행 금지(실제 Hermes 게이트와의 간섭 방지) |
| Phase 2 적대적 검증 | Blocker/High 발견 13건을 Opus 반박 에이전트 5개가 "기본값 refuted"로 독립 재검증(코드 경로 + 스텁 Hermes 대상 실제 재현). 결과: CONFIRMED 9, PARTIALLY-CONFIRMED 4, REFUTED 0. 심각도 상향 3건(F-T3-2, F-T5-1 → Blocker; F-T2-2 → 병합), 하향 4건(F-T4-1, F-T4-3, F-T7-1, F-T1-2) |
| Phase 3 종합 | 오케스트레이터가 Blocker/High 전건의 코드 경로를 직접 읽어 확인(§5 증거의 `file:line`은 직접 확인분), 트랙 간 충돌 판정을 결정, 매트릭스 작성 |

---

## 3. 실행 증적

### 3.1 macOS (이 호스트: Go 1.26.6, Watchman 2026.07.27.00, Hermes 0.19.1 — frozen baseline과 정확히 일치)

| 항목 | 결과 |
|---|---|
| `go test -count=1 -v ./...` | **514 PASS / 5 FAIL / 3 SKIP** (top-level). FAIL 5건 모두 `internal/adapters/hermeskanban` sink 테스트, 메시지 `hermes version timed out after 5s`(스텁 hermes 프로브 타임아웃) |
| `go test -race -count=1 ./...` | cli 패키지 1 FAIL: `TestSuppressedDirtyWithPendingReconcile` — `hermes version timed out after 30s` |
| 격리 재실행 | **6건 모두 PASS** (0.4–5.4s). 실행 당시 load avg 7–8(타 세션의 cargo test, 컨테이너, 에이전트 동시 실행) → 고정 5s/30s 타임아웃의 부하 플레이크로 판정. 제품 결함 아님, 테스트 견고성 결함(M-28) |
| 게이트 테스트 | G1 `TestG1AC101..110`+`TestG1NoSettleSleep` 11/11 PASS · G2 `TestG2AC201..206`+`TestG2MultiProcessOneActiveRouteDispatch` PASS, **`TestG2AC207` SKIP** · G3 `TestG3AC301And305RealTriggerEndToEnd`, `AC302`, `AC303`, `AC304`, `AC306` 5/5 PASS (실제 Hermes 보드 생성/삭제, 실제 Watchman 트리거) · `TestRealHermesProbeIfAvailable`, `TestRealHermesDisposableBoardSubmitDedupLookup`, `TestHermesCompanionSkillValidated`, `TestWatchmanCLILifecycle`, `TestLifecycle*` PASS · G4 `TestG4FeedbackLoopGate`, `StructuralScenarios`, `DistinctOperatorOperations`, `ProductionGateReviewed` 4/4 PASS · G5 `TestG5AC501/502/503/504/506`, `TestG5UpgradeAndBackupRehearsal` PASS |
| 결정적 SKIP 3건 | `TestG2AC207` (`g2_test.go:300` "ledger not created before the first migration"), `TestIntentStatesMatchContractSchema`, `TestReceiptAxesMatchContractSchema` (`contract_lockstep_test.go:16,65` 경로 `../../docs/schemas` → 실제는 `../../../`) — 양 플랫폼 공통, 환경 무관 |
| `make` 기타 타깃 | fmt-check/vet/staticcheck/check-imports(35 pkg)/manifest-check/schema-validation(12 스키마, 모든 예제 valid)/traceability(드리프트 없음)/schedule-check(plutil OK, systemd 부재) **전부 통과** |
| `make release` ×2 | `dist/SHA256SUMS` 바이트 동일 — darwin-arm64 `90561e26…`, linux-amd64 `9854316c…` → **재현 빌드 주장 확인** |
| `version --output json` | `config_version:"1"`, `schema_range:"1-4"`, adapter_versions 5종 포함 (CLI-001 엔벨로프 확인) |
| 환경 원복 | 실행 전후 `hermes kanban boards list`(default만)·`watchman watch-list` 동일, `git status` clean |

### 3.2 Linux (Docker `golang:1.26` = go1.26.7 linux/arm64, Debian trixie, systemd 257 설치)

| 실행 | 결과 |
|---|---|
| root 컨테이너 | `go test` EXIT=1, `make verify` EXIT=2. FAIL: `secretresolver.TestRunSecurityControls`, `TestRunSecuritySeparatesStderr`(keychain, 플랫폼 가드 없음) + `cli.TestMaintenanceBackupWritesVerifiedSnapshot`, `TestInitStateDirIOFailureUsesConfigInvalid`(root는 권한 거부가 발생하지 않는 환경 아티팩트) |
| 비root 컨테이너(uid 1000) | **506 PASS / 2 FAIL / 14 SKIP**, `make verify` EXIT=2. FAIL은 keychain 테스트 2건뿐. SKIP: hermes/watchman 부재 11건(기록된 환경 갭) + 위 결정적 3건 |
| 의미 | 제품 코드는 Linux에서 빌드·동작하나(`GOOS=linux` amd64/arm64 build·vet 통과), **출시 상태의 `make verify`는 Linux에서 원천적으로 실패** → SCP-008 GAP, AC-505 GAP. 주의: 릴리스 산출물은 linux/**amd64**이고 이번 실행은 arm64, Go 1.26.7(이미지)이다 |

### 3.3 리뷰 중 발생한 부작용(공개)

- T6·T8 에이전트가 임시 config로 `init --state-dir <tmp>`를 실행한 뒤 후속 명령에 `--state-dir`를 빠뜨려, 플랫폼 기본 경로 `~/Library/Application Support/Agent Dispatch`에 빈 DB가 자동 마이그레이션으로 생성되었고 각 에이전트가 이를 즉시 `rm -rf`로 제거했다. 원인은 M-29(`init`이 `state_dir: ""`를 기록). 오케스트레이터 확인: 해당 경로와 `~/.config/agent-dispatch`는 현재 존재하지 않으며, 사용자의 기존 상태는 구명칭 디렉터리 `~/Library/Application Support/JJUKKUMI`(2026-08-21)에 그대로 있다. 신명칭 경로는 리네임(오늘 17:17) 이전에 존재할 수 없었고 게이트 테스트는 임시 HOME을 쓰므로, 삭제된 디렉터리는 에이전트가 생성한 것으로 판단한다.
- 테스트 스위트의 G3 게이트가 일회용 Hermes 보드를 생성·삭제했다(프로젝트 자체 `make verify`와 동일 동작, 사용자 승인). 기본 보드는 건드리지 않았다.

---

## 4. Goal 달성 판정

**규칙:** 모든 MUST가 PASS/PASS-NOTE이고 모든 AC가 PASS이며 SHOULD 예외가 기록되어 있으면 "충족". 하나라도 MUST GAP 또는 AC GAP/NOT-EVIDENCED가 있으면 "부분 충족".

**결과: 부분 충족.** MUST GAP 14건(SCP-008, PTH-007, DAT-009, POL-008, DUR-010, DUR-011, CON-001, CON-003, HER-006, FBK-005, CLI-004, SEC-010, OPS-002, TST-004), SHOULD 미기록 예외 1건(SEC-008), AC GAP 3건(AC-102, AC-203, AC-505), AC NOT-EVIDENCED 1건(AC-207). `required-spec.md:7` "MUST requirements block v0.1 release" 규칙상 v0.1.0 릴리스 선언(`roadmap.md:1455` "all MUST requirements pass")은 현재 상태에서 성립하지 않는다.

프로젝트 헌장(`project-charter.md:76-84`)의 목표 8개 기준으로 보면: 목표 1(한 burst → 한 요청)·3(중복 전달 dedup)·5(혼합 변경 미삭제)·6(protected/bulk/overflow 가시적 실패)·8(sink port 독립) **달성**; 목표 2(크래시·재시작 생존) **미달성**(B-1); 목표 4(에이전트 변경 무한 재귀 방지)와 7(운영자 검사 가능) **부분**(B-3, H-5).

---

## 5. 발견 목록 (심각도순)

각 항목: 요구사항 ID · 증거(`file:line`) · 재현 · 권고. 검증 표기: **CONFIRMED**(반박 검증 통과+오케스트레이터 직접 확인), **PARTIAL**(기전 확인, 범위/심각도 조정), **EXEC**(실행 증적으로 확인).

### Blocker (3)

**B-1. 제출 중 프로세스가 죽으면 intent와 route slot이 영구 고착 — 만료 lease 복구 경로가 제품에 없음** · DUR-010, DUR-005, AC-203, OPS-005 · CONFIRMED
- `internal/app/dispatch/runtime.go:239` `Recover()`는 테스트 외 호출자가 없다(`RecoverExpiredSubmitting`도 동일). `Drain`(`runtime.go:214-218`)은 `ready/retry_wait`만 처리하고 나머지는 `skipped`에도 세지 않는다. `SubmitOnce`(`runtime.go:70-75`)는 `submitting`이면 만료 여부와 무관하게 `ErrLeaseHeld`. `AcquireAttempt` SQL(`internal/adapters/sqlite/dispatch.go:150-168`)도 `state IN ('ready','retry_wait')`만 허용. `OperatorService.Retry`(`operator.go:37-56`)는 `dead_lettered/retry_wait`만 허용.
- doctor는 `stale_attempt_lease`를 감지하지만 remediation 문구(`internal/app/doctor/doctor.go:192,199,254`)가 "run dispatches drain"으로, 실제로는 아무것도 복구하지 못한다.
- 재현(반박 검증): `create`가 30초 지연하는 스텁 hermes에 `dispatch` 실행 중 `kill -9` → 60초 후 lease 만료 → `dispatches drain` → `{"processed":0,"skipped":0}`, 행은 `submitting` 그대로, route `ACTIVE_CLEAN` 고정. `retry`/`reconcile`/`route disable`/`maintenance`로도 해제 불가. 이후 모든 변경은 dirty generation에만 누적되어 영원히 처리되지 않는다. 행을 수동으로 `unknown`으로 바꾸면 drain이 즉시 DUR-006 lookup을 수행(복구 기계 자체는 정상, `submitting→unknown` 단계만 미연결).
- VALIDATION.md:93의 AC-203 증적 `TestG2AC203`은 `rt.Recover()`를 직접 호출(`g2_test.go:157`)하며 fake sink를 한 번도 호출하지 않아 "두 번째 task를 만들지 않는다" 단언이 공허하다.
- 권고: `runDispatch`·`runDispatchesDrain` 시작 시(unknown reconcile 이전) `Runtime.Recover` 호출 또는 `dispatches recover` 명령 추가; doctor remediation 문구 수정; 실제 프로세스 kill 기반 G2 테스트 추가.

**B-2. `dispatches rerun`에 상태 가드가 없고 `Drain`이 route slot을 확인하지 않아 한 route에 두 개의 권위 있는 Hermes task가 생성됨** · CON-001, DUR-006, 도메인 불변식 4/5 · CONFIRMED(반박 검증에서 Blocker로 상향)
- `internal/app/dispatch/operator.go:65-113` `Rerun`은 원본 intent의 state/lease를 검사하지 않으며 원본을 supersede하지 않는다. `internal/adapters/sqlite/inspection.go:479-498` `saveIntentTakeOverOriginal`은 slot만 가져간다. `runtime.go:210-233` `Drain`은 route의 모든 `ready/retry_wait` intent를 순회하며 `route_runtime_state.active_dispatch_id`를 참조하지 않는다.
- 재현(크래시 없음, 건강한 시스템, 운영자 명령 2개): `dispatch --no-submit`(A ready) → `dispatches rerun A --yes --reason x`(B ready, 새 키, slot 점유, **A는 여전히 ready**) → `dispatches drain` → `processed: 2`, A·B 모두 `accepted`, 보드에 `t_00000001`(gen 2)·`t_00000002`(gen 1) 두 task 공존. `submitting`/`unknown` 원본에 대한 rerun도 lookup 없이 성공.
- `failure-recovery.md` 원칙 6 "parallel unbounded tasks are not [safer]" 위반.
- 권고: rerun은 원본을 `superseded`로 전이(선언된 엣지 존재)하고 `submitting`(미만료 lease)/`unknown`은 거부 또는 복구·reconcile 선행 강제; `Drain`/`SubmitOnce`에 route active slot 일치 검사 추가.

**B-3. follow-up intent가 생성은 되지만 절대 활성화·자동 제출되지 않아 두 번째 generation부터 route가 `FOLLOWUP_READY`에 고착되고 `work begin`이 거부됨** · CON-003, FBK-005, FBK-008, 헌장 사용자 스토리 8–9단계, `feedback-loop-and-reconciliation.md:100`, `persistence-and-state-machines.md:113` · CONFIRMED(상향)
- `internal/adapters/sqlite/coordination.go:286` `ActivateFollowup`과 `internal/app/dispatch/coordination.go:92` `Coordinator.Activate`는 프로덕션 호출자가 없다. `CompleteAttempt`(`sqlite/dispatch.go:215-277`)는 route 전이를 하지 않는다. `internal/app/workreceipt/service.go:374`는 `IsActive()`(ACTIVE_CLEAN/DIRTY)만 허용.
- 재현: enable → dispatch(accepted) → `work begin` → 두 번째 burst(merge, dirty 1) → `work complete --manifest -`(`[]`) → route `FOLLOWUP_READY`, `…-followup-2` ready → `dispatches drain` → accepted(`t_00000002`) → `route show`: 여전히 `FOLLOWUP_READY` → `work begin --dispatch-id …-followup-2` → **exit 4 `work_receipt_invalid`** ("is not the active dispatch … state FOLLOWUP_READY, active "…-followup-2"" — 자기모순 메시지). 새 arrival은 merge만, `reconcile --reason manual`·`rerun`·`work fail`로도 해제 불가, `doctor`는 route finding 없음.
- 추가: follow-up은 자동 제출도 되지 않는다. Watchman arrival은 merge만 하고, 패키징된 스케줄 작업 `reconcile --reason scheduled --submit`은 IDLE route에서만 작업을 만든다. 운영자가 `dispatches drain`을 수동 실행하지 않으면 CON-003 follow-up은 Hermes에 도달하지 않는다.
- 모든 다중 generation 테스트는 스토어를 직접 호출해 이 공백을 우회한다(`g4_test.go:210-216` 주석 "the production activation happens at target acceptance" — 사실이 아님; `e5t1_test.go:373`, `e5t3_test.go:323,516`, `e5t4_test.go:370,578,667,711,732,833,915`). 따라서 G4 증적(AC-402/403/405)은 제품 경로가 아닌 테스트 전용 경로의 증적이다.
- 권고: 수락 시(`CompleteAttempt` 또는 submit 경로) `FOLLOWUP_READY→ACTIVE_CLEAN` 전이 호출; Watchman arrival 또는 스케줄 경로에서 due follow-up 자동 제출; 스토어 직접 호출 없이 전체 generation을 구동하는 CLI 수준 회귀 테스트 추가.

### High (5)

**H-1. 제출 직전 route revision·target 재검증 부재 — 설정 변경 후 drain이 오래된 intent를 그대로, 심지어 다른 타입의 sink로 제출** · POL-008, SEC-010, ADR-0010, `processing-pipeline.md:152-158` · CONFIRMED (T2 F-T2-1/F-T2-2 병합)
- `internal/cli/plan.go:192-196`의 "revalidation"은 `plan.Route.Revision`을 자신을 만든 `revision`과 비교하는 항진식. `ports.IntentSnapshot`(`internal/ports/dispatch.go:136-153`)에 route revision 필드가 없고 `LoadIntent`(`sqlite/dispatch.go:116-118`)는 `route_revision`을 SELECT하지 않는다. `SubmitOnce`/`Drain`/`runDispatchesDrain`(`dispatches.go:337-391`)은 비교하지 않으며, `intent.go:76`의 `ready→superseded: route_revision_invalidated` 엣지는 writer가 없다(`e6t2_test.go:41`이 raw SQL로 `superseded`를 만드는 이유). `acknowledged_revision`은 표시용으로만 읽힌다.
- 재현 A: `dispatch --no-submit` → YAML `dispatch.profile` 변경(revision 변경) → `drain` → 오래된 profile·revision으로 task 생성, 경고 없음. 재현 B: target을 다른 Kanban 보드로 변경 → 보드 β에 task가 생기지만 DB에는 `target_id=kanban-a`, `target_scope=board-alpha`로 기록(거짓 lineage, 이후 scope 증명 오염). 재현 C: target을 webhook으로 변경 → **저장된 kanban intent가 webhook으로 POST**됨(`durable_acceptance` 불가 sink로 전달; 미도달 endpoint만이 전달을 막음).
- `Rerun`(`operator.go:105`)·`BuildFollowupRequest`(`coordination.go:128`)도 저장된 구 revision을 그대로 승계.
- 권고: `IntentSnapshot`에 `route_revision`(및 이미 저장된 target_id/type/scope) 추가, `SubmitOnce`에서 `AcquireAttempt` 전에 현재 `config.RouteRevision`·resolved sink와 비교, 불일치 시 선언된 `superseded` 엣지 + 대체 decision 생성.

**H-2. 이전 path digest가 제품 경로에서 전혀 참조되지 않아 unchanged/metadata-only modify가 항상 dispatch됨 — AC-102 도달 불가** · PTH-007(metadata-only 절), PTH-006(공허 충족), AC-102, AC-103 교차 호출 · CONFIRMED
- `internal/cli/plan.go:168`이 `route plan`·`dispatch --dry-run`·**내구 `dispatch`** 모두에 `ingest.NoFacts{}`를 하드코딩(유일한 `BuildBatch` 호출자). 스토어 기반 `PathFacts` 구현은 없다; `path_facts`는 `reconcile/full.go:246`만 쓰고 `full.go:107`만 읽는다. `batch.go:29` 주석("E5-T3에서 도착")과 `roadmap.md:803`의 정정 주장은 사실과 다르다(E5-T3은 receipt attribution을 전달).
- 추가 leg: `planner.go:130-192`는 `Batch.Dropped`를 읽지 않아 `unchanged_content` 사유 코드가 plan/decision에 도달할 수 없다(AC-102가 요구하는 사유).
- 재현: `reconcile --reason initial`로 `path_facts`에 `Notes/a.md` digest 적재 → 바이트 동일 파일을 `route plan` → `disposition: dispatch`, `reason_codes: ["normal_batch"]`; `touch -m`(내용 불변)도 dispatch. E0-T5 보고서 `:102-103`는 "same-content modify 억제는 Agent Dispatch 책임"이라고 명시.
- `TestG1AC102UnchangedModifyDrops`(`g1_test.go:98`)는 prior 없음 leg만 검사; 억제 단언은 `MapFacts`를 쓰는 단위 테스트(`batch_test.go:130`)에만 있다 → VALIDATION.md:71의 AC-102 "검증됨"은 제품 경로의 증적이 아니다. `route plan`/dry-run의 NoFacts는 `roadmap.md:618`에 문서화됨(PASS-NOTE), 내구 경로는 미문서.
- 권고: SQLite `path_facts` 기반 `PathFacts` 구현·주입, ingestion 트랜잭션에서 path facts 갱신(`processing-pipeline.md:119` step 5), planner에 dropped 사유 전파.

**H-3. SCP-008(Linux 검증)이 릴리스 시 수행되지 않았고, 출시 상태의 스위트는 Linux에서 통과 불가하며, 문서는 "Linux CI leg 검증됨"이라고 잘못 주장** · SCP-008, AC-505, TST-009 · EXEC + CONFIRMED
- `internal/adapters/secretresolver/resolver_test.go` `TestRunSecurityControls`(:149)·`TestRunSecuritySeparatesStderr`(:282)는 darwin 전용 `runSecurity`를 빌드 태그/skip 없이 호출 → `platform_other.go`의 "keychain references are not supported on this platform"으로 Linux에서 실패(§3.2).
- `roadmap.md:1468` "Verified by make verify … including the Linux CI leg for AC-505", `CHANGELOG.md:15` vs `VALIDATION.md:156` "no such run is recorded yet"; `VALIDATION.md:148`은 같은 섹션에서 :156과 모순. CI 제거 커밋 `924f60a` 이후에도 `roadmap.md:300,1416,1423`, `CHANGELOG.md:41,46`, `acceptance-criteria.md:85`, `project-charter.md:103`, `canonical-record-contracts.md:150`, `examples/scripts/README.md:6`에 CI 주장 잔존. decision-log에 SCP-008 예외 기록 없음.
- 권고: 두 테스트에 `//go:build darwin` 또는 `keychainSupported()` skip 가드; root 환경 가드(`TestMaintenanceBackupWritesVerifiedSnapshot`, `TestInitStateDirIOFailureUsesConfigInvalid`)는 선택; 지원 Linux(amd64) 호스트에서 `make verify` 실행·기록; roadmap/CHANGELOG/VALIDATION 주장 정정.

**H-4. 게이트 증적의 무결성 — 실행되지 않는/공허한 테스트와 존재하지 않는 테스트 인용** · TST-004, TST-009, AC-207, AC-203, E3-T1 수용기준 · EXEC
- `TestG2AC207`(`g2_test.go:288-310`): `migrate-partial --steps 0`으로 마이그레이션 전 종료 → ledger 부재로 `SchemaVersion()` 에러 → `t.Skipf` → "재시작 후 최신 버전 도달·무결성" 단언이 **한 번도 실행되지 않음**(macOS·Linux 모두 SKIP). VALIDATION.md:97의 "pre-ledger version read is skipped"는 테스트 전체 skip을 가린다. 다중 statement 마이그레이션 단위 내부에서의 프로세스 종료는 어디서도 시험되지 않는다(`TestMigrationFailureIsAtomic`은 SQL 구문 오류 주입; `crashbin lease … --die`(`crashbin/main.go:42-48,260-264`)는 어떤 테스트도 호출하지 않는 dead code).
- `internal/domain/state/contract_lockstep_test.go:16,65`: `../../docs/schemas` → `internal/docs/schemas`(부재) → 항상 skip. 스키마↔enum 드리프트 가드가 무효(현재 수동 대조 결과 intent 12 state·receipt enum은 일치). `roadmap.md:710`이 이를 증적으로 인용.
- `VALIDATION.md:159`가 인용한 `TestG2MigrationInterruptedUpgrade`는 저장소에 존재하지 않는다(`grep -rn` 0건).
- TST-004 경계 매핑: before-commit ✅ 실제 프로세스 사망, after-commit ✅, **during submit ❌**(`runtime_test.go:249` in-process `Close()`), **after remote acceptance ❌**(`TestG2AC203` fake sink 미호출), during migration △(단위 경계에서만). VALIDATION.md:87 "real process deaths … every defined crash boundary"는 과장.
- 권고: AC-207 테스트를 `--steps 1..3`(단위 사이) + 단위 내부 kill 훅으로 재작성, lockstep 경로 수정 후 파일 부재 시 `t.Fatal`, 인용 정정, `lease --die` 기반 제출 중 크래시 테스트 추가.

**H-5. CLI 계약 미이행 — `config show` 미구현, `dispatches show`가 스펙의 "전체 lineage"를 누락** · CLI-004, OPS-002, `cli-spec.md:32,67,138` · 정적 확인(고신뢰)
- `internal/cli/config.go:22-24` `config show` → `command_not_implemented` exit 2(주석은 "E6-T2에서 도착" — E6-T2는 완료됨).
- `dispatches show`: `ports.IntentLineage`(`internal/ports/inspection.go:74-79`)는 Intent/Attempts/Receipts/Transitions만; `LoadIntentLineage`(`sqlite/inspection.go:65-137`)는 observation·batch·decision(정책 사유 코드)·route state·work receipts를 조회하지 않는다(`receipts list --kind work`는 union을 구현하고 있어 불일치). 게다가 B-1/M-3로 인해 주 경로 intent는 `Transitions: null`.
- `dispatches list`는 스펙(`:134`)의 age/external-ref/causal-ID 필터와 페이지네이션이 없다(M-13).
- 권고: `config show` 구현(`config.Normalized` 재사용), `LoadIntentLineage`에 decision→batch→observation 체인과 work_receipts 조인.

### Medium (32)

| ID | 요구사항 | 발견 | 증거 | 검증 |
|---|---|---|---|---|
| M-1 | cli-spec:78, TST-008 | `route disable` 후에도 `dispatches drain`이 ready intent를 제출(비IDLE route의 rerun도 동일). Watchman ingress와 스케줄 `reconcile --submit`은 올바르게 차단됨 | `runtime.go:64-133` activation 미참조; `route_cmds.go:206-230`은 activation_state만 변경 | CONFIRMED(Medium) |
| M-2 | configuration-spec:253, cli-spec:78 | YAML `routes.<id>.enabled`가 어디서도 읽히지 않음(두 키 게이트의 permit 절반이 무효; revision 입력도 아님). `enabled: false`인 채 `route enable`하면 제출됨 | `config/types.go:80` 외 reader 없음 | CONFIRMED |
| M-3 | DUR-011, `processing-pipeline.md:119` | route 전이 9개 호출 지점 중 8개가 `state_transitions`에 기록되지 않음(`applyRouteTransition`이 `now/contextJSON`을 버림); `CommitLineage`/`SaveIntent`는 `:created` 행 미기록. 재현: 전이 4회 후 route 감사 행 0 | `sqlite/coordination.go:344-358`; `store.go:133-157` | PARTIAL |
| M-4 | OPS-009 하위 문서(`persistence:148`, `migration-and-versioning:30`) | 애플리케이션 레벨 마이그레이션 락 부재: 최초 동시 실행 6개 중 5개 exit 20(`table resources already exists`). fail-closed, DB는 정상 | `migrate.go:112,142-155,193-205` | CONFIRMED(Medium) |
| M-5 | OPS-008, error-model:96 | 최초 동시 open 시 `PRAGMA journal_mode=WAL`이 SQLITE_BUSY → `sqlite_open_failed`(20, non-retryable); `sqlite_busy`(10)는 어디서도 방출되지 않음. `init`은 DB를 만들지 않음 | `sqlite.go:63-73`; `state.go:124-125` | T3 |
| M-6 | DUR-004, OPS-006, `persistence:115-120` | route 엣지 4개(`ACTIVE_*→UNCERTAIN` stale, `UNCERTAIN→ACTIVE_CLEAN`, `UNCERTAIN→QUARANTINED`, `QUARANTINED→IDLE`)에 프로덕션 writer 없음; `active_stale_after`는 경고만, stale ACTIVE의 운영자 출구 없음; doctor `stale_active_route` remediation 무효 | `route.go:59-65,104-111` | T3/T5 일치 |
| M-7 | DUR-009, `persistence:59-60` | dead letter를 닫는 경로 없음(`discard` 부재, `superseded` 미생성), `resolvedTerminalStates()`가 `dead_lettered` 제외 → 영구 미정리; 예산 소진 `retry_wait`도 출구 없음 | `dispatches.go:23`; `maintenance.go:83` | T3 |
| M-8 | DAT-009 | 저장 페이로드 버전이 읽기 시 검증되지 않음(`request_version`/`payload_version`/`schema_version` SELECT 없음); acceptance receipt `payload_version`이 NULL로 저장; webhook sink에 contract_version 게이트 없음(`/v9` 페이로드가 전송 계층까지 도달 재현); Rerun/follow-up이 미검증 페이로드에 현재 버전을 재도장. Kanban renderer(`renderer.go:90-91`)만 fail-closed | `sqlite/dispatch.go:116-118`; `inspection.go:678-680`; `hermeswebhook/sink.go:208-209` | PARTIAL |
| M-9 | HER-006 | Kanban task에 acceptance criteria가 렌더링되지 않음(`renderer.go:140-142` 검증만, 골든 파일에 없음); webhook 페이로드에는 포함. 기준 4("manifest path가 여전히 존재한다고 가정하지 말 것")는 본문에 동등 문장 없음(`hermes-integration.md:92` vs 계약 §4 템플릿 불일치) | `renderer.go:168-199`; `testdata/renderer-golden.txt` | PARTIAL(Low–Med) |
| M-10 | SCP-004, SCP-003 | `file_scope: markdown`(스키마 const)이 ingest에서 미적용 — `include: ["**/*"]`이면 png/pdf/canvas가 해시·dispatch되고 `config validate`는 valid. `reconcile/full.go:386`만 `.md/.markdown` 필터(경로별 scope 불일치). 기본 설정(`**/*.md`)은 안전 | `ingest/batch.go:161-179`; `semantic.go:22-43` | PARTIAL |
| M-11 | SRC-002, DAT-009 | 영속 observation의 `flags_json`이 JSON 태그 없는 Go 필드명(`{"Overflow":…}`)으로 `source-observation.schema.json`(`additionalProperties:false`) 위반; `source.position`(since/clock) 미저장(fresh instance면 `source_event_key` NULL) | `records/change.go:82-89`; `plan.go:482` | T1 |
| M-12 | SEC-008(SHOULD) | 상태 DB 파일이 umask 의존 0644(재현), `-wal/-shm` chmod 없음; 기존 0755 디렉터리를 state_dir로 지정하면 노출. decision-log에 예외 기록 없음 | `sqlite.go:26-104`(Chmod 없음) | T7/T8 일치 |
| M-13 | CLI-004, cli-spec:134 | `dispatches list`: 스펙 6개 필터 중 3개 구현, 페이지네이션 없음(`--limit` 상한 1000) | `ports/inspection.go:14-20`; `dispatches.go:57,138` | T6 |
| M-14 | cli-spec §12, error-model §1 | envelope `trace_id`가 항상 `""`(`--trace-id`는 파싱되어 로그에만 사용); `remediation` 필드 미사용(메시지에 병합), `retryable` 항상 false | `cli.go:279,309,327,329` | EXEC 확인 |
| M-15 | CLI-008 | dead-lettered `dispatches retry`에 `--reason` 누락 시 `internal_unclassified` exit 40(정확히는 `flag_invalid` 2) | `operator.go:43-46`; `dispatches.go:474-476` | T6 |
| M-16 | CLI-008, error-model §3 vs §4 | `*_not_found` 4개 코드가 category `usage`인데 exit 4(§3 1:1 규칙 위반, D-002와 충돌). 코드는 §4대로 구현·테스트됨 → 문서 §4의 category를 `input_rejected`로 정정하는 것이 안전. 또한 exit는 category에서 기계적으로 도출되지 않고 ~60개 호출 지점에서 독립 지정 | `error-model.md:49,110-113`; `cli.go:316-331` | T6/T9 일치 |
| M-17 | OPS-001 | 문서화된 로그 이벤트 27개 중 18개가 어디서도 방출되지 않음(ingest/policy/route/work/feedback/quarantine/reconciliation 이벤트 전부) | `observability/logger.go:64-90` vs 호출 9곳 | T7/T8 일치 |
| M-18 | POL-006, canonical-record-contracts:114-118 | `merge_pending`이 영속 decision에 기록되지 않음(`Input.ActiveDispatchExists` writer 없음) → 병합된 burst의 decision이 `disposition='dispatch'`, envelope은 `merge_pending`으로 불일치 | `planner.go:58,209-213`; `plan.go:277-280,474-518` | T5 |
| M-19 | AC-409, PTH-008 | `dispatches reprocess`가 현재 정책을 평가하지 않고 `disposition='dispatch'`, `classification='normal'`을 하드코딩(보호 경로 batch도 동일 기록; intent는 생성되지 않아 실해는 없음) | `dispatches.go:250-263` | T5 |
| M-20 | FBK-002/003, OPS-004 | prune이 미해결 run의 `begun` work receipt를 삭제(`DELETE FROM work_receipts WHERE submitted_at < ?` 무가드) → attribution 앵커 상실, `work complete`가 `ErrRunNotBegun` | `maintenance.go:249-252` | T5 |
| M-21 | error-model, CHANGELOG:149 | disabled route의 `reconcile --submit` 거부가 `sqlite_query_failed`/storage(20)로 분류(기대 `transition_invalid` 14); rerun 거부는 exit 40 | `reconcile/full.go:204-206`; `quarantine.go:235-251` | T5/반박 일치 |
| M-22 | HER-005, AC-002, AC-306, configuration-spec:242-255 | `required_capabilities`가 `route enable`·오프라인 `config validate`·대상 도달 불가 시 검증되지 않음(보고서는 로컬 파일인데도); 제출 시에는 fail-closed | `semantic.go:16-27`; `route_cmds.go:154-203`; `adapter.go:105-110` | T4 |
| M-23 | HER-005 운영 표면 | `doctor --probe-targets`가 정상 hermes-kanban 대상에 대해 `maxManifestBytes=0`으로 sink를 만들어 항상 `target_gate_failed` 경고(재현) | `status.go:360`; `hermeskanban/sink.go:43-45` | T4 |
| M-24 | SEC-003, PTH-002, SRC-005 | 분류 불가 파일명(`max_path_bytes` 초과·잘못된 UTF-8) 하나가 전체 full reconcile을 중단(인접한 unreadable-subtree 분기와 불일치) → 복구 경로 자체가 막힘 | `reconcile/full.go:379-382` | T7 |
| M-25 | SEC-006, configuration-spec:232 | `file:` 시크릿 참조가 권한 검사 없이 읽힘(world-readable 허용, doctor 경고 없음) | `secretresolver/resolver.go:66-79` | T4/T7 일치 |
| M-26 | SEC-007 | 로그 redaction이 키 이름 기반뿐(값 패턴 `Bearer …`/`?token=` 없음), `message` 필드는 path 정책 미적용. 현재 emitter는 자격증명을 로그에 넣지 않아 실제 누출은 없음 | `logger.go:140-151,258-279` | T7 |
| M-27 | release-checklist:17, repository-layout:64 | LICENSE 파일 없음, 의존성 라이선스 검토 산출물 없음 | `find -iname '*license*'` 0건 | T8 |
| M-28 | D-015 "single deterministic entrypoint" | 스텁 hermes 프로브의 고정 5s/30s 타임아웃이 병렬 패키지 실행+부하에서 플레이크(§3.1; 6건, 격리 시 전부 통과). `make verify` 결과가 부하에 비결정적 | `sink_test.go:162…`; `e5t3_test.go:353` | EXEC |
| M-29 | 운영 안전성(installation.md §2) | `init --state-dir X`가 X를 생성하지만 config에는 `state_dir: ""`를 기록 → 이후 명령이 플랫폼 기본 경로로 빠짐. 두 리뷰어가 실제로 걸려 사용자 기본 경로에 DB가 생성됨(§3.3) | `internal/cli/init.go:72,107`; 생성된 config `instance.state_dir: ""` | EXEC |
| M-30 | TST-004, VALIDATION.md:87,101 | H-4의 세부: "during submit"·"after remote acceptance" 경계에 실제 프로세스 사망 테스트 없음, `crashbin lease --die` 미사용, VALIDATION 과장 | `g2_test.go:137-172`; `runtime_test.go:249-268` | T3 |
| M-31 | TST-009, 문서 정합성 | VALIDATION.md 헤더 "2026-08-20 / SOT 1.0.4 / 9 Completed 24 Planned"(:3-4,21) · `docs/README.md:3` "SOT 1.0.4"(실제 1.0.12) · "eight JSON Schemas"(:9,30; 실제 12) · release-checklist 50/50 미체크·E6-T4 미인용 · E4-T5만 `### Evidence` 부재 · E0-T1/T2/T3 `### Dependencies` 부재(E0-T3 자체 수용기준 위반) · `pending_reconcile` ABA 수용 잔존(CHANGELOG:162)이 릴리스 노트 "Known deferred work"에 없음(자가 치유·지연 1회로 평가됨) · E6-T3 hardening 잔존 6건이 개별 처분 없이 "deferral"로 남음(roadmap:19 "Deferred 0"과 공존) · task-execution-rules §5 YAML 레코드 미유지 · repository-layout 6개 이탈 미설명(LICENSE, `migrations/` 빈·미추적, `test/e2e`·`test/helpers` doc.go만, ports 파일명, `processrunner`/`gitlocal` 빈 패키지) · testing-strategy §5 fixture 레이아웃/§9 E2E 아티팩트/§10 CI 절 미반영 · skip 8개 지점(docs-schemas ×4, crashbin, go toolchain, permission ×2) 승인 사유 미기록 · `e4t4_test.go:350` stale skip · CONTRIBUTING/README의 `make verify` 구성에 `schedule-check` 누락 · `roadmap.md:441` stray `---`, E6-T4 evidence 헤딩 중복 | T9 표 참조 | T9 |
| M-32 | SRC-005, AC-107, `watchman-integration.md:82-83` | "unusable" position(`WATCHMAN_SINCE=not-a-clock`)은 정상 처리되어 dispatch; recrawl 증거는 trigger 모드에서 감지 불가(E0-T5에 기록됨, 축소 보증으로는 미기록); `source_position_unusable` 코드 미방출 | `watchman.go:42-45,117-123` | T1 |

### Low / Info (요약)

- T1: AC-106 테스트가 "읽히지 않음"이 아니라 "변경되지 않음"을 단언(`g1_test.go:184-186`, testing-strategy:56) · overflow/fresh-instance fixture 파일 없음(환경 변수 기반 테스트만) · `WATCHMAN_RELATIVE_ROOT` 기록만 하고 미적용·미거부 · 경로 관련 오류 코드 6종(`path_traversal_rejected` 등) 미방출, payload traversal이 class 4(`source_malformed_json`)로 분류(문서는 class 30) · 경로별 제외 사유 계산 후 폐기 · uncertain delete 사유 미표시 · fixture 디렉터리 레이아웃 미준수(Info).
- T2: attempt/receipt/follow-up/rerun ID가 문자열 연결(domain-model §14는 UUIDv7 요구) · 정규 인코더가 `\b`/`\f`에서 RFC 8785와 불일치, Unicode 정규화 정책 없음 · reason code 레지스트리 미문서(`meaningful_markdown_change` 예시는 미방출) · testing-strategy §3 정책 조합 3개·route-revision 골든 미구현 · CLI attempt/receipt 레코드에 `schema_version` 없음(스키마는 required) · overflow→quarantine 시 `generation_action: merge_reconcile`이지만 pending 미설정.
- T3: `AcquireLease`/`TransitionIntent` 공개 메서드가 가드·감사 우회(호출자 없음) · prune 감사 행 `transition_id` NULL · testing-strategy §6의 pragma 검증·busy-timeout 테스트 없음 · lease TTL 1분 하드코딩·미문서 · `state_directory_not_local`은 `init`만 방출(open 경로는 20) · `pending_reconcile` ABA는 자가 치유(Info).
- T4: `lookup_by_idempotency_key: true` 선언 vs 포트는 `capability_unsupported`(이름 과부하; route requirement로 통과 후 런타임 거부) · 아카이브 시 멱등 키 해제 주의사항·Kanban CLI 인증 모델이 보고서에 미승계 · sink 계약 §9 중 Kanban의 "accepted non-durable"/"definite rejection" 도달 불가(N/A 기록 없음), webhook "malformed output" 직접 테스트 없음 · webhook scope re-point 거부 테스트 없음 · `fakesink.Rejected()`가 `DurableTrue` · `failure-recovery.md:6`이 지원되지 않는 key lookup을 안내 · Watchman lifecycle 클라이언트에 `cmd.Dir`·write-side 출력 경계 없음(SEC-004 문자상 미충족, 운영자 명령 전용·도달 경로 없음 → Low; `secretresolver`의 `security` 호출도 `cmd.Dir` 없음; implementation-guide §11 "one reusable runner" 미준수).
- T5: `docs/examples/hermes-skill/SKILL.md:3` "provisional" 문구 잔존 · `route enable` 거부 메시지가 올바른 revision을 출력(의도된 anti-accident 게이트, Info) · `TestOperatorRerunCreatesNewLineageAndKey`는 키가 원본과 다른지 단언하지 않음 · full reconcile의 symlink/비정규 항목을 `FileRegular`로 보고.
- T6: `parseOpsFlags`가 모든 ops 명령에 `--probe-targets/--dry-run/--yes/--full`을 묵시 허용 · 약 20개 명령이 json 전용인데 cli-spec에 미문서, `watchman *`는 `--output` 자체를 거부 · `doctor` 실패 시 stdout에 `ok:true` envelope + stderr 오류(스펙이 지시하나 `cli.go` 패키지 문서와 모순) · exit 12/13 클래스는 성공 envelope의 `state`로만 표현(의도적, Info).
- T7: webhook endpoint 쿼리스트링 토큰이 `dispatch_attempts.diagnostic`·`dispatch_intents.target_scope`에 그대로 저장(로그에는 미출력, 지원되지 않는 설정 형태 → Low) · `definiteNotSubmitted` diagnostic·`receipts.Service` bounded_payload 자체 경계 없음 · `context_json`을 `%q`로 조립(운영자 자유 텍스트로 잘못된 JSON 가능) · config 위치(vault 내부)·파일 권한에 대한 doctor 경고 없음 · full reconcile 열거 상한 없음 · govulncheck 도달 취약점 0(`golang.org/x/text` 간접 advisory 1건), TLS(1.2+, redirect/proxy 없음, InsecureSkipVerify 0건)·SQL(파라미터 바인딩) 깨끗.
- T8: `toolchain` 지시어 없음(자동 상위 툴체인 허용) · 재현 빌드 자동 검증 없음(이번 리뷰에서 수동 확인) · prune 감사에 policy revision 없음 · doctor가 트리거 정의 드리프트를 검사하지 않음.
- 코드 위생: 구현 완료 패키지에 "intentionally defines none yet" placeholder `doc.go` 잔존(`adapters/watchman`, `config`, `app/reconcile`); 빈 패키지(`adapters/gitlocal`, `adapters/processrunner`, `domain/errors`); `test/e2e`·`test/helpers` doc.go만; `migrations/` 빈 미추적 디렉터리.

---

## 6. 요구사항 판정 매트릭스 (126)

표기: P=PASS, N=PASS-NOTE, G=GAP. 각 셀의 괄호는 근거 발견 ID.

| 그룹 | 판정 |
|---|---|
| **BND** (7) | 001 P · 002 P · 003 P(HER-010 grep 청정) · 004 P · 005 P(vault 쓰기 경로 없음, 해시 읽기만) · 006 P(planner 순수, LLM 없음) · 007 P |
| **SCP** (9) | 001 P(G3 실제) · 002 P · 003 P · 004 N(M-10) · 005 N(toolchain 지시어 없음) · 006 P · 007 P(statfs 거부, 양 플랫폼) · **008 G(H-3)** · 009 P(NoGit) |
| **SRC** (8) | 001 P · 002 N(M-11) · 003 P · 004 P · 005 N(M-32) · 006 P(`time.Sleep` 0건, settle 키 없음) · 007 P · 008 P |
| **PTH** (8) | 001 P · 002 P · 003 P · 004 P · 005 P · 006 N(공허 충족, H-2) · **007 G(H-2 metadata-only)** · 008 P |
| **DAT** (9) | 001 P · 002 N(파생 ID) · 003 P · 004 N · 005 N(JCS 경계) · 006 P · 007 N(receipt attempt_id·intent policy_revision 등 누락) · 008 P · **009 G(M-8)** |
| **POL** (8) | 001 P · 002 P · 003 P · 004 P · 005 P · 006 N(M-18) · 007 P(실측: 행동 영향 키 전부 revision 변경) · **008 G(H-1)** |
| **DUR** (12) | 001 P · 002 P · 003 P · 004 N(M-6) · 005 N · 006 N(B-2 rerun 우회) · 007 P · 008 P · 009 N(M-7) · **010 G(B-1)** · **011 G(M-3)** · 012 P |
| **CON** (6) | **001 G(B-2)** · 002 P · **003 G(B-3)** · 004 P · 005 P · 006 N(mutex 전달이 capability에 비게이트) |
| **HER** (10) | 001 P · 002 P · 003 P · 004 P · 005 N(M-22) · **006 G(M-9)** · 007 P · 008 P · 009 P · 010 P |
| **WHK** (5) | 001 P(커밋 순서 확인) · 002 P · 003 N(쿼리 토큰 Low) · 004 P · 005 P |
| **FBK** (8) | 001 P · 002 N(양방향 집합 동등, 운영 비교 없음) · 003 N(M-20) · 004 P · **005 G(B-3)** · 006 P(SHOULD) · 007 P · 008 N(B-3로 체인 길이가 우연히 1) |
| **CLI** (8) | 001 N · 002 P · 003 P(dry-run은 DB를 열지 않음) · **004 G(H-5, M-13)** · 005 P · 006 N(actor 상수 "operator") · 007 N(Watchman 입력 차단은 트리거 argv 구조에만 의존) · 008 N(M-14/15/16) |
| **SEC** (10) | 001 P · 002 N(중간 컴포넌트 TOCTOU 문서화) · 003 N · 004 N(Low) · 005 P · 006 N(M-25) · 007 N(M-26) · **008 G(SHOULD, M-12, 예외 미기록)** · 009 N · **010 G(H-1)** |
| **OPS** (9) | 001 N(M-17) · **002 G(H-5)** · 003 P · 004 N(M-20) · 005 N(M-23, trigger drift 없음) · 006 N(M-6) · 007 P · 008 N(M-5) · 009 N(M-4, H-4) |
| **TST** (9) | 001 N(lockstep 무효) · 002 P · 003 N(fixture 2종 파일 없음) · **004 G(H-4, M-30)** · 005 P · 006 P · 007 P · 008 N(M-1/M-2) · 009 N(M-31) |

---

## 7. AC 판정 매트릭스 (40)

| 게이트 | 판정 |
|---|---|
| **G0** | AC-001 N(Kanban CLI 인증 모델·`resource_mutex`/`cancellation` fixture 미기록) · AC-002 N(M-22) |
| **G1** | 101 P · **102 G(H-2)** · 103 P · 104 P · 105 N(경로별 사유 미표시) · 106 P(단언 방식 Low) · 107 P · 108 P · 109 P · 110 P — 오늘 실제 실행 PASS |
| **G2** | 201 P · 202 P · **203 G(B-1; 테스트 공허)** · 204 P · 205 P · 206 P · **207 NOT-EVIDENCED(항상 skip)** |
| **G3** | 301 P · 302 P(실제 보드 dedup) · 303 P · 304 P · 305 P · 306 N(게이트가 enable이 아닌 submit 시점) — 오늘 실제 Hermes 0.19.1로 PASS |
| **G4** | 401 P · 402 N(follow-up 생성은 되나 활성화·자동 제출 없음, B-3) · 403 N(테스트가 스토어 직접 활성화) · 404 P · 405 N · 406 N(문자 그대로의 "receipt 없음" 미시험) · 407 P · 408 P · 409 N(B-2, M-19) |
| **G5** | 501 P · 502 P · 503 N(M-20) · 504 N(실제 `watchman install`·스크립트 실행 미포함) · **505 G(H-3: Linux 실행 실패)** · 506 N(스키마 1/12만 존재 확인; 재현성은 이번 리뷰에서 확인) |

---

## 8. 권장 후속 조치 (우선순위)

1. **Blocker 3건 수정 후 G2/G4 재검증** — `Recover` 연결(B-1), rerun supersede + Drain slot 검사(B-2), follow-up 활성화·자동 제출(B-3). G4 테스트에서 스토어 직접 호출 우회를 제거하고 제품 경로만으로 다중 generation을 구동.
2. **제출 전 재검증(H-1)과 PathFacts 연결(H-2)** — 둘 다 선언된 엣지·테이블·헬퍼가 이미 존재하며 배선만 빠져 있다.
3. **Linux 레그(H-3)** — keychain 테스트 플랫폼 가드, 지원 amd64 호스트에서 `make verify` 기록, "Linux CI leg" 주장 정정. 필요 시 SCP-008 예외를 decision-log에 기록.
4. **증적 무결성(H-4)** — AC-207·lockstep 테스트 복구, 존재하지 않는 테스트 인용 제거, VALIDATION.md 헤더/G2 서술 정정. `task-execution-rules.md §4`에 따라 E3-T2/E3-T3/E3-T4/E3-T5/E5-T3/E5-T5/E6-T4는 공식 감사로 재개(In Progress)되어야 한다.
5. **CLI 계약(H-5, M-13, M-14)** — `config show`, `dispatches show` lineage, 필터/페이지네이션, `trace_id`.
6. Medium 일괄: 자동 쓰기 게이트 구멍(M-1/M-2), 감사 기록(M-3), 마이그레이션 락·WAL 초기화(M-4/M-5), stale route 출구·dead letter 종료(M-6/M-7), 버전 검증(M-8), HER-006 렌더링(M-9), `init --state-dir` 영속(M-29), 테스트 타임아웃 견고성(M-28).
7. 문서 정합성 일괄(M-31) 및 release-checklist 실제 운영.

---

## 9. 리뷰의 한계

- 조사 트랙은 `go test`를 실행하지 않았고(간섭 방지) 단언을 읽어 판정했다; 실행 증적은 오케스트레이터의 §3 실행으로 보완했다.
- Linux 실행은 arm64 컨테이너·Go 1.26.7이며 릴리스 대상 amd64·1.26.6과 다르다. Watchman/Hermes가 없어 해당 테스트 11건은 skip되었다(기록된 환경 갭).
- 실제 Hermes 대상 재현(B-1/B-2/B-3/H-1)은 스텁 hermes(프로젝트의 `stubhermes`와 동일 형태)로 수행했다; 실제 Hermes에서의 동작은 G3 테스트가 단일 generation에 대해서만 증명한다.
- ADR별 구현 충실도(release-checklist:9)와 의존성 라이선스 검토의 법적 정확성은 범위 밖이다.
- 트랙별 원본 발견 ID(F-T1-1 … F-T9-18)는 본문 괄호로 대응시켰으며, 병합·상향·하향 내역은 §2 Phase 2에 요약했다.
