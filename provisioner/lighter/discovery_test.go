package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (value roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return value(request)
}

func discoveryClient(handler func(*http.Request) (int, string)) *liveLighterClient {
	client := newLiveLighterClient("https://lighter.test", 300)
	client.http = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		status, body := handler(request)
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    request,
		}, nil
	})}
	return client
}

func TestDiscoverEmptySubaccountAndNextNonce(t *testing.T) {
	var mu sync.Mutex
	requested := make([]string, 0, 3)
	client := discoveryClient(func(request *http.Request) (int, string) {
		mu.Lock()
		requested = append(requested, request.URL.RequestURI())
		mu.Unlock()
		switch request.URL.Path {
		case "/api/v1/accountsByL1Address":
			return http.StatusOK, fmt.Sprintf(`{"code":200,"l1_address":%q,"sub_accounts":[%s,%s]}`,
				testOwner, accountJSON(0, 10, testOwner, "0", "0"), accountJSON(1, 42, testOwner, "0.000", "0"))
		case "/api/v1/account":
			return http.StatusOK, fmt.Sprintf(`{"code":200,"total":1,"accounts":[%s],"next_cursor":""}`,
				detailedAccountJSON(42, testOwner, "0", "0", "0", "0"))
		case "/api/v1/nextNonce":
			return http.StatusOK, `{"code":200,"nonce":7}`
		default:
			return http.StatusNotFound, `{"code":404}`
		}
	})

	accountIndex, err := client.DiscoverEmptySubaccount(context.Background(), testOwner)
	if err != nil {
		t.Fatal(err)
	}
	if accountIndex != 42 {
		t.Fatalf("account index = %d", accountIndex)
	}
	nonce, err := client.NextNonce(context.Background(), accountIndex, 4)
	if err != nil {
		t.Fatal(err)
	}
	if nonce != 7 {
		t.Fatalf("nonce = %d", nonce)
	}
	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(requested, "\n")
	for _, expected := range []string{
		"/api/v1/accountsByL1Address?l1_address=0x1111111111111111111111111111111111111111",
		"/api/v1/account?active_only=true&by=index&value=42",
		"/api/v1/nextNonce?account_index=42&api_key_index=4",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing request %s in:\n%s", expected, joined)
		}
	}
}

func TestDiscoveryRejectsFundedOrMasterAccounts(t *testing.T) {
	client := discoveryClient(func(*http.Request) (int, string) {
		return http.StatusOK, fmt.Sprintf(`{"code":200,"l1_address":%q,"sub_accounts":[%s,%s]}`,
			testOwner, accountJSON(0, 10, testOwner, "0", "0"), accountJSON(1, 42, testOwner, "1", "1"))
	})

	if _, err := client.DiscoverEmptySubaccount(context.Background(), testOwner); !errors.Is(err, errNoEmptySubaccount) {
		t.Fatalf("discovery error = %v", err)
	}
}

func TestDiscoveryFailsWhenMultipleSubaccountsAreEligible(t *testing.T) {
	client := discoveryClient(func(request *http.Request) (int, string) {
		if request.URL.Path == "/api/v1/accountsByL1Address" {
			return http.StatusOK, fmt.Sprintf(`{"code":200,"l1_address":%q,"sub_accounts":[%s,%s,%s]}`,
				testOwner, accountJSON(0, 10, testOwner, "0", "0"),
				accountJSON(1, 42, testOwner, "0", "0"), accountJSON(1, 43, testOwner, "0", "0"))
		}
		index := parseTestIndex(t, request.URL.Query().Get("value"))
		return http.StatusOK, fmt.Sprintf(`{"code":200,"total":1,"accounts":[%s],"next_cursor":""}`,
			detailedAccountJSON(index, testOwner, "0", "0", "0", "0"))
	})

	if _, err := client.DiscoverEmptySubaccount(context.Background(), testOwner); !errors.Is(err, errAmbiguousEmptySubaccounts) {
		t.Fatalf("discovery error = %v", err)
	}
}

func TestDiscoveryRejectsHiddenPositionsAndOwnerMismatch(t *testing.T) {
	for _, test := range []struct {
		name          string
		detailOwner   string
		position      string
		expectedError string
	}{
		{name: "position", detailOwner: testOwner, position: "0.01"},
		{name: "owner", detailOwner: "0x2222222222222222222222222222222222222222", position: "0", expectedError: "did not match"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := discoveryClient(func(request *http.Request) (int, string) {
				if request.URL.Path == "/api/v1/accountsByL1Address" {
					return http.StatusOK, fmt.Sprintf(`{"code":200,"l1_address":%q,"sub_accounts":[%s,%s]}`,
						testOwner, accountJSON(0, 10, testOwner, "0", "0"), accountJSON(1, 42, testOwner, "0", "0"))
				}
				return http.StatusOK, fmt.Sprintf(`{"code":200,"total":1,"accounts":[%s],"next_cursor":""}`,
					detailedAccountJSON(42, test.detailOwner, "0", "0", test.position, "0"))
			})

			_, err := client.DiscoverEmptySubaccount(context.Background(), testOwner)
			if test.expectedError == "" {
				if !errors.Is(err, errNoEmptySubaccount) {
					t.Fatalf("discovery error = %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.expectedError) {
				t.Fatalf("discovery error = %v", err)
			}
		})
	}
}

func accountJSON(accountType int, index int64, owner, available, collateral string) string {
	return fmt.Sprintf(`{"account_type":%d,"index":%d,"l1_address":%q,"total_order_count":0,"total_isolated_order_count":0,"pending_order_count":0,"available_balance":%q,"status":1,"collateral":%q}`,
		accountType, index, owner, available, collateral)
}

func detailedAccountJSON(index int64, owner, available, collateral, position, assetBalance string) string {
	return fmt.Sprintf(`{"account_type":1,"index":%d,"account_index":%d,"l1_address":%q,"total_order_count":0,"total_isolated_order_count":0,"pending_order_count":0,"available_balance":%q,"status":1,"collateral":%q,"positions":[{"open_order_count":0,"pending_order_count":0,"position_tied_order_count":0,"position":%q,"position_value":"0","allocated_margin":"0"}],"assets":[{"balance":%q,"locked_balance":"0"}],"total_asset_value":"0","cross_asset_value":"0"}`,
		index, index, owner, available, collateral, position, assetBalance)
}

func parseTestIndex(t *testing.T, value string) int64 {
	t.Helper()
	if value == "42" {
		return 42
	}
	if value == "43" {
		return 43
	}
	t.Fatalf("unexpected account index %q", value)
	return 0
}
