package prompts

// Task ids that may appear as keys in the prompts YAML file (besides default).
const (
	TaskGenerateChatReply = "generate_chat_reply"
	TaskUpdateProfile     = "update_participant_profile"
	TaskRebuildProfile    = "rebuild_participant_profile"
	TaskUpdateTopic       = "update_chat_topic"
	TaskRebuildTopic      = "rebuild_chat_topic"
	TaskDailySummary      = "daily_summary"
)

// YAMLTaskIDs returns prompts file task keys.
func YAMLTaskIDs() []string {
	return []string{
		TaskGenerateChatReply,
		TaskUpdateProfile,
		TaskRebuildProfile,
		TaskUpdateTopic,
		TaskRebuildTopic,
		TaskDailySummary,
	}
}

// IsYAMLTaskKey reports whether key is allowed in the prompts YAML file (besides default).
func IsYAMLTaskKey(key string) bool {
	for _, id := range YAMLTaskIDs() {
		if id == key {
			return true
		}
	}
	return false
}
