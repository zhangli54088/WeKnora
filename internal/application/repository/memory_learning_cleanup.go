package repository

import (
	"context"
	"database/sql"
	"sort"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func (r *memoryRepository) WithLearningCleanup(
	ctx context.Context, scope interfaces.MemoryScope, itemID string,
	fn func(interfaces.MemoryRepository, interfaces.LearningProfileRepository, []string) error,
) error {
	if !scope.Valid() || fn == nil {
		return gorm.ErrInvalidData
	}
	// Serialization conflicts roll back the whole operation. On PostgreSQL,
	// the repeatable snapshot also excludes items inserted after cleanup began.
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		memoryRepo := &memoryRepository{db: tx}
		items := memoryRepo.scoped(ctx, scope).Select("id").Clauses(clause.Locking{Strength: "UPDATE"})
		links := memoryRepo.scoped(ctx, scope).Model(&types.MemoryWikiLink{})
		if itemID != "" {
			items = items.Where("id = ?", itemID)
			links = links.Where("memory_item_id = ?", itemID)
		}
		// Read before cascading deletes. On PostgreSQL the parent-row lock also
		// serializes FK-backed link insertion against this deletion.
		var lockedItems []*types.MemoryItem
		if err := items.Find(&lockedItems).Error; err != nil {
			return err
		}
		var linkedPages []struct{ WikiPageID string }
		if err := links.Session(&gorm.Session{}).Select("wiki_page_id").Scan(&linkedPages).Error; err != nil {
			return err
		}
		pages := map[string]bool{}
		for _, page := range linkedPages {
			pages[page.WikiPageID] = true
		}

		evidence := memoryRepo.scoped(ctx, scope).Model(&types.LearningEvidence{}).
			Where("source_type = ? AND evidence_type = ?", types.LearningEvidenceSourceMemoryWikiLink, types.LearningEvidenceTypeMemoryLink)
		if itemID != "" {
			// A subquery avoids parameter-count limits for heavily linked items.
			evidence = evidence.Where("source_id IN (?)", links.Session(&gorm.Session{}).Select("id"))
		}
		// Bulk Memory.Clear also repairs pre-existing orphan memory evidence.
		// It never touches chat_message or future evidence source types.
		var evidencePages []string
		if err := evidence.Session(&gorm.Session{}).Distinct("wiki_page_id").Pluck("wiki_page_id", &evidencePages).Error; err != nil {
			return err
		}
		for _, pageID := range evidencePages {
			pages[pageID] = true
		}
		if err := evidence.Delete(&types.LearningEvidence{}).Error; err != nil {
			return err
		}
		affected := make([]string, 0, len(pages))
		for pageID := range pages {
			affected = append(affected, pageID)
		}
		sort.Strings(affected)
		if err := fn(memoryRepo, &learningProfileRepository{db: tx}, affected); err != nil {
			return err
		}
		// Usually already removed by FK cascade; explicit cleanup also supports
		// SQLite connections whose FK enforcement is disabled. Same transaction.
		return links.Delete(&types.MemoryWikiLink{}).Error
	}, &sql.TxOptions{Isolation: sql.LevelSerializable})
}
