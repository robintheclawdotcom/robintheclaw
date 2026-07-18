package quoteauthority

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/robin-the-claw/liveexec/protocol"
)

func TestCoordinatorEpisodeResolverRequiresSchemaV2Target(t *testing.T) {
	intentID := testHash("resolver-intent")
	base := OpenEpisode{
		SchemaVersion: 2, ExecutionAccountID: "account-canary-1", IntentID: intentID,
		TargetStrategyManifestSHA256: protocol.PreviousStrategyManifestSHA256,
		Phase:                        "perp_and_spot", SpotAmount: "1000000000000000000", PerpBaseAmount: 10_000,
		ObservedAtMS: 100_000,
	}
	tests := []struct {
		name     string
		mutate   func(*OpenEpisode)
		unsigned bool
		wantErr  bool
	}{
		{name: "valid predecessor"},
		{name: "unsigned response", unsigned: true, wantErr: true},
		{name: "old schema", mutate: func(episode *OpenEpisode) {
			episode.SchemaVersion = 1
		}, wantErr: true},
		{name: "missing target", mutate: func(episode *OpenEpisode) {
			episode.TargetStrategyManifestSHA256 = ""
		}, wantErr: true},
		{name: "unknown target", mutate: func(episode *OpenEpisode) {
			episode.TargetStrategyManifestSHA256 = testHash("unknown-target")[2:]
		}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			episode := base
			if test.mutate != nil {
				test.mutate(&episode)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				path := "/v1/open-episodes/" + base.ExecutionAccountID + "/" + intentID
				if request.URL.Path != path {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				body, err := json.Marshal(episode)
				if err != nil {
					t.Fatal(err)
				}
				body = append(body, '\n')
				if !test.unsigned {
					w.Header().Set(
						"X-RTC-Response-Signature",
						hex.EncodeToString(protocol.ResponseMAC(
							[]byte("01234567890123456789012345678901"),
							path,
							"quote-authority",
							request.Header.Get("X-RTC-Nonce"),
							http.StatusOK,
							body,
						)),
					)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(body)
			}))
			defer server.Close()

			resolver, err := NewCoordinatorEpisodeResolver(
				server.URL,
				"quote-authority",
				[]byte("01234567890123456789012345678901"),
			)
			if err != nil {
				t.Fatal(err)
			}
			resolver.client = server.Client()
			resolver.now = func() time.Time { return time.Unix(100, 0) }
			resolver.nonce = func() (string, error) {
				return "0123456789abcdef0123456789abcdef", nil
			}
			got, err := resolver.Resolve(context.Background(), base.ExecutionAccountID, intentID)
			if test.wantErr {
				if err == nil {
					t.Fatal("invalid open episode was accepted")
				}
				return
			}
			if err != nil || got.TargetStrategyManifestSHA256 != base.TargetStrategyManifestSHA256 {
				t.Fatalf("valid open episode rejected: episode=%+v err=%v", got, err)
			}
		})
	}
}
