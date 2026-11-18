package authenticity

import (
	"testing"

	"apiunit/internal/engine"
	"apiunit/internal/model"
	"apiunit/internal/protocol"
	"apiunit/internal/tokens"
)

func TestLivePublisherExposesStageAndChecks(t *testing.T) {
	st, targetID := testStore(t)
	sess := engine.NewSession("r_live", model.KindAuthenticity, st, protocol.NewClient(), tokens.New(t.TempDir(), true), nil)
	j := job{
		target: model.TargetSecret{Target: model.Target{ID: targetID, Name: "mock", Protocol: model.ProtocolOpenAI}},
		spec:   TargetSpec{TargetID: targetID, Protocol: "openai"},
		model:  "gpt-6-luna",
	}
	pub := newLivePublisher(sess, []job{j})
	pub.start(j, "接口指纹校验")
	pub.addCheck(j, CheckReport{
		ID: "P1-1", Group: GroupFingerprint, GroupName: "接口指纹",
		Name: "reasoning_effort=xxx", Status: StatusPass, Weight: WeightHard,
		Evidence: "HTTP 400",
	})
	pub.setStage(j, "能力挑战：第 1 轮工具调用")
	live := sess.LiveSnapshot()
	raw, ok := live.Extra["authenticityLive"]
	if !ok {
		t.Fatal("authenticityLive missing")
	}
	list, ok := raw.([]LiveModel)
	if !ok || len(list) != 1 {
		t.Fatalf("unexpected live payload: %#v", raw)
	}
	if list[0].Stage != "能力挑战：第 1 轮工具调用" {
		t.Fatalf("stage=%q", list[0].Stage)
	}
	if len(list[0].Checks) != 1 || list[0].Checks[0].Status != StatusPass {
		t.Fatalf("checks=%+v", list[0].Checks)
	}
	pub.finish(j, VerdictAuthentic, "")
	live = sess.LiveSnapshot()
	list = live.Extra["authenticityLive"].([]LiveModel)
	if !list[0].Done || list[0].Verdict != VerdictAuthentic {
		t.Fatalf("final live=%+v", list[0])
	}
}
