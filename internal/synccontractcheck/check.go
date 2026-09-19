package synccontractcheck

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

var expectedArtifacts = []string{
	"commands.json", "peer.json", "results.json", "errors.json", "trust-fixtures.json",
	"../../schemas/sync-membership.schema.json", "../../schemas/sync-membership-plan.schema.json", "../../schemas/sync-checkpoint.schema.json", "../../schemas/sync-checkpoint-plan.schema.json", "../../schemas/sync-import-acknowledgement.schema.json", "../../schemas/sync-publication.schema.json", "../../schemas/sync-delivery.schema.json", "../../schemas/sync-import.schema.json", "../../schemas/sync-control.schema.json", "../../schemas/sync-verification.schema.json", "../../schemas/sync-nudge.schema.json", "../../schemas/sync-status-request.schema.json", "../../schemas/sync-status-response.schema.json",
	"../../examples/sync-checkpoint-plan.json", "../../examples/sync-checkpoint.json", "../../examples/sync-control.json", "../../examples/sync-delivery.json", "../../examples/sync-import-acknowledgement.json", "../../examples/sync-import.json", "../../examples/sync-membership-plan.json", "../../examples/sync-membership.json", "../../examples/sync-nudge.json", "../../examples/sync-publication.json", "../../examples/sync-status-request.json", "../../examples/sync-status-response.json", "../../examples/sync-verification.json",
	"../../examples/invalid/sync-checkpoint-malformed-oid.json", "../../examples/invalid/sync-control-invalid-state.json", "../../examples/invalid/sync-delivery-unknown-without-retention.json", "../../examples/invalid/sync-import-empty-target.json", "../../examples/invalid/sync-membership-duplicate-instance.json", "../../examples/invalid/sync-membership-invalid-ref.json", "../../examples/invalid/sync-membership-plan-stale-predecessor.json", "../../examples/invalid/sync-membership-third-active.json", "../../examples/invalid/sync-publication-commit-mismatch.json", "../../examples/invalid/sync-publication-missing-proof.json", "../../examples/invalid/sync-publication-unresolved-prunable.json", "../../examples/invalid/sync-verification-duplicate-node.json", "../../examples/invalid/sync-verification-empty-pair.json", "../../examples/invalid/sync-verification-false-complete.json", "../../examples/invalid/sync-verification-obsolete-incarnation.json",
}

type bundle struct {
	SchemaVersion string   `json:"schema_version"`
	BundleID      string   `json:"bundle_id"`
	Artifacts     []string `json:"artifacts"`
}

type commands struct {
	SchemaVersion string `json:"schema_version"`
	Commands      []struct {
		Path         string `json:"path"`
		Capability   string `json:"capability"`
		Availability string `json:"availability"`
		SideEffect   string `json:"side_effect"`
	} `json:"commands"`
	HelpContract struct {
		RequiredSections []string `json:"required_sections"`
		OutputModes      []string `json:"output_modes"`
	} `json:"help_contract"`
	FlagContracts []struct {
		Path  string   `json:"path"`
		Flags []string `json:"flags"`
	} `json:"flag_contracts"`
	ListContracts []struct {
		Record        string `json:"record"`
		MaxPageSize   int    `json:"max_page_size"`
		SnapshotToken string `json:"snapshot_token"`
	} `json:"list_contracts"`
}

type commandTuple struct{ Path, Capability, Availability, SideEffect string }

var expectedCommandTuples = []commandTuple{
	{"sync capabilities", "contract_read", "implemented", "none"}, {"sync status", "status_read", "implemented_disabled_only", "none"},
	{"sync publish", "publication", "reserved", "git_write"}, {"sync reconcile", "reconciliation", "reserved", "git_read_write"}, {"sync verify", "pair_verification", "reserved", "network_read"}, {"sync serve", "peer_service", "reserved", "listener"}, {"sync pause", "control", "reserved", "state_write"}, {"sync resume", "control", "reserved", "state_write"},
	{"sync membership plan", "membership_plan", "reserved", "none"}, {"sync membership apply", "membership_apply", "reserved", "git_write"}, {"sync checkpoint plan", "checkpoint_plan", "reserved", "none"}, {"sync checkpoint apply", "checkpoint_apply", "reserved", "git_write"},
	{"sync service render", "service_render", "reserved", "none"}, {"sync service install", "service_install", "reserved", "service_write"}, {"sync service inspect", "service_inspect", "reserved", "none"}, {"sync service stop", "service_stop", "reserved", "service_write"}, {"sync service disable", "service_disable", "reserved", "service_write"}, {"sync service uninstall", "service_uninstall", "reserved", "service_write"},
}

var expectedCommandFlags = [][]string{
	{"--output"}, {"--config", "--group", "--output"}, {"--group", "--expected-config-revision", "--output"}, {"--group", "--output"}, {"--group", "--output"}, {"--group"}, {"--group", "--expected-control-revision", "--output"}, {"--group", "--expected-control-revision", "--output"},
	{"--group", "--change", "--output"}, {"--group", "--plan", "--expected-membership-predecessor", "--output"}, {"--group", "--target-commit", "--kind", "--output"}, {"--group", "--plan", "--output"},
	{"--group", "--output"}, {"--group", "--yes", "--output"}, {"--group", "--output"}, {"--group", "--yes", "--output"}, {"--group", "--yes", "--output"}, {"--group", "--yes", "--output"},
}

type peer struct {
	SchemaVersion string `json:"schema_version"`
	Routes        []struct {
		Method                string   `json:"method"`
		Path                  string   `json:"path"`
		Success               string   `json:"success"`
		HTTPStatus            int      `json:"http_status"`
		MaxBodyBytes          int      `json:"max_body_bytes"`
		RequestSchema         string   `json:"request_schema"`
		RequestSelectedFields []string `json:"request_selected_fields"`
	} `json:"routes"`
	Authentication struct {
		Transport                    string   `json:"transport"`
		Credential                   string   `json:"credential"`
		Binds                        []string `json:"binds"`
		VerifyEndpointAndCertificate bool     `json:"verify_endpoint_and_certificate"`
		TailscaleHeadersAuthorize    bool     `json:"tailscale_headers_authorize"`
		PublicBinding                bool     `json:"public_binding"`
		FailureCode                  string   `json:"failure_code"`
		FailureHTTPStatus            int      `json:"failure_http_status"`
	} `json:"authentication"`
	ForbiddenRequestFields []string `json:"forbidden_request_fields"`
}

type resultShape struct {
	OK           bool     `json:"ok"`
	ErrorCode    string   `json:"error_code,omitempty"`
	Category     string   `json:"category,omitempty"`
	ExitCode     int      `json:"exit_code,omitempty"`
	State        string   `json:"state,omitempty"`
	Reason       string   `json:"reason,omitempty"`
	Proof        string   `json:"proof,omitempty"`
	SideEffects  []string `json:"side_effects,omitempty"`
	DoesNotProve []string `json:"does_not_prove,omitempty"`
}

type results struct {
	SchemaVersion         string      `json:"schema_version"`
	Unavailable           resultShape `json:"unavailable"`
	DisabledStatus        resultShape `json:"disabled_status"`
	NudgeAccepted         resultShape `json:"nudge_accepted"`
	PublicationConfirmed  resultShape `json:"publication_confirmed"`
	ImportCompleted       resultShape `json:"import_completed"`
	VerificationCompleted resultShape `json:"verification_completed"`
}

type errorsContract struct {
	SchemaVersion string `json:"schema_version"`
	Errors        []struct {
		Code     string `json:"code"`
		Category string `json:"category"`
		ExitCode int    `json:"exit_code"`
	} `json:"errors"`
}

type trustFixtures struct {
	SchemaVersion string `json:"schema_version"`
	Fixtures      []struct {
		Name            string `json:"name"`
		DocumentKey     string `json:"document_key"`
		PinnedKey       string `json:"pinned_key"`
		SelfAuthorizing bool   `json:"self_authorizing"`
		ExpectedCode    string `json:"expected_code"`
	} `json:"fixtures"`
	Claim string `json:"claim"`
}

func Check(dir string) error {
	var b bundle
	if err := decodeClosed(filepath.Join(dir, "bundle.json"), &b); err != nil {
		return err
	}
	if b.SchemaVersion != "agent-dispatch.sync-provider-bundle/v1" || b.BundleID != "agent-dispatch-sync-provider-v1" {
		return fmt.Errorf("bundle identity mismatch")
	}
	if !reflect.DeepEqual(b.Artifacts, expectedArtifacts) {
		return fmt.Errorf("bundle artifact allowlist mismatch")
	}
	if err := verifySums(dir, b.Artifacts); err != nil {
		return err
	}
	var c commands
	if err := decodeClosed(filepath.Join(dir, "commands.json"), &c); err != nil {
		return err
	}
	if c.SchemaVersion != "agent-dispatch.sync-provider.commands/v1" {
		return fmt.Errorf("commands version mismatch")
	}
	got := make([]commandTuple, 0, len(c.Commands))
	for _, command := range c.Commands {
		got = append(got, commandTuple{command.Path, command.Capability, command.Availability, command.SideEffect})
	}
	if !reflect.DeepEqual(got, expectedCommandTuples) {
		return fmt.Errorf("command tree mismatch")
	}
	if !reflect.DeepEqual(c.HelpContract.RequiredSections, []string{"summary", "usage", "key_flags", "defaults", "approval_requirements", "exit_codes", "side_effects", "examples", "next_safe_command"}) || !reflect.DeepEqual(c.HelpContract.OutputModes, []string{"human", "json"}) || len(c.FlagContracts) != len(expectedCommandTuples) {
		return fmt.Errorf("command help contract mismatch")
	}
	for i, flags := range c.FlagContracts {
		if flags.Path != expectedCommandTuples[i].Path || !reflect.DeepEqual(flags.Flags, expectedCommandFlags[i]) {
			return fmt.Errorf("command flag contract mismatch at %d", i)
		}
	}
	if len(c.ListContracts) != 4 {
		return fmt.Errorf("retained-record pagination coverage mismatch")
	}
	wantRecords := []string{"publication", "delivery", "import", "verification"}
	for i, list := range c.ListContracts {
		if list.Record != wantRecords[i] || list.MaxPageSize != 100 || list.SnapshotToken != "required_after_first_page" {
			return fmt.Errorf("%s list contract is not snapshot bounded", list.Record)
		}
	}
	var p peer
	if err := decodeClosed(filepath.Join(dir, "peer.json"), &p); err != nil {
		return err
	}
	if p.SchemaVersion != "agent-dispatch.sync-provider.peer/v1" || len(p.Routes) != 2 {
		return fmt.Errorf("peer contract mismatch")
	}
	wantRoutes := []struct {
		path, success string
		status, max   int
		schema        string
	}{{"/v1/sync/nudges", "inbox_committed", 202, 262144, "agent-dispatch.sync-nudge/v1"}, {"/v1/sync/status", "fresh_status", 200, 16384, "agent-dispatch.sync-status-request/v1"}}
	for i, route := range p.Routes {
		want := wantRoutes[i]
		if len(route.RequestSelectedFields) != 0 || route.Method != "POST" || route.Path != want.path || route.Success != want.success || route.HTTPStatus != want.status || route.MaxBodyBytes != want.max || route.RequestSchema != want.schema {
			return fmt.Errorf("peer route %s is not the frozen descriptor", route.Path)
		}
	}
	a := p.Authentication
	if a.Transport != "tailscale_https" || a.Credential != "direction_specific_secret_reference" || !reflect.DeepEqual(a.Binds, []string{"group_id", "sender", "receiver"}) || !a.VerifyEndpointAndCertificate || a.TailscaleHeadersAuthorize || a.PublicBinding || a.FailureCode != "sync_trust_failed" || a.FailureHTTPStatus != 401 {
		return fmt.Errorf("peer authentication contract mismatch")
	}
	wantForbidden := []string{"credential", "executable", "force", "path", "profile", "ref", "remote"}
	sort.Strings(p.ForbiddenRequestFields)
	if strings.Join(p.ForbiddenRequestFields, "\n") != strings.Join(wantForbidden, "\n") {
		return fmt.Errorf("peer forbidden fields mismatch")
	}
	if err := verifyPeerSchemaLinks(dir, p); err != nil {
		return err
	}
	var r results
	if err := decodeClosed(filepath.Join(dir, "results.json"), &r); err != nil {
		return err
	}
	if r.SchemaVersion != "agent-dispatch.sync-provider.results/v1" || !exactResults(r) {
		return fmt.Errorf("provider result contract mismatch")
	}
	var e errorsContract
	if err := decodeClosed(filepath.Join(dir, "errors.json"), &e); err != nil {
		return err
	}
	if e.SchemaVersion != "agent-dispatch.sync-provider.errors/v1" || len(e.Errors) != 8 {
		return fmt.Errorf("provider error registry mismatch")
	}
	wantErrors := []struct {
		code, category string
		exit           int
	}{{"sync_group_not_found", "configuration", 3}, {"sync_capability_unavailable", "configuration", 3}, {"sync_contract_mismatch", "configuration", 3}, {"sync_payload_invalid", "input_rejected", 4}, {"sync_identity_obsolete", "input_rejected", 4}, {"sync_effect_unknown", "acceptance_unknown", 13}, {"sync_precondition_failed", "conflict", 14}, {"sync_trust_failed", "security", 30}}
	for i, entry := range e.Errors {
		want := wantErrors[i]
		if entry.Code != want.code || entry.Category != want.category || entry.ExitCode != want.exit {
			return fmt.Errorf("provider error registry drift at %d", i)
		}
	}
	var tf trustFixtures
	if err := decodeClosed(filepath.Join(dir, "trust-fixtures.json"), &tf); err != nil {
		return err
	}
	wantTrust := []struct{ name, code string }{{"membership-unpinned-administrator", "sync_trust_failed"}, {"membership-self-authorizing-root", "sync_trust_failed"}, {"membership-stale-predecessor", "sync_precondition_failed"}, {"verification-obsolete-incarnation", "sync_identity_obsolete"}}
	if tf.SchemaVersion != "agent-dispatch.sync-provider.trust-fixtures/v1" || len(tf.Fixtures) != len(wantTrust) || !strings.Contains(tf.Claim, "runtime signature verification is not implemented") {
		return fmt.Errorf("trust fixture contract mismatch")
	}
	for i, fixture := range tf.Fixtures {
		if fixture.Name != wantTrust[i].name || fixture.ExpectedCode != wantTrust[i].code {
			return fmt.Errorf("trust fixture drift at %d", i)
		}
	}
	if tf.Fixtures[0].DocumentKey == tf.Fixtures[0].PinnedKey || tf.Fixtures[0].PinnedKey == "" || tf.Fixtures[0].SelfAuthorizing || tf.Fixtures[1].PinnedKey != "" || !tf.Fixtures[1].SelfAuthorizing || tf.Fixtures[2].DocumentKey != tf.Fixtures[2].PinnedKey || tf.Fixtures[3].DocumentKey != tf.Fixtures[3].PinnedKey {
		return fmt.Errorf("trust fixture semantics drift")
	}
	return nil
}

func exactResults(r results) bool {
	want := results{SchemaVersion: "agent-dispatch.sync-provider.results/v1"}
	want.Unavailable = resultShape{ErrorCode: "sync_capability_unavailable", Category: "configuration", ExitCode: 3, SideEffects: []string{}}
	want.DisabledStatus = resultShape{OK: true, State: "disabled", Reason: "not_configured_or_disabled", SideEffects: []string{}}
	want.NudgeAccepted = resultShape{OK: true, State: "accepted", Proof: "inbox_committed", DoesNotProve: []string{"fetch", "import", "verification"}}
	want.PublicationConfirmed = resultShape{OK: true, State: "published", Proof: "remote_ref_matches_candidate", DoesNotProve: []string{"nudge_acceptance", "import", "verification"}}
	want.ImportCompleted = resultShape{OK: true, State: "applied", Proof: "journal_and_path_evidence_committed", DoesNotProve: []string{"historical_delivery", "verification"}}
	want.VerificationCompleted = resultShape{OK: true, State: "complete", Proof: "fresh_two_node_evidence", DoesNotProve: []string{}}
	return reflect.DeepEqual(r, want)
}

func verifyPeerSchemaLinks(dir string, p peer) error {
	targets := map[string]struct{ path, id string }{"agent-dispatch.sync-nudge/v1": {"../../schemas/sync-nudge.schema.json", "urn:agent-dispatch:schema:sync-nudge:v1"}, "agent-dispatch.sync-status-request/v1": {"../../schemas/sync-status-request.schema.json", "urn:agent-dispatch:schema:sync-status-request:v1"}}
	for _, route := range p.Routes {
		target, ok := targets[route.RequestSchema]
		var schema struct {
			ID         string `json:"$id"`
			Properties map[string]struct {
				Const string `json:"const"`
			} `json:"properties"`
		}
		if !ok {
			return fmt.Errorf("peer schema link is not bundled: %s", route.RequestSchema)
		}
		raw, err := os.ReadFile(filepath.Join(dir, target.path))
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			return err
		}
		if schema.ID != target.id || schema.Properties["schema_version"].Const != route.RequestSchema {
			return fmt.Errorf("peer schema link identity mismatch: %s", route.RequestSchema)
		}
	}
	return nil
}

func decodeClosed(path string, dst any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("%s: trailing JSON", path)
	}
	return nil
}

func verifySums(dir string, artifacts []string) error {
	f, err := os.Open(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		return err
	}
	defer f.Close()
	sums := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			return fmt.Errorf("malformed checksum line")
		}
		if _, duplicate := sums[fields[1]]; duplicate {
			return fmt.Errorf("duplicate checksum entry for %s", fields[1])
		}
		sums[fields[1]] = fields[0]
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(sums) != len(artifacts) {
		return fmt.Errorf("checksum coverage mismatch")
	}
	docsRoot, err := filepath.Abs(filepath.Join(dir, "../.."))
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, rel := range artifacts {
		if filepath.IsAbs(rel) || strings.Contains(rel, "\\") || seen[rel] {
			return fmt.Errorf("unsafe artifact path %q", rel)
		}
		seen[rel] = true
		path, err := filepath.Abs(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		if path != docsRoot && !strings.HasPrefix(path, docsRoot+string(os.PathSeparator)) {
			return fmt.Errorf("artifact path escapes docs root: %s", rel)
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("artifact is not a regular file: %s", rel)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		actual := sha256.Sum256(data)
		if sums[rel] != hex.EncodeToString(actual[:]) {
			return fmt.Errorf("checksum drift for %s", rel)
		}
	}
	return nil
}
