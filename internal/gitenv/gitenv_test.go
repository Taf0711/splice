package gitenv

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestEnvSetsSafeSettings(t *testing.T) {
	env, err := Env([]string{"PATH=/bin", "HOME=/h"})
	if err != nil {
		t.Fatalf("Env() error = %v", err)
	}
	want := []string{
		"PATH=/bin", "HOME=/h",
		"GIT_CONFIG_KEY_0=safe.bareRepository", "GIT_CONFIG_VALUE_0=explicit",
		"GIT_CONFIG_KEY_1=core.fsmonitor", "GIT_CONFIG_VALUE_1=false",
		"GIT_CONFIG_COUNT=2",
	}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("env = %#v\nwant %#v", env, want)
	}
}

func TestEnvAppendsAfterCallerSettings(t *testing.T) {
	env, err := Env([]string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=user.name",
		"GIT_CONFIG_VALUE_0=someone",
	})
	if err != nil {
		t.Fatalf("Env() error = %v", err)
	}
	counts := 0
	for _, entry := range env {
		if strings.HasPrefix(entry, "GIT_CONFIG_COUNT=") {
			counts++
		}
	}
	if counts != 1 {
		t.Fatalf("want exactly one GIT_CONFIG_COUNT, got %d in %#v", counts, env)
	}
	for _, want := range []string{
		"GIT_CONFIG_KEY_0=user.name", "GIT_CONFIG_VALUE_0=someone",
		"GIT_CONFIG_KEY_1=safe.bareRepository", "GIT_CONFIG_VALUE_1=explicit",
		"GIT_CONFIG_KEY_2=core.fsmonitor", "GIT_CONFIG_VALUE_2=false",
		"GIT_CONFIG_COUNT=3",
	} {
		if !containsString(env, want) {
			t.Fatalf("missing %q in %#v", want, env)
		}
	}
}

func TestEnvRefusesUnreadableCount(t *testing.T) {
	for _, bad := range []string{"GIT_CONFIG_COUNT=two", "GIT_CONFIG_COUNT=-1"} {
		if _, err := Env([]string{bad}); err == nil {
			t.Fatalf("%s: want error, got nil", bad)
		}
	}
}

func TestHardenUsesInheritedEnvWhenUnset(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "user.name")
	t.Setenv("GIT_CONFIG_VALUE_0", "someone")
	cmd := exec.Command("git", "version")
	if err := Harden(cmd); err != nil {
		t.Fatalf("Harden() error = %v", err)
	}
	for _, want := range []string{"GIT_CONFIG_KEY_0=user.name", "GIT_CONFIG_KEY_2=core.fsmonitor", "GIT_CONFIG_COUNT=3"} {
		if !containsString(cmd.Env, want) {
			t.Fatalf("missing %q in %#v", want, cmd.Env)
		}
	}
}

func TestHardenRefusesUnreadableCount(t *testing.T) {
	cmd := exec.Command("git", "version")
	cmd.Env = []string{"GIT_CONFIG_COUNT=two"}
	if err := Harden(cmd); err == nil {
		t.Fatal("Harden() with a bad GIT_CONFIG_COUNT: want error, got nil")
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
