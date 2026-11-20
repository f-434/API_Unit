package api

import (
	"bytes"
	"testing"

	"apiunit/internal/model"
	"apiunit/internal/modules/imagedetection"
)

func TestReportTemplateImageDetection(t *testing.T) {
	data := exportData{
		Run:      model.Run{ID: "r_test", Kind: model.KindImageDetection, Status: model.StatusCompleted},
		Headline: model.RunSummary{Total: 1, Success: 1},
		ImageDetection: &imagedetection.Report{
			Summary: imagedetection.Summary{Models: 1, Phases: 1, Passed: 1, C2PADetected: 1},
			Models: []imagedetection.ModelReport{{
				TargetName: "mock", Model: "mock-image", Kind: "images", Size: "8x6",
				Passed: 1, Total: 1,
				Phases: []imagedetection.PhaseReport{{
					ID: "generation_transparent", Name: "文生图 · transparent", Status: "pass",
					Checks: []imagedetection.CheckDetail{{Name: "尺寸", Status: "pass", Detail: "8x6"}},
					Image: &imagedetection.ImageReport{
						Path:   "/api/image-detection/images/r_test/abc.png",
						Format: "png", Width: 8, Height: 6,
						Signature: imagedetection.SignatureReport{
							C2PA:    imagedetection.LocalSignature{Status: "detected"},
							SynthID: imagedetection.LocalSignature{Status: "unknown"},
							Online:  &imagedetection.OnlineSignature{Status: "checked"},
						},
					},
				}},
			}},
			Notes: []string{"测试备注"},
		},
	}
	var buf bytes.Buffer
	if err := reportTemplate.Execute(&buf, data); err != nil {
		t.Fatalf("渲染生图检测报告失败: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("生图检测")) {
		t.Fatal("HTML 报告缺少生图检测区块")
	}
}
