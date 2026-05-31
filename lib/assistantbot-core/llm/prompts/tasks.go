package prompts

// Task ids that may appear as keys in the prompts YAML file (besides default).
const (
	TaskGenerateChatReply    = "generate_chat_reply"
	TaskUpdateProfile        = "update_participant_profile"
	TaskUpdateTopic          = "update_chat_topic"
	TaskClassifyMessageTopic = "classify_message_topic"
)

// YAMLTaskIDs returns prompts file task keys.
func YAMLTaskIDs() []string {
	return []string{
		TaskGenerateChatReply,
		TaskUpdateProfile,
		TaskUpdateTopic,
		TaskClassifyMessageTopic,
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
