package secrets

import (
	"reflect"
	"testing"
)

func TestScrubChildEnv(t *testing.T) {
	tests := []struct {
		name string
		env  []string
		want []string
	}{
		{
			name: "strips known keys",
			env: []string{
				"OPENAI_API_KEY=sk-xxx",
				"ANTHROPIC_API_KEY=sk-ant-xxx",
				"AWS_ACCESS_KEY_ID=AKIAxxx",
				"AWS_SECRET_ACCESS_KEY=yyy",
				"AWS_SESSION_TOKEN=zzz",
				"AZURE_CLIENT_SECRET=secret",
				"AZURE_TENANT_ID=tenant",
				"GOOGLE_APPLICATION_CREDENTIALS=/path",
				"GITHUB_TOKEN=ghtoken",
				"GH_TOKEN=ghtoken2",
				"ZERO_API_KEY=zkey",
				"SPLICE_API_KEY=skey",
				"API_KEY=ak",
				"AUTH_TOKEN=at",
				"ACCESS_TOKEN=act",
				"REFRESH_TOKEN=rt",
			},
			want: []string{"AZURE_TENANT_ID=tenant"},
		},
		{
			name: "suffix matching works",
			env: []string{
				"MY_API_KEY=secret",
				"MY_TOKEN=secret",
				"MY_SECRET=secret",
				"MY_SECRET_KEY=secret",
				"MY_ACCESS_KEY=secret",
				"MY_PASSWORD=secret",
				"KEEP_ME=safe",
			},
			want: []string{"KEEP_ME=safe"},
		},
		{
			name: "allowlist keeps selected var",
			env: []string{
				"SPLICE_CHILD_ENV_ALLOWLIST=MY_API_KEY",
				"MY_API_KEY=secret",
				"OTHER_API_KEY=also-secret",
				"KEEP_ME=safe",
			},
			want: []string{
				"SPLICE_CHILD_ENV_ALLOWLIST=MY_API_KEY",
				"MY_API_KEY=secret",
				"KEEP_ME=safe",
			},
		},
		{
			name: "non-credential vars preserved",
			env: []string{
				"PATH=/usr/bin",
				"HOME=/home/user",
				"EDITOR=vim",
			},
			want: []string{
				"PATH=/usr/bin",
				"HOME=/home/user",
				"EDITOR=vim",
			},
		},
		{
			name: "empty env handled",
			env:  []string{},
			want: []string{},
		},
		{
			name: "malformed entries skipped",
			env: []string{
				"OPENAI_API_KEY=sk-xxx",
				"no-equals-sign",
				"=starts-with-equals=skipped",
				"PATH=/usr/bin",
			},
			want: []string{"PATH=/usr/bin"},
		},
		{
			name: "allowlist is case-insensitive",
			env: []string{
				"SPLICE_CHILD_ENV_ALLOWLIST=My_Api_Key",
				"MY_API_KEY=secret",
				"my_api_key=lower-secret",
			},
			want: []string{
				"SPLICE_CHILD_ENV_ALLOWLIST=My_Api_Key",
				"MY_API_KEY=secret",
				"my_api_key=lower-secret",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ScrubChildEnv(tt.env)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ScrubChildEnv() = %v, want %v", got, tt.want)
			}
		})
	}
}
