package billassemble

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bianjiefilm/public-ai/sdk/go/platformconsumer"
)

// Live 调用已经存在的平台接口。预占、结算和释放在本轮不发送。
type Live struct {
	AppID        string
	BillingToken string
	TaskToken    string
	BillingBase  string
	TaskBase     string
	Enabled      bool
	HTTP         *http.Client
	Now          func() time.Time
}

func (p *Live) Quote(ctx context.Context, in QuoteCall) (QuoteFact, error) {
	if err := p.billingReady(); err != nil {
		return QuoteFact{Connection: ConnMissing, FundsState: FundsUnconfigured}, err
	}
	if strings.TrimSpace(in.PayerAccountID) == "" || in.Quantity <= 0 || strings.TrimSpace(in.Capability) == "" || strings.TrimSpace(in.IdempotencyKey) == "" || strings.TrimSpace(in.PricingVersion) == "" {
		return QuoteFact{Connection: ConnReady, FundsState: FundsNotSent}, ErrUsageInvalid
	}
	usageID, err := p.ingest(ctx, in)
	if err != nil {
		state := FundsNotSent
		conn := ConnReady
		if errors.Is(err, ErrConfigMissing) {
			state = FundsUnconfigured
			conn = ConnMissing
		}
		return QuoteFact{Connection: conn, FundsState: state}, err
	}
	fact, err := p.quoteUsage(ctx, usageID)
	if err != nil {
		return QuoteFact{Connection: ConnReady, UsageID: usageID, FundsState: FundsNotSent}, err
	}
	fact.UsageID = usageID
	fact.Connection = ConnReady
	fact.Sent = true
	fact.Currency = "CNY"
	fact.FundsState = FundsNotSent
	p.attachBalance(ctx, in.TenantID, in.PayerAccountID, in.Capability, &fact)
	return fact, nil
}

func (p *Live) Hold(context.Context, HoldCall) (HoldFact, error) {
	if err := p.billingReady(); err != nil {
		return HoldFact{Connection: ConnMissing, FundsAction: FundsUnconfigured, Sent: false}, err
	}
	return HoldFact{Connection: ConnReady, FundsAction: FundsNotSent, Sent: false}, ErrFundsNotExecuted
}

func (p *Live) Release(context.Context, ReleaseCall) (ReleaseFact, error) {
	if err := p.billingReady(); err != nil {
		return ReleaseFact{FundsAction: FundsUnconfigured, Sent: false}, err
	}
	return ReleaseFact{FundsAction: FundsNotSent, Sent: false}, ErrFundsNotExecuted
}

func (p *Live) Settle(context.Context, SettleCall) (SettleFact, error) {
	if err := p.billingReady(); err != nil {
		return SettleFact{FundsAction: FundsUnconfigured, Sent: false}, err
	}
	return SettleFact{FundsAction: FundsNotSent, Sent: false}, ErrFundsNotExecuted
}

func (p *Live) Lookup(ctx context.Context, in LookupCall) (LookupFact, error) {
	out := LookupFact{Settlement: SettlementUnknown, TaskID: strings.TrimSpace(in.TaskID)}
	if out.TaskID == "" {
		return out, nil
	}
	if strings.TrimSpace(p.TaskBase) == "" || strings.TrimSpace(p.TaskToken) == "" || strings.TrimSpace(p.AppID) == "" || loopbackBase(p.TaskBase) != nil {
		out.Connection = ConnMissing
		return out, ErrConfigMissing
	}
	client := p.consumer(p.TaskToken)
	client.Bases.Task = strings.TrimRight(p.TaskBase, "/")
	res, err := client.Call(ctx, platformconsumer.Call{
		Operation:  "task.get",
		PathParams: map[string]string{"id": out.TaskID},
		Query:      map[string]string{"app_id": p.AppID},
	})
	if err != nil || !res.Sent || res.StatusCode < 200 || res.StatusCode >= 300 {
		return out, ErrUnavailable
	}
	var doc map[string]any
	if json.Unmarshal(res.Body, &doc) != nil {
		return out, ErrUnavailable
	}
	out.Sent = true
	out.FoundTask = true
	if status, _ := doc["status"].(string); status != "" {
		out.TaskStatus = status
	}
	for _, key := range []string{"charge_id", "charge_ref", "fee_ref"} {
		if ref, _ := doc[key].(string); strings.TrimSpace(ref) != "" {
			out.FoundCharge = true
			out.ChargeRef = strings.TrimSpace(ref)
			break
		}
	}
	return out, nil
}

func (p *Live) ReadBalance(ctx context.Context, accountID string) (Balance, error) {
	return p.ReadBalanceFor(ctx, "", accountID)
}

// ReadBalanceFor 只读指定租户名下的账号。租户不匹配时余额未知，不返回别的租户的数字。
func (p *Live) ReadBalanceFor(ctx context.Context, tenantID, accountID string) (Balance, error) {
	return p.ReadBalanceForCapability(ctx, tenantID, accountID, "")
}

// ReadBalanceForCapability 读取指定租户、账号在特定能力下可用的现金 + 定向额度余额。
func (p *Live) ReadBalanceForCapability(ctx context.Context, tenantID, accountID, capability string) (Balance, error) {
	if err := p.billingReady(); err != nil {
		return Balance{Connection: ConnMissing}, err
	}
	var fact QuoteFact
	p.attachBalance(ctx, tenantID, accountID, capability, &fact)
	if !fact.BalanceKnown {
		return Balance{Known: false, Connection: ConnReady}, nil
	}
	return Balance{Known: true, Minor: fact.BalanceMinor, Connection: ConnReady}, nil
}

func (p *Live) billingReady() error {
	if p == nil || !p.Enabled || strings.TrimSpace(p.BillingBase) == "" || strings.TrimSpace(p.BillingToken) == "" || strings.TrimSpace(p.AppID) == "" {
		return ErrConfigMissing
	}
	if err := loopbackBase(p.BillingBase); err != nil {
		return ErrConfigMissing
	}
	return nil
}

func (p *Live) ingest(ctx context.Context, in QuoteCall) (string, error) {
	payload := map[string]any{
		"app_id": p.AppID, "capability": in.Capability, "quantity": in.Quantity,
		"pricing_version": in.PricingVersion, "business_ref": in.BusinessRef,
		"payer_account_id": in.PayerAccountID, "idempotency_key": in.IdempotencyKey,
	}
	if strings.TrimSpace(in.TenantID) != "" {
		payload["tenant_id"] = strings.TrimSpace(in.TenantID)
	}
	if strings.TrimSpace(in.Model) != "" {
		payload["model"] = strings.TrimSpace(in.Model)
	}
	if strings.TrimSpace(in.Size) != "" {
		payload["size"] = strings.TrimSpace(in.Size)
	}
	status, body, err := p.postBilling(ctx, "/internal/v1/billing/unified/usage", payload)
	if err != nil {
		return "", ErrUnavailable
	}
	if billingCode(body) == "billing_route_not_found" {
		return "", ErrUnimplemented
	}
	if status < 200 || status >= 300 {
		return "", ErrUnavailable
	}
	var doc struct {
		Fact struct {
			UsageID string `json:"usage_id"`
		} `json:"fact"`
	}
	if json.Unmarshal(body, &doc) != nil || strings.TrimSpace(doc.Fact.UsageID) == "" {
		return "", ErrUnavailable
	}
	return doc.Fact.UsageID, nil
}

func (p *Live) quoteUsage(ctx context.Context, usageID string) (QuoteFact, error) {
	raw, err := json.Marshal(map[string]string{"usage_id": usageID})
	if err != nil {
		return QuoteFact{}, err
	}
	client := p.consumer(p.BillingToken)
	client.Bases.Billing = strings.TrimRight(p.BillingBase, "/")
	res, err := client.Call(ctx, platformconsumer.Call{Operation: "billing.quote", Body: raw})
	if billingCode(res.Body) == "billing_route_not_found" {
		return QuoteFact{}, ErrUnimplemented
	}
	if err != nil || !res.Sent || res.StatusCode < 200 || res.StatusCode >= 300 {
		return QuoteFact{}, ErrUnavailable
	}
	var doc struct {
		Quote struct {
			AmountMinor int64  `json:"amount_minor"`
			Origin      string `json:"origin"`
		} `json:"quote"`
	}
	if json.Unmarshal(res.Body, &doc) != nil {
		return QuoteFact{}, ErrUnavailable
	}
	return QuoteFact{AmountMinor: doc.Quote.AmountMinor, Currency: "CNY", Origin: strings.TrimSpace(doc.Quote.Origin)}, nil
}

func (p *Live) attachBalance(ctx context.Context, tenantID, accountID, capability string, fact *QuoteFact) {
	query := url.Values{}
	query.Set("account_id", accountID)
	if strings.TrimSpace(tenantID) != "" {
		query.Set("tenant_id", strings.TrimSpace(tenantID))
	}
	status, body, err := p.getBilling(ctx, "/internal/v1/billing/unified/accounts?"+query.Encode())
	if err != nil || status != http.StatusOK {
		return
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil {
		return
	}
	var okFlag bool
	if json.Unmarshal(root["ok"], &okFlag) != nil || !okFlag {
		return
	}
	var snap map[string]json.RawMessage
	if json.Unmarshal(root["snapshot"], &snap) != nil {
		return
	}
	var account struct {
		AccountID          string `json:"account_id"`
		Status             string `json:"status"`
		CashAvailableMinor *int64 `json:"cash_available_minor"`
		CashBalanceMinor   *int64 `json:"cash_balance_minor"`
	}
	if json.Unmarshal(snap["account"], &account) != nil {
		return
	}
	reqAccountID := strings.TrimSpace(accountID)
	snapAccountID := strings.TrimSpace(account.AccountID)
	if reqAccountID != "" && snapAccountID != "" && snapAccountID != reqAccountID {
		return
	}
	snapStatus := strings.TrimSpace(account.Status)
	if snapStatus != "" && snapStatus != "active" {
		return
	}
	var cashMinor int64
	switch {
	case account.CashAvailableMinor != nil:
		cashMinor = *account.CashAvailableMinor
	case account.CashBalanceMinor != nil:
		cashMinor = *account.CashBalanceMinor
	default:
		return
	}
	if cashMinor < 0 {
		return
	}
	appID := strings.TrimSpace(p.AppID)
	targetCapability := strings.TrimSpace(capability)
	nowUnix := p.now().Unix()
	scopedMinor, ok := availableScopedCredits(snap["scoped_credits"], appID, targetCapability, nowUnix)
	if !ok || scopedMinor > math.MaxInt64-cashMinor {
		return
	}

	fact.BalanceKnown = true
	fact.BalanceMinor = cashMinor + scopedMinor
	if !fact.IncludedAllowance && fact.BalanceMinor < fact.AmountMinor {
		fact.FundsShort = true
	}
	raw, ok := snap["entitlements"]
	if !ok {
		return
	}
	trimmedEnt := bytes.TrimSpace(raw)
	if len(trimmedEnt) == 0 || bytes.Equal(trimmedEnt, []byte("null")) {
		return
	}
	var rows []struct {
		AppID          string `json:"app_id"`
		Capability     string `json:"capability"`
		Status         string `json:"status"`
		ValidFromUnix  int64  `json:"valid_from_unix"`
		ValidUntilUnix int64  `json:"valid_until_unix"`
	}
	if json.Unmarshal(trimmedEnt, &rows) != nil {
		return
	}
	fact.EntitlementsKnown = true
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		capName := strings.TrimSpace(row.Capability)
		if capName == "" {
			continue
		}
		if strings.TrimSpace(row.AppID) != appID {
			continue
		}
		if strings.TrimSpace(row.Status) != "active" {
			continue
		}
		if row.ValidFromUnix > nowUnix {
			continue
		}
		if row.ValidUntilUnix < 0 || (row.ValidUntilUnix != 0 && row.ValidUntilUnix < nowUnix) {
			continue
		}
		if _, dup := seen[capName]; dup {
			continue
		}
		seen[capName] = struct{}{}
		fact.Entitlements = append(fact.Entitlements, capName)
	}
}

func availableScopedCredits(raw json.RawMessage, appID, targetCapability string, nowUnix int64) (int64, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return 0, true
	}
	var rows []struct {
		AppID          string `json:"app_id"`
		Capability     string `json:"capability"`
		Status         string `json:"status"`
		AvailableMinor *int64 `json:"available_minor"`
		RemainingMinor *int64 `json:"remaining_minor"`
		ExpiresAtUnix  int64  `json:"expires_at_unix"`
	}
	if json.Unmarshal(trimmed, &rows) != nil {
		return 0, false
	}
	var total int64
	for _, row := range rows {
		if strings.TrimSpace(row.AppID) != appID {
			continue
		}
		if strings.TrimSpace(row.Status) != "active" {
			continue
		}
		if row.ExpiresAtUnix < 0 || (row.ExpiresAtUnix != 0 && row.ExpiresAtUnix < nowUnix) {
			continue
		}
		creditCap := strings.TrimSpace(row.Capability)
		if targetCapability != "" && creditCap != "" && creditCap != targetCapability {
			continue
		}
		var minor int64
		switch {
		case row.AvailableMinor != nil:
			minor = *row.AvailableMinor
		case row.RemainingMinor != nil:
			minor = *row.RemainingMinor
		default:
			continue
		}
		if minor < 0 {
			return 0, false
		}
		if minor > math.MaxInt64-total {
			return 0, false
		}
		total += minor
	}
	return total, true
}

func (p *Live) now() time.Time {
	if p != nil && p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Live) postBilling(ctx context.Context, path string, payload any) (int, []byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.BillingBase, "/")+path, bytes.NewReader(raw))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	p.auth(req)
	return p.do(req)
}

func (p *Live) getBilling(ctx context.Context, path string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.BillingBase, "/")+path, nil)
	if err != nil {
		return 0, nil, err
	}
	p.auth(req)
	return p.do(req)
}

func (p *Live) auth(req *http.Request) {
	req.Header.Set(platformconsumer.HeaderApp, p.AppID)
	req.Header.Set(platformconsumer.HeaderToken, p.BillingToken)
}

func (p *Live) do(req *http.Request) (int, []byte, error) {
	res, err := p.httpClient().Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return res.StatusCode, nil, err
	}
	return res.StatusCode, body, nil
}

func (p *Live) httpClient() *http.Client {
	if p.HTTP != nil {
		return p.HTTP
	}
	return &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func (p *Live) consumer(token string) *platformconsumer.Client {
	return &platformconsumer.Client{
		Caller: platformconsumer.Caller{Mode: "app", AppID: p.AppID, Token: token},
		HTTP:   p.httpClient(),
	}
}

func billingCode(body []byte) string {
	var doc struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &doc)
	return doc.Error.Code
}

func loopbackBase(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	host := u.Hostname()
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		return nil
	}
	return ErrConfigMissing
}
