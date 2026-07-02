package summary

import "testing"

func TestParseModels(t *testing.T) {
	models, err := parseModels("gemini-3.1-flash-lite-preview:thinking, gemini-3.1-flash-lite:thinking,gemini-2.5-flash-lite")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 3 {
		t.Fatalf("want 3 models, got %d: %+v", len(models), models)
	}
	if !models[0].useThinking || models[0].name != "gemini-3.1-flash-lite-preview" {
		t.Errorf("bad first entry: %+v", models[0])
	}
	if models[2].useThinking || models[2].name != "gemini-2.5-flash-lite" {
		t.Errorf("bad last entry: %+v", models[2])
	}

	if _, err := parseModels(""); err == nil {
		t.Error("empty list should error")
	}
	if _, err := parseModels("model:bogus"); err == nil {
		t.Error("unknown suffix should error")
	}
}
