package version

import "testing"

func TestSatisfiesMin(t *testing.T) {
	cases := []struct {
		name    string
		current string
		min     string
		want    bool
		wantErr bool
	}{
		{name: "empty requirement passes", current: "1.0.0", min: "", want: true},
		{name: "dev build skips", current: "dev", min: "99.0.0", want: true},
		{name: "empty current skips", current: "", min: "1.0.0", want: true},
		{name: "unparseable current skips", current: "1.2", min: "1.0.0", want: true},
		{name: "exact match passes", current: "1.2.3", min: "1.2.3", want: true},
		{name: "newer passes", current: "1.2.4", min: "1.2.3", want: true},
		{name: "older fails", current: "1.2.2", min: "1.2.3", want: false},
		{name: "v prefix and prerelease pass", current: "v1.2.3-beta.1", min: "1.2.0", want: true},
		{name: "prerelease of the requirement passes", current: "1.2.3-rc.1", min: "1.2.3", want: true},
		{name: "major bump passes", current: "2.0.0", min: "1.99.99", want: true},
		{name: "malformed requirement fails loud", current: "1.2.3", min: "not-a-version", wantErr: true},
		{name: "malformed requirement fails loud on dev", current: "dev", min: "not-a-version", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SatisfiesMin(tc.current, tc.min)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("SatisfiesMin(%q, %q) = %v, want error", tc.current, tc.min, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("SatisfiesMin(%q, %q) error = %v", tc.current, tc.min, err)
			}
			if got != tc.want {
				t.Fatalf("SatisfiesMin(%q, %q) = %v, want %v", tc.current, tc.min, got, tc.want)
			}
		})
	}
}
