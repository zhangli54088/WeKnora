package interfaces

import (
	"context"

	"github.com/hibiken/asynq"
)

// ChatLearningService records conservative learning-profile exposure from a
// successfully completed user chat turn without extending the chat critical
// path.
type ChatLearningService interface {
	ScheduleChatTurn(ctx context.Context, sessionID, messageID string, knowledgeBaseIDs []string)
	RecordChatTurn(ctx context.Context, sessionID, messageID, content string, knowledgeBaseIDs []string) error
	Handle(ctx context.Context, task *asynq.Task) error
}
