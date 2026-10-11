-- HUI-2388 source-side record of matrix persistent drafts created at the real
-- destination (public-ai matrixdraft @ d8facafc). One row per selected output
-- version. The row records the exact facts that were sent (facts_json) plus the
-- per-version quality/license facts; license facts are write-once per version
-- (mismatch is refused upstream, never silently rewritten).
-- GET/read-back never inserts a row.

CREATE TABLE matrix_draft_refs (
    id                 TEXT PRIMARY KEY,
    tenant_scope       TEXT NOT NULL,
    project_id         TEXT NOT NULL REFERENCES projects(id),
    output_id          TEXT NOT NULL REFERENCES project_outputs(id),
    draft_id           TEXT NOT NULL,
    license_ref        TEXT NOT NULL,
    license_expires_at TEXT NULL,
    quality_verdict    TEXT NOT NULL,
    brief_version      TEXT NOT NULL,
    source_revision    TEXT NOT NULL,
    asset_sha256       TEXT NOT NULL,
    facts_json         TEXT NOT NULL,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL,
    UNIQUE (tenant_scope, project_id, output_id)
);
