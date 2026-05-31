package llm

import "github.com/AntonTyutin/assistantbot-core/llm/prompts"

// LLM task identifiers for metrics, model overrides, and API calls.
const (
	TaskGenerateChatReply    = prompts.TaskGenerateChatReply
	TaskUpdateProfile        = prompts.TaskUpdateProfile
	TaskUpdateTopic          = prompts.TaskUpdateTopic
	TaskClassifyMessageTopic = prompts.TaskClassifyMessageTopic
	TaskChatWithTools        = "chat_with_tools"
)

// AllTaskIDs returns every task id used for LLM calls.
func AllTaskIDs() []string {
	ids := prompts.YAMLTaskIDs()
	return append(ids, TaskChatWithTools)
}

// IsTaskID reports whether key is a known LLM task id.
func IsTaskID(key string) bool {
	if key == TaskChatWithTools {
		return true
	}
	return prompts.IsYAMLTaskKey(key)
}
