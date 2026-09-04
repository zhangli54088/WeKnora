package memory

import (
	"context"
	"encoding/json"
	"math"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

func (s *learningProfileService) ExportProfile(ctx context.Context) (*types.LearningProfileExport, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil, err
	}
	logCtx := logger.WithFields(ctx, logger.Fields{"tenant_id": scope.TenantID, "subject_id": scope.SubjectID})
	snapshot, err := s.repo.ExportSnapshot(ctx, scope)
	if err != nil {
		logger.Warn(logCtx, "[learning-profile] export_failed")
		return nil, err
	}
	evidence := make([]*types.LearningEvidenceExport, 0, len(snapshot.Evidence))
	for _, item := range snapshot.Evidence {
		evidence = append(evidence, &types.LearningEvidenceExport{
			ID: item.ID, WikiPageID: item.WikiPageID, EvidenceType: item.EvidenceType,
			Level: item.Level, SourceType: item.SourceType, SourceID: item.SourceID,
			Weight: item.Weight, OccurredAt: item.OccurredAt, Metadata: exportEvidenceMetadata(item.Metadata),
		})
	}
	result := &types.LearningProfileExport{
		Version: 1, ExportedAt: time.Now().UTC(),
		Scope: types.LearningProfileExportScope{TenantID: scope.TenantID, SubjectID: scope.SubjectID},
		Memory: snapshot.Memory,
		LearningProfile: types.LearningProfileDataExport{
			MemoryWikiLinks: snapshot.Links, LearningEvidences: evidence, KnowledgeStates: snapshot.States,
		},
	}
	logger.Info(logger.WithFields(logCtx, logger.Fields{
		"item_count": len(snapshot.Memory.Items), "link_count": len(snapshot.Links),
		"evidence_count": len(evidence), "state_count": len(snapshot.States),
	}), "[learning-profile] export_success")
	return result, nil
}

func (s *learningProfileService) ClearProfile(ctx context.Context) (*types.LearningProfileDeleteResult, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil, err
	}
	logCtx := logger.WithFields(ctx, logger.Fields{"tenant_id": scope.TenantID, "subject_id": scope.SubjectID})
	result, err := s.repo.ClearProfile(ctx, scope)
	if err != nil {
		logger.Warn(logCtx, "[learning-profile] delete_failed")
		return nil, err
	}
	logger.Info(logger.WithFields(logCtx, logger.Fields{
		"memory_wiki_links_deleted": result.MemoryWikiLinksDeleted,
		"learning_evidences_deleted": result.LearningEvidencesDeleted,
		"knowledge_states_deleted": result.KnowledgeStatesDeleted,
	}), "[learning-profile] delete_success")
	return result, nil
}

// Only existing technical provenance is portable. Arbitrary nested metadata,
// prompts, credentials and future fields must never leak into an export.
func exportEvidenceMetadata(metadata types.JSONMap) types.JSONMap {
	result := types.JSONMap{}
	for _, key := range []string{"memory_item_id", "message_id", "session_id", "knowledge_base_id"} {
		if value, ok := metadata[key].(string); ok {
			result[key] = value
		}
	}
	if method, ok := metadata["mapping_method"].(string); ok {
		switch method {
		case types.MemoryWikiMethodChunkRef, types.MemoryWikiMethodSourceRef, types.MemoryWikiMethodManual:
			result["mapping_method"] = method
		}
	}
	for _, key := range []string{"mapping_score", "rank"} {
		var number float64
		switch value := metadata[key].(type) {
		case float64:
			number = value
		case int:
			number = float64(value)
		case json.Number:
			var err error
			number, err = value.Float64()
			if err != nil { continue }
		default:
			continue
		}
		if !math.IsNaN(number) && !math.IsInf(number, 0) { result[key] = number }
	}
	return result
}
