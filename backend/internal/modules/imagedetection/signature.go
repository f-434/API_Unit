package imagedetection

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	adobeVerifyURL  = "https://contentauthenticity.adobe.com/inspect"
	adobeSourceURL  = "https://inspect.cr"
	openaiVerifyURL = "https://openai.com/zh-Hans-CN/research/verify/"
	azureVerifyURL  = "https://ai.azure.com/nextgen/validate"

	maxProvenanceBytes   = 50 << 20
	maxProvenanceRespMiB = 2
)

// analyzeLocalC2PA 扫描文件中的 C2PA / JUMBF 标记。
//
// 这里只判断“元数据是否存在”，不做证书链和签名验签：完整验签依赖
// 第三方校验服务，本地扫描的结果仅作为线索。
func analyzeLocalC2PA(raw []byte) LocalSignature {
	lower := bytes.ToLower(raw)
	format, _ := sniffImage(raw)
	evidence := make([]string, 0, 8)
	detected := false

	// 强信号：签名容器/清单块本身。
	if bytes.Contains(raw, []byte("JUMBF")) {
		evidence = append(evidence, "检测到 JUMBF 签名容器")
		detected = true
	}
	if format == "png" && bytes.Contains(raw, []byte("caBX")) {
		evidence = append(evidence, "检测到 PNG caBX 块")
		detected = true
	}
	if format == "webp" && hasWebPChunk(raw, "C2PA") {
		evidence = append(evidence, "检测到 WebP C2PA 块")
		detected = true
	}
	if format == "jpeg" && bytes.Contains(raw, []byte{0xFF, 0xEB}) && bytes.Contains(lower, []byte("c2pa")) {
		evidence = append(evidence, "检测到 JPEG APP11 c2pa 段")
		detected = true
	}
	// 弱信号：只说明元数据中出现相关文字，单独不足以判定已签名。
	if bytes.Contains(lower, []byte("c2pa")) {
		evidence = append(evidence, "检测到 c2pa 清单标识")
	}
	if bytes.Contains(lower, []byte("claim_generator")) {
		evidence = append(evidence, "检测到 claim_generator 字段")
	}
	if bytes.Contains(lower, []byte("trainedalgorithmicmedia")) {
		evidence = append(evidence, "XMP / IPTC 标记 AI 生成（trainedAlgorithmicMedia）")
	}
	if bytes.Contains(lower, []byte("digitalsourcetype")) {
		evidence = append(evidence, "检测到 digitalSourceType 来源声明")
	}
	if bytes.Contains(lower, []byte("contentcredentials")) || bytes.Contains(lower, []byte("content credentials")) {
		evidence = append(evidence, "检测到 Content Credentials 文案")
	}

	// 只做提示，不用关键字单独判定“已签名”。
	hints := []struct{ label, needle string }{
		{"OpenAI", "openai"},
		{"Microsoft", "microsoft"},
		{"Adobe", "adobe"},
		{"Google", "google"},
		{"DALL·E", "dall"},
		{"gpt-image", "gpt-image"},
		{"Gemini", "gemini"},
		{"SynthID", "synthid"},
	}
	hintSeen := 0
	for _, h := range hints {
		if hintSeen >= 4 {
			break
		}
		if bytes.Contains(lower, []byte(h.needle)) {
			evidence = append(evidence, "元数据出现关键字 "+h.label+"（仅提示，不代表签名有效）")
			hintSeen++
		}
	}

	status := SignatureNotDetected
	if detected {
		status = SignatureDetected
	} else if len(evidence) > 0 {
		status = SignaturePossible
	}
	if len(evidence) == 0 {
		evidence = append(evidence, "未找到 C2PA / JUMBF 标记")
	}
	return LocalSignature{Status: status, Evidence: evidence}
}

// analyzeLocalSynthID 只能发现显式的 SynthID 标记字符串。
// SynthID 本身是像素级水印，必须由在线服务检测。
func analyzeLocalSynthID(raw []byte) LocalSignature {
	lower := bytes.ToLower(raw)
	if bytes.Contains(lower, []byte("synthid")) {
		return LocalSignature{
			Status:   SignaturePossible,
			Evidence: []string{"文件中出现 SynthID 标记字符串；像素级水印仍需在线验证"},
		}
	}
	return LocalSignature{
		Status:   SignatureUnknown,
		Evidence: []string{"SynthID 为像素级不可见水印，本地无法判定，需通过在线服务验证"},
	}
}

// buildVerifyLinks 生成三个在线校验入口。Adobe Inspect 的 source 参数
// 需要可被公网访问且允许 CORS 的图片地址，前端会优先用本站图片地址补全。
func buildVerifyLinks(upstreamURL string) []VerifyLink {
	autoURL := ""
	if isHTTPURL(upstreamURL) {
		autoURL = adobeSourceURL + "?source=" + url.QueryEscape(upstreamURL)
	}
	return []VerifyLink{
		{
			Key:     "adobe",
			Label:   "Adobe Content Credentials",
			URL:     adobeVerifyURL,
			Mode:    "auto",
			Hint:    "若本站图片地址可被公网访问，会自动带上 source 参数；否则下载图片后手动上传。",
			AutoURL: autoURL,
		},
		{
			Key:   "openai",
			Label: "OpenAI 内容验证",
			URL:   openaiVerifyURL,
			Mode:  "upload",
			Hint:  "上传图片，同时检查 C2PA 与 SynthID；也可在设置中配置 API Key 后自动检测。",
		},
		{
			Key:   "azure",
			Label: "Azure AI 内容验证",
			URL:   azureVerifyURL,
			Mode:  "upload",
			Hint:  "上传图片验证 C2PA 内容凭据与来源信息。",
		},
	}
}

// hasWebPChunk 按 RIFF 结构查找指定块，避免在压缩像素数据里误匹配。
func hasWebPChunk(raw []byte, id string) bool {
	if len(raw) < 12 || !bytes.HasPrefix(raw, []byte("RIFF")) || string(raw[8:12]) != "WEBP" {
		return false
	}
	off := 12
	for off+8 <= len(raw) {
		fourcc := string(raw[off : off+4])
		size := int(binary.LittleEndian.Uint32(raw[off+4 : off+8]))
		if fourcc == id {
			return true
		}
		if size < 0 {
			return false
		}
		off += 8 + size + (size & 1)
	}
	return false
}

func isHTTPURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

type provenanceResponse struct {
	Object    string             `json:"object"`
	CreatedAt int64              `json:"created_at"`
	Results   []provenanceResult `json:"results"`
}

type provenanceResult struct {
	Type            string `json:"type"`
	Outcome         string `json:"outcome"`
	ValidationState string `json:"validation_state"`
	Issuer          string `json:"issuer"`
	Model           string `json:"model"`
	GeneratedAt     string `json:"generated_at"`
}

// checkOnlineProvenance 调用 OpenAI Content Provenance API，
// 一次请求同时校验 C2PA 元数据与 SynthID 水印。
func checkOnlineProvenance(ctx context.Context, settings Settings, raw []byte, filename string) OnlineSignature {
	if strings.TrimSpace(settings.APIKey) == "" {
		return OnlineSignature{Status: OnlineUnconfigured, Error: "未配置 OpenAI API Key"}
	}
	if len(raw) == 0 {
		return OnlineSignature{Status: OnlineError, Error: "图片内容为空"}
	}
	if len(raw) > maxProvenanceBytes {
		return OnlineSignature{Status: OnlineError, Error: fmt.Sprintf("图片超过 %d MB，在线验签接口不支持", maxProvenanceBytes>>20)}
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return OnlineSignature{Status: OnlineError, Error: "构造上传请求失败: " + err.Error()}
	}
	if _, err := part.Write(raw); err != nil {
		return OnlineSignature{Status: OnlineError, Error: "写入上传内容失败: " + err.Error()}
	}
	if err := mw.Close(); err != nil {
		return OnlineSignature{Status: OnlineError, Error: "结束上传请求失败: " + err.Error()}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, provenanceEndpoint(settings), &buf)
	if err != nil {
		return OnlineSignature{Status: OnlineError, Error: "构造请求失败: " + err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(settings.APIKey))
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "apiunit-image-detection/1.0")

	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return OnlineSignature{Status: OnlineError, Error: "在线验签请求失败: " + err.Error()}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProvenanceRespMiB<<20))
	if err != nil {
		return OnlineSignature{Status: OnlineError, Error: "读取在线验签响应失败: " + err.Error()}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := truncateText(string(body), 500)
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return OnlineSignature{
			Status: OnlineError,
			Error:  fmt.Sprintf("在线验签接口 HTTP %d: %s", resp.StatusCode, msg),
			Raw:    truncateText(string(body), 8000),
		}
	}

	out, err := parseProvenanceResponse(body)
	if err != nil {
		return OnlineSignature{Status: OnlineError, Error: err.Error(), Raw: truncateText(string(body), 8000)}
	}
	return out
}

// parseProvenanceResponse 解析 OpenAI Content Provenance API 的返回。
// 结果数组按 type 区分，不能按数组下标取值：不适用的检查会被直接省略。
func parseProvenanceResponse(body []byte) (OnlineSignature, error) {
	var pr provenanceResponse
	if err := json.Unmarshal(body, &pr); err != nil {
		return OnlineSignature{}, fmt.Errorf("解析在线验签响应失败: %w", err)
	}
	out := OnlineSignature{Status: OnlineChecked, CreatedAt: pr.CreatedAt, Raw: truncateText(string(body), 8000)}
	for _, r := range pr.Results {
		switch strings.ToLower(strings.TrimSpace(r.Type)) {
		case "c2pa":
			out.C2PA = OnlineC2PA{
				Outcome:         r.Outcome,
				ValidationState: r.ValidationState,
				Issuer:          r.Issuer,
				Model:           r.Model,
				GeneratedAt:     r.GeneratedAt,
			}
		case "synthid":
			out.SynthID = OnlineSynthID{
				Outcome:     r.Outcome,
				Model:       r.Model,
				GeneratedAt: r.GeneratedAt,
			}
		}
	}
	return out, nil
}

// signatureReport 汇总本地检查、可选的在线检查和三个在线校验入口。
func signatureReport(ctx context.Context, cfg *Config, settings Settings, raw []byte, filename, upstreamURL string) SignatureReport {
	out := SignatureReport{
		C2PA:    analyzeLocalC2PA(raw),
		SynthID: analyzeLocalSynthID(raw),
		Links:   buildVerifyLinks(upstreamURL),
	}
	if !cfg.Signature.Local {
		out.C2PA = LocalSignature{Status: SignatureUnknown, Evidence: []string{"本地 C2PA 解析已关闭"}}
	}
	if !cfg.Signature.Online {
		out.Online = &OnlineSignature{Status: OnlineSkipped, Error: "本次运行未启用在线验签"}
		return out
	}
	online := checkOnlineProvenance(ctx, settings, raw, filename)
	out.Online = &online
	if online.Status == OnlineUnconfigured {
		out.Notes = append(out.Notes, "未配置 OpenAI API Key，无法自动校验 C2PA / SynthID；可使用下方在线校验入口手动上传图片。")
	}
	return out
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
