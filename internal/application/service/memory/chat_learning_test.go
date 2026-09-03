package memory

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type chatLearningMessageRepoStub struct {
	interfaces.MessageRepository
	message  *types.Message
	contexts []context.Context
}

func (s *chatLearningMessageRepoStub) GetMessage(
	ctx context.Context, sessionID, messageID string,
) (*types.Message, error) {
	s.contexts = append(s.contexts, ctx)
	if s.message != nil && s.message.SessionID == sessionID && s.message.ID == messageID {
		return s.message, nil
	}
	return nil, errors.New("message not found")
}

type chatLearningMapperStub struct {
	interfaces.MemoryWikiService
	candidates map[string][]*types.MemoryWikiCandidate
	errors     map[string]error
	calls      []string
	topKs      []int
}

func (s *chatLearningMapperStub) FindCandidatesForText(
	_ context.Context, text, knowledgeBaseID string, topK int,
) ([]*types.MemoryWikiCandidate, error) {
	s.calls = append(s.calls, knowledgeBaseID+":"+text)
	s.topKs = append(s.topKs, topK)
	return s.candidates[knowledgeBaseID], s.errors[knowledgeBaseID]
}

type chatLearningProfileStub struct {
	interfaces.LearningProfileService
	calls []chatLearningProfileCall
	err   error
}

type chatLearningProfileCall struct {
	sessionID, messageID, knowledgeBaseID string
	candidates                            []*types.MemoryWikiCandidate
	ctx                                   context.Context
}

func (s *chatLearningProfileStub) RecordChatInteractions(
	ctx context.Context, sessionID, messageID, knowledgeBaseID string,
	candidates []*types.MemoryWikiCandidate,
) error {
	s.calls = append(s.calls, chatLearningProfileCall{
		sessionID: sessionID, messageID: messageID, knowledgeBaseID: knowledgeBaseID,
		candidates: candidates, ctx: ctx,
	})
	return s.err
}

func TestChatLearningUserMessageCreatesExposureCandidates(t *testing.T) {
	mapper := &chatLearningMapperStub{candidates: map[string][]*types.MemoryWikiCandidate{
		"kb-a": {
			{WikiPageID: "page-a", KnowledgeBaseID: "kb-a", Method: types.MemoryWikiMethodChunkRef, Score: 0.8},
			{WikiPageID: "summary-a", KnowledgeBaseID: "kb-a", Method: types.MemoryWikiMethodSourceRef, Score: 0.9},
		},
	}}
	profile := &chatLearningProfileStub{}
	svc := &chatLearningService{mapper: mapper, profile: profile}

	err := svc.RecordChatTurn(
		memoryWikiTestContext(t, 1, "alice"), "session-1", "message-1", "Transformer 与 Mamba", []string{"kb-a"},
	)
	require.NoError(t, err)
	require.Equal(t, []int{chatLearningCandidateTopK}, mapper.topKs)
	require.Len(t, profile.calls, 1)
	require.Equal(t, "session-1", profile.calls[0].sessionID)
	require.Equal(t, "message-1", profile.calls[0].messageID)
	require.Len(t, profile.calls[0].candidates, 1)
	require.Equal(t, "page-a", profile.calls[0].candidates[0].WikiPageID)
}

func TestChatLearningNoKnowledgeBaseCreatesNoEvidence(t *testing.T) {
	mapper := &chatLearningMapperStub{}
	profile := &chatLearningProfileStub{}
	svc := &chatLearningService{mapper: mapper, profile: profile}

	require.NoError(t, svc.RecordChatTurn(
		memoryWikiTestContext(t, 1, "alice"), "session-1", "message-1", "hello", nil,
	))
	require.Empty(t, mapper.calls)
	require.Empty(t, profile.calls)
}

func TestChatLearningWikiDisabledSkipsAndMappingErrorIsBackgroundError(t *testing.T) {
	mapper := &chatLearningMapperStub{
		candidates: map[string][]*types.MemoryWikiCandidate{},
		errors: map[string]error{
			"kb-disabled": ErrMemoryWikiDisabled,
			"kb-broken":   errors.New("retrieval unavailable"),
		},
	}
	profile := &chatLearningProfileStub{}
	svc := &chatLearningService{mapper: mapper, profile: profile}
	ctx := memoryWikiTestContext(t, 1, "alice")

	require.NoError(t, svc.RecordChatTurn(ctx, "session-1", "message-1", "hello", []string{"kb-disabled"}))
	require.ErrorContains(t, svc.RecordChatTurn(ctx, "session-1", "message-2", "hello", []string{"kb-broken"}), "retrieval unavailable")
	require.Empty(t, profile.calls)
}

func TestChatLearningDeduplicatesKBsAndWikiPages(t *testing.T) {
	mapper := &chatLearningMapperStub{candidates: map[string][]*types.MemoryWikiCandidate{
		"kb-a": {
			{WikiPageID: "page-a", KnowledgeBaseID: "kb-a", Method: types.MemoryWikiMethodChunkRef},
			{WikiPageID: "page-a", KnowledgeBaseID: "kb-a", Method: types.MemoryWikiMethodChunkRef},
		},
	}}
	profile := &chatLearningProfileStub{}
	svc := &chatLearningService{mapper: mapper, profile: profile}

	require.NoError(t, svc.RecordChatTurn(
		memoryWikiTestContext(t, 1, "alice"), "session-1", "message-1", "hello", []string{"kb-a", "kb-a"},
	))
	require.Len(t, mapper.calls, 1)
	require.Len(t, profile.calls, 1)
	require.Len(t, profile.calls[0].candidates, 1)
}

func TestChatLearningSchedulePayloadUsesStableReferencesWithoutPrompt(t *testing.T) {
	enqueuer := &stubEnqueuer{}
	svc := &chatLearningService{enqueuer: enqueuer}
	svc.ScheduleChatTurn(
		memoryWikiTestContext(t, 1, "alice"), "session-1", "message-1", []string{"kb-a", "kb-a"},
	)
	task := enqueuer.pop()
	require.NotNil(t, task)
	require.Equal(t, types.TypeChatLearningProfile, task.Type())
	var raw map[string]any
	require.NoError(t, json.Unmarshal(task.Payload(), &raw))
	require.Equal(t, "message-1", raw["message_id"])
	require.Equal(t, "session-1", raw["session_id"])
	require.NotContains(t, raw, "content")
	require.NotContains(t, raw, "prompt")
	require.NotContains(t, raw, "tenant")
	require.NotContains(t, raw, "tenant_info")
}

func TestChatLearningWorkerLoadsOnlyPersistedUserMessage(t *testing.T) {
	mapper := &chatLearningMapperStub{candidates: map[string][]*types.MemoryWikiCandidate{
		"kb-a": {{WikiPageID: "page-a", KnowledgeBaseID: "kb-a", Method: types.MemoryWikiMethodChunkRef}},
	}}
	profile := &chatLearningProfileStub{}
	svc := &chatLearningService{
		tenantRepo: &chatLearningTenantRepoStub{tenant: &types.Tenant{ID: 1}},
		sessionRepo: &chatLearningSessionRepoStub{session: &types.Session{
			ID: "session-1", TenantID: 1, UserID: "alice",
		}},
		messageRepo: &chatLearningMessageRepoStub{message: &types.Message{
			ID: "message-1", SessionID: "session-1", Role: "user", Content: "Mamba",
		}},
		mapper: mapper, profile: profile,
	}
	payload, err := json.Marshal(types.ChatLearningPayload{
		TenantID: 1, SubjectID: "web_user:alice",
		PrincipalType: types.PrincipalWebUser, PrincipalID: "alice",
		SessionID: "session-1", MessageID: "message-1", KnowledgeBaseIDs: []string{"kb-a"},
	})
	require.NoError(t, err)
	require.NoError(t, svc.Handle(t.Context(), asynq.NewTask(types.TypeChatLearningProfile, payload)))
	require.Len(t, profile.calls, 1)
	require.Contains(t, mapper.calls[0], "Mamba")

	svc.messageRepo = &chatLearningMessageRepoStub{message: &types.Message{
		ID: "message-1", SessionID: "session-1", Role: "assistant", Content: "assistant output",
	}}
	profile.calls = nil
	require.NoError(t, svc.Handle(t.Context(), asynq.NewTask(types.TypeChatLearningProfile, payload)))
	require.Empty(t, profile.calls)
}
