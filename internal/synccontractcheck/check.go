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
	"commands.json", "peer.json", "results.json", "errors.json", "trust-fixtures.json", "digest-vectors.json",
	"../../schemas/sync-membership.schema.json", "../../schemas/sync-membership-plan.schema.json", "../../schemas/sync-checkpoint.schema.json", "../../schemas/sync-checkpoint-plan.schema.json", "../../schemas/sync-import-acknowledgement.schema.json", "../../schemas/sync-publication.schema.json", "../../schemas/sync-delivery.schema.json", "../../schemas/sync-import.schema.json", "../../schemas/sync-control.schema.json", "../../schemas/sync-verification.schema.json", "../../schemas/sync-nudge.schema.json", "../../schemas/sync-status-request.schema.json", "../../schemas/sync-status-response.schema.json",
	"../../examples/sync-checkpoint-plan.json", "../../examples/sync-checkpoint.json", "../../examples/sync-control.json", "../../examples/sync-delivery.json", "../../examples/sync-import-acknowledgement.json", "../../examples/sync-import.json", "../../examples/sync-membership-plan.json", "../../examples/sync-membership.json", "../../examples/sync-nudge.json", "../../examples/sync-publication.json", "../../examples/sync-status-request.json", "../../examples/sync-status-response.json", "../../examples/sync-verification.json", "../../examples/sync-verification-collecting.json",
	"../../examples/invalid/sync-checkpoint-malformed-oid.json", "../../examples/invalid/sync-control-invalid-state.json", "../../examples/invalid/sync-delivery-unknown-without-retention.json", "../../examples/invalid/sync-delivery-contradictory-reason.json", "../../examples/invalid/sync-import-duplicate-alias.json", "../../examples/invalid/sync-import-contradictory-reason.json", "../../examples/invalid/sync-import-empty-target.json", "../../examples/invalid/sync-import-sensitive-alias.json", "../../examples/invalid/sync-import-unsafe-path.json", "../../examples/invalid/sync-import-unicode-alias.json", "../../examples/invalid/sync-membership-administrator-key-reused.json", "../../examples/invalid/sync-membership-duplicate-instance.json", "../../examples/invalid/sync-membership-duplicate-publisher-key.json", "../../examples/invalid/sync-membership-invalid-ref.json", "../../examples/invalid/sync-membership-plan-stale-predecessor.json", "../../examples/invalid/sync-membership-plan-missing-predecessor.json", "../../examples/invalid/sync-membership-public-endpoint.json", "../../examples/invalid/sync-membership-third-active.json", "../../examples/invalid/sync-nudge-invalid-ref.json", "../../examples/invalid/sync-publication-commit-mismatch.json", "../../examples/invalid/sync-publication-contradictory-reason.json", "../../examples/invalid/sync-publication-missing-proof.json", "../../examples/invalid/sync-publication-unresolved-prunable.json", "../../examples/invalid/sync-verification-duplicate-node.json", "../../examples/invalid/sync-verification-duplicate-incarnation.json", "../../examples/invalid/sync-verification-empty-pair.json", "../../examples/invalid/sync-verification-incomplete-pair.json", "../../examples/invalid/sync-verification-false-complete.json", "../../examples/invalid/sync-verification-false-freshness.json", "../../examples/invalid/sync-verification-obsolete-incarnation.json",
}

// expectedArtifactSetDigest is the independently reviewed golden over each
// allowlisted path and its bytes in bundle order. SHA256SUMS supports ordinary
// corruption detection; this pin ensures the checksum regeneration command
// cannot silently bless coordinated schema/fixture drift.
const expectedArtifactSetDigest = "75c29977d43c20c9e369c9bfca4b09bc247e2a606b90354ab6cb2f285d0305e9"

type bundle struct {
	SchemaVersion  string   `json:"schema_version"`
	BundleID       string   `json:"bundle_id"`
	ContractDigest string   `json:"contract_digest"`
	Artifacts      []string `json:"artifacts"`
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
	{"sync capabilities", "contract_read", "implemented", "none"}, {"sync status", "status_read", "implemented", "none"},
	{"sync publish", "publication", "implemented", "git_write"}, {"sync reconcile", "reconciliation", "implemented", "git_read_write"}, {"sync verify", "pair_verification", "implemented", "network_read_and_state_write"}, {"sync serve", "peer_service", "implemented", "listener"}, {"sync pause", "control", "implemented", "state_write"}, {"sync resume", "control", "implemented", "state_write"},
	{"sync membership plan", "membership_plan", "implemented", "none"}, {"sync membership apply", "membership_apply", "implemented", "git_write"}, {"sync checkpoint plan", "checkpoint_plan", "implemented", "network_read"}, {"sync checkpoint apply", "checkpoint_apply", "implemented", "git_write"},
	{"sync service render", "service_render", "implemented", "none"}, {"sync service install", "service_install", "implemented", "service_write"}, {"sync service inspect", "service_inspect", "implemented", "none"}, {"sync service stop", "service_stop", "implemented", "service_write"}, {"sync service disable", "service_disable", "implemented", "service_write"}, {"sync service uninstall", "service_uninstall", "implemented", "service_write"},
}

var expectedCommandFlags = [][]string{
	{"--output"}, {"--config", "--group", "--output"}, {"--group", "--expected-config-revision", "--output"}, {"--group", "--output"}, {"--group", "--output"}, {"--group", "--config", "--managed"}, {"--group", "--expected-control-revision", "--output"}, {"--group", "--expected-control-revision", "--output"},
	{"--group", "--change", "--instance", "--output"}, {"--group", "--plan", "--expected-membership-predecessor", "--output"}, {"--group", "--target-commit", "--kind", "--output"}, {"--group", "--plan", "--output"},
	{"--group", "--config", "--output"}, {"--group", "--config", "--output"}, {"--group", "--config", "--output"}, {"--group", "--config", "--output"}, {"--group", "--config", "--output"}, {"--group", "--config", "--output"},
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
		ResponseSchema        string   `json:"response_schema,omitempty"`
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
	Projection            string      `json:"projection"`
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
		Name                    string `json:"name"`
		DocumentKey             string `json:"document_key"`
		PinnedKey               string `json:"pinned_key"`
		SelfAuthorizing         bool   `json:"self_authorizing"`
		PredecessorMatches      bool   `json:"predecessor_matches"`
		StateIncarnationCurrent bool   `json:"state_incarnation_current"`
		ExpectedCode            string `json:"expected_code"`
	} `json:"fixtures"`
	Claim string `json:"claim"`
}

type digestVectors struct {
	SchemaVersion string `json:"schema_version"`
	Vectors       []struct {
		Name           string `json:"name"`
		Domain         string `json:"domain"`
		CanonicalJSON  string `json:"canonical_json"`
		ExpectedDigest string `json:"expected_digest"`
	} `json:"vectors"`
}

func Check(dir string) error {
	var b bundle
	if err := decodeClosed(filepath.Join(dir, "bundle.json"), &b); err != nil {
		return err
	}
	if b.SchemaVersion != "agent-dispatch.sync-provider-bundle/v1" || b.BundleID != "agent-dispatch-sync-provider-v1" || b.ContractDigest != "sha256:30cf47b1bd854a0271aa9df3e7b37f0cc14cdd86d3f06a65a2cb787c6741131b" {
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
	if !reflect.DeepEqual(c.HelpContract.RequiredSections, []string{"summary", "usage", "key_flags", "defaults", "approval_requirements", "exit_codes", "side_effects", "examples", "next_safe_command"}) || !reflect.DeepEqual(c.HelpContract.OutputModes, []string{"json"}) || len(c.FlagContracts) != len(expectedCommandTuples) {
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
		path, success     string
		status, max       int
		request, response string
	}{{"/v1/sync/nudges", "inbox_committed", 202, 262144, "agent-dispatch.sync-nudge/v1", ""}, {"/v1/sync/status", "fresh_status", 200, 16384, "agent-dispatch.sync-status-request/v1", "agent-dispatch.sync-status-response/v1"}}
	for i, route := range p.Routes {
		want := wantRoutes[i]
		if len(route.RequestSelectedFields) != 0 || route.Method != "POST" || route.Path != want.path || route.Success != want.success || route.HTTPStatus != want.status || route.MaxBodyBytes != want.max || route.RequestSchema != want.request || route.ResponseSchema != want.response {
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
	if e.SchemaVersion != "agent-dispatch.sync-provider.errors/v1" || len(e.Errors) != 10 {
		return fmt.Errorf("provider error registry mismatch")
	}
	wantErrors := []struct {
		code, category string
		exit           int
	}{{"sync_group_not_found", "configuration", 3}, {"sync_capability_unavailable", "configuration", 3}, {"sync_contract_mismatch", "configuration", 3}, {"sync_payload_invalid", "input_rejected", 4}, {"sync_identity_obsolete", "input_rejected", 4}, {"sync_retryable", "transient_local", 10}, {"sync_service_io_failed", "storage", 20}, {"sync_effect_unknown", "acceptance_unknown", 13}, {"sync_precondition_failed", "conflict", 14}, {"sync_trust_failed", "security", 30}}
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
	if tf.SchemaVersion != "agent-dispatch.sync-provider.trust-fixtures/v1" || len(tf.Fixtures) != len(wantTrust) || !strings.Contains(tf.Claim, "runtime signature and linear-history verification") {
		return fmt.Errorf("trust fixture contract mismatch")
	}
	for i, fixture := range tf.Fixtures {
		if fixture.Name != wantTrust[i].name || fixture.ExpectedCode != wantTrust[i].code {
			return fmt.Errorf("trust fixture drift at %d", i)
		}
		if got := evaluateTrustFixture(fixture.DocumentKey, fixture.PinnedKey, fixture.SelfAuthorizing, fixture.PredecessorMatches, fixture.StateIncarnationCurrent); got != fixture.ExpectedCode {
			return fmt.Errorf("trust fixture %q evaluates to %q, want %q", fixture.Name, got, fixture.ExpectedCode)
		}
	}
	var dv digestVectors
	if err := decodeClosed(filepath.Join(dir, "digest-vectors.json"), &dv); err != nil {
		return err
	}
	if dv.SchemaVersion != "agent-dispatch.sync-provider.digest-vectors/v1" || len(dv.Vectors) != 1 {
		return fmt.Errorf("digest vector contract mismatch")
	}
	vector := dv.Vectors[0]
	if vector.Name != "canonical-json-escaping-and-order" || vector.Domain != "agent-dispatch.sync-canonical-json-test/v1" {
		return fmt.Errorf("canonical JSON digest vector identity mismatch")
	}
	var canonicalValue any
	if err := json.Unmarshal([]byte(vector.CanonicalJSON), &canonicalValue); err != nil {
		return fmt.Errorf("canonical JSON digest vector is invalid: %w", err)
	}
	encoded, err := json.Marshal(canonicalValue)
	if err != nil || string(encoded) != vector.CanonicalJSON {
		return fmt.Errorf("canonical JSON escaping or ordering drift")
	}
	h := sha256.New()
	_, _ = h.Write([]byte(vector.Domain))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(vector.CanonicalJSON))
	_, _ = h.Write([]byte{'\n'})
	if got := "sha256:" + hex.EncodeToString(h.Sum(nil)); got != vector.ExpectedDigest {
		return fmt.Errorf("canonical JSON digest vector mismatch: got %s", got)
	}
	return verifyArtifactSetGolden(dir, b.Artifacts)
}

func verifyArtifactSetGolden(dir string, artifacts []string) error {
	h := sha256.New()
	for _, rel := range artifacts {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(data)
		_, _ = h.Write([]byte{0})
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != expectedArtifactSetDigest {
		return fmt.Errorf("artifact-set golden drift: got %s", got)
	}
	return nil
}

func evaluateTrustFixture(documentKey, pinnedKey string, selfAuthorizing, predecessorMatches, stateIncarnationCurrent bool) string {
	switch {
	case pinnedKey == "", documentKey != pinnedKey, selfAuthorizing:
		return "sync_trust_failed"
	case !predecessorMatches:
		return "sync_precondition_failed"
	case !stateIncarnationCurrent:
		return "sync_identity_obsolete"
	default:
		return ""
	}
}

func exactResults(r results) bool {
	want := results{SchemaVersion: "agent-dispatch.sync-provider.results/v1", Projection: "semantic_outcome_fragment"}
	want.Unavailable = resultShape{ErrorCode: "sync_capability_unavailable", Category: "configuration", ExitCode: 3, SideEffects: []string{}}
	want.DisabledStatus = resultShape{OK: true, State: "disabled", Reason: "not_configured_or_disabled", SideEffects: []string{}}
	want.NudgeAccepted = resultShape{OK: true, State: "accepted", Proof: "inbox_committed", DoesNotProve: []string{"fetch", "import", "verification"}}
	want.PublicationConfirmed = resultShape{OK: true, State: "published", Proof: "remote_ref_matches_candidate", DoesNotProve: []string{"nudge_acceptance", "import", "verification"}}
	want.ImportCompleted = resultShape{OK: true, State: "applied", Proof: "journal_and_path_evidence_committed", DoesNotProve: []string{"historical_delivery", "verification"}}
	want.VerificationCompleted = resultShape{OK: true, State: "complete", Proof: "fresh_two_node_evidence", DoesNotProve: []string{"historical_delivery"}}
	return reflect.DeepEqual(r, want)
}

func verifyPeerSchemaLinks(dir string, p peer) error {
	targets := map[string]struct {
		path, id   string
		properties []string
	}{
		"agent-dispatch.sync-nudge/v1":           {"../../schemas/sync-nudge.schema.json", "urn:agent-dispatch:schema:sync-nudge:v1", []string{"content_ref", "group_id", "membership_revision", "publication_id", "receiver", "schema_version", "sender", "target_commit"}},
		"agent-dispatch.sync-status-request/v1":  {"../../schemas/sync-status-request.schema.json", "urn:agent-dispatch:schema:sync-status-request:v1", []string{"content_ref", "contract_digest", "group_id", "membership_revision", "nonce", "receiver", "schema_version", "scope_digest", "sender", "target_commit"}},
		"agent-dispatch.sync-status-response/v1": {"../../schemas/sync-status-response.schema.json", "urn:agent-dispatch:schema:sync-status-response:v1", []string{"content_ref", "contract_digest", "evidence_age_seconds", "evidence_generation", "governed_dirty", "group_id", "membership_current", "membership_revision", "nonce", "pending_work", "responder", "schema_version", "scope_digest", "state", "state_incarnation_id", "target_commit", "uncertain"}},
	}
	verify := func(schemaVersion string) error {
		target, ok := targets[schemaVersion]
		if !ok {
			return fmt.Errorf("peer schema link is not bundled: %s", schemaVersion)
		}
		var schema struct {
			ID                   string                     `json:"$id"`
			AdditionalProperties bool                       `json:"additionalProperties"`
			Required             []string                   `json:"required"`
			Properties           map[string]json.RawMessage `json:"properties"`
		}
		raw, err := os.ReadFile(filepath.Join(dir, target.path))
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			return err
		}
		keys := make([]string, 0, len(schema.Properties))
		for key := range schema.Properties {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		required := append([]string(nil), schema.Required...)
		sort.Strings(required)
		if schema.ID != target.id {
			return fmt.Errorf("peer schema link identity mismatch: %s", schemaVersion)
		}
		var version struct {
			Const string `json:"const"`
		}
		if err := json.Unmarshal(schema.Properties["schema_version"], &version); err != nil || version.Const != schemaVersion {
			return fmt.Errorf("peer schema link identity mismatch: %s", schemaVersion)
		}
		if schema.AdditionalProperties || !reflect.DeepEqual(keys, target.properties) || !reflect.DeepEqual(required, target.properties) {
			return fmt.Errorf("peer schema shape mismatch: %s", schemaVersion)
		}
		for _, forbidden := range p.ForbiddenRequestFields {
			if _, present := schema.Properties[forbidden]; present {
				return fmt.Errorf("peer schema %s admits forbidden field %s", schemaVersion, forbidden)
			}
		}
		return nil
	}
	for _, route := range p.Routes {
		if err := verify(route.RequestSchema); err != nil {
			return err
		}
		if route.ResponseSchema != "" {
			if err := verify(route.ResponseSchema); err != nil {
				return err
			}
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

// UpdateChecksums deterministically rewrites SHA256SUMS in bundle order after
// validating that every artifact is a regular file contained by docs/.
func UpdateChecksums(dir string) error {
	var b bundle
	if err := decodeClosed(filepath.Join(dir, "bundle.json"), &b); err != nil {
		return err
	}
	if !reflect.DeepEqual(b.Artifacts, expectedArtifacts) {
		return fmt.Errorf("bundle artifact allowlist mismatch")
	}
	docsRoot, err := filepath.Abs(filepath.Join(dir, "../.."))
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	var output strings.Builder
	for _, rel := range b.Artifacts {
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
		sum := sha256.Sum256(data)
		fmt.Fprintf(&output, "%s  %s\n", hex.EncodeToString(sum[:]), rel)
	}
	temporary := filepath.Join(dir, ".SHA256SUMS.tmp")
	if err := os.WriteFile(temporary, []byte(output.String()), 0o644); err != nil {
		return err
	}
	if err := os.Rename(temporary, filepath.Join(dir, "SHA256SUMS")); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}
