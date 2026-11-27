package store

import (
	"strings"
	"testing"

	"apiunit/internal/model"
)

// TestRunConfigEncryptedAtRest 验证运行配置（可能含自定义探针请求头凭据）
// 在数据库中不以明文保存，读取时能透明解密。
func TestRunConfigEncryptedAtRest(t *testing.T) {
	st := newTestStore(t)
	cfg := `{"targets":[{"targetId":"t_1"}],"customProbes":[{"headers":{"Authorization":"Bearer probe-secret"}}]}`
	run := &model.Run{ID: "r_test", Kind: model.KindAuthenticity, Status: model.StatusQueued, Config: cfg}
	if err := st.CreateRun(run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	var raw string
	if err := st.db.QueryRow(`SELECT config_json FROM runs WHERE id=?`, run.ID).Scan(&raw); err != nil {
		t.Fatalf("read raw config: %v", err)
	}
	if strings.Contains(raw, "probe-secret") {
		t.Fatal("运行配置中的凭据不应以明文落库")
	}
	if !strings.HasPrefix(raw, runConfigEncPrefix) {
		t.Fatalf("运行配置未加密: %q", raw)
	}

	got, err := st.GetRun(run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Config != cfg {
		t.Fatalf("解密后配置不一致: %s", got.Config)
	}
}
