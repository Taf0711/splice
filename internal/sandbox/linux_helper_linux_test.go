//go:build linux

package sandbox

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestLinuxSandboxInnerStageAppliesNetworkDenySeccomp(t *testing.T) {
	originalNetworkDeny := applyLinuxNetworkDenyFilter
	originalUnixBlock := applyUnixSocketBlockFilter
	t.Cleanup(func() {
		applyLinuxNetworkDenyFilter = originalNetworkDeny
		applyUnixSocketBlockFilter = originalUnixBlock
	})

	networkDenyCalls := 0
	unixBlockCalls := 0
	applyLinuxNetworkDenyFilter = func() error {
		networkDenyCalls++
		return nil
	}
	applyUnixSocketBlockFilter = func() error {
		unixBlockCalls++
		return nil
	}

	var stderr bytes.Buffer
	code := runLinuxSandboxInnerStage(LinuxSandboxHelperConfig{
		PermissionProfile: PermissionProfile{Network: NetworkPolicy{Mode: NetworkDeny}},
		BlockUnixSockets:  true,
		Command:           []string{"definitely-not-a-real-splice-test-command"},
	}, &stderr)

	if code != 127 {
		t.Fatalf("exit code = %d, want lookup failure 127 after filters; stderr=%s", code, stderr.String())
	}
	if networkDenyCalls != 1 {
		t.Fatalf("network deny filter calls = %d, want 1", networkDenyCalls)
	}
	if unixBlockCalls != 1 {
		t.Fatalf("unix socket filter calls = %d, want 1", unixBlockCalls)
	}
}

func TestLinuxSandboxInnerStageSkipsNetworkDenySeccompWhenAllowed(t *testing.T) {
	originalNetworkDeny := applyLinuxNetworkDenyFilter
	t.Cleanup(func() {
		applyLinuxNetworkDenyFilter = originalNetworkDeny
	})

	applyLinuxNetworkDenyFilter = func() error {
		return errors.New("network deny filter should not run")
	}

	var stderr bytes.Buffer
	code := runLinuxSandboxInnerStage(LinuxSandboxHelperConfig{
		PermissionProfile: PermissionProfile{Network: NetworkPolicy{Mode: NetworkAllow}},
		Command:           []string{"definitely-not-a-real-splice-test-command"},
	}, &stderr)

	if code != 127 {
		t.Fatalf("exit code = %d, want lookup failure 127 without network filter; stderr=%s", code, stderr.String())
	}
}

func TestLinuxSandboxInnerStageFailsClosedOnUnixSocketBlockError(t *testing.T) {
	originalUnixBlock := applyUnixSocketBlockFilter
	t.Cleanup(func() {
		applyUnixSocketBlockFilter = originalUnixBlock
	})

	applyUnixSocketBlockFilter = func() error {
		return errors.New("seccomp install failed")
	}

	var stderr bytes.Buffer
	code := runLinuxSandboxInnerStage(LinuxSandboxHelperConfig{
		PermissionProfile: PermissionProfile{Network: NetworkPolicy{Mode: NetworkAllow}},
		BlockUnixSockets:  true,
		Command:           []string{"definitely-not-a-real-splice-test-command"},
	}, &stderr)

	if code != 125 {
		t.Fatalf("exit code = %d, want 125 on unix-socket filter failure; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "seccomp install failed") {
		t.Fatalf("stderr = %q, want seccomp error message", stderr.String())
	}
}
