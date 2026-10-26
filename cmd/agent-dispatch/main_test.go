package main

import (
	"os"
	"os/exec"
	"testing"
)

func TestMainClosedOutputPipeExitsInternalError(t *testing.T) {
	if os.Getenv("AGENT_DISPATCH_TEST_CLOSED_OUTPUT") == "1" {
		os.Args = []string{"agent-dispatch", "version", "--json"}
		main()
		return
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	reader.Close()
	defer writer.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestMainClosedOutputPipeExitsInternalError$")
	cmd.Env = append(os.Environ(), "AGENT_DISPATCH_TEST_CLOSED_OUTPUT=1")
	cmd.Stdout = writer
	err = cmd.Run()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 40 {
		t.Fatalf("closed output must exit 40, got %v", err)
	}
}
