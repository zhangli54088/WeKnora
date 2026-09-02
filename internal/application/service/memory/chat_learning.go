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
)

const (
	chatLearningCandidateTopK = 3
	chatLearningMaxKBsPerTurn = 8
)

type chatLearningService struct {
	messageRepo interfaces.MessageRepository
	mapper      interfaces.MemoryWikiService
	profile     interfaces.LearningProfileService
	enqueuer    interfaces.TaskEnqueuer
}

func NewChatLearningService(
	messageRepo interfaces.MessageRepository,
	mapper interfaces.MemoryWikiService,
	profile interfaces.LearningProfileService,
	enqueuer interfaces.TaskEnqueuer,
) interfaces.ChatLearningService {
	return &chatLearningService{
		messageRepo: messageRepo,
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
	scope, err := ResolveScope(ctx)
	if err != nil || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(messageID) == "" {
		return
	}
	kbIDs := normalizeChatLearningKBIDs(knowledgeBaseIDs)
	if len(kbIDs) == 0 {
		return
	}
	principal, ok := types.PrincipalFromContext(ctx)
	if !ok || principal.StorageID() != scope.SubjectID {
		return
	}
	if s.enqueuer == nil {
		logger.Warnf(ctx, "chat learning: no task enqueuer configured, skipping message %s", messageID)
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
		}, "chat learning: marshal task failed")
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
		}, "chat learning: enqueue failed")
	}
}

// Handle restores the authenticated scope carried by the task and loads only
// the referenced durable user message. Assistant messages are always ignored.
func (s *chatLearningService) Handle(ctx context.Context, task *asynq.Task) error {
	var payload types.ChatLearningPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal chat learning payload: %w", err)
	}
	principal := types.Principal{Type: payload.PrincipalType, ID: payload.PrincipalID}.Normalize()
	if payload.TenantID == 0 || !principal.Valid() || principal.StorageID() != payload.SubjectID {
		logger.Warnf(ctx, "chat learning: invalid task scope, dropping")
		return nil
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	ctx = types.WithPrincipal(ctx, principal)
	if payload.Language != "" {
		ctx = context.WithValue(ctx, types.LanguageContextKey, payload.Language)
	}
	message, err := s.messageRepo.GetMessage(ctx, payload.SessionID, payload.MessageID)
	if err != nil {
		return fmt.Errorf("load chat learning message: %w", err)
	}
	if message == nil || message.Role != "user" || message.ID != payload.MessageID ||
		message.SessionID != payload.SessionID {
		logger.Warnf(ctx, "chat learning: message %s is not a persisted user message, dropping", payload.MessageID)
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
	if strings.TrimSpace(messageID) == "" || strings.TrimSpace(content) == "" {
		return nil
	}
	kbIDs := normalizeChatLearningKBIDs(knowledgeBaseIDs)
	if len(kbIDs) == 0 {
		return nil
	}
	scope, err := ResolveScope(ctx)
	if err != nil {
		return err
	}

	var firstErr error
	seenPages := make(map[string]struct{})
	for _, kbID := range kbIDs {
		candidates, err := s.mapper.FindCandidatesForText(
			ctx, content, kbID, chatLearningCandidateTopK,
		)
		if errors.Is(err, ErrMemoryWikiDisabled) || errors.Is(err, ErrMemoryWikiKnowledgeBaseNotFound) {
			continue
		}
		if err != nil {
			logger.WarnWithFields(ctx, logger.Fields{
				"tenant_id": scope.TenantID, "subject_id": scope.SubjectID,
				"session_id": sessionID, "message_id": messageID,
				"knowledge_base_id": kbID, "error": err.Error(),
			}, "chat learning: wiki mapping failed")
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
		if len(safe) == 0 {
			continue
		}
		if err := s.profile.RecordChatInteractions(ctx, sessionID, messageID, kbID, safe); err != nil {
			logger.WarnWithFields(ctx, logger.Fields{
				"tenant_id": scope.TenantID, "subject_id": scope.SubjectID,
				"session_id": sessionID, "message_id": messageID,
				"knowledge_base_id": kbID, "error": err.Error(),
			}, "chat learning: evidence write failed")
			if firstErr == nil {
				firstErr = err
			}
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
