import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import styles from "@/components/ui/ui.module.css";

export type StartScreenProps = {
  title: string;
  showsCatalog: boolean;
  scope: string;
  steps: string[];
  status: string;
  statusLabel: string;
  outputAssetId: string;
  showImage: boolean;
  quoteLabel: string;
  usefulProven: boolean;
  fidelityLabel: string;
  headline: string;
  uploadRequired: boolean;
  taskId: string;
  sourceLabel?: string;
  returnHref?: string;
  authorizedAssets?: string[];
  // compact: the personal start path. Only the title and the working area; the
  // legacy status badges, step list and developer-facing lines are not shown.
  compact?: boolean;
  children?: React.ReactNode;
};

export function StartScreen(props: StartScreenProps) {
  const assets = props.authorizedAssets ?? [];
  if (props.compact) {
    return (
      <Card
        data-page-pattern="creation"
        data-entry-screen="start"
        data-catalog={props.showsCatalog ? "true" : "false"}
        data-scope={props.scope}
        data-task-id={props.taskId}
        data-task-status={props.status}
        data-fake-output="false"
        data-useful-proven="false"
        data-upload-required={props.uploadRequired ? "true" : "false"}
        data-quote={props.quoteLabel}
      >
        <header data-region="header">
          <h1 className={styles.title} data-testid="start-title">{props.title}</h1>
          <CardDescription>选一张商品照片，写下想要的背景，确认费用后生成。商品本身不会被改动。</CardDescription>
        </header>
        <p className="muted" data-region="progress_context">选照片 → 写背景 → 确认费用 → 生成 → 查看结果</p>
        <div data-region="input_sections">{props.children}</div>
      </Card>
    );
  }
  return (
    <Card
      data-page-pattern="creation"
      data-entry-screen="start"
      data-catalog={props.showsCatalog ? "true" : "false"}
      data-scope={props.scope}
      data-task-id={props.taskId}
      data-task-status={props.status}
      data-fake-output="false"
      data-useful-proven={props.usefulProven ? "true" : "false"}
      data-upload-required={props.uploadRequired ? "true" : "false"}
      data-quote={props.quoteLabel}
    >
      <div className="media-bench">
        <section className="media-materials" aria-label="素材与简报" data-region="input_sections">
          <CardHeader>
            <CardTitle>{props.title}</CardTitle>
            <CardDescription>
              上传商品照片，选定场景和背景方向，确认费用后再生成。这里不是生态目录。
            </CardDescription>
          </CardHeader>
          <p>
            <Badge>范围 {props.scope}</Badge>{" "}
            <Badge variant="warn">{props.quoteLabel}</Badge>{" "}
            <Badge>{props.statusLabel}</Badge>
          </p>
          <ol>
            {props.steps.map((step) => (
              <li key={step}>{step}</li>
            ))}
          </ol>
          {props.sourceLabel ? <p>来源 {props.sourceLabel}</p> : null}
          {assets.length > 0 ? (
            <ul className="asset-strip">
              {assets.map((asset) => (
                <li key={asset}>已授权素材 {asset}</li>
              ))}
            </ul>
          ) : null}
          {props.returnHref ? <a href={props.returnHref}>返回来源 {props.returnHref}</a> : null}
          {props.uploadRequired ? null : (
            <Button type="button" variant="secondary" disabled>
              已带入素材
            </Button>
          )}
        </section>
        <section className="media-stage" aria-label="候选" data-region="media_preview">
          <div className="stage-empty">
            <p className="stage-kicker">候选</p>
            <p data-fidelity={props.fidelityLabel}>主体保真 {props.fidelityLabel}</p>
            <h2>{props.headline}</h2>
            {props.showImage && props.outputAssetId ? <p>候选引用 {props.outputAssetId}</p> : <p>结果会显示在这里。还没有成片时，不会用别的图代替。</p>}
          </div>
        </section>
        <aside className="media-rail" aria-label="费用与下一步" data-region="review_summary">
          {props.children}
        </aside>
      </div>
    </Card>
  );
}
