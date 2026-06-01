package metrics

// Prompt part labels for RecordPromptPartBytes (per LLM task).
const (
	PromptPartSystem            = "system"
	PromptPartTools             = "tools"
	PromptPartToolsDefinitions  = "tools_definitions"
	PromptPartUser              = "user"
)
