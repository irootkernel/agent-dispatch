package hermeskanban

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/observability"
	"github.com/irootkernel/agent-dispatch/internal/ports"
	"github.com/irootkernel/agent-dispatch/internal/testsupport/stubhermes"
)

// TestE9T3MutexSuppressionReported proves the E9-T3/T3-F007 contract at
// the renderer: dropping a configured mutex key the target cannot honor
// is reported through RenderedTask.SuppressedMutex — never silent — and
// the flag stays clear when the key was honored or never configured.
func TestE9T3MutexSuppressionReported(t *testing.T) {
	req := loadGoldenRequest(t)
	if req.Assignment == nil || req.Assignment.MutexKey == "" {
		t.Fatal("golden request must carry a mutex key for this test")
	}
	supported, err := Render(req, RenderOptions{MaxManifestBytes: 1 << 20, ResourceMutexSupported: true})
	if err != nil {
		t.Fatal(err)
	}
	if supported.SuppressedMutex {
		t.Fatal("a target that honors resource_mutex has nothing to suppress")
	}
	unsupported, err := Render(req, RenderOptions{MaxManifestBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if unsupported.CreateOptions.MutexKey != "" || !unsupported.SuppressedMutex {
		t.Fatal("dropping the configured mutex key must leave the flag clear on the options and set on the report")
	}
	noMutex := req
	noMutex.Assignment = nil
	absent, err := Render(noMutex, RenderOptions{MaxManifestBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if absent.SuppressedMutex {
		t.Fatal("no configured mutex key means no suppression to report")
	}
}

// TestE9T3SinkWarnsOnMutexSuppression proves the E9-T3/T3-F007 contract
// at the sink: a submission that drops the mutex key emits exactly one
// dispatch.mutex_suppressed warning carrying the causal correlation
// (trace, dispatch, route, target) while the submission itself still
// succeeds.
func TestE9T3SinkWarnsOnMutexSuppression(t *testing.T) {
	sink, err := NewSink("hermes-main", stubhermes.Write(t), "", "agent-dispatch-test", ProcessLimits{SubmitTimeout: 30 * time.Second, LookupTimeout: 30 * time.Second}, 262144)
	if err != nil {
		t.Fatalf("sink: %v", err)
	}
	// NewSink binds the frozen 0.19.1 interface's resource_mutex support
	// (E11-T1 interim truth source); this test drives the suppression
	// path itself, so it simulates the capability the E11-T2 probe will
	// record per executable.
	sink.renderOpts.ResourceMutexSupported = false
	if _, err := sink.Probe(context.Background()); err != nil {
		t.Fatalf("probe: %v", err)
	}
	var logBuf bytes.Buffer
	sink.Log = observability.New(&logBuf, observability.LevelWarn, observability.PathsRelative)
	sink.TraceID = "trace-e9t3"
	req := goldenRequestMut(t, nil)
	res, err := sink.Submit(context.Background(), req)
	if err != nil || res.Classification != ports.SubmitAccepted {
		t.Fatalf("the suppression is a warning, not a failure: %+v %v", res, err)
	}
	var warned bool
	sc := bufio.NewScanner(&logBuf)
	for sc.Scan() {
		var line map[string]any
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			t.Fatalf("every log line is JSON: %q", sc.Text())
		}
		if line["event"] != "dispatch.mutex_suppressed" {
			continue
		}
		if warned {
			t.Fatal("the suppression must be warned exactly once")
		}
		warned = true
		if line["level"] != "warn" {
			t.Fatalf("the suppression is a warning, got level %v", line["level"])
		}
		if line["trace_id"] != "trace-e9t3" || line["dispatch_id"] != req.DispatchID {
			t.Fatalf("the warning must carry the causal correlation: %v", line)
		}
		if line["route_id"] != req.Route.ID || line["target_id"] != "hermes-main" {
			t.Fatalf("the warning must name the route and target: %v", line)
		}
	}
	if !warned {
		t.Fatal("the dropped mutex key must emit dispatch.mutex_suppressed")
	}
}
