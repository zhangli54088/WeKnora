package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type memoryWikiRepository struct {
	db *gorm.DB
}

// NewMemoryWikiRepository creates the relation repository.
func NewMemoryWikiRepository(db *gorm.DB) interfaces.MemoryWikiRepository {
	return &memoryWikiRepository{db: db}
}

// UpsertLink verifies both endpoints inside the transaction before writing.
// The checks are repeated here even though the service already validates them,
// so a future caller cannot bypass the tenant and subject boundary.
func (r *memoryWikiRepository) UpsertLink(
	ctx context.Context, scope interfaces.MemoryScope, link *types.MemoryWikiLink,
) (*types.MemoryWikiLink, error) {
	if !scope.Valid() || link == nil || link.MemoryItemID == "" ||
		link.WikiPageID == "" || link.KnowledgeBaseID == "" {
		return nil, gorm.ErrRecordNotFound
	}

	var stored types.MemoryWikiLink
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var memoryCount int64
		if err := tx.Model(&types.MemoryItem{}).
			Where("id = ? AND tenant_id = ? AND subject_id = ?",
				link.MemoryItemID, scope.TenantID, scope.SubjectID).
			Count(&memoryCount).Error; err != nil {
			return err
		}
		if memoryCount != 1 {
			return gorm.ErrRecordNotFound
		}

		var pageCount int64
		if err := tx.Model(&types.WikiPage{}).
			Where("id = ? AND tenant_id = ? AND knowledge_base_id = ? AND status <> ?",
				link.WikiPageID, scope.TenantID, link.KnowledgeBaseID, types.WikiPageStatusArchived).
			Count(&pageCount).Error; err != nil {
			return err
		}
		if pageCount != 1 {
			return gorm.ErrRecordNotFound
		}

		now := time.Now()
		link.ID = uuid.New().String()
		link.TenantID = scope.TenantID
		link.SubjectID = scope.SubjectID
		link.CreatedAt = now
		link.UpdatedAt = now
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "tenant_id"}, {Name: "subject_id"},
				{Name: "memory_item_id"}, {Name: "wiki_page_id"},
			},
			DoUpdates: clause.AssignmentColumns([]string{
				"knowledge_base_id", "score", "method", "updated_at",
			}),
		}).Create(link).Error; err != nil {
			return err
		}

		return tx.Where(
			"tenant_id = ? AND subject_id = ? AND memory_item_id = ? AND wiki_page_id = ?",
			scope.TenantID, scope.SubjectID, link.MemoryItemID, link.WikiPageID,
		).First(&stored).Error
	})
	if err != nil {
		return nil, err
	}
	return &stored, nil
}

func (r *memoryWikiRepository) ListLinks(
	ctx context.Context, scope interfaces.MemoryScope,
) ([]*types.MemoryWikiLink, error) {
	if !scope.Valid() {
		return nil, nil
	}
	var links []*types.MemoryWikiLink
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND subject_id = ?", scope.TenantID, scope.SubjectID).
		Order("updated_at DESC, id ASC").
		Find(&links).Error
	return links, err
}

func (r *memoryWikiRepository) DeleteLink(
	ctx context.Context, scope interfaces.MemoryScope, id string,
) (bool, error) {
	if !scope.Valid() || id == "" {
		return false, nil
	}
	result := r.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ? AND subject_id = ?", id, scope.TenantID, scope.SubjectID).
		Delete(&types.MemoryWikiLink{})
	return result.RowsAffected > 0, result.Error
}

func (r *memoryWikiRepository) GetWikiPage(
	ctx context.Context, tenantID uint64, knowledgeBaseID, pageID string,
) (*types.WikiPage, error) {
	var page types.WikiPage
	query := r.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ? AND status <> ?",
			pageID, tenantID, types.WikiPageStatusArchived)
	if knowledgeBaseID != "" {
		query = query.Where("knowledge_base_id = ?", knowledgeBaseID)
	}
	err := query.First(&page).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &page, nil
}

// ListWikiPagesBySourceRefs batch-loads the lightweight Wiki fields needed by
// candidate projection. PostgreSQL keeps its indexed JSONB containment path;
// SQLite uses the JSON text representation written by StringArray.
func (r *memoryWikiRepository) ListWikiPagesBySourceRefs(
	ctx context.Context, tenantID uint64, knowledgeBaseID string, knowledgeIDs []string,
) ([]*types.WikiPage, error) {
	if tenantID == 0 || knowledgeBaseID == "" || len(knowledgeIDs) == 0 {
		return nil, nil
	}

	clauses := make([]string, 0, len(knowledgeIDs))
	args := make([]interface{}, 0, len(knowledgeIDs)*2)
	postgres := r.db.Dialector.Name() == "postgres"
	for _, knowledgeID := range knowledgeIDs {
		if knowledgeID == "" {
			continue
		}
		if postgres {
			needle, err := json.Marshal([]string{knowledgeID})
			if err != nil {
				return nil, fmt.Errorf("marshal source ref needle: %w", err)
			}
			prefix, err := json.Marshal(knowledgeID + "|")
			if err != nil {
				return nil, fmt.Errorf("marshal source ref prefix: %w", err)
			}
			prefixText := string(prefix)
			if len(prefixText) >= 2 && prefixText[len(prefixText)-1] == '"' {
				prefixText = prefixText[:len(prefixText)-1]
			}
			clauses = append(clauses, "(source_refs @> ?::jsonb OR source_refs::text LIKE ?)")
			args = append(args, string(needle), "%"+escapeLikePattern(prefixText)+"%")
			continue
		}

		encoded, err := json.Marshal(knowledgeID)
		if err != nil {
			return nil, fmt.Errorf("marshal source ref: %w", err)
		}
		bare := escapeLikePattern(string(encoded))
		prefix := strings.TrimSuffix(bare, `"`) + "|"
		clauses = append(clauses, "(CAST(source_refs AS TEXT) LIKE ? OR CAST(source_refs AS TEXT) LIKE ?)")
		args = append(args, "%"+bare+"%", "%"+prefix+"%")
	}
	if len(clauses) == 0 {
		return nil, nil
	}

	var pages []*types.WikiPage
	err := r.db.WithContext(ctx).
		Model(&types.WikiPage{}).
		Select("id", "tenant_id", "knowledge_base_id", "slug", "title", "page_type", "status", "source_refs", "chunk_refs").
		Where("tenant_id = ? AND knowledge_base_id = ? AND status <> ?",
			tenantID, knowledgeBaseID, types.WikiPageStatusArchived).
		Where("("+strings.Join(clauses, " OR ")+")", args...).
		Find(&pages).Error
	return pages, err
}
