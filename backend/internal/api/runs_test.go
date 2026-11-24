package api

import (
	"encoding/json"
	"testing"

	"apiunit/internal/model"
)

func decodeRetestConfig(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("解析重测配置失败: %v", err)
	}
	return cfg
}

func TestFilterRetestConfigAuthenticity(t *testing.T) {
	raw := json.RawMessage(`{
		"targets": [
			{"targetId": "t1", "models": ["m1", "m2", "m3"], "protocol": "auto"},
			{"targetId": "t2", "models": ["m4"]}
		],
		"concurrency": 3,
		"repetitions": 2,
		"checks": {"fingerprint": true}
	}`)
	out, models, err := filterRetestConfig(model.KindAuthenticity, raw, []retestSelection{
		{TargetID: "t1", Model: "m2"},
		{TargetID: "t1", Model: "m2"}, // 同一模型多轮次重复勾选时应去重
	})
	if err != nil {
		t.Fatalf("收窄配置失败: %v", err)
	}
	if models != 1 {
		t.Fatalf("模型数量 = %d, 期望 1", models)
	}
	cfg := decodeRetestConfig(t, out)
	targets, _ := cfg["targets"].([]any)
	if len(targets) != 1 {
		t.Fatalf("targets 数量 = %d, 期望 1", len(targets))
	}
	target := targets[0].(map[string]any)
	if target["targetId"] != "t1" || target["protocol"] != "auto" {
		t.Fatalf("target 字段未保留: %#v", target)
	}
	kept, _ := target["models"].([]any)
	if len(kept) != 1 || kept[0] != "m2" {
		t.Fatalf("models = %#v, 期望 [m2]", kept)
	}
	// 未勾选字段必须原样保留。
	if cfg["repetitions"].(float64) != 2 {
		t.Fatalf("repetitions = %v, 期望 2", cfg["repetitions"])
	}
	checks, _ := cfg["checks"].(map[string]any)
	if checks["fingerprint"] != true {
		t.Fatalf("checks 未保留: %#v", cfg["checks"])
	}
}

func TestFilterRetestConfigImageDetection(t *testing.T) {
	raw := json.RawMessage(`{
		"targets": [
			{"targetId": "t1", "models": [
				{"name": "img-a", "kind": "images", "size": "1024x1024"},
				{"name": "img-b", "kind": "images", "size": "2048x2048"}
			], "protocol": "openai"},
			{"targetId": "t2", "models": [{"name": "img-c", "kind": "chat"}]}
		],
		"phases": ["generation_transparent"],
		"signature": {"local": true, "online": false}
	}`)
	out, models, err := filterRetestConfig(model.KindImageDetection, raw, []retestSelection{
		{TargetID: "t1", Model: "img-b"},
	})
	if err != nil {
		t.Fatalf("收窄配置失败: %v", err)
	}
	if models != 1 {
		t.Fatalf("模型数量 = %d, 期望 1", models)
	}
	cfg := decodeRetestConfig(t, out)
	targets, _ := cfg["targets"].([]any)
	if len(targets) != 1 {
		t.Fatalf("targets 数量 = %d, 期望 1", len(targets))
	}
	target := targets[0].(map[string]any)
	kept, _ := target["models"].([]any)
	if len(kept) != 1 {
		t.Fatalf("models 数量 = %d, 期望 1", len(kept))
	}
	obj := kept[0].(map[string]any)
	if obj["name"] != "img-b" || obj["size"] != "2048x2048" {
		t.Fatalf("模型对象字段未保留: %#v", obj)
	}
	if _, ok := cfg["phases"]; !ok {
		t.Fatal("phases 未保留")
	}
}

func TestFilterRetestConfigRejects(t *testing.T) {
	raw := json.RawMessage(`{"targets":[{"targetId":"t1","models":["m1"]}]}`)
	if _, _, err := filterRetestConfig(model.KindInjection, raw, []retestSelection{{TargetID: "t1", Model: "m1"}}); err == nil {
		t.Fatal("不支持的测试类型应返回错误")
	}
	if _, _, err := filterRetestConfig(model.KindAuthenticity, raw, []retestSelection{{TargetID: "t1", Model: "nope"}}); err == nil {
		t.Fatal("勾选不存在的模型应返回错误")
	}
	if _, _, err := filterRetestConfig(model.KindAuthenticity, raw, nil); err == nil {
		t.Fatal("空勾选应返回错误")
	}
}
