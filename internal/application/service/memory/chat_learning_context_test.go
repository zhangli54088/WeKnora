package memory

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type chatLearningTenantRepoStub struct {
	interfaces.TenantRepository
	tenant *types.Tenant
	err    error
	ids    []uint64
}

func (s *chatLearningTenantRepoStub) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	s.ids = append(s.ids, id)
	return s.tenant, s.err
}

type chatLearningSessionRepoStub struct {
	interfaces.SessionRepository
	session   *types.Session
	err       error
	tenantIDs []uint64
}

func (s *chatLearningSessionRepoStub) GetByID(
	_ context.Context, tenantID uint64, id string,
) (*types.Session, error) {
	s.tenantIDs = append(s.tenantIDs, tenantID)
	if s.err != nil {
		return nil, s.err
	}
	if s.session == nil || s.session.TenantID != tenantID || s.session.ID != id {
		return nil, apperrors.ErrSessionNotFound
	}
	return s.session, nil
}

// Exercise the real MemoryWiki text-mapping path up to the retrieval boundary,
// without an embedding model, database, or retrieval engine.
type chatLearningTenantAwareKBStub struct {
	*memoryWikiKBServiceStub
	contexts []context.Context
}

func (s *chatLearningTenantAwareKBStub) HybridSearch(
	ctx context.Context, kbID string, params types.SearchParams,
) ([]*types.SearchResult, error) {
	s.contexts = append(s.contexts, ctx)
	if _, ok := types.TenantInfoFromContext(ctx); !ok {
		return nil, errors.New("tenant information missing in context")
	}
	return s.memoryWikiKBServiceStub.HybridSearch(ctx, kbID, params)
}

func chatLearningWorkerPayload() types.ChatLearningPayload {
	return types.ChatLearningPayload{
		TenantID: 1, SubjectID: "web_user:alice",
		PrincipalType: types.PrincipalWebUser, PrincipalID: "alice",
		SessionID: "session-1", MessageID: "message-1", KnowledgeBaseIDs: []string{"kb-a"},
	}
}

func chatLearningWorkerTask(t *testing.T, payload types.ChatLearningPayload) *asynq.Task {
	t.Helper()
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	return asynq.NewTask(types.TypeChatLearningProfile, body)
}

func newChatLearningWorkerFixture() *chatLearningService {
	return NewChatLearningService(
		&chatLearningMessageRepoStub{message: &types.Message{
			ID: "message-1", SessionID: "session-1", Role: "user", Content: "Mamba",
		}},
		&chatLearningTenantRepoStub{tenant: &types.Tenant{ID: 1}},
		&chatLearningSessionRepoStub{session: &types.Session{
			ID: "session-1", TenantID: 1, UserID: "alice",
		}},
		&chatLearningMapperStub{candidates: map[string][]*types.MemoryWikiCandidate{
			"kb-a": {{WikiPageID: "page-a", KnowledgeBaseID: "kb-a", Method: types.MemoryWikiMethodChunkRef}},
		}},
		&chatLearningProfileStub{},
		nil,
	).(*chatLearningService)
}

func requireChatLearningProfileContext(t *testing.T, ctx context.Context, tenant *types.Tenant) {
	t.Helper()
	info, ok := types.TenantInfoFromContext(ctx)
	require.True(t, ok)
	require.Same(t, tenant, info)
	scope, err := ResolveScope(ctx)
	require.NoError(t, err)
	require.Equal(t, interfaces.MemoryScope{TenantID: 1, SubjectID: "web_user:alice"}, scope)
}

func TestChatLearningWorkerRestoresTenantInfoBeforeMessageAndSearch(t *testing.T) {
	for _, tc := range []struct {
		name     string
		incoming *types.Tenant
	}{
		{name: "missing tenant info"},
		{name: "stale profile tenant info", incoming: &types.Tenant{ID: 1, Name: "stale"}},
		{name: "inherited resource tenant info", incoming: &types.Tenant{ID: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newChatLearningWorkerFixture()
			tenantRepo := svc.tenantRepo.(*chatLearningTenantRepoStub)
			tenantRepo.tenant = &types.Tenant{
				ID: 1, Name: "current database configuration",
				RetrieverEngines: types.RetrieverEngines{Engines: []types.RetrieverEngineParams{
					{RetrieverType: types.VectorRetrieverType, RetrieverEngineType: types.PostgresRetrieverEngineType},
				}},
			}
			mapper, kb := newMemoryWikiCandidateService([]*types.SearchResult{
				{ID: "chunk-a", KnowledgeID: "doc-a", KnowledgeBaseID: "kb-1", Score: 0.8},
			}, map[string][]*types.WikiPage{
				"doc-a": {{
					ID: "page-a", TenantID: 1, KnowledgeBaseID: "kb-1", Status: types.WikiPageStatusPublished,
					SourceRefs: types.StringArray{"doc-a"}, ChunkRefs: types.StringArray{"chunk-a"},
				}},
			})
			search := &chatLearningTenantAwareKBStub{memoryWikiKBServiceStub: kb}
			mapper.kbService = search
			svc.mapper = mapper
			payload := chatLearningWorkerPayload()
			payload.KnowledgeBaseIDs = []string{"kb-1"}
			task := chatLearningWorkerTask(t, payload)
			ctx := t.Context()
			if tc.incoming != nil {
				ctx = context.WithValue(ctx, types.TenantInfoContextKey, tc.incoming)
				ctx = context.WithValue(ctx, types.TenantIDContextKey, tc.incoming.ID)
			}

			require.NoError(t, svc.Handle(ctx, task))
			require.Equal(t, []uint64{1}, tenantRepo.ids)
			messages := svc.messageRepo.(*chatLearningMessageRepoStub)
			require.Len(t, messages.contexts, 1)
			requireChatLearningProfileContext(t, messages.contexts[0], tenantRepo.tenant)
			require.Len(t, search.contexts, 1)
			requireChatLearningProfileContext(t, search.contexts[0], tenantRepo.tenant)
			info, _ := types.TenantInfoFromContext(search.contexts[0])
			require.Equal(t, tenantRepo.tenant.RetrieverEngines.Engines, info.GetEffectiveEngines())
			profile := svc.profile.(*chatLearningProfileStub)
			require.Len(t, profile.calls, 1)
			requireChatLearningProfileContext(t, profile.calls[0].ctx, tenantRepo.tenant)
			require.Equal(t, "message-1", profile.calls[0].messageID)
			require.Equal(t, "page-a", profile.calls[0].candidates[0].WikiPageID)
			require.Equal(t, types.MemoryWikiMethodChunkRef, profile.calls[0].candidates[0].Method)

			// A retry must reload again, even when its incoming context has TenantInfo.
			tenantRepo.tenant = &types.Tenant{ID: 1, Name: "updated database configuration"}
			require.NoError(t, svc.Handle(messages.contexts[0], task))
			require.Equal(t, []uint64{1, 1}, tenantRepo.ids)
			require.Len(t, search.contexts, 2)
			requireChatLearningProfileContext(t, search.contexts[1], tenantRepo.tenant)
			require.Len(t, profile.calls, 2)
			requireChatLearningProfileContext(t, profile.calls[1].ctx, tenantRepo.tenant)
		})
	}
}

func TestChatLearningWorkerTenantRestoreFailureStopsBeforeMessage(t *testing.T) {
	lookupErr := errors.New("tenant database unavailable")
	for _, tc := range []struct {
		name    string
		repo    *chatLearningTenantRepoStub
		wantErr bool
	}{
		{name: "missing repository", wantErr: true},
		{name: "tenant not found", repo: &chatLearningTenantRepoStub{err: repository.ErrTenantNotFound}, wantErr: true},
		{name: "nil tenant", repo: &chatLearningTenantRepoStub{}, wantErr: true},
		{name: "transient lookup error", repo: &chatLearningTenantRepoStub{err: lookupErr}, wantErr: true},
		{name: "mismatched loaded tenant", repo: &chatLearningTenantRepoStub{tenant: &types.Tenant{ID: 2}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newChatLearningWorkerFixture()
			svc.tenantRepo = nil
			if tc.repo != nil {
				svc.tenantRepo = tc.repo
			}
			err := svc.Handle(t.Context(), chatLearningWorkerTask(t, chatLearningWorkerPayload()))
			if tc.wantErr {
				require.Error(t, err)
				// Ordinary errors keep the existing bounded Asynq retry behavior.
				require.NotErrorIs(t, err, asynq.SkipRetry)
				if tc.repo != nil && tc.repo.err != nil {
					require.ErrorIs(t, err, tc.repo.err)
				}
			} else {
				require.NoError(t, err)
			}
			require.Empty(t, svc.sessionRepo.(*chatLearningSessionRepoStub).tenantIDs)
			require.Empty(t, svc.messageRepo.(*chatLearningMessageRepoStub).contexts)
			require.Empty(t, svc.mapper.(*chatLearningMapperStub).calls)
			require.Empty(t, svc.profile.(*chatLearningProfileStub).calls)
		})
	}
}

func TestChatLearningWorkerRejectsInvalidPrincipalSubject(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*types.ChatLearningPayload)
	}{
		{name: "missing tenant", mutate: func(p *types.ChatLearningPayload) { p.TenantID = 0 }},
		{name: "missing principal type", mutate: func(p *types.ChatLearningPayload) { p.PrincipalType = "" }},
		{name: "missing principal id", mutate: func(p *types.ChatLearningPayload) { p.PrincipalID = "" }},
		{name: "forged subject", mutate: func(p *types.ChatLearningPayload) { p.SubjectID = "web_user:bob" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newChatLearningWorkerFixture()
			payload := chatLearningWorkerPayload()
			tc.mutate(&payload)
			require.NoError(t, svc.Handle(t.Context(), chatLearningWorkerTask(t, payload)))
			require.Empty(t, svc.tenantRepo.(*chatLearningTenantRepoStub).ids)
			require.Empty(t, svc.sessionRepo.(*chatLearningSessionRepoStub).tenantIDs)
			require.Empty(t, svc.messageRepo.(*chatLearningMessageRepoStub).contexts)
			require.Empty(t, svc.mapper.(*chatLearningMapperStub).calls)
			require.Empty(t, svc.profile.(*chatLearningProfileStub).calls)
		})
	}
}

func TestChatLearningWorkerForgedTenantCannotWriteAnotherProfile(t *testing.T) {
	svc := newChatLearningWorkerFixture()
	// The durable session/message belong to tenant A. Changing only the task's
	// profile tenant to B must not let that message create evidence in B.
	payload := chatLearningWorkerPayload()
	payload.TenantID = 2
	svc.tenantRepo.(*chatLearningTenantRepoStub).tenant = &types.Tenant{ID: 2}
	err := svc.Handle(t.Context(), chatLearningWorkerTask(t, payload))
	require.ErrorIs(t, err, apperrors.ErrSessionNotFound)
	require.Equal(t, []uint64{2}, svc.sessionRepo.(*chatLearningSessionRepoStub).tenantIDs)
	require.Empty(t, svc.messageRepo.(*chatLearningMessageRepoStub).contexts)
	require.Empty(t, svc.mapper.(*chatLearningMapperStub).calls)
	require.Empty(t, svc.profile.(*chatLearningProfileStub).calls)
}

func TestChatLearningWorkerSharedKBDoesNotReplaceProfileTenant(t *testing.T) {
	svc := newChatLearningWorkerFixture()
	mapper, kb := newMemoryWikiCandidateService(nil, nil)
	// General search supports shared KBs, but the current MemoryWiki rule rejects
	// foreign-owned KBs. Rehydration must not pivot to B to bypass that rule.
	kb.kb.TenantID = 2
	search := &chatLearningTenantAwareKBStub{memoryWikiKBServiceStub: kb}
	mapper.kbService = search
	svc.mapper = mapper
	payload := chatLearningWorkerPayload()
	payload.KnowledgeBaseIDs = []string{kb.kb.ID}
	ctx := context.WithValue(t.Context(), types.TenantInfoContextKey, &types.Tenant{ID: 2})
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(2))
	require.NoError(t, svc.Handle(ctx, chatLearningWorkerTask(t, payload)))
	messages := svc.messageRepo.(*chatLearningMessageRepoStub)
	require.Len(t, messages.contexts, 1)
	requireChatLearningProfileContext(t, messages.contexts[0], svc.tenantRepo.(*chatLearningTenantRepoStub).tenant)
	require.Empty(t, search.contexts)
	require.Empty(t, svc.profile.(*chatLearningProfileStub).calls)
}
