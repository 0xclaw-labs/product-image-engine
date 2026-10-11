package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// MatrixDraftRef 是源端对真实矩阵草稿的登记(一次选定输出版本最多一条)。
// 它不是发布声明;draft 的 published/charged 由目的端恒为 false。
type MatrixDraftRef struct {
	ID               string    `json:"id"`
	TenantScope      string    `json:"tenant_scope"`
	ProjectID        string    `json:"project_id"`
	OutputID         string    `json:"output_id"`
	DraftID          string    `json:"draft_id"`
	LicenseRef       string    `json:"license_ref"`
	LicenseExpiresAt string    `json:"license_expires_at,omitempty"`
	QualityVerdict   string    `json:"quality_verdict"`
	BriefVersion     string    `json:"brief_version"`
	SourceRevision   string    `json:"source_revision"`
	AssetSHA256      string    `json:"asset_sha256"`
	FactsJSON        string    `json:"facts_json"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

const matrixDraftRefCols = `id, tenant_scope, project_id, output_id, draft_id, license_ref, license_expires_at,
	quality_verdict, brief_version, source_revision, asset_sha256, facts_json, created_at, updated_at`

func scanMatrixDraftRef(row interface{ Scan(...any) error }) (MatrixDraftRef, error) {
	var v MatrixDraftRef
	var lic sql.NullString
	var created, updated string
	if err := row.Scan(&v.ID, &v.TenantScope, &v.ProjectID, &v.OutputID, &v.DraftID, &v.LicenseRef, &lic,
		&v.QualityVerdict, &v.BriefVersion, &v.SourceRevision, &v.AssetSHA256, &v.FactsJSON, &created, &updated); err != nil {
		return MatrixDraftRef{}, err
	}
	if lic.Valid {
		v.LicenseExpiresAt = lic.String
	}
	v.CreatedAt, _ = time.Parse(time.RFC3339, created)
	v.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	return v, nil
}

// GetMatrixDraftRef 按 (租户, 工程, 输出) 读取登记。没有就是 ErrNotFound(不创建)。
func (s *Store) GetMatrixDraftRef(ctx context.Context, tenantScope, projectID, outputID string) (MatrixDraftRef, error) {
	v, err := scanMatrixDraftRef(s.db.QueryRowContext(ctx,
		`SELECT `+matrixDraftRefCols+` FROM matrix_draft_refs WHERE tenant_scope=? AND project_id=? AND output_id=?`,
		tenantScope, projectID, outputID))
	if errors.Is(err, sql.ErrNoRows) {
		return MatrixDraftRef{}, ErrNotFound
	}
	if err != nil {
		return MatrixDraftRef{}, fmt.Errorf("store: 查询矩阵草稿登记失败: %w", err)
	}
	return v, nil
}

// ListMatrixDraftRefs 列出工程下的全部矩阵草稿登记(旧→新)。
func (s *Store) ListMatrixDraftRefs(ctx context.Context, tenantScope, projectID string) ([]MatrixDraftRef, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+matrixDraftRefCols+` FROM matrix_draft_refs WHERE tenant_scope=? AND project_id=? ORDER BY created_at, id`,
		tenantScope, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: 列出矩阵草稿登记失败: %w", err)
	}
	defer rows.Close()
	out := []MatrixDraftRef{}
	for rows.Next() {
		v, err := scanMatrixDraftRef(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// PutMatrixDraftRef 落一条登记。同 output 已有登记时:
//   - license 事实(license_ref + license_expires_at)一致 → 原样返回既有(same=true);
//   - license 事实不一致 → ErrConflict(许可必须随版本保持,不得静默改写)。
func (s *Store) PutMatrixDraftRef(ctx context.Context, ref MatrixDraftRef) (MatrixDraftRef, bool, error) {
	if ref.TenantScope == "" || ref.ProjectID == "" || ref.OutputID == "" || ref.DraftID == "" ||
		ref.LicenseRef == "" || ref.QualityVerdict == "" || ref.AssetSHA256 == "" || ref.FactsJSON == "" {
		return MatrixDraftRef{}, false, fmt.Errorf("%w: 矩阵草稿登记字段不完整", ErrValidation)
	}
	existing, err := s.GetMatrixDraftRef(ctx, ref.TenantScope, ref.ProjectID, ref.OutputID)
	if err == nil {
		if existing.LicenseRef == ref.LicenseRef && existing.LicenseExpiresAt == ref.LicenseExpiresAt {
			return existing, true, nil
		}
		return MatrixDraftRef{}, false, fmt.Errorf("%w: 同一输出版本的许可事实不得改写", ErrConflict)
	}
	if !errors.Is(err, ErrNotFound) {
		return MatrixDraftRef{}, false, err
	}
	ref.ID = newID("mdr")
	now := Now().Format(time.RFC3339)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO matrix_draft_refs (`+matrixDraftRefCols+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		ref.ID, ref.TenantScope, ref.ProjectID, ref.OutputID, ref.DraftID, ref.LicenseRef,
		nullableText(ref.LicenseExpiresAt), ref.QualityVerdict, ref.BriefVersion, ref.SourceRevision,
		ref.AssetSHA256, ref.FactsJSON, now, now); err != nil {
		return MatrixDraftRef{}, false, fmt.Errorf("store: 写入矩阵草稿登记失败: %w", err)
	}
	got, gerr := s.GetMatrixDraftRef(ctx, ref.TenantScope, ref.ProjectID, ref.OutputID)
	return got, false, gerr
}

func nullableText(s string) any {
	if s == "" {
		return nil
	}
	return s
}
