package modcommon

import (
	"fmt"
	"strings"

	"apiunit/internal/model"
	"apiunit/internal/protocol"
	"apiunit/internal/store"
)

func ResolveTarget(st *store.Store, id string) (protocol.TargetInfo, error) {
	if strings.TrimSpace(id) == "" {
		return protocol.TargetInfo{}, fmt.Errorf("请选择上游目标")
	}
	t, err := st.GetTarget(id, true)
	if err != nil {
		return protocol.TargetInfo{}, fmt.Errorf("目标不存在或读取失败: %w", err)
	}
	return protocol.TargetInfo{
		ID:       t.ID,
		Name:     t.Name,
		BaseURL:  t.BaseURL,
		APIKey:   t.APIKey,
		Protocol: t.Protocol,
	}, nil
}

func Messages(systemPrompt, userPrompt string) []protocol.ChatMessage {
	out := make([]protocol.ChatMessage, 0, 2)
	if strings.TrimSpace(systemPrompt) != "" {
		out = append(out, protocol.ChatMessage{Role: "system", Content: systemPrompt})
	}
	out = append(out, protocol.ChatMessage{Role: "user", Content: userPrompt})
	return out
}

func NormProtocol(p string) model.Protocol {
	switch model.Protocol(strings.ToLower(strings.TrimSpace(p))) {
	case model.ProtocolOpenAI:
		return model.ProtocolOpenAI
	case model.ProtocolClaude:
		return model.ProtocolClaude
	case model.ProtocolGemini:
		return model.ProtocolGemini
	default:
		return model.ProtocolAuto
	}
}
