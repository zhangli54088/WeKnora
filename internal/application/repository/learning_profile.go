package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type learningProfileRepository struct {
	db *gorm.DB
}

func NewLearningProfileRepository(db *gorm.DB) interfaces.LearningProfileRepository {
	return &learningProfileRepository{db: db}
}

func (r *learningProfileRepository) InTransaction(
	ctx context.Context, fn func(interfaces.LearningProfileRepository) error,
) error {
	if fn == nil {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&learningProfileRepository{db: tx})
	})
}

func (r *learningProfileRepository) UpsertEvidence(
	ctx context.Context,
	scope interfaces.MemoryScope,
	evidence *types.LearningEvidence,
) (*types.LearningEvidence, error) {
	if !scope.Valid() || evidence == nil || evidence.WikiPageID == "" ||
		evidence.EvidenceType == "" || evidence.Level == "" ||
		evidence.SourceType == "" || evidence.SourceID == "" {
		return nil, gorm.ErrRecordNotFound
	}

	var pageCount int64
	if err := r.db.WithContext(ctx).Model(&types.WikiPage{}).
		Where("id = ? AND tenant_id = ?", evidence.WikiPageID, scope.TenantID).
		Count(&pageCount).Error; err != nil {
		return nil, err
	}
	if pageCount != 1 {
		return nil, gorm.ErrRecordNotFound
	}

	now := time.Now()
	if evidence.ID == "" {
		evidence.ID = uuid.New().String()
	}
	evidence.TenantID = scope.TenantID
	evidence.SubjectID = scope.SubjectID
	if evidence.OccurredAt.IsZero() {
		evidence.OccurredAt = now
	}
	evidence.CreatedAt = now
	evidence.UpdatedAt = now
	if evidence.Metadata == nil {
		evidence.Metadata = types.JSONMap{}
	}

	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "tenant_id"}, {Name: "subject_id"}, {Name: "source_type"},
			{Name: "source_id"}, {Name: "wiki_page_id"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"evidence_type", "level", "weight", "metadata", "occurred_at", "updated_at",
		}),
	}).Create(evidence).Error; err != nil {
		return nil, err
	}

	var stored types.LearningEvidence
	err := r.db.WithContext(ctx).Where(
		"tenant_id = ? AND subject_id = ? AND source_type = ? AND source_id = ? AND wiki_page_id = ?",
		scope.TenantID, scope.SubjectID, evidence.SourceType, evidence.SourceID, evidence.WikiPageID,
	).First(&stored).Error
	if err != nil {
		return nil, err
	}
	return &stored, nil
}

func (r *learningProfileRepository) ListEvidence(
	ctx context.Context, scope interfaces.MemoryScope, wikiPageID string,
) ([]*types.LearningEvidence, error) {
	if !scope.Valid() {
		return []*types.LearningEvidence{}, nil
	}
	var evidence []*types.LearningEvidence
	query := r.db.WithContext(ctx).
		Where("tenant_id = ? AND subject_id = ?", scope.TenantID, scope.SubjectID)
	if wikiPageID != "" {
		query = query.Where("wiki_page_id = ?", wikiPageID)
	}
	err := query.Order("occurred_at DESC, id ASC").Find(&evidence).Error
	if evidence == nil {
		evidence = []*types.LearningEvidence{}
	}
	return evidence, err
}

func (r *learningProfileRepository) DeleteEvidenceBySource(
	ctx context.Context,
	scope interfaces.MemoryScope,
	sourceType, sourceID, wikiPageID string,
) (int64, error) {
	if !scope.Valid() || sourceType == "" || sourceID == "" {
		return 0, nil
	}
	query := r.db.WithContext(ctx).Where(
		"tenant_id = ? AND subject_id = ? AND source_type = ? AND source_id = ?",
		scope.TenantID, scope.SubjectID, sourceType, sourceID,
	)
	if wikiPageID != "" {
		query = query.Where("wiki_page_id = ?", wikiPageID)
	}
	result := query.Delete(&types.LearningEvidence{})
	return result.RowsAffected, result.Error
}

func (r *learningProfileRepository) UpsertKnowledgeState(
	ctx context.Context,
	scope interfaces.MemoryScope,
	state *types.UserKnowledgeState,
) (*types.UserKnowledgeState, error) {
	if !scope.Valid() || state == nil || state.WikiPageID == "" || state.Status == "" {
		return nil, gorm.ErrRecordNotFound
	}

	var pageCount int64
	if err := r.db.WithContext(ctx).Model(&types.WikiPage{}).
		Where("id = ? AND tenant_id = ?", state.WikiPageID, scope.TenantID).
		Count(&pageCount).Error; err != nil {
		return nil, err
	}
	if pageCount != 1 {
		return nil, gorm.ErrRecordNotFound
	}

	now := time.Now()
	if state.ID == "" {
		state.ID = uuid.New().String()
	}
	state.TenantID = scope.TenantID
	state.SubjectID = scope.SubjectID
	state.CreatedAt = now
	state.UpdatedAt = now
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "tenant_id"}, {Name: "subject_id"}, {Name: "wiki_page_id"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"status", "confidence", "evidence_count", "last_evidence_at", "updated_at",
		}),
	}).Create(state).Error; err != nil {
		return nil, err
	}

	var stored types.UserKnowledgeState
	err := r.db.WithContext(ctx).Where(
		"tenant_id = ? AND subject_id = ? AND wiki_page_id = ?",
		scope.TenantID, scope.SubjectID, state.WikiPageID,
	).First(&stored).Error
	if err != nil {
		return nil, err
	}
	return &stored, nil
}

func (r *learningProfileRepository) DeleteKnowledgeState(
	ctx context.Context, scope interfaces.MemoryScope, wikiPageID string,
) (bool, error) {
	if !scope.Valid() || wikiPageID == "" {
		return false, nil
	}
	result := r.db.WithContext(ctx).Where(
		"tenant_id = ? AND subject_id = ? AND wiki_page_id = ?",
		scope.TenantID, scope.SubjectID, wikiPageID,
	).Delete(&types.UserKnowledgeState{})
	return result.RowsAffected > 0, result.Error
}

func (r *learningProfileRepository) GetKnowledgeState(
	ctx context.Context, scope interfaces.MemoryScope, wikiPageID string,
) (*types.UserKnowledgeState, error) {
	if !scope.Valid() || wikiPageID == "" {
		return nil, nil
	}
	var state types.UserKnowledgeState
	err := r.db.WithContext(ctx).Where(
		"tenant_id = ? AND subject_id = ? AND wiki_page_id = ?",
		scope.TenantID, scope.SubjectID, wikiPageID,
	).First(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &state, nil
}

func (r *learningProfileRepository) ListKnowledgeStates(
	ctx context.Context, scope interfaces.MemoryScope,
) ([]*types.UserKnowledgeState, error) {
	if !scope.Valid() {
		return []*types.UserKnowledgeState{}, nil
	}
	var states []*types.UserKnowledgeState
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND subject_id = ?", scope.TenantID, scope.SubjectID).
		Order("updated_at DESC, id ASC").
		Find(&states).Error
	if states == nil {
		states = []*types.UserKnowledgeState{}
	}
	return states, err
}
