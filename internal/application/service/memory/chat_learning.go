package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

const (
	chatLearningCandidateTopK = 3
	chatLearningMaxKBsPerTurn = 8
)

type chatLearningService struct {
	messageRepo interfaces.MessageRepository
	tenantRepo  interfaces.TenantRepository
	sessionRepo interfaces.SessionRepository
	mapper      interfaces.MemoryWikiService
	profile     interfaces.LearningProfileService
	enqueuer    interfaces.TaskEnqueuer
}

func NewChatLearningService(
	messageRepo interfaces.MessageRepository,
	tenantRepo interfaces.TenantRepository,
	sessionRepo interfaces.SessionRepository,
	mapper interfaces.MemoryWikiService,
	profile interfaces.LearningProfileService,
	enqueuer interfaces.TaskEnqueuer,
) interfaces.ChatLearningService {
	return &chatLearningService{
		messageRepo: messageRepo,
		tenantRepo:  tenantRepo,
		sessionRepo: sessionRepo,
		mapper:      mapper,
		profile:     profile,
		enqueuer:    enqueuer,
	}
}

// ScheduleChatTurn enqueues a durable best-effort projection. The prompt is
// intentionally absent from the payload; the worker reloads the persisted user
// message by its stable database ID.
func (s *chatLearningService) ScheduleChatTurn(
	ctx context.Context, sessionID, messageID string, knowledgeBaseIDs []string,
) {
	logCtx := logger.WithFields(ctx, logger.Fields{
		"session_id": sessionID, "message_id": messageID, "input_kb_count": len(knowledgeBaseIDs),
	})
	logger.Info(logCtx, "[chat-learning] schedule_start")
	scope, err := ResolveScope(ctx)
	if err != nil {
		logger.WarnWithFields(logCtx, logger.Fields{
			"skip_reason": "resolve_scope_failed", "error": err.Error(),
		}, "[chat-learning] schedule_skip")
		return
	}
	logCtx = logger.WithFields(logCtx, logger.Fields{
		"tenant_id": scope.TenantID, "subject_id": scope.SubjectID,
	})
	if strings.TrimSpace(sessionID) == "" {
		logger.Info(logger.WithField(logCtx, "skip_reason", "empty_session_id"), "[chat-learning] schedule_skip")
		return
	}
	if strings.TrimSpace(messageID) == "" {
		logger.Info(logger.WithField(logCtx, "skip_reason", "empty_message_id"), "[chat-learning] schedule_skip")
		return
	}
	kbIDs := normalizeChatLearningKBIDs(knowledgeBaseIDs)
	if len(kbIDs) == 0 {
		logger.Info(logger.WithField(logCtx, "skip_reason", "no_normalized_kbs"), "[chat-learning] schedule_skip")
		return
	}
	principal, ok := types.PrincipalFromContext(ctx)
	if !ok {
		logger.Info(logger.WithField(logCtx, "skip_reason", "principal_missing"), "[chat-learning] schedule_skip")
		return
	}
	if principal.StorageID() != scope.SubjectID {
		logger.Info(logger.WithField(logCtx, "skip_reason", "principal_scope_mismatch"), "[chat-learning] schedule_skip")
		return
	}
	if s.enqueuer == nil {
		logger.WarnWithFields(logCtx, logger.Fields{
			"skip_reason": "enqueuer_missing",
		}, "[chat-learning] schedule_skip")
		return
	}
	payload := types.ChatLearningPayload{
		TenantID:         scope.TenantID,
		SubjectID:        scope.SubjectID,
		PrincipalType:    principal.Type,
		PrincipalID:      principal.ID,
		SessionID:        strings.TrimSpace(sessionID),
		MessageID:        strings.TrimSpace(messageID),
		KnowledgeBaseIDs: kbIDs,
		Language:         types.LanguageNameFromContext(ctx),
	}
	langfuse.InjectTracing(ctx, &payload)
	body, err := json.Marshal(payload)
	if err != nil {
		logger.WarnWithFields(ctx, logger.Fields{
			"tenant_id": scope.TenantID, "subject_id": scope.SubjectID,
			"session_id": sessionID, "message_id": messageID,
			"knowledge_base_ids": kbIDs, "error": err.Error(),
		}, "[chat-learning] marshal_failed")
		return
	}
	if _, err := s.enqueuer.Enqueue(
		asynq.NewTask(types.TypeChatLearningProfile, body),
		asynq.Queue(types.QueueMemory),
		asynq.MaxRetry(2),
	); err != nil {
		logger.WarnWithFields(ctx, logger.Fields{
			"tenant_id": scope.TenantID, "subject_id": scope.SubjectID,
			"session_id": sessionID, "message_id": messageID,
			"knowledge_base_ids": kbIDs, "error": err.Error(),
		}, "[chat-learning] enqueue_failed")
	} else {
		logger.Info(logger.WithField(logCtx, "kb_count", len(kbIDs)), "[chat-learning] enqueue_success")
	}
}

// Handle restores the authenticated scope carried by the task and loads only
// the referenced durable user message. Assistant messages are always ignored.
func (s *chatLearningService) Handle(ctx context.Context, task *asynq.Task) error {
	var payload types.ChatLearningPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		logger.WarnWithFields(ctx, logger.Fields{"error": err.Error()}, "[chat-learning] payload_decode_failed")
		return fmt.Errorf("unmarshal chat learning payload: %w", err)
	}
	logCtx := logger.WithFields(ctx, logger.Fields{
		"tenant_id": payload.TenantID, "subject_id": payload.SubjectID,
		"session_id": payload.SessionID, "message_id": payload.MessageID,
		"kb_count": len(payload.KnowledgeBaseIDs),
	})
	logger.Info(logCtx, "[chat-learning] worker_start")
	principal := types.Principal{Type: payload.PrincipalType, ID: payload.PrincipalID}.Normalize()
	if payload.TenantID == 0 || !principal.Valid() || principal.StorageID() != payload.SubjectID {
		logger.WarnWithFields(logCtx, logger.Fields{
			"skip_reason": "invalid_task_scope",
		}, "[chat-learning] worker_skip")
		return nil
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	ctx = types.WithPrincipal(ctx, principal)
	if payload.Language != "" {
		ctx = context.WithValue(ctx, types.LanguageContextKey, payload.Language)
	}
	// Follow the summary-refresh worker pattern: reload current tenant settings
	// instead of serializing them in the task or reusing a resource-tenant context.
	if s.tenantRepo == nil {
		logger.WarnWithFields(logCtx, logger.Fields{
			"skip_reason": "tenant_context_restore_failed",
		}, "[chat-learning] tenant_context_restore_failed")
		return fmt.Errorf("chat learning tenant repository is unavailable")
	}
	tenant, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		logger.WarnWithFields(logCtx, logger.Fields{
			"skip_reason": "tenant_context_restore_failed", "error": err.Error(),
		}, "[chat-learning] tenant_context_restore_failed")
		return fmt.Errorf("load chat learning tenant: %w", err)
	}
	if tenant == nil {
		logger.WarnWithFields(logCtx, logger.Fields{
			"skip_reason": "tenant_not_found",
		}, "[chat-learning] tenant_context_restore_failed")
		return fmt.Errorf("chat learning tenant %d not found", payload.TenantID)
	}
	if tenant.ID != payload.TenantID {
		logger.WarnWithFields(logCtx, logger.Fields{
			"skip_reason": "invalid_task_scope",
		}, "[chat-learning] worker_skip")
		return nil
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)
	logger.Info(logCtx, "[chat-learning] tenant_context_restored")

	// GetMessage is scoped only by session/message IDs. Bind the durable session
	// to the profile tenant before loading its content; a KB's owner is not the
	// profile owner. Principal/subject consistency is checked above.
	if s.sessionRepo == nil {
		logger.WarnWithFields(logCtx, logger.Fields{
			"skip_reason": "session_load_failed",
		}, "[chat-learning] session_load_failed")
		return fmt.Errorf("chat learning session repository is unavailable")
	}
	session, err := s.sessionRepo.GetByID(ctx, payload.TenantID, payload.SessionID)
	if err != nil {
		logger.WarnWithFields(logCtx, logger.Fields{
			"skip_reason": "session_load_failed", "error": err.Error(),
		}, "[chat-learning] session_load_failed")
		return fmt.Errorf("load chat learning session: %w", err)
	}
	if session == nil || session.TenantID != payload.TenantID || session.ID != payload.SessionID {
		logger.WarnWithFields(logCtx, logger.Fields{
			"skip_reason": "invalid_task_scope",
		}, "[chat-learning] worker_skip")
		return nil
	}
	message, err := s.messageRepo.GetMessage(ctx, payload.SessionID, payload.MessageID)
	if err != nil {
		skipReason := "message_load_failed"
		if errors.Is(err, gorm.ErrRecordNotFound) {
			skipReason = "message_not_found"
		}
		logger.WarnWithFields(logCtx, logger.Fields{
			"skip_reason": skipReason, "error": err.Error(),
		}, "[chat-learning] message_load_failed")
		return fmt.Errorf("load chat learning message: %w", err)
	}
	if message == nil {
		logger.WarnWithFields(logCtx, logger.Fields{
			"skip_reason": "message_not_found",
		}, "[chat-learning] worker_skip")
		return nil
	}
	logger.Info(logger.WithFields(logCtx, logger.Fields{
		"role": message.Role, "message_id": message.ID, "session_id": message.SessionID,
	}), "[chat-learning] message_loaded")
	var skipReason string
	switch {
	case message.Role != "user":
		skipReason = "message_not_user"
	case message.ID != payload.MessageID:
		skipReason = "message_id_mismatch"
	case message.SessionID != payload.SessionID:
		skipReason = "session_id_mismatch"
	}
	if skipReason != "" {
		logger.WarnWithFields(logCtx, logger.Fields{
			"skip_reason": skipReason, "loaded_message_id": message.ID,
			"loaded_session_id": message.SessionID, "role": message.Role,
		}, "[chat-learning] worker_skip")
		return nil
	}
	return s.RecordChatTurn(
		ctx, payload.SessionID, payload.MessageID, message.Content, payload.KnowledgeBaseIDs,
	)
}

// RecordChatTurn maps one user message within only the KBs actually used by
// the completed turn. Source-ref fallbacks remain available to the manual
// candidate API but are deliberately excluded from automatic evidence.
func (s *chatLearningService) RecordChatTurn(
	ctx context.Context,
	sessionID, messageID, content string,
	knowledgeBaseIDs []string,
) error {
	logCtx := logger.WithFields(ctx, logger.Fields{"session_id": sessionID, "message_id": messageID})
	if strings.TrimSpace(messageID) == "" {
		logger.Info(logger.WithField(logCtx, "skip_reason", "empty_message_id"), "[chat-learning] record_skip")
		return nil
	}
	if strings.TrimSpace(content) == "" {
		logger.Info(logger.WithField(logCtx, "skip_reason", "empty_user_query"), "[chat-learning] record_skip")
		return nil
	}
	kbIDs := normalizeChatLearningKBIDs(knowledgeBaseIDs)
	if len(kbIDs) == 0 {
		logger.Info(logger.WithField(logCtx, "skip_reason", "no_normalized_kbs"), "[chat-learning] record_skip")
		return nil
	}
	scope, err := ResolveScope(ctx)
	if err != nil {
		logger.WarnWithFields(logCtx, logger.Fields{
			"skip_reason": "resolve_scope_failed", "error": err.Error(),
		}, "[chat-learning] record_skip")
		return err
	}
	logCtx = logger.WithFields(logCtx, logger.Fields{
		"tenant_id": scope.TenantID, "subject_id": scope.SubjectID,
	})

	var firstErr error
	seenPages := make(map[string]struct{})
	for _, kbID := range kbIDs {
		kbLogCtx := logger.WithField(logCtx, "knowledge_base_id", kbID)
		candidates, err := s.mapper.FindCandidatesForText(
			ctx, content, kbID, chatLearningCandidateTopK,
		)
		var exactChunkRefCount, sourceRefCount, otherCount int
		for _, candidate := range candidates {
			switch {
			case candidate == nil:
				otherCount++
			case candidate.Method == types.MemoryWikiMethodChunkRef:
				exactChunkRefCount++
			case candidate.Method == types.MemoryWikiMethodSourceRef:
				sourceRefCount++
			default:
				otherCount++
			}
		}
		logger.Info(logger.WithFields(kbLogCtx, logger.Fields{
			"raw_count": len(candidates), "exact_chunk_ref_count": exactChunkRefCount,
			"source_ref_count": sourceRefCount, "other_count": otherCount,
		}), "[chat-learning] candidates")
		if errors.Is(err, ErrMemoryWikiDisabled) || errors.Is(err, ErrMemoryWikiKnowledgeBaseNotFound) {
			skipReason := "wiki_disabled"
			if errors.Is(err, ErrMemoryWikiKnowledgeBaseNotFound) {
				skipReason = "knowledge_base_not_found"
			}
			logger.Info(logger.WithField(kbLogCtx, "skip_reason", skipReason), "[chat-learning] kb_skip")
			continue
		}
		if err != nil {
			logger.WarnWithFields(ctx, logger.Fields{
				"tenant_id": scope.TenantID, "subject_id": scope.SubjectID,
				"session_id": sessionID, "message_id": messageID,
				"knowledge_base_id": kbID, "error": err.Error(),
			}, "[chat-learning] mapping_failed")
			if firstErr == nil {
				firstErr = err
			}
			continue
		}

		safe := make([]*types.MemoryWikiCandidate, 0, len(candidates))
		for _, candidate := range candidates {
			if candidate == nil || candidate.Method != types.MemoryWikiMethodChunkRef ||
				candidate.KnowledgeBaseID != kbID {
				continue
			}
			if _, exists := seenPages[candidate.WikiPageID]; exists {
				continue
			}
			seenPages[candidate.WikiPageID] = struct{}{}
			safe = append(safe, candidate)
		}
		acceptedFields := logger.Fields{"accepted_count": len(safe)}
		if len(safe) == 0 {
			acceptedFields["skip_reason"] = "no_exact_chunk_ref_candidates"
		}
		logger.Info(logger.WithFields(kbLogCtx, acceptedFields), "[chat-learning] accepted_candidates")
		if len(safe) == 0 {
			continue
		}
		for rank, candidate := range safe {
			logger.Info(logger.WithFields(kbLogCtx, logger.Fields{
				"wiki_page_id": candidate.WikiPageID, "mapping_method": candidate.Method,
				"mapping_score": candidate.Score, "rank": rank + 1,
			}), "[chat-learning] accepted_candidate")
		}
		if err := s.profile.RecordChatInteractions(ctx, sessionID, messageID, kbID, safe); err != nil {
			logger.WarnWithFields(ctx, logger.Fields{
				"tenant_id": scope.TenantID, "subject_id": scope.SubjectID,
				"session_id": sessionID, "message_id": messageID,
				"knowledge_base_id": kbID, "error": err.Error(),
			}, "[chat-learning] evidence_write_failed")
			if firstErr == nil {
				firstErr = err
			}
		} else {
			logger.Info(logger.WithField(kbLogCtx, "candidate_count", len(safe)), "[chat-learning] evidence_written")
		}
	}
	return firstErr
}

func normalizeChatLearningKBIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
		if len(out) == chatLearningMaxKBsPerTurn {
			break
		}
	}
	return out
}
