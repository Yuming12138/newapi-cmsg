package service

import "testing"

func TestParseKimiBalanceResponse(t *testing.T) {
	balance, err := parseKimiBalanceResponse([]byte(`{"code":0,"status":true,"data":{"available_balance":24.98}}`))
	if err != nil || balance != 24.98 {
		t.Fatalf("parseKimiBalanceResponse() = (%v, %v), want 24.98", balance, err)
	}
	for _, payload := range []string{
		`{"code":1,"status":false,"data":{"available_balance":24.98}}`,
		`{"code":0,"status":true}`,
		`{"code":0,"status":true,"data":{"available_balance":-1}}`,
	} {
		if _, err := parseKimiBalanceResponse([]byte(payload)); err == nil {
			t.Fatalf("parseKimiBalanceResponse(%s) should fail", payload)
		}
	}
}
