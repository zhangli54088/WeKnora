package repository

import (
	"context"
	"database/sql"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ExportSnapshot uses the profile scope on every personal table. Wiki joins
// only read lightweight identities for already-scoped states, never content.
func (r *learningProfileRepository) ExportSnapshot(
	ctx context.Context, scope interfaces.MemoryScope,
) (*types.LearningProfileSnapshot, error) {
	if !scope.Valid() {
		return nil, gorm.ErrRecordNotFound
	}
	result := &types.LearningProfileSnapshot{
		Memory: types.LearningProfileMemoryExport{
			Items: []*types.MemoryItem{}, Topics: []*types.MemoryTopicStat{}, Documents: []*types.MemoryDocAffinity{},
		},
		Links: []*types.MemoryWikiLink{}, Evidence: []*types.LearningEvidence{}, States: []*types.KnowledgeStateExport{},
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, dest := range []interface{}{&result.Memory.Items, &result.Memory.Topics, &result.Memory.Documents, &result.Links, &result.Evidence} {
			if err := tx.Where("tenant_id = ? AND subject_id = ?", scope.TenantID, scope.SubjectID).
				Order("id ASC").Find(dest).Error; err != nil {
				return err
			}
		}
		return tx.Table("user_knowledge_states AS state").
			Select("state.id, state.wiki_page_id, COALESCE(page.title, '') AS title, COALESCE(page.slug, '') AS slug, COALESCE(page.knowledge_base_id, '') AS knowledge_base_id, state.status, state.confidence, state.evidence_count, state.last_evidence_at, state.created_at, state.updated_at").
			Joins("LEFT JOIN wiki_pages AS page ON page.id = state.wiki_page_id").
			Where("state.tenant_id = ? AND state.subject_id = ?", scope.TenantID, scope.SubjectID).
			Order("state.id ASC").Scan(&result.States).Error
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ClearProfile is separate from Memory.Clear: long-term memory, preferences,
// embeddings, KBs and public Wiki data are intentionally untouched. A later
// real learning event may rebuild the profile; this is not an opt-out.
func (r *learningProfileRepository) ClearProfile(
	ctx context.Context, scope interfaces.MemoryScope,
) (*types.LearningProfileDeleteResult, error) {
	if !scope.Valid() {
		return nil, gorm.ErrRecordNotFound
	}
	result := &types.LearningProfileDeleteResult{}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, deletion := range []struct {
			model interface{}
			count *int64
		}{
			{&types.LearningEvidence{}, &result.LearningEvidencesDeleted},
			{&types.UserKnowledgeState{}, &result.KnowledgeStatesDeleted},
			{&types.MemoryWikiLink{}, &result.MemoryWikiLinksDeleted},
		} {
			deleted := tx.Where("tenant_id = ? AND subject_id = ?", scope.TenantID, scope.SubjectID).Delete(deletion.model)
			if deleted.Error != nil {
				return deleted.Error
			}
			*deletion.count = deleted.RowsAffected
		}
		return nil
	})
	if err != nil {
		return nil, err // Never report counts from a rolled-back transaction.
	}
	return result, nil
}
