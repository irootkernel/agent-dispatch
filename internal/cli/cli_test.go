package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	buildversion "github.com/irootkernel/agent-dispatch/internal/version"
)

func TestVersionJSONSummary(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"version", "--json"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit code %d, stderr: %s", code, errb.String())
	}
	if errb.Len() != 0 {
		t.Fatalf("diagnostics leaked to stderr: %q", errb.String())
	}
	var summary map[string]string
	if err := json.Unmarshal(out.Bytes(), &summary); err != nil {
		t.Fatalf("stdout is not valid JSON (%v): %s", err, out.String())
	}
	if len(summary) != 2 || summary["name"] != "agent-dispatch" || summary["version"] != "v0.1.7" {
		t.Fatalf("unexpected version summary: %#v", summary)
	}
	if got := out.String(); got != "{\"name\":\"agent-dispatch\",\"version\":\"v0.1.7\"}\n" {
		t.Fatalf("version JSON bytes = %q", got)
	}
}

func TestVersionHumanOutputIsNotJSON(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"version"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit code %d, stderr: %s", code, errb.String())
	}
	if json.Valid(out.Bytes()) {
		t.Errorf("human output must not be a JSON document: %q", out.String())
	}
	if got := out.String(); got != "agent-dispatch v0.1.7\n" {
		t.Errorf("human output = %q", got)
	}
}

func TestVersionNormalizesLeadingV(t *testing.T) {
	original := buildversion.Version
	t.Cleanup(func() { buildversion.Version = original })

	for _, value := range []string{"0.1.7", "v0.1.7"} {
		t.Run(value, func(t *testing.T) {
			buildversion.Version = value
			var out, errb bytes.Buffer
			if code := Run([]string{"version"}, &out, &errb); code != 0 || out.String() != "agent-dispatch v0.1.7\n" || errb.Len() != 0 {
				t.Fatalf("human version = %q, exit %d, stderr %s", out.String(), code, errb.String())
			}
			out.Reset()
			errb.Reset()
			if code := Run([]string{"version", "--json"}, &out, &errb); code != 0 || out.String() != "{\"name\":\"agent-dispatch\",\"version\":\"v0.1.7\"}\n" || errb.Len() != 0 {
				t.Fatalf("JSON version = %q, exit %d, stderr %s", out.String(), code, errb.String())
			}
		})
	}
}

func TestUnknownCommandFailsClosed(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"definitely-not-a-command"}, &out, &errb)
	if code == 0 {
		t.Fatal("unknown command must not exit 0")
	}
	if out.Len() != 0 {
		t.Errorf("failed command must not write to stdout: %q", out.String())
	}
	var env ErrorEnvelope
	if err := json.Unmarshal(errb.Bytes(), &env); err != nil {
		t.Fatalf("stderr is not the error envelope (%v): %s", err, errb.String())
	}
	if env.OK || env.Error.Code != "command_unknown" {
		t.Errorf("unexpected error envelope: %+v", env)
	}
}

func TestEveryRegisteredCommandIsImplemented(t *testing.T) {
	// E6-T3 completes the v0.1 tree: every registered command must
	// reach its own run function. Each command runs in a fresh
	// sandboxed home so a bare `init` can neither touch the
	// developer's configuration nor change what the next bare command
	// sees.
	for name := range knownCommands {
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", "")
			t.Setenv("XDG_STATE_HOME", "")
			t.Setenv("AGENT_DISPATCH_CONFIG", "")
			t.Setenv("AGENT_DISPATCH_STATE_DIR", "")
			var out, errb bytes.Buffer
			code := Run([]string{name}, &out, &errb)
			_ = out
			// version prints its result and a sandboxed bare init
			// legitimately writes the example configuration; doctor
			// now emits both the findings log on stderr and the
			// stable error envelope; every bare invocation must be
			// its own usage or configuration failure — never
			// command_not_implemented or command_unknown.
			if code == 0 {
				if name != "version" && name != "init" {
					t.Errorf("bare invocation unexpectedly succeeded")
				}
				return
			}
			if code != 2 && code != 3 {
				t.Errorf("bare invocation exits %d, want its own usage (2) or configuration (3) failure", code)
				return
			}
			// doctor's stderr carries the structured log lines
			// before the error envelope; decode the last
			// api_version-prefixed line.
			envBytes := errb.Bytes()
			if name == "doctor" {
				lines := strings.Split(strings.TrimRight(errb.String(), "\n"), "\n")
				for i := len(lines) - 1; i >= 0; i-- {
					if strings.HasPrefix(lines[i], "{\"api_version\":") {
						envBytes = []byte(lines[i])
						break
					}
				}
			}
			var env ErrorEnvelope
			if err := json.Unmarshal(envBytes, &env); err != nil {
				t.Fatalf("stderr is not the error envelope: %s", errb.String())
			}
			if env.Error.Code == "command_not_implemented" || env.Error.Code == "command_unknown" {
				t.Errorf("fell through to the default classification (%s)", env.Error.Code)
			}
		})
	}
}

func TestUnrecognizedCommandNameIsCommandUnknown(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"frobnicate"}, &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	var env ErrorEnvelope
	if err := json.Unmarshal(errb.Bytes(), &env); err != nil {
		t.Fatalf("stderr is not the error envelope: %s", errb.String())
	}
	if env.Error.Code != "command_unknown" {
		t.Errorf("error code = %q, want command_unknown", env.Error.Code)
	}
}

func TestNoArgumentsUsageError(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run(nil, &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	var env ErrorEnvelope
	if err := json.Unmarshal(errb.Bytes(), &env); err != nil {
		t.Fatalf("stderr is not the error envelope: %s", errb.String())
	}
}

func TestVersionUsageErrors(t *testing.T) {
	cases := [][]string{
		{"version", "--output"},
		{"version", "--output", "json"},
		{"version", "--output=json"},
		{"version", "-o", "json"},
		{"version", "--output", "yaml"},
		{"version", "--json", "--json"},
		{"version", "--bogus"},
	}
	for _, args := range cases {
		var out, errb bytes.Buffer
		if code := Run(args, &out, &errb); code != 2 {
			t.Errorf("%v: exit = %d, want 2", args, code)
		}
		if out.Len() != 0 {
			t.Errorf("%v: stdout = %q, want empty", args, out.String())
		}
		var env ErrorEnvelope
		if err := json.Unmarshal(errb.Bytes(), &env); err != nil || env.Error.Code != "flag_invalid" {
			t.Errorf("%v: unexpected envelope %v (%s)", args, err, errb.String())
		}
	}
}

func TestWriteEnvelopeFailureExits40(t *testing.T) {
	code := writeEnvelope(alwaysFailWriter{}, "version", map[string]any{})
	if code != 40 {
		t.Errorf("envelope write failure exit = %d, want 40", code)
	}
}

type alwaysFailWriter struct{}

func (alwaysFailWriter) Write([]byte) (int, error) { return 0, errWriteFailed }

var errWriteFailed = errors.New("write failed")
