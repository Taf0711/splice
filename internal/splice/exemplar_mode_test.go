package splice

import (
	"strings"
	"testing"
)

// Unset env means "both": today's behavior, byte-identically.
func TestResolveExemplarModeDefaultBoth(t *testing.T) {
	t.Setenv("SPLICE_EXEMPLAR_MODE", "")
	mode, err := resolveExemplarMode()
	if err != nil {
		t.Fatalf("unset env must not error: %v", err)
	}
	if mode != ExemplarModeBoth {
		t.Fatalf("mode = %q, want both", mode)
	}
	if !mode.deliverObservations() || !mode.deliverExemplars() {
		t.Fatal("both mode must deliver both classes")
	}
}

// Every named mode resolves and gates the right classes.
func TestResolveExemplarModeNamed(t *testing.T) {
	cases := []struct {
		raw      string
		want     ExemplarMode
		obs      bool
		exemplar bool
	}{
		{"both", ExemplarModeBoth, true, true},
		{"obs-only", ExemplarModeObsOnly, true, false},
		{"exemplar-only", ExemplarModeExemplarOnly, false, true},
		{"none", ExemplarModeNone, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Setenv("SPLICE_EXEMPLAR_MODE", tc.raw)
			mode, err := resolveExemplarMode()
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if mode != tc.want {
				t.Fatalf("mode = %q, want %q", mode, tc.want)
			}
			if mode.deliverObservations() != tc.obs {
				t.Fatalf("deliverObservations = %v, want %v", mode.deliverObservations(), tc.obs)
			}
			if mode.deliverExemplars() != tc.exemplar {
				t.Fatalf("deliverExemplars = %v, want %v", mode.deliverExemplars(), tc.exemplar)
			}
		})
	}
}

// An invalid value is a loud, named configuration error (no silent default).
func TestResolveExemplarModeInvalidFailsLoud(t *testing.T) {
	t.Setenv("SPLICE_EXEMPLAR_MODE", "sometimes")
	_, err := resolveExemplarMode()
	if err == nil {
		t.Fatal("invalid mode must error")
	}
	if !strings.Contains(err.Error(), "SPLICE_EXEMPLAR_MODE") || !strings.Contains(err.Error(), "sometimes") {
		t.Fatalf("error must name the env var and value: %v", err)
	}
}
