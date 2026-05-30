package reply

import (
	"testing"

	"github.com/AntonTyutin/assistantbot-core/transport"
)

func TestClassifierDetectsBotName(t *testing.T) {
	classifier := NewClassifier([]string{"чатик"})
	result := classifier.Classify(transport.Message{Text: "чатик, сделай саммари", IsGroup: true}, false)
	if result.Intent != IntentAddressed {
		t.Fatalf("expected addressed intent, got %s", result.Intent)
	}
}

func TestClassifierDetectsCorrection(t *testing.T) {
	classifier := NewClassifier([]string{"бот"})
	result := classifier.Classify(transport.Message{Text: "ты ошибся, я не разбираюсь в крипте", IsGroup: true}, false)
	if result.Intent != IntentCorrection {
		t.Fatalf("expected correction intent, got %s", result.Intent)
	}
}

func TestClassifierDetectsEnglishSummaryRequest(t *testing.T) {
	classifier := NewClassifier([]string{"bot"})
	result := classifier.Classify(transport.Message{Text: "can someone summarize what we decided", IsGroup: true}, false)
	if result.Intent != IntentSummary {
		t.Fatalf("expected summary intent, got %s", result.Intent)
	}
}

func TestClassifierTreatsPrivateChatAsAddressed(t *testing.T) {
	classifier := NewClassifier([]string{"bot"})
	result := classifier.Classify(transport.Message{Text: "hello", IsGroup: false}, false)
	if result.Intent != IntentAddressed {
		t.Fatalf("expected addressed intent, got %s", result.Intent)
	}
}

func TestClassifierIgnoresBotMessages(t *testing.T) {
	classifier := NewClassifier([]string{"бот"})
	result := classifier.Classify(transport.Message{Text: "бот?", IsFromSelf: true}, false)
	if result.Intent != IntentNone {
		t.Fatalf("expected no intent, got %s", result.Intent)
	}
}
