package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestVersionJSONEnvelope(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"version", "--output", "json"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit code %d, stderr: %s", code, errb.String())
	}
	if errb.Len() != 0 {
		t.Fatalf("diagnostics leaked to stderr: %q", errb.String())
	}
	var env struct {
		APIVersion string `json:"api_version"`
		Command    string `json:"command"`
		OK         bool   `json:"ok"`
		Result     struct {
			Version       string            `json:"version"`
			Commit        string            `json:"commit"`
			BuildTime     string            `json:"build_time"`
			ConfigVersion string            `json:"config_version"`
			SchemaRange   string            `json:"schema_range"`
			Adapters      map[string]string `json:"adapter_versions"`
		} `json:"result"`
		Warnings []string `json:"warnings"`
		TraceID  string   `json:"trace_id"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not valid JSON (%v): %s", err, out.String())
	}
	if env.APIVersion != "jjukkumi.cli/v1" {
		t.Errorf("api_version = %q", env.APIVersion)
	}
	if env.Command != "version" || !env.OK {
		t.Errorf("command/ok = %q/%v", env.Command, env.OK)
	}
	if env.Result.Version == "" || env.Result.Commit == "" || env.Result.BuildTime == "" {
		t.Errorf("missing build metadata: %+v", env.Result)
	}
	if env.Result.ConfigVersion == "" || env.Result.SchemaRange == "" {
		t.Errorf("missing config/schema metadata: %+v", env.Result)
	}
	if len(env.Result.Adapters) == 0 {
		t.Error("adapter_versions is empty")
	}
	if env.Warnings == nil {
		t.Error("warnings must serialize as [], not null")
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
	if !strings.Contains(out.String(), "jjukkumi") {
		t.Errorf("human output missing program name: %q", out.String())
	}
}

func TestUnknownCommandFailsClosed(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"dispatch"}, &out, &errb)
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
	if env.OK || env.Error.Code != "command_not_implemented" {
		t.Errorf("unexpected error envelope: %+v", env)
	}
}

func TestEveryKnownCommandClassifiesAsNotImplemented(t *testing.T) {
	for name := range knownCommands {
		if name == "version" {
			continue // the one implemented command
		}
		var out, errb bytes.Buffer
		if code := Run([]string{name}, &out, &errb); code != 2 {
			t.Errorf("%s: exit code = %d, want 2", name, code)
		}
		var env ErrorEnvelope
		if err := json.Unmarshal(errb.Bytes(), &env); err != nil {
			t.Fatalf("%s: stderr is not the error envelope: %s", name, errb.String())
		}
		if env.Error.Code != "command_not_implemented" {
			t.Errorf("%s: error code = %q, want command_not_implemented", name, env.Error.Code)
		}
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
