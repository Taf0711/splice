package flags

import (
	"reflect"
	"strings"
	"testing"
)

// TestRegistryEntriesAreWellFormed is the producer side of the pairing test.
// Every declared flag must carry a description and a named consumer, because a
// flag that is declared and never read is the project's dominant defect class.
func TestRegistryEntriesAreWellFormed(t *testing.T) {
	seen := make(map[Flag]bool, len(Registry))
	for _, def := range Registry {
		if err := ValidateName(string(def.Name)); err != nil {
			t.Errorf("registry entry %q: %v", def.Name, err)
		}
		if def.Description == "" {
			t.Errorf("registry entry %q has an empty Description", def.Name)
		}
		if def.ReadBy == "" {
			t.Errorf("registry entry %q has an empty ReadBy: name the consumer", def.Name)
		}
		if seen[def.Name] {
			t.Errorf("registry entry %q is declared twice", def.Name)
		}
		seen[def.Name] = true
	}
}

// TestDeclaredConstantsAreRegistered pins the constants against the registry.
// A constant that is not registered cannot be resolved or enabled.
func TestDeclaredConstantsAreRegistered(t *testing.T) {
	for _, name := range []Flag{StageSecurityAuditor, StageTestGenerator, TUIPipelineEnabled} {
		if _, ok := Lookup(name); !ok {
			t.Errorf("declared flag constant %q is missing from Registry", name)
		}
	}
}

func TestParseList(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    map[string]bool
		wantErr bool
	}{
		{name: "empty", raw: "", want: map[string]bool{}},
		{name: "enable short form", raw: "a.b", want: map[string]bool{"a.b": true}},
		{name: "disable short form", raw: "-a.b", want: map[string]bool{"a.b": false}},
		{name: "explicit one", raw: "a.b=1", want: map[string]bool{"a.b": true}},
		{name: "explicit zero", raw: "a.b=0", want: map[string]bool{"a.b": false}},
		{name: "explicit true word", raw: "a.b=true", want: map[string]bool{"a.b": true}},
		{name: "explicit off word", raw: "a.b=off", want: map[string]bool{"a.b": false}},
		{name: "whitespace and trailing comma", raw: " a.b , c.d=0 , ", want: map[string]bool{"a.b": true, "c.d": false}},
		{name: "duplicate is an error", raw: "a.b,-a.b", wantErr: true},
		{name: "invalid boolean", raw: "a.b=yes", wantErr: true},
		{name: "invalid name", raw: "A.B", wantErr: true},
		{name: "empty name from dash", raw: "-", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseList(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseList(%q) = %v, want error", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseList(%q) unexpected error: %v", tc.raw, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseList(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestResolveDefaults(t *testing.T) {
	set, err := Resolve(Sources{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, def := range Registry {
		if got := set.Enabled(def.Name); got != def.Default {
			t.Errorf("default for %q = %v, want %v", def.Name, got, def.Default)
		}
	}
	// The zero Set must behave identically to an all-defaults resolution.
	var zero Set
	for _, def := range Registry {
		if zero.Enabled(def.Name) != def.Default {
			t.Errorf("zero Set default for %q = %v, want %v", def.Name, zero.Enabled(def.Name), def.Default)
		}
	}
}

func TestResolvePrecedence(t *testing.T) {
	target := StageTestGenerator
	set, err := Resolve(Sources{
		User:    map[string]bool{string(target): false},
		CLI:     map[string]bool{string(target): true},
		Env:     "-" + string(target),
		Project: map[string]bool{},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !set.Enabled(target) {
		t.Fatalf("CLI must outrank env, user, and project; got disabled")
	}

	// Drop the CLI layer: env must win over user config.
	set, err = Resolve(Sources{
		User: map[string]bool{string(target): true},
		Env:  "-" + string(target),
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if set.Enabled(target) {
		t.Fatalf("env must outrank user config; got enabled")
	}
}

func TestResolveRejectsUnknownFlag(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  Sources
		want string
	}{
		{name: "user config", src: Sources{User: map[string]bool{"nope": true}}, want: "user config"},
		{name: "cli", src: Sources{CLI: map[string]bool{"nope": true}}, want: "command line"},
		{name: "env", src: Sources{Env: "nope"}, want: EnvVar},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Resolve(tc.src)
			if err == nil {
				t.Fatalf("Resolve accepted unknown flag from %s", tc.name)
			}
			if !strings.Contains(err.Error(), "unknown feature flag") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q must name the unknown flag and the source %q", err, tc.want)
			}
		})
	}
}

// TestResolveRefusesUserScopeFromProject is the adversarial guard: a cloned
// repository must not be able to relax a user-scope flag.
func TestResolveRefusesUserScopeFromProject(t *testing.T) {
	_, err := Resolve(Sources{Project: map[string]bool{string(StageSecurityAuditor): false}})
	if err == nil {
		t.Fatal("project config was allowed to disable a user-scope flag")
	}
	if !strings.Contains(err.Error(), "user-scope") {
		t.Fatalf("error %q must explain the scope refusal", err)
	}

	// A project-scope flag from project config is allowed.
	set, err := Resolve(Sources{Project: map[string]bool{string(TUIPipelineEnabled): false}})
	if err != nil {
		t.Fatalf("Resolve with project-scope flag: %v", err)
	}
	if set.Enabled(TUIPipelineEnabled) {
		t.Fatal("project-scope flag set from project config was ignored")
	}
}

func TestEnabledNamesSortedAndStable(t *testing.T) {
	set, err := Resolve(Sources{User: map[string]bool{string(StageTestGenerator): false}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	got := set.EnabledNames()
	want := []string{string(StageSecurityAuditor), string(TUIPipelineEnabled)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EnabledNames() = %v, want %v", got, want)
	}
	for i := 0; i < 5; i++ {
		if again := set.EnabledNames(); !reflect.DeepEqual(again, got) {
			t.Fatalf("EnabledNames() not stable: %v then %v", got, again)
		}
	}
}
