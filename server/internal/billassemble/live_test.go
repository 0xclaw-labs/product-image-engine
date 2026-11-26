package billassemble

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLiveQuoteDoesNotHoldOrCharge(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	var payer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		switch r.URL.Path {
		case "/internal/v1/billing/unified/usage":
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			payer, _ = req["payer_account_id"].(string)
			if req["pricing_version"] != PricingVersion {
				t.Errorf("价格版本 %v", req["pricing_version"])
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "fact": map[string]any{"usage_id": "usg_live"}})
		case "/internal/v1/billing/unified/quote":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "quote": map[string]any{
				"usage_id": "usg_live", "amount_minor": 150, "quantity": 1, "pricing_version": PricingVersion,
			}})
		case "/internal/v1/billing/unified/accounts":
			if r.URL.Query().Get("account_id") != "acct_person" {
				t.Errorf("余额账号 %s", r.URL.Query().Get("account_id"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "snapshot": map[string]any{
				"account":      map[string]any{"cash_balance_minor": 1000},
				"entitlements": []any{map[string]any{"capability": "image.generate.standard", "app_id": "product-image", "status": "active"}},
			}})
		default:
			t.Errorf("不该发送 %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	live := &Live{AppID: "product-image", BillingToken: "bill-tok", BillingBase: srv.URL, Enabled: true}
	quoted, err := live.Quote(context.Background(), QuoteCall{
		PayerAccountID: "acct_person", Capability: "image.generate.standard",
		Quantity: 1, PricingVersion: PricingVersion, BusinessRef: "proj_1", IdempotencyKey: "fp-1",
	})
	if err != nil || quoted.UsageID != "usg_live" || quoted.AmountMinor != 150 || !quoted.BalanceKnown || quoted.FundsShort || quoted.FundsState != FundsNotSent {
		t.Fatalf("报价: %+v %v", quoted, err)
	}
	if payer != "acct_person" {
		t.Fatalf("付款人应是服务端账号, got %q", payer)
	}
	if !quoted.EntitlementsKnown || len(quoted.Entitlements) != 1 {
		t.Fatalf("权益: %+v", quoted.Entitlements)
	}
	held, err := live.Hold(context.Background(), HoldCall{UsageID: "usg_live", AmountMinor: 150, IdempotencyKey: "hold-1"})
	if err != ErrFundsNotExecuted || held.FundsAction != FundsNotSent || held.Sent {
		t.Fatalf("真实预占应拒绝发送: %+v %v", held, err)
	}
	settled, err := live.Settle(context.Background(), SettleCall{UsageID: "usg_live", IdempotencyKey: "chg-1"})
	if err != ErrFundsNotExecuted || settled.FundsAction != FundsNotSent || settled.Sent {
		t.Fatalf("真实结算应拒绝发送: %+v %v", settled, err)
	}
	released, err := live.Release(context.Background(), ReleaseCall{HoldID: "uhold_1", IdempotencyKey: "rel-1"})
	if err != ErrFundsNotExecuted || released.FundsAction != FundsNotSent || released.Sent {
		t.Fatalf("真实释放应拒绝发送: %+v %v", released, err)
	}
	for _, path := range paths {
		if strings.Contains(path, "hold") || strings.Contains(path, "charge") || strings.Contains(path, "refund") {
			t.Fatalf("资金路径被发送: %v", paths)
		}
	}
}

func TestLiveMissingConfigDoesNotDial(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	t.Cleanup(srv.Close)
	live := &Live{AppID: "product-image", BillingBase: srv.URL}
	_, err := live.Quote(context.Background(), QuoteCall{PayerAccountID: "acct_person", Quantity: 1, Capability: "image.generate.standard", PricingVersion: PricingVersion, IdempotencyKey: "fp"})
	if err != ErrConfigMissing || called {
		t.Fatalf("缺配置不应发请求: err=%v called=%v", err, called)
	}
	held, holdErr := live.Hold(context.Background(), HoldCall{AmountMinor: 150, IdempotencyKey: "hold"})
	if holdErr != ErrConfigMissing || held.FundsAction != FundsUnconfigured || called {
		t.Fatalf("缺配置的预占不能发出: %+v %v called=%v", held, holdErr, called)
	}
}

func TestLiveRouteMissingIsUnimplemented(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"ok":false,"error":{"code":"billing_route_not_found"}}`))
	}))
	t.Cleanup(srv.Close)
	live := &Live{AppID: "product-image", BillingToken: "tok", BillingBase: srv.URL, Enabled: true}
	_, err := live.Quote(context.Background(), QuoteCall{
		PayerAccountID: "acct_person", Quantity: 1, Capability: "image.generate.standard",
		PricingVersion: PricingVersion, IdempotencyKey: "fp", BusinessRef: "proj",
	})
	if err != ErrUnimplemented {
		t.Fatalf("路由不存在应是未实现, got %v", err)
	}
}

func TestLiveLookupDoesNotInventACharge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/tasks/task_1" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-App-ID") != "product-image" {
			t.Errorf("app %s", r.Header.Get("X-App-ID"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"task_id": "task_1", "status": "succeeded"})
	}))
	t.Cleanup(srv.Close)
	live := &Live{AppID: "product-image", TaskToken: "task-tok", TaskBase: srv.URL, Enabled: true}
	got, err := live.Lookup(context.Background(), LookupCall{TaskID: "task_1"})
	if err != nil || !got.FoundTask || got.FoundCharge || got.Settlement != SettlementUnknown {
		t.Fatalf("查回应保持未知: %+v %v", got, err)
	}
	reconciled := Reconcile(true, got)
	if reconciled.Settlement != PendingCheck || reconciled.Regenerate || reconciled.BillingPassed {
		t.Fatalf("查回后仍待核对: %+v", reconciled)
	}
}

func TestLiveLookupFeeRefIsNotASettlement(t *testing.T) {
	for _, key := range []string{"charge_id", "charge_ref", "fee_ref"} {
		t.Run(key, func(t *testing.T) {
			var paths []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				if r.URL.Path != "/internal/v1/tasks/task_fee" {
					t.Errorf("不该请求 %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"task_id": "task_fee", "status": "succeeded", key: "chg_on_ticket"})
			}))
			t.Cleanup(srv.Close)
			live := &Live{AppID: "product-image", TaskToken: "task-tok", TaskBase: srv.URL, Enabled: true}
			got, err := live.Lookup(context.Background(), LookupCall{TaskID: "task_fee"})
			if err != nil || !got.FoundTask || !got.FoundCharge || got.ChargeRef != "chg_on_ticket" || got.Settlement != SettlementUnknown {
				t.Fatalf("费用引用可以记下，但不是已结算: %+v %v", got, err)
			}
			done := Reconcile(true, got)
			if done.BillingPassed || done.Settlement != PendingCheck || done.Regenerate {
				t.Fatalf("作品完成仍待核对: %+v", done)
			}
			open := Reconcile(false, LookupFact{})
			if open.Settlement != SettlementUnknown || open.BillingPassed || open.Regenerate {
				t.Fatalf("没有任务仍是未知: %+v", open)
			}
			for _, path := range paths {
				if strings.Contains(path, "hold") || strings.Contains(path, "charge") || strings.Contains(path, "refund") || strings.Contains(path, "billing") {
					t.Fatalf("查回不能发资金请求: %v", paths)
				}
			}
		})
	}
}

func startSnapshotBillingServer(t *testing.T, amountMinor int64, snapshot map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/v1/billing/unified/usage":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "fact": map[string]any{"usage_id": "usg_snap"}})
		case "/internal/v1/billing/unified/quote":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "quote": map[string]any{
				"usage_id": "usg_snap", "amount_minor": amountMinor, "quantity": 1, "pricing_version": PricingVersion,
			}})
		case "/internal/v1/billing/unified/accounts":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "snapshot": snapshot})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestLiveSnapshotFiltersEntitlementsByAppStatusAndWindow(t *testing.T) {
	now := time.Unix(1_760_000_000, 0)
	nowUnix := now.Unix()

	tests := []struct {
		name         string
		entitlements []any
		want         []string
	}{
		{
			name: "other app active entitlement rejected",
			entitlements: []any{
				map[string]any{
					"app_id": "aicut", "capability": "image.generate.standard",
					"status": "active", "valid_from_unix": 0, "valid_until_unix": 0,
				},
				map[string]any{
					"app_id": "goboost", "capability": "image.generate.premium",
					"status": "active", "valid_from_unix": nowUnix - 100, "valid_until_unix": nowUnix + 100,
				},
				map[string]any{
					"capability": "image.generate.standard",
					"status":     "active",
				},
			},
			want: nil,
		},
		{
			name: "expired and not-yet-valid and non-active entitlements rejected",
			entitlements: []any{
				map[string]any{
					"app_id": "product-image", "capability": "cap.expired.past",
					"status": "active", "valid_from_unix": nowUnix - 100, "valid_until_unix": nowUnix - 1,
				},
				map[string]any{
					"app_id": "product-image", "capability": "cap.expired.boundary",
					"status": "active", "valid_from_unix": nowUnix - 100, "valid_until_unix": nowUnix,
				},
				map[string]any{
					"app_id": "product-image", "capability": "cap.future",
					"status": "active", "valid_from_unix": nowUnix + 1, "valid_until_unix": nowUnix + 100,
				},
				map[string]any{
					"app_id": "product-image", "capability": "cap.revoked",
					"status": "revoked", "valid_from_unix": 0, "valid_until_unix": 0,
				},
				map[string]any{
					"app_id": "product-image", "capability": "cap.empty.status",
					"status": "", "valid_from_unix": 0, "valid_until_unix": 0,
				},
				map[string]any{
					"app_id": "product-image", "capability": "cap.negative.until",
					"status": "active", "valid_from_unix": 0, "valid_until_unix": -1,
				},
			},
			want: nil,
		},
		{
			name: "current app active in-window entitlements accepted and deduplicated",
			entitlements: []any{
				map[string]any{
					"app_id": "aicut", "capability": "image.generate.premium",
					"status": "active", "valid_from_unix": 0, "valid_until_unix": 0,
				},
				map[string]any{
					"app_id": "product-image", "capability": "image.generate.standard",
					"status": "active", "valid_from_unix": 0, "valid_until_unix": 0,
				},
				map[string]any{
					"app_id": "product-image", "capability": "image.generate.standard",
					"status": "active", "valid_from_unix": nowUnix - 10, "valid_until_unix": nowUnix + 10,
				},
				map[string]any{
					"app_id": "product-image", "capability": "image.generate.hd",
					"status": "active", "valid_from_unix": nowUnix, "valid_until_unix": nowUnix + 1,
				},
				map[string]any{
					"app_id": "product-image", "capability": "image.generate.expired",
					"status": "active", "valid_from_unix": nowUnix - 10, "valid_until_unix": nowUnix,
				},
			},
			want: []string{"image.generate.standard", "image.generate.hd"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := startSnapshotBillingServer(t, 150, map[string]any{
				"account": map[string]any{
					"cash_balance_minor":   1000,
					"cash_reserved_minor":  0,
					"cash_available_minor": 1000,
				},
				"entitlements": tc.entitlements,
			})
			live := &Live{
				AppID: "product-image", BillingToken: "bill-tok", BillingBase: srv.URL,
				Enabled: true, Now: func() time.Time { return now },
			}
			quoted, err := live.Quote(context.Background(), QuoteCall{
				PayerAccountID: "acct_person", Capability: "image.generate.standard",
				Quantity: 1, PricingVersion: PricingVersion, BusinessRef: "proj_1", IdempotencyKey: "fp-ent",
			})
			if err != nil {
				t.Fatalf("Quote failed: %v", err)
			}
			if !quoted.EntitlementsKnown {
				t.Fatalf("expected EntitlementsKnown=true")
			}
			if len(quoted.Entitlements) != len(tc.want) {
				t.Fatalf("entitlements mismatch: got %v, want %v", quoted.Entitlements, tc.want)
			}
			for i := range tc.want {
				if quoted.Entitlements[i] != tc.want[i] {
					t.Fatalf("entitlement[%d]: got %q, want %q (all=%v)", i, quoted.Entitlements[i], tc.want[i], quoted.Entitlements)
				}
			}
		})
	}
}

func TestLiveSnapshotUsesCashAvailableMinorUnderInFlightHold(t *testing.T) {
	now := time.Unix(1_760_000_000, 0)

	t.Run("partial hold makes available balance insufficient", func(t *testing.T) {
		srv := startSnapshotBillingServer(t, 150, map[string]any{
			"account": map[string]any{
				"cash_balance_minor":   1000,
				"cash_reserved_minor":  900,
				"cash_available_minor": 100,
			},
			"entitlements": []any{
				map[string]any{"app_id": "product-image", "capability": "image.generate.standard", "status": "active"},
			},
		})
		live := &Live{
			AppID: "product-image", BillingToken: "bill-tok", BillingBase: srv.URL,
			Enabled: true, Now: func() time.Time { return now },
		}
		quoted, err := live.Quote(context.Background(), QuoteCall{
			PayerAccountID: "acct_hold", Capability: "image.generate.standard",
			Quantity: 1, PricingVersion: PricingVersion, BusinessRef: "proj_1", IdempotencyKey: "fp-hold-1",
		})
		if err != nil {
			t.Fatalf("Quote failed: %v", err)
		}
		if !quoted.BalanceKnown || quoted.BalanceMinor != 100 || !quoted.FundsShort {
			t.Fatalf("expected BalanceMinor=100 FundsShort=true, got %+v", quoted)
		}
		bal, err := live.ReadBalance(context.Background(), "acct_hold")
		if err != nil || !bal.Known || bal.Minor != 100 {
			t.Fatalf("ReadBalance should use cash_available_minor=100, got %+v err=%v", bal, err)
		}
	})

	t.Run("full hold zero available does not fall back to cash_balance_minor", func(t *testing.T) {
		srv := startSnapshotBillingServer(t, 150, map[string]any{
			"account": map[string]any{
				"cash_balance_minor":   1000,
				"cash_reserved_minor":  1000,
				"cash_available_minor": 0,
			},
			"entitlements": []any{
				map[string]any{"app_id": "product-image", "capability": "image.generate.standard", "status": "active"},
			},
		})
		live := &Live{
			AppID: "product-image", BillingToken: "bill-tok", BillingBase: srv.URL,
			Enabled: true, Now: func() time.Time { return now },
		}
		quoted, err := live.Quote(context.Background(), QuoteCall{
			PayerAccountID: "acct_zero", Capability: "image.generate.standard",
			Quantity: 1, PricingVersion: PricingVersion, BusinessRef: "proj_1", IdempotencyKey: "fp-hold-0",
		})
		if err != nil {
			t.Fatalf("Quote failed: %v", err)
		}
		if !quoted.BalanceKnown || quoted.BalanceMinor != 0 || !quoted.FundsShort {
			t.Fatalf("expected BalanceMinor=0 FundsShort=true, got %+v", quoted)
		}
		bal, err := live.ReadBalance(context.Background(), "acct_zero")
		if err != nil || !bal.Known || bal.Minor != 0 {
			t.Fatalf("ReadBalance should return 0 when fully held, got %+v err=%v", bal, err)
		}
	})
}

func TestLiveSnapshotAddsActiveScopedCreditsForCurrentAppOnly(t *testing.T) {
	now := time.Unix(1_760_000_000, 0)
	nowUnix := now.Unix()

	srv := startSnapshotBillingServer(t, 150, map[string]any{
		"account": map[string]any{
			"cash_balance_minor":   500,
			"cash_reserved_minor":  450,
			"cash_available_minor": 50,
		},
		"entitlements": []any{
			map[string]any{"app_id": "product-image", "capability": "image.generate.standard", "status": "active"},
		},
		"scoped_credits": []any{
			// 计入：本 app 通用、active、永不过期，优先 available_minor=60（忽略 remaining_minor=100）
			map[string]any{
				"app_id": "product-image", "capability": "", "status": "active",
				"remaining_minor": 100, "reserved_minor": 40, "available_minor": 60, "expires_at_unix": 0,
			},
			// 计入：本 app 匹配能力、active、未过期
			map[string]any{
				"app_id": "product-image", "capability": "image.generate.standard", "status": "active",
				"remaining_minor": 70, "reserved_minor": 0, "available_minor": 70, "expires_at_unix": nowUnix + 60,
			},
			// 不计入：本 app 另一能力，Quote standard 时不能用
			map[string]any{
				"app_id": "product-image", "capability": "image.generate.premium", "status": "active",
				"remaining_minor": 500, "reserved_minor": 0, "available_minor": 500, "expires_at_unix": 0,
			},
			// 不计入：本 app 全额冻结（available_minor=0，不能回退 remaining_minor）
			map[string]any{
				"app_id": "product-image", "capability": "", "status": "active",
				"remaining_minor": 300, "reserved_minor": 300, "available_minor": 0, "expires_at_unix": 0,
			},
			// 不计入：其他 app
			map[string]any{
				"app_id": "aicut", "capability": "", "status": "active",
				"remaining_minor": 999, "reserved_minor": 0, "available_minor": 999, "expires_at_unix": 0,
			},
			// 不计入：已过期（边界 == now）
			map[string]any{
				"app_id": "product-image", "capability": "", "status": "active",
				"remaining_minor": 400, "reserved_minor": 0, "available_minor": 400, "expires_at_unix": nowUnix,
			},
			// 不计入：已吊销
			map[string]any{
				"app_id": "product-image", "capability": "", "status": "revoked",
				"remaining_minor": 400, "reserved_minor": 0, "available_minor": 400, "expires_at_unix": 0,
			},
		},
	})
	live := &Live{
		AppID: "product-image", BillingToken: "bill-tok", BillingBase: srv.URL,
		Enabled: true, Now: func() time.Time { return now },
	}
	quoted, err := live.Quote(context.Background(), QuoteCall{
		PayerAccountID: "acct_credit", Capability: "image.generate.standard",
		Quantity: 1, PricingVersion: PricingVersion, BusinessRef: "proj_1", IdempotencyKey: "fp-credit",
	})
	if err != nil {
		t.Fatalf("Quote failed: %v", err)
	}
	// 50 (cash available) + 60 (app-wide credit) + 70 (standard credit) = 180 >= 150
	if !quoted.BalanceKnown || quoted.BalanceMinor != 180 || quoted.FundsShort {
		t.Fatalf("expected BalanceMinor=180 FundsShort=false, got %+v", quoted)
	}
	// ReadBalance 不限定 capability 时汇总本 app 所有有效活跃 scoped_credits: 50 + 60 + 70 + 500 = 680
	bal, err := live.ReadBalance(context.Background(), "acct_credit")
	if err != nil || !bal.Known || bal.Minor != 680 {
		t.Fatalf("expected ReadBalance=680, got %+v err=%v", bal, err)
	}
}
