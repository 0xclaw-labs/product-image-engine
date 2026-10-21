import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { estimateShown, topupHref, type CompletionLevels } from "@/lib/consume";

export type ConsumePanelProps = {
  payerDisplay: string;
  presentation: string;
  generateAllowed: boolean;
  mustRequote: boolean;
  topup?: { entry: string; returnTarget: string; quoteRef: string; billingCenterUrl?: string } | null;
  levels: CompletionLevels;
  connection?: string;
  settlement?: string;
  fundsState?: string;
  quoteOrigin?: string;
  quoted?: boolean;
  onReconfirm?: () => void;
};

export function ConsumePanel(props: ConsumePanelProps) {
  const topup = props.topup;
  const presentation = estimateShown(props.presentation, props.quoted);
  const href = topup ? topupHref(topup.billingCenterUrl, topup.returnTarget, topup.quoteRef) : null;
  return (
    <section
      data-consume-panel="true"
      data-generate={props.generateAllowed ? "true" : "false"}
      data-funds-state={props.fundsState || ""}
      data-quote-origin={props.quoteOrigin || ""}
      data-billing-pass={props.levels.billing}
      data-payer-source="server"
    >
      <p>
        付款主体 <Badge>{props.payerDisplay}</Badge>
      </p>
      <p>
        报价 <Badge variant="warn">{presentation}</Badge>
      </p>
      {props.fundsState ? <p>资金状态 {props.fundsState}</p> : null}
      {props.connection ? <p>连接 {props.connection}</p> : null}
      {props.settlement ? <p>结算 {props.settlement}</p> : null}
      <p>生成完成不等于已结算。</p>
      {props.mustRequote ? (
        <Button type="button" variant="secondary" onClick={props.onReconfirm}>
          重新确认报价
        </Button>
      ) : null}
      {topup ? (
        <div data-testid="topup-handoff" data-billing-entry={topup.entry}>
          <p>余额不足。需要先充值；充值返回后必须重新确认报价，才能生成。</p>
          {href ? (
            <p>
              <a className="link" href={href} rel="noreferrer" referrerPolicy="no-referrer" data-testid="billing-center-link">
                前往统一充值
              </a>
            </p>
          ) : (
            <p data-testid="billing-center-unregistered">
              本环境未登记统一充值页地址，不能在这里直接打开。请从平台账户中心完成充值后回到本页；不会在本地新建钱包。
            </p>
          )}
          <p className="muted">
            入口 <code>{topup.entry}</code> · 返回位置 <code>{topup.returnTarget}</code> · 报价引用 <code>{topup.quoteRef}</code>
          </p>
        </div>
      ) : null}
      <p>
        完成等级 {props.levels.implementationReady} / {props.levels.integration} / {props.levels.billing} /{" "}
        {props.levels.channelBilling} / {props.levels.productionAuthorized}
      </p>
    </section>
  );
}
