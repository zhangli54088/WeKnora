package session

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type chatLearningMessageServiceStub struct {
	interfaces.MessageService
	updated *types.Message
}

func (s *chatLearningMessageServiceStub) UpdateMessage(_ context.Context, message *types.Message) error {
	s.updated = message
	return nil
}

func (s *chatLearningMessageServiceStub) IndexMessageToKB(
	context.Context, string, string, string, string,
) {
}

type chatLearningScheduleStub struct {
	interfaces.ChatLearningService
	calls []chatLearningScheduleCall
}

type chatLearningScheduleCall struct {
	sessionID, messageID string
	kbIDs                []string
}

func (s *chatLearningScheduleStub) ScheduleChatTurn(
	_ context.Context, sessionID, messageID string, kbIDs []string,
) {
	s.calls = append(s.calls, chatLearningScheduleCall{
		sessionID: sessionID, messageID: messageID, kbIDs: append([]string(nil), kbIDs...),
	})
}

func chatLearningHandlerContext(t *testing.T) context.Context {
	t.Helper()
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(1))
	return types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: "alice"})
}

func TestChatLearningSuccessfulTurnSchedulesUserMessageWithActualKBs(t *testing.T) {
	messages := &chatLearningMessageServiceStub{}
	learning := &chatLearningScheduleStub{}
	h := &Handler{messageService: messages, chatLearningService: learning}
	assistant := &types.Message{
		ID: "assistant-1", SessionID: "session-1", Role: "assistant",
		KnowledgeReferences: types.References{
			{KnowledgeBaseID: "kb-a"}, {KnowledgeBaseID: "kb-a"}, {KnowledgeBaseID: "kb-b"},
		},
	}

	h.completeAssistantMessage(
		chatLearningHandlerContext(t), assistant, "user question", "user-message-1", true,
	)
	require.Len(t, learning.calls, 1)
	require.Equal(t, "user-message-1", learning.calls[0].messageID)
	require.Equal(t, []string{"kb-a", "kb-b"}, learning.calls[0].kbIDs)
	require.Equal(t, "assistant-1", messages.updated.ID)
}

func TestChatLearningFailedTurnAndAssistantOnlyDoNotSchedule(t *testing.T) {
	learning := &chatLearningScheduleStub{}
	h := &Handler{messageService: &chatLearningMessageServiceStub{}, chatLearningService: learning}
	assistant := &types.Message{
		ID: "assistant-1", SessionID: "session-1", Role: "assistant",
		KnowledgeReferences: types.References{{KnowledgeBaseID: "kb-a"}},
	}

	h.completeAssistantMessage(
		chatLearningHandlerContext(t), assistant, "user question", "user-message-1", false,
	)
	h.completeAssistantMessage(
		chatLearningHandlerContext(t), assistant, "", "assistant-1", true,
	)
	require.Empty(t, learning.calls)
}
