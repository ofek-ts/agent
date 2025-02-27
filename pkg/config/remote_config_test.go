package config

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/prometheus/common/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRemoteConfigHTTP(t *testing.T) {
	testCfg := `
metrics:
  global:
    scrape_timeout: 33s
`

	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/agent.yml" {
			_, _ = w.Write([]byte(testCfg))
		}
	}))

	svrWithBasicAuth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ := r.BasicAuth()
		if user != "foo" && pass != "bar" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/agent.yml" {
			_, _ = w.Write([]byte(testCfg))
		}
	}))

	tempDir := t.TempDir()
	err := os.WriteFile(fmt.Sprintf("%s/password-file.txt", tempDir), []byte("bar"), 0644)
	require.NoError(t, err)

	passwdFileCfg := &config.HTTPClientConfig{
		BasicAuth: &config.BasicAuth{
			Username:     "foo",
			PasswordFile: fmt.Sprintf("%s/password-file.txt", tempDir),
		},
	}
	dir, err := os.Getwd()
	require.NoError(t, err)
	passwdFileCfg.SetDirectory(dir)

	type args struct {
		rawURL string
		opts   *remoteOpts
	}
	tests := []struct {
		name    string
		args    args
		want    []byte
		wantErr bool
	}{
		{
			name: "httpScheme config",
			args: args{
				rawURL: fmt.Sprintf("%s/agent.yml", svr.URL),
			},
			want:    []byte(testCfg),
			wantErr: false,
		},
		{
			name: "httpScheme config with basic auth",
			args: args{
				rawURL: fmt.Sprintf("%s/agent.yml", svrWithBasicAuth.URL),
				opts: &remoteOpts{
					HTTPClientConfig: &config.HTTPClientConfig{
						BasicAuth: &config.BasicAuth{
							Username: "foo",
							Password: "bar",
						},
					},
				},
			},
			want:    []byte(testCfg),
			wantErr: false,
		},
		{
			name: "httpScheme config with basic auth password file",
			args: args{
				rawURL: fmt.Sprintf("%s/agent.yml", svrWithBasicAuth.URL),
				opts: &remoteOpts{
					HTTPClientConfig: passwdFileCfg,
				},
			},
			want:    []byte(testCfg),
			wantErr: false,
		},
		{
			name: "unsupported scheme throws error",
			args: args{
				rawURL: "ssh://unsupported/scheme",
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "invalid url throws error",
			args: args{
				rawURL: "://invalid/url",
			},
			want:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc, err := newRemoteProvider(tt.args.rawURL, tt.args.opts)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			bb, err := rc.retrieve()
			assert.NoError(t, err)
			assert.Equal(t, string(tt.want), string(bb))
		})
	}
}
