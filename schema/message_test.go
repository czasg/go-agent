package schema

import "testing"

func TestEstimateTokens_SimpleMessage(t *testing.T) {
	m := &Message{Role: User, Content: "hello"} // 5 bytes + 4 overhead = 9, /4 = 2
	tokens := m.EstimateTokens()
	if tokens != 2 {
		t.Fatalf("expected 2, got %d", tokens)
	}
}

func TestEstimateTokens_WithToolCalls(t *testing.T) {
	m := &Message{
		Role:    Assistant,
		Content: "calling tool", // 12 bytes
		ToolCalls: []*ToolCall{
			{
				Function: FunctionCall{
					Name:      "read_file",     // 9 bytes
					Arguments: `{"path":"a.go"}`, // 15 bytes
				},
			},
		},
	}
	// 12 + 4 + 9 + 15 = 40, /4 = 10
	tokens := m.EstimateTokens()
	if tokens != 10 {
		t.Fatalf("expected 10, got %d", tokens)
	}
}

func TestEstimateTokens_EmptyMessage(t *testing.T) {
	m := &Message{Role: Assistant}
	// 0 + 4 = 4, /4 = 1
	tokens := m.EstimateTokens()
	if tokens != 1 {
		t.Fatalf("expected 1, got %d", tokens)
	}
}

func TestEstimateTokens_ChineseContent(t *testing.T) {
	m := &Message{Role: User, Content: "你好世界"} // 12 bytes (4 chars * 3 bytes UTF-8) + 4 = 16, /4 = 4
	tokens := m.EstimateTokens()
	if tokens != 4 {
		t.Fatalf("expected 4, got %d", tokens)
	}
}

func TestEstimateTokens_MultipleToolCalls(t *testing.T) {
	m := &Message{
		Role: Assistant,
		ToolCalls: []*ToolCall{
			{Function: FunctionCall{Name: "a", Arguments: "{}"}},
			{Function: FunctionCall{Name: "b", Arguments: "{}"}},
		},
	}
	// 0 + 4 + (1+2) + (1+2) = 10, /4 = 2
	tokens := m.EstimateTokens()
	if tokens != 2 {
		t.Fatalf("expected 2, got %d", tokens)
	}
}

func TestEstimateTokens_WithImagePart(t *testing.T) {
	m := &Message{
		Role: User,
		MultiParts: []ContentPart{
			{Type: "text", Text: "describe this"}, // 13 bytes
			{Type: "image_url", URL: "https://example.com/img.png"},
		},
	}
	// (13 + 4 + 85*4) / 4 = (13 + 4 + 340) / 4 = 357 / 4 = 89
	tokens := m.EstimateTokens()
	if tokens != 89 {
		t.Fatalf("expected 89, got %d", tokens)
	}
}

// ── 便捷构造函数 ────────────────────────────────────────────────

func TestTextPart(t *testing.T) {
	p := TextPart("hello")
	if p.Type != "text" || p.Text != "hello" {
		t.Fatalf("unexpected: %+v", p)
	}
}

func TestImageURLPart(t *testing.T) {
	p := ImageURLPart("https://example.com/cat.jpg")
	if p.Type != "image_url" || p.URL != "https://example.com/cat.jpg" {
		t.Fatalf("unexpected: %+v", p)
	}
}

func TestImageBase64Part(t *testing.T) {
	p := ImageBase64Part("iVBORw0...", "image/png")
	if p.Type != "image_url" || p.Base64 != "iVBORw0..." || p.MIMEType != "image/png" {
		t.Fatalf("unexpected: %+v", p)
	}
}

func TestHasMultiParts(t *testing.T) {
	m1 := &Message{Content: "text only"}
	if m1.HasMultiParts() {
		t.Fatal("expected false")
	}
	m2 := &Message{MultiParts: []ContentPart{TextPart("hi")}}
	if !m2.HasMultiParts() {
		t.Fatal("expected true")
	}
}
