package motionref

// HUI-2746 Done 残差承接(AG05-HANDOFF,R14c):消费 sdk/go motionref v0.4.0
// (tag sdk/go/v0.4.0 → 6065fba6,consumer-link 契约)做源端形状。
//
// 红线:
//   - 不伪造 Motion 执行:只有 Intact 的诚实声明(还没生成成片)才可出链;
//     Motion 真实执行/渲染归 AG09,本切片没有真实 Motion 工程,故不挂任何
//     HTTP 出链端点、不为真实流量签发链接——正向腿 NOT_RUN;
//   - MotionProjectID 必须由调用方供给(未来 AG09 执行产物),空即拒绝,
//     绝不代填、绝不编造;
//   - digest 双域分明:pie 声明 digest(product-motion-ref/v1 域)不进 Ref;
//     Ref.Digest 由 sdk ComputeDigest 绑定完整 13 键声明,签名自洽才出链;
//   - 链接是引用声明,不是凭据:不授权、不证明原工程存在、不改付费方判定。

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"

	sdkmotionref "github.com/bianjiefilm/public-ai/sdk/go/motionref"
)

var (
	// ErrNoMotionProject 没有真实 Motion 工程就不出链(不伪造执行)。
	ErrNoMotionProject = errors.New("motionref: no real motion project to reference; execution belongs to AG09")
	// ErrUnfinishedDeclaration 声明必须仍是诚实未完成态。
	ErrUnfinishedDeclaration = errors.New("motionref: declaration must be an honest unfinished reference")
)

// ConsumerFacts 是出链所需的消费方事实(全部由调用方供给,本包不发明)。
type ConsumerFacts struct {
	// MotionProjectID/MotionRevisionID 是 Motion Studio 侧真实工程/修订。
	// 本切片没有:空即 ErrNoMotionProject。
	MotionProjectID  string
	MotionRevisionID string
	OwnerTenantID    string
	OwnerAccountID   string
	// OriginContextRef 是源端定位串(opaque ≤256B,建议含产品图工程与声明 ID)。
	OriginContextRef string
	// Base 是消费方面板地址(https 恒可;http 仅回环开发态;不带 query)。
	Base string
}

// ConsumerLink 把一条诚实的产品图 Motion 引用声明组装成消费方到站深链
// <base>?motionRef=<percent-encoded JSON>(五存储键,收件权威在消费方前端)。
func ConsumerLink(d Declaration, f ConsumerFacts) (string, error) {
	if err := d.Intact(); err != nil {
		return "", ErrUnfinishedDeclaration
	}
	if f.MotionProjectID == "" || f.MotionRevisionID == "" {
		return "", ErrNoMotionProject
	}
	originApp := "product-image"
	ref := sdkmotionref.Ref{
		ProjectID:        f.MotionProjectID,
		RevisionID:       f.MotionRevisionID,
		Owner:            sdkmotionref.Owner{TenantID: f.OwnerTenantID, AccountID: f.OwnerAccountID},
		OriginApp:        originApp,
		OriginContextRef: f.OriginContextRef,
		// 诚实最小授权:只给看渲染;flattened 档禁止 open/param/creative/export(R3)。
		Permissions:    sdkmotionref.Permissions{CanViewRender: true},
		EditableLevel:  sdkmotionref.LevelFlattened,
		AllowedActions: []string{sdkmotionref.ActionViewRender},
		Version:        1,
	}
	// 签名自洽:digest 由 sdk 域全量绑定;与 pie 声明 digest(product-motion-ref/v1 域)无关。
	ref.Digest = sdkmotionref.ComputeDigest(ref)
	return sdkmotionref.BuildConsumerHandoffLink(f.Base, ref)
}

// OriginContextRefFor 生成源端定位串(确定性,含工程与声明 ID)。
func OriginContextRefFor(projectID, declarationID string) string {
	sum := sha256.Sum256([]byte(projectID + "\x1f" + declarationID))
	return "product-image/projects/" + projectID + "/motion-refs/" + declarationID + "#" + hex.EncodeToString(sum[:])[:8]
}
