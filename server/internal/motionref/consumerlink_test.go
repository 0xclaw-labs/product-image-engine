package motionref

// consumer-link 源端形状测试(R14c:形状+测试;正向腿 NOT_RUN,无 HTTP 出链端点)。

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	sdkmotionref "github.com/bianjiefilm/public-ai/sdk/go/motionref"
)

const linkTestHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func honestDeclaration() Declaration {
	d, err := Declare(AdoptedAsset{AssetID: "asset-1", ContentHash: linkTestHash, RevisionID: "rev-1", Adopted: true})
	if err != nil {
		panic(err)
	}
	d.ID = "mref_testdecl1"
	return d
}

func consumerFacts() ConsumerFacts {
	return ConsumerFacts{
		MotionProjectID: "mp-1", MotionRevisionID: "mpr-1",
		OwnerTenantID: "acct_t1", OwnerAccountID: "acct_t1",
		OriginContextRef: OriginContextRefFor("proj_1", "mref_testdecl1"),
		Base:             "https://goboost.example.invalid/",
	}
}

func TestConsumerLinkShapeAndFiveStorageKeys(t *testing.T) {
	link, err := ConsumerLink(honestDeclaration(), consumerFacts())
	if err != nil {
		t.Fatalf("出链失败: %v", err)
	}
	const param = "motionRef"
	if !strings.HasPrefix(link, "https://goboost.example.invalid/?"+param+"=") {
		t.Fatalf("链接形状: %s", link)
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	token := u.Query().Get(param)
	if token == "" {
		t.Fatal("缺 motionRef 参数")
	}
	// 手工百分号解码(收件方语义:路径不得解码后跳转;这里只验证 payload 形状)。
	decoded, err := url.QueryUnescape(token)
	if err != nil {
		t.Fatalf("token 必须是合法百分号编码: %v", err)
	}
	var stored map[string]any
	if err := json.Unmarshal([]byte(decoded), &stored); err != nil {
		t.Fatalf("payload 必须是 JSON: %v", err)
	}
	// 收件方五存储键,多一字段都会在收件方落空态。
	want := map[string]string{
		"project_id": "mp-1", "revision_id": "mpr-1",
		"origin_app": "product-image",
	}
	for k, v := range want {
		if stored[k] != v {
			t.Fatalf("存储键 %s: got %v want %s", k, stored[k], v)
		}
	}
	if len(stored) != 5 {
		t.Fatalf("必须恰好五个存储键: %v", stored)
	}
	if !strings.HasPrefix(stored["digest"].(string), "sha256:") {
		t.Fatalf("digest 形状: %v", stored["digest"])
	}
	if oc, _ := stored["origin_context_ref"].(string); !strings.HasPrefix(oc, "product-image/projects/proj_1/motion-refs/") {
		t.Fatalf("origin_context_ref: %v", oc)
	}
}

func TestConsumerLinkDigestBindsRef(t *testing.T) {
	link, err := ConsumerLink(honestDeclaration(), consumerFacts())
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(link)
	token, _ := url.QueryUnescape(u.Query().Get("motionRef"))
	var stored struct {
		ProjectID  string `json:"project_id"`
		RevisionID string `json:"revision_id"`
		Digest     string `json:"digest"`
	}
	if err := json.Unmarshal([]byte(token), &stored); err != nil {
		t.Fatal(err)
	}
	// 链内 digest 必须与 Ref 全量声明自洽(ComputeDigest 除外键全等)。
	ref := sdkmotionref.Ref{
		ProjectID: stored.ProjectID, RevisionID: stored.RevisionID,
		Owner:            sdkmotionref.Owner{TenantID: "acct_t1", AccountID: "acct_t1"},
		OriginApp:        "product-image",
		OriginContextRef: OriginContextRefFor("proj_1", "mref_testdecl1"),
		Permissions:      sdkmotionref.Permissions{CanViewRender: true},
		EditableLevel:    sdkmotionref.LevelFlattened,
		AllowedActions:   []string{sdkmotionref.ActionViewRender},
		Version:          1,
	}
	if got := sdkmotionref.ComputeDigest(ref); got != stored.Digest {
		t.Fatalf("digest 未绑定该声明: %s != %s", got, stored.Digest)
	}
}

func TestConsumerLinkRefusesFakeExecution(t *testing.T) {
	// 篡改成"已生成成片/已核验" → 拒绝(不伪造 Motion 执行)。
	forged := honestDeclaration()
	forged.VideoGenerated = true
	if _, err := ConsumerLink(forged, consumerFacts()); err != ErrUnfinishedDeclaration {
		t.Fatalf("伪造执行必须拒绝: %v", err)
	}
	verified := honestDeclaration()
	verified.Verified = true
	if _, err := ConsumerLink(verified, consumerFacts()); err != ErrUnfinishedDeclaration {
		t.Fatalf("伪造核验必须拒绝: %v", err)
	}
	finished := honestDeclaration()
	finished.FinishedCopy = "已生成成片"
	if _, err := ConsumerLink(finished, consumerFacts()); err != ErrUnfinishedDeclaration {
		t.Fatalf("改写文案必须拒绝: %v", err)
	}
}

func TestConsumerLinkRefusesInventedProject(t *testing.T) {
	f := consumerFacts()
	f.MotionProjectID = ""
	if _, err := ConsumerLink(honestDeclaration(), f); err != ErrNoMotionProject {
		t.Fatalf("没有真实 Motion 工程必须拒绝出链: %v", err)
	}
	f2 := consumerFacts()
	f2.MotionRevisionID = ""
	if _, err := ConsumerLink(honestDeclaration(), f2); err != ErrNoMotionProject {
		t.Fatalf("缺修订必须拒绝出链: %v", err)
	}
}

func TestConsumerLinkBaseRules(t *testing.T) {
	bad := []string{
		"https://goboost.example.invalid/?utm=1", // base 不允许带 query
		"https://goboost.example.invalid//x",     // // 开头的协议相对形路径
		"ftp://goboost.example.invalid/",         // scheme 闸
	}
	for _, base := range bad {
		f := consumerFacts()
		f.Base = base
		if link, err := ConsumerLink(honestDeclaration(), f); err == nil {
			t.Fatalf("base %q 必须被拒: %s", base, link)
		}
	}
	// http 仅回环开发态。
	loop := consumerFacts()
	loop.Base = "http://127.0.0.1:3000/p/1/canvases/2"
	if _, err := ConsumerLink(honestDeclaration(), loop); err != nil {
		t.Fatalf("回环 http 开发态应可出链: %v", err)
	}
	nonLoop := consumerFacts()
	nonLoop.Base = "http://goboost.example.invalid/"
	if _, err := ConsumerLink(honestDeclaration(), nonLoop); err == nil {
		t.Fatal("非回环 http 必须拒绝")
	}
}

func TestConsumerLinkIsDeterministic(t *testing.T) {
	a, err := ConsumerLink(honestDeclaration(), consumerFacts())
	if err != nil {
		t.Fatal(err)
	}
	b, err := ConsumerLink(honestDeclaration(), consumerFacts())
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("同声明同事实必须同链: %s vs %s", a, b)
	}
}
