package imagedetection

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"apiunit/internal/engine"
	"apiunit/internal/model"
	"apiunit/internal/netguard"
)

const (
	maxImageResponseBytes = 64 << 20
	maxImageFetchBytes    = 64 << 20
	maxStoredResponse     = 1 << 20
	maxStoredRequest      = 8 << 20
)

type job struct {
	target   model.TargetSecret
	spec     TargetSpec
	model    ModelSpec
	protocol model.Protocol
	phases   []string
}

type modelState struct {
	sourceBytes      []byte
	sourceFormat     string
	sourceBackground string
}

type runner struct {
	cfg       *Config
	settings  Settings
	runID     string
	imageRoot string
	http      *http.Client
	sess      *engine.Session
	pub       *livePublisher
}

func newRunner(cfg *Config, settings Settings, runID, imageRoot string, sess *engine.Session, pub *livePublisher) *runner {
	timeout := time.Duration(cfg.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 600 * time.Second
	}
	return &runner{
		cfg:       cfg,
		settings:  settings,
		runID:     runID,
		imageRoot: imageRoot,
		http: &http.Client{
			Timeout:   timeout,
			Transport: newImageTransport(settings),
		},
		sess: sess,
		pub:  pub,
	}
}

// newImageTransport 为生图请求构建独立的传输层：
// 支持自定义代理（含带账号密码的代理）与可选强制 TLS 1.2。
// 不配置代理时仍沿用 HTTP_PROXY / HTTPS_PROXY 环境变量。
func newImageTransport(settings Settings) *http.Transport {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if raw := strings.TrimSpace(settings.ProxyURL); raw != "" {
		if u, err := url.Parse(raw); err == nil && u.Host != "" {
			tr.Proxy = http.ProxyURL(u)
		}
	}
	if settings.ForceTLS12 {
		// 部分网络会限速 TLS 1.3 连接；强制 TLS 1.2 并使用 AES-256-GCM
		// 可显著提高大响应体（base64 图片）的传输速度。
		tr.TLSClientConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			MaxVersion: tls.VersionTLS12,
			NextProtos: []string{"http/1.1"},
			CipherSuites: []uint16{
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
				tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
			},
		}
	}
	return tr
}

func (r *runner) runModel(ctx context.Context, j job) ModelReport {
	start := time.Now()
	rep := ModelReport{
		TargetID:   j.target.ID,
		TargetName: j.target.Name,
		Model:      j.model.Name,
		Kind:       j.model.Kind,
		Size:       j.model.Size,
		Protocol:   string(j.protocol),
		Endpoint:   modelEndpoint(j),
		Phases:     make([]PhaseReport, 0, len(j.phases)),
	}
	state := &modelState{}

	for _, phase := range j.phases {
		if ctx.Err() != nil {
			break
		}
		r.sess.SetCurrentModel(j.model.Name)
		r.sess.SetPhase(phaseLabel(phase))
		r.pub.setStage(j, phaseLabel(phase))

		pr, imgBytes := r.runPhaseWithRetry(ctx, j, phase, state)
		pr.ID = phase
		pr.Name = phaseLabel(phase)
		rep.Phases = append(rep.Phases, pr)

		switch pr.Status {
		case StatusPass, StatusWarn:
			// 警告表示参数检查通过，但存在无法精确确认的项目（例如 WebP 透明背景）。
			rep.Passed++
		case StatusFail:
			rep.Failed++
		case StatusNA:
			rep.NA++
		}

		// 改图需要原图；优先使用 transparent 的结果，其次任意成功的文生图。
		if imgBytes != nil && (phase == PhaseGenTransparent || phase == PhaseEditSource || state.sourceBytes == nil) {
			state.sourceBytes = imgBytes
			state.sourceFormat = imageFormatOf(imgBytes)
			state.sourceBackground = backgroundOfPhase(phase)
		}

		r.sess.AddExtra("imageDetectionDone", 1)
		r.recordItem(j, pr)
		r.pub.phaseDone(j, pr)
	}

	rep.Total = len(rep.Phases)
	rep.DurationMs = time.Since(start).Milliseconds()
	return rep
}

func (r *runner) runPhaseWithRetry(ctx context.Context, j job, phase string, state *modelState) (PhaseReport, []byte) {
	attempts := r.cfg.Retry.MaxRetries + 1
	if attempts < 1 {
		attempts = 1
	}
	var last PhaseReport
	var lastImg []byte
	for i := 0; i < attempts; i++ {
		if ctx.Err() != nil {
			return canceledPhase(phase), nil
		}
		last, lastImg = r.runPhase(ctx, j, phase, state)
		// 只有传输错误或可重试的服务端错误才重试；
		// 参数检查失败、解析失败等确定性结果不重试。
		if last.Status != StatusFail || !last.retryable {
			return last, lastImg
		}
		if i < attempts-1 {
			backoff := r.cfg.Retry.BackoffMs
			if backoff <= 0 {
				backoff = 800
			}
			for n := 0; n < i; n++ {
				backoff *= 2
				if max := r.cfg.Retry.MaxBackoffMs; max > 0 && backoff > max {
					backoff = max
					break
				}
			}
			select {
			case <-time.After(time.Duration(backoff) * time.Millisecond):
			case <-ctx.Done():
				return canceledPhase(phase), nil
			}
		}
	}
	return last, lastImg
}

func canceledPhase(phase string) PhaseReport {
	return PhaseReport{
		ID:     phase,
		Name:   phaseLabel(phase),
		Status: StatusFail,
		Error:  "请求已取消",
		Checks: []CheckDetail{{Name: "执行", Status: StatusFail, Detail: "请求已取消"}},
	}
}

// failTransport 记录网络连接失败、响应体读取超时等传输错误。
// 是否重试由 retryOnNetwork 决定，避免对长时间超时无脑重试。
func (r *runner) failTransport(pr PhaseReport, detail string) PhaseReport {
	pr = failPhase(pr, "HTTP 请求", detail)
	pr.retryable = r.cfg.Retry.RetryOnNetwork
	return pr
}

// failStatus 记录非 200 状态码，并按状态码与重试配置判断是否重试。
func (r *runner) failStatus(pr PhaseReport, status int, detail string) PhaseReport {
	pr = failPhase(pr, "HTTP 状态码", detail)
	switch {
	case status == http.StatusTooManyRequests:
		pr.retryable = r.cfg.Retry.RetryOn429
	case status == http.StatusRequestTimeout, status == http.StatusConflict, status == http.StatusTooEarly:
		pr.retryable = r.cfg.Retry.RetryOn5xx
	case status >= 500:
		pr.retryable = r.cfg.Retry.RetryOn5xx
	}
	return pr
}

func (r *runner) runPhase(ctx context.Context, j job, phase string, state *modelState) (PhaseReport, []byte) {
	switch phase {
	case PhaseGenTransparent, PhaseGenOpaque, PhaseGenAuto, PhaseEditSource:
		background := backgroundOfPhase(phase)
		if phase == PhaseEditSource {
			background = "transparent"
		}
		pr, img := r.doGeneration(ctx, j, phase, background)
		if phase == PhaseEditSource && pr.Status == StatusPass {
			pr.Reason = "自动生成原图（仅勾选改图测试，作为编辑输入）"
		}
		return pr, img
	case PhaseEditImages, PhaseEditAlias:
		return r.doEdit(ctx, j, phase, state), nil
	case PhaseChatGeneration:
		return r.doChat(ctx, j, phase), nil
	default:
		return PhaseReport{
			ID: phase, Name: phaseLabel(phase), Status: StatusFail,
			Error:  "未知测试项: " + phase,
			Checks: []CheckDetail{{Name: "执行", Status: StatusFail, Detail: "未知测试项: " + phase}},
		}, nil
	}
}

// ---------------- 文生图 ----------------

func (r *runner) doGeneration(ctx context.Context, j job, phase, background string) (pr PhaseReport, img []byte) {
	start := time.Now()
	// 命名返回值：defer 里写入的耗时才能带回给调用方。
	pr = PhaseReport{ID: phase, Name: phaseLabel(phase), Checks: make([]CheckDetail, 0, 8)}
	defer func() { pr.DurationMs = time.Since(start).Milliseconds() }()

	token := randomHex(6)
	reqBody, _ := json.MarshalIndent(map[string]any{
		"model":         j.model.Name,
		"prompt":        r.cfg.GenerationPrompt,
		"n":             1,
		"size":          j.model.Size,
		"background":    background,
		"output_format": r.cfg.OutputFormat,
	}, "", "  ")
	pr.RequestJSON = clip(string(reqBody), maxStoredRequest)
	r.saveAux(token, phase, "request", reqBody)

	endpoint := strings.TrimRight(j.target.BaseURL, "/") + "/v1/images/generations"
	res, err := r.postJSON(ctx, endpoint, reqBody, j)
	pr.StatusCode = res.Status
	pr.RequestID = res.RequestID
	if err != nil {
		return r.failTransport(pr, "请求失败: "+err.Error()), nil
	}
	r.saveAux(token, phase, "response", res.Body)
	pr.ResponseJSON = sanitizeResponse(res.Body)

	if res.Status != http.StatusOK {
		return r.failStatus(pr, res.Status, fmt.Sprintf("HTTP %d: %s", res.Status, truncateText(string(res.Body), 500))), nil
	}
	pr.Checks = append(pr.Checks, CheckDetail{Name: "HTTP 状态码", Status: StatusPass, Detail: "200"})

	var apiResp imageResponse
	if err := json.Unmarshal(res.Body, &apiResp); err != nil {
		return failPhase(pr, "响应解析", "解析响应 JSON 失败: "+err.Error()), nil
	}
	pr.ResponseModel = apiResp.Model
	pr.Checks = append(pr.Checks, CheckDetail{Name: "响应解析", Status: StatusPass, Detail: "成功"})
	if apiResp.Error != nil {
		return failPhase(pr, "接口错误字段", "接口返回错误: "+apiResp.Error.Message), nil
	}
	pr.Checks = append(pr.Checks, CheckDetail{Name: "接口错误字段", Status: StatusPass, Detail: "无错误"})

	imgBytes, upstreamURL, err := r.extractImageFromResponse(ctx, apiResp)
	if err != nil {
		return failPhase(pr, "图片提取", "提取图片失败: "+err.Error()), nil
	}
	pr.Checks = append(pr.Checks, CheckDetail{Name: "图片提取", Status: StatusPass, Detail: fmt.Sprintf("%d 字节", len(imgBytes))})

	image, ok := r.finishImage(ctx, &pr, token, phase, imgBytes, upstreamURL, j.model.Size, background)
	if !ok {
		return pr, imgBytes
	}
	pr.Image = image
	pr.Status = checksStatus(pr.Checks)
	pr.Reason = checksReason(pr.Checks)
	return pr, imgBytes
}

// ---------------- 改图 ----------------

func (r *runner) doEdit(ctx context.Context, j job, phase string, state *modelState) (pr PhaseReport) {
	start := time.Now()
	// 命名返回值：defer 里写入的耗时才能带回给调用方。
	pr = PhaseReport{ID: phase, Name: phaseLabel(phase), Checks: make([]CheckDetail, 0, 8)}
	defer func() { pr.DurationMs = time.Since(start).Milliseconds() }()

	if len(state.sourceBytes) == 0 {
		return failPhase(pr, "前置条件", "没有可用的原图，无法进行改图测试")
	}
	path := "/v1/images/edits"
	if phase == PhaseEditAlias {
		path = "/v1/edits"
	}
	token := randomHex(6)
	reqInfo, _ := json.MarshalIndent(map[string]any{
		"model":         j.model.Name,
		"prompt":        r.cfg.EditPrompt,
		"n":             1,
		"size":          j.model.Size,
		"background":    state.sourceBackground,
		"output_format": r.cfg.OutputFormat,
		"path":          path,
		"image":         fmt.Sprintf("<%d bytes %s>", len(state.sourceBytes), state.sourceFormat),
	}, "", "  ")
	pr.RequestJSON = clip(string(reqInfo), maxStoredRequest)
	r.saveAux(token, phase, "request", reqInfo)

	endpoint := strings.TrimRight(j.target.BaseURL, "/") + path
	res, err := r.postMultipart(ctx, endpoint, j, state.sourceBytes, state.sourceFormat, state.sourceBackground)
	pr.StatusCode = res.Status
	pr.RequestID = res.RequestID
	if err != nil {
		return r.failTransport(pr, "请求失败: "+err.Error())
	}
	r.saveAux(token, phase, "response", res.Body)
	pr.ResponseJSON = sanitizeResponse(res.Body)

	if res.Status != http.StatusOK {
		return r.failStatus(pr, res.Status, fmt.Sprintf("HTTP %d: %s", res.Status, truncateText(string(res.Body), 500)))
	}
	pr.Checks = append(pr.Checks, CheckDetail{Name: "HTTP 状态码", Status: StatusPass, Detail: "200"})

	var apiResp imageResponse
	if err := json.Unmarshal(res.Body, &apiResp); err != nil {
		return failPhase(pr, "响应解析", "解析响应 JSON 失败: "+err.Error())
	}
	pr.ResponseModel = apiResp.Model
	pr.Checks = append(pr.Checks, CheckDetail{Name: "响应解析", Status: StatusPass, Detail: "成功"})
	if apiResp.Error != nil {
		return failPhase(pr, "接口错误字段", "接口返回错误: "+apiResp.Error.Message)
	}
	pr.Checks = append(pr.Checks, CheckDetail{Name: "接口错误字段", Status: StatusPass, Detail: "无错误"})

	imgBytes, upstreamURL, err := r.extractImageFromResponse(ctx, apiResp)
	if err != nil {
		return failPhase(pr, "图片提取", "提取图片失败: "+err.Error())
	}
	pr.Checks = append(pr.Checks, CheckDetail{Name: "图片提取", Status: StatusPass, Detail: fmt.Sprintf("%d 字节", len(imgBytes))})

	image, ok := r.finishImage(ctx, &pr, token, phase, imgBytes, upstreamURL, j.model.Size, state.sourceBackground)
	if !ok {
		return pr
	}
	pr.Image = image
	pr.Status = checksStatus(pr.Checks)
	pr.Reason = checksReason(pr.Checks)
	if pr.Status == StatusPass {
		pr.Reason = "全部参数检查通过，改图效果请人工审核"
	}
	return pr
}

// ---------------- 聊天生图 ----------------

func (r *runner) doChat(ctx context.Context, j job, phase string) (pr PhaseReport) {
	start := time.Now()
	// 命名返回值：defer 里写入的耗时才能带回给调用方。
	pr = PhaseReport{ID: phase, Name: phaseLabel(phase), Checks: make([]CheckDetail, 0, 8)}
	defer func() { pr.DurationMs = time.Since(start).Milliseconds() }()

	token := randomHex(6)
	reqBody, _ := json.MarshalIndent(map[string]any{
		"model":  j.model.Name,
		"stream": false,
		"messages": []map[string]any{
			{"role": "user", "content": r.cfg.GenerationPrompt},
		},
	}, "", "  ")
	pr.RequestJSON = clip(string(reqBody), maxStoredRequest)
	r.saveAux(token, phase, "request", reqBody)

	endpoint := strings.TrimRight(j.target.BaseURL, "/") + "/v1/chat/completions"
	res, err := r.postJSON(ctx, endpoint, reqBody, j)
	pr.StatusCode = res.Status
	pr.RequestID = res.RequestID
	if err != nil {
		return r.failTransport(pr, "请求失败: "+err.Error())
	}
	r.saveAux(token, phase, "response", res.Body)
	pr.ResponseJSON = sanitizeResponse(res.Body)

	if res.Status != http.StatusOK {
		return r.failStatus(pr, res.Status, fmt.Sprintf("HTTP %d: %s", res.Status, truncateText(string(res.Body), 500)))
	}
	pr.Checks = append(pr.Checks, CheckDetail{Name: "HTTP 状态码", Status: StatusPass, Detail: "200"})
	var chatMeta struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(res.Body, &chatMeta)
	pr.ResponseModel = chatMeta.Model

	imgBytes, upstreamURL, err := r.extractImageFromChat(ctx, res.Body)
	if err != nil {
		return failPhase(pr, "图片提取", "从聊天响应提取图片失败: "+err.Error())
	}
	pr.Checks = append(pr.Checks, CheckDetail{Name: "图片提取", Status: StatusPass, Detail: fmt.Sprintf("%d 字节", len(imgBytes))})

	image, ok := r.finishImage(ctx, &pr, token, phase, imgBytes, upstreamURL, j.model.Size, "auto")
	if !ok {
		return pr
	}
	pr.Image = image
	pr.Status = checksStatus(pr.Checks)
	pr.Reason = checksReason(pr.Checks)
	if pr.Status == StatusPass {
		pr.Reason = "全部参数检查通过（聊天类型，未做改图测试）"
	}
	return pr
}

// finishImage 保存原始字节、执行参数检查与签名检查。
func (r *runner) finishImage(ctx context.Context, pr *PhaseReport, token, phase string, imgBytes []byte, upstreamURL, expectedSize, background string) (*ImageReport, bool) {
	format, ext := sniffImage(imgBytes)
	if format == "" || ext == "" {
		pr.Checks = append(pr.Checks, CheckDetail{Name: "图片格式", Status: StatusFail, Detail: "无法识别的图片格式（响应可能不是图片）"})
		pr.Status = StatusFail
		pr.Reason = checksReason(pr.Checks)
		return nil, false
	}
	fileName := token + "_" + phase + ext
	dir := filepath.Join(r.imageRoot, r.runID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		pr.Checks = append(pr.Checks, CheckDetail{Name: "图片保存", Status: StatusFail, Detail: "创建目录失败: " + err.Error()})
		pr.Status = StatusFail
		pr.Reason = checksReason(pr.Checks)
		return nil, false
	}
	full := filepath.Join(dir, fileName)
	if err := os.WriteFile(full, imgBytes, 0o600); err != nil {
		pr.Checks = append(pr.Checks, CheckDetail{Name: "图片保存", Status: StatusFail, Detail: err.Error()})
		pr.Status = StatusFail
		pr.Reason = checksReason(pr.Checks)
		return nil, false
	}
	pr.Checks = append(pr.Checks, CheckDetail{Name: "图片保存", Status: StatusPass, Detail: fileName})

	actualFormat, width, height, imgChecks := checkImage(imgBytes, expectedSize, r.cfg.OutputFormat, background)
	pr.Checks = append(pr.Checks, imgChecks...)

	sig := signatureReport(ctx, r.cfg, r.settings, imgBytes, fileName, upstreamURL)
	sum := sha256.Sum256(imgBytes)
	return &ImageReport{
		Path:        "/api/image-detection/images/" + r.runID + "/" + fileName,
		UpstreamURL: upstreamURL,
		Format:      actualFormat,
		Width:       width,
		Height:      height,
		Bytes:       len(imgBytes),
		SHA256:      hex.EncodeToString(sum[:]),
		Signature:   sig,
	}, true
}

// ---------------- HTTP ----------------

type httpResult struct {
	Status    int
	RequestID string
	Body      []byte
}

func (r *runner) postJSON(ctx context.Context, endpoint string, body []byte, j job) (httpResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return httpResult{}, err
	}
	r.setAuthHeaders(req, j)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return r.do(req)
}

func (r *runner) postMultipart(ctx context.Context, endpoint string, j job, image []byte, format, background string) (httpResult, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fields := map[string]string{
		"model":         j.model.Name,
		"prompt":        r.cfg.EditPrompt,
		"n":             "1",
		"size":          j.model.Size,
		"background":    background,
		"output_format": r.cfg.OutputFormat,
	}
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			return httpResult{}, err
		}
	}
	name := "source"
	switch format {
	case "png":
		name += ".png"
	case "jpeg":
		name += ".jpg"
	case "gif":
		name += ".gif"
	case "webp":
		name += ".webp"
	}
	part, err := mw.CreateFormFile("image", name)
	if err != nil {
		return httpResult{}, err
	}
	if _, err := part.Write(image); err != nil {
		return httpResult{}, err
	}
	if err := mw.Close(); err != nil {
		return httpResult{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buf)
	if err != nil {
		return httpResult{}, err
	}
	r.setAuthHeaders(req, j)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	return r.do(req)
}

func (r *runner) setAuthHeaders(req *http.Request, j job) {
	key := strings.TrimSpace(j.target.APIKey)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	req.Header.Set("User-Agent", "apiunit-image-detection/1.0")
}

func (r *runner) do(req *http.Request) (httpResult, error) {
	// 这些请求携带目标 API Key，禁止通过明文 HTTP 发往公网。
	if err := netguard.CheckURL(req.URL.String()); err != nil {
		return httpResult{}, err
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return httpResult{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxImageResponseBytes))
	if err != nil {
		return httpResult{Status: resp.StatusCode, RequestID: requestID(resp)}, err
	}
	return httpResult{Status: resp.StatusCode, RequestID: requestID(resp), Body: raw}, nil
}

func requestID(resp *http.Response) string {
	for _, key := range []string{"x-request-id", "x-oneapi-request-id", "request-id", "x-amzn-requestid"} {
		if v := strings.TrimSpace(resp.Header.Get(key)); v != "" {
			return v
		}
	}
	return ""
}

// ---------------- 图片提取 ----------------

type apiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

type imageDatum struct {
	B64JSON       string `json:"b64_json"`
	URL           string `json:"url"`
	RevisedPrompt string `json:"revised_prompt"`
}

type imageResponse struct {
	Created int          `json:"created"`
	Data    []imageDatum `json:"data"`
	Model   string       `json:"model"`
	Error   *apiError    `json:"error"`
}

func (r *runner) extractImageFromResponse(ctx context.Context, apiResp imageResponse) ([]byte, string, error) {
	if len(apiResp.Data) == 0 {
		return nil, "", errors.New("响应 data 为空")
	}
	item := apiResp.Data[0]
	if strings.TrimSpace(item.B64JSON) != "" {
		raw, err := decodeBase64String(item.B64JSON)
		if err != nil {
			return nil, "", fmt.Errorf("解码 b64_json 失败: %w", err)
		}
		return raw, "", nil
	}
	if strings.TrimSpace(item.URL) != "" {
		raw, err := r.fetchImageURL(ctx, item.URL)
		return raw, usableSourceURL(item.URL), err
	}
	return nil, "", errors.New("响应中既没有 b64_json 也没有 url")
}

var (
	dataURLRe  = regexp.MustCompile(`data:image/(png|jpeg|jpg|webp|gif);base64,([A-Za-z0-9+/=\s]+)`)
	mdImgURLRe = regexp.MustCompile(`!\[[^\]]*\]\((https?://[^\s\)]+)\)`)
	bareImgURL = regexp.MustCompile(`https?://[^\s"'\)\]]+`)
)

func (r *runner) extractImageFromChat(ctx context.Context, raw []byte) ([]byte, string, error) {
	// 1) 某些兼容实现在 chat 响应里也返回 data 数组。
	var ir imageResponse
	if err := json.Unmarshal(raw, &ir); err == nil {
		if ir.Error != nil {
			return nil, "", errors.New(ir.Error.Message)
		}
		if len(ir.Data) > 0 {
			return r.extractImageFromResponse(ctx, ir)
		}
	}

	// 2) 标准 chat completions 结构。
	var cr struct {
		Choices []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
				Images  []struct {
					ImageURL struct {
						URL string `json:"url"`
					} `json:"image_url"`
				} `json:"images"`
			} `json:"message"`
		} `json:"choices"`
		Error *apiError `json:"error"`
	}
	if err := json.Unmarshal(raw, &cr); err != nil {
		return nil, "", err
	}
	if cr.Error != nil {
		return nil, "", errors.New(cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return nil, "", errors.New("响应不含 choices")
	}
	for _, item := range cr.Choices[0].Message.Images {
		if strings.TrimSpace(item.ImageURL.URL) != "" {
			b, err := r.fetchImageURL(ctx, item.ImageURL.URL)
			return b, usableSourceURL(item.ImageURL.URL), err
		}
	}
	return r.extractImageFromContent(ctx, cr.Choices[0].Message.Content)
}

func (r *runner) extractImageFromContent(ctx context.Context, raw json.RawMessage) ([]byte, string, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil && strings.TrimSpace(text) != "" {
		return r.extractImageFromString(ctx, text)
	}

	var arr []struct {
		Type     string          `json:"type"`
		Text     string          `json:"text"`
		ImageURL json.RawMessage `json:"image_url"`
		Source   struct {
			Type      string `json:"type"`
			Data      string `json:"data"`
			MediaType string `json:"media_type"`
		} `json:"source"`
	}
	if err := json.Unmarshal(raw, &arr); err == nil {
		for _, item := range arr {
			if len(item.ImageURL) > 0 {
				var s string
				if err := json.Unmarshal(item.ImageURL, &s); err == nil && s != "" {
					if b, err := r.fetchImageURL(ctx, s); err == nil {
						return b, usableSourceURL(s), nil
					}
				}
				var obj struct {
					URL string `json:"url"`
				}
				if err := json.Unmarshal(item.ImageURL, &obj); err == nil && obj.URL != "" {
					if b, err := r.fetchImageURL(ctx, obj.URL); err == nil {
						return b, usableSourceURL(obj.URL), nil
					}
				}
			}
			if item.Source.Data != "" {
				if b, err := r.decodeDataURLOrBase64(item.Source.Data); err == nil {
					return b, "", nil
				}
			}
			if item.Text != "" {
				if b, u, err := r.extractImageFromString(ctx, item.Text); err == nil {
					return b, u, nil
				}
			}
		}
	}
	return nil, "", errors.New("content 中未找到图片")
}

func (r *runner) extractImageFromString(ctx context.Context, s string) ([]byte, string, error) {
	if m := dataURLRe.FindStringSubmatch(s); len(m) == 3 {
		raw, err := decodeBase64String(m[2])
		if err != nil {
			return nil, "", err
		}
		return raw, "", nil
	}
	if m := mdImgURLRe.FindStringSubmatch(s); len(m) == 2 {
		raw, err := r.fetchImageURL(ctx, m[1])
		return raw, m[1], err
	}
	for _, m := range bareImgURL.FindAllString(s, -1) {
		if isImageURLString(m) {
			raw, err := r.fetchImageURL(ctx, m)
			if err == nil {
				return raw, m, nil
			}
		}
	}
	return nil, "", errors.New("字符串中未找到图片")
}

func isImageURLString(s string) bool {
	lower := strings.ToLower(s)
	return strings.Contains(lower, ".png") || strings.Contains(lower, ".jpg") ||
		strings.Contains(lower, ".jpeg") || strings.Contains(lower, ".webp") ||
		strings.Contains(lower, ".gif")
}

func (r *runner) fetchImageURL(ctx context.Context, rawURL string) ([]byte, error) {
	if strings.HasPrefix(strings.ToLower(rawURL), "data:") {
		return r.decodeDataURLOrBase64(rawURL)
	}
	// 图片地址来自上游响应，防止被诱导去访问云元数据等链路本地服务。
	if err := netguard.CheckFetchURL(rawURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "apiunit-image-detection/1.0")
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("下载图片失败，HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxImageFetchBytes))
}

// usableSourceURL 避免把体积巨大的 data URL 写进报告摘要；
// 只有可公开引用的 http(s) 地址才作为上游图片地址保存。
func usableSourceURL(u string) string {
	u = strings.TrimSpace(u)
	if strings.HasPrefix(strings.ToLower(u), "data:") {
		return ""
	}
	return u
}

func (r *runner) decodeDataURLOrBase64(s string) ([]byte, error) {
	if m := dataURLRe.FindStringSubmatch(s); len(m) == 3 {
		return decodeBase64String(m[2])
	}
	return decodeBase64String(s)
}

// decodeBase64String 容忍换行、空格以及 URL-safe / 无 padding 变体，
// 不同网关返回的 base64 编码并不统一。
func decodeBase64String(s string) ([]byte, error) {
	s = strings.Join(strings.Fields(s), "")
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("base64 解码失败")
}

// ---------------- 辅助 ----------------

func (r *runner) saveAux(token, phase, kind string, raw []byte) {
	if len(raw) == 0 {
		return
	}
	dir := filepath.Join(r.imageRoot, r.runID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, token+"_"+phase+"."+kind+".json"), raw, 0o600)
}

func failPhase(pr PhaseReport, name, detail string) PhaseReport {
	pr.Checks = append(pr.Checks, CheckDetail{Name: name, Status: StatusFail, Detail: detail})
	pr.Status = StatusFail
	pr.Reason = checksReason(pr.Checks)
	return pr
}

func checksStatus(checks []CheckDetail) string {
	hasWarn := false
	for _, c := range checks {
		switch c.Status {
		case StatusFail:
			return StatusFail
		case StatusWarn:
			hasWarn = true
		}
	}
	if hasWarn {
		return StatusWarn
	}
	return StatusPass
}

func checksReason(checks []CheckDetail) string {
	reasons := make([]string, 0, 4)
	for _, c := range checks {
		if c.Status == StatusFail {
			reasons = append(reasons, c.Name+"："+c.Detail)
		}
	}
	if len(reasons) == 0 {
		return "全部检查通过"
	}
	return strings.Join(reasons, "；")
}

func checksToItem(checks []CheckDetail) []model.CheckResult {
	out := make([]model.CheckResult, 0, len(checks))
	for _, c := range checks {
		out = append(out, model.CheckResult{
			Check: model.Check{
				Kind:        "image_" + slug(c.Name),
				Value:       c.Name,
				Description: c.Name,
			},
			Matched:  c.Status == StatusPass,
			Evidence: c.Detail,
			Decisive: true,
		})
	}
	return out
}

func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '/':
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "check"
	}
	return b.String()
}

func (r *runner) recordItem(j job, pr PhaseReport) {
	it := model.Item{
		TargetID:      j.target.ID,
		TargetName:    j.target.Name,
		Model:         j.model.Name,
		Protocol:      string(j.protocol),
		CaseID:        pr.ID,
		CaseName:      pr.Name,
		Category:      j.model.Kind,
		ResponseModel: pr.ResponseModel,
		StartedAt:     model.NowMs() - pr.DurationMs,
		Status:        pr.StatusCode,
		OK:            pr.Status == StatusPass || pr.Status == StatusWarn,
		LatencyMs:     float64(pr.DurationMs),
		RequestID:     pr.RequestID,
		OutputPreview: clip(pr.Reason, 600),
		RequestJSON:   clip(pr.RequestJSON, maxStoredRequest),
		ResponseJSON:  clip(pr.ResponseJSON, maxStoredResponse),
		Verdict:       pr.Status,
		Checks:        checksToItem(pr.Checks),
		Meta: map[string]any{
			"phase":    pr.ID,
			"kind":     j.model.Kind,
			"size":     j.model.Size,
			"endpoint": modelEndpoint(j),
		},
		ErrorType:    "",
		ErrorMessage: "",
	}
	if pr.Error != "" {
		it.ErrorMessage = pr.Error
	}
	if pr.Status == StatusFail && pr.Reason != "" {
		it.ErrorType = "check"
		it.ErrorMessage = pr.Reason
	}
	if pr.Image != nil {
		it.Meta["imagePath"] = pr.Image.Path
		it.Meta["imageFormat"] = pr.Image.Format
		it.Meta["imageWidth"] = pr.Image.Width
		it.Meta["imageHeight"] = pr.Image.Height
		it.Meta["imageSha256"] = pr.Image.SHA256
		it.Meta["signature"] = pr.Image.Signature
	}
	r.sess.Record(it)
}

func modelEndpoint(j job) string {
	base := strings.TrimRight(j.target.BaseURL, "/")
	if j.model.Kind == ModelKindChat {
		return base + "/v1/chat/completions"
	}
	return base + "/v1/images/generations"
}

func backgroundOfPhase(phase string) string {
	switch phase {
	case PhaseGenTransparent, PhaseEditSource:
		return "transparent"
	case PhaseGenOpaque:
		return "opaque"
	case PhaseGenAuto:
		return "auto"
	}
	return "auto"
}

func imageFormatOf(raw []byte) string {
	f, _ := sniffImage(raw)
	return f
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// sanitizeResponse 去掉响应中的 base64 图片内容，避免把几 MB 的图片
// 重复写进 SQLite；原始响应已单独保存到磁盘。
func sanitizeResponse(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return clip(string(raw), 4000)
	}
	v = redactBase64(v)
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return clip(string(raw), 4000)
	}
	return clip(string(b), maxStoredResponse)
}

func redactBase64(v any) any {
	switch m := v.(type) {
	case map[string]any:
		for k, val := range m {
			if s, ok := val.(string); ok {
				if k == "b64_json" || ((k == "data" || k == "base64" || k == "image_base64") && looksBase64(s)) {
					m[k] = fmt.Sprintf("<base64 omitted, %d chars>", len(s))
					continue
				}
			}
			m[k] = redactBase64(val)
		}
		return m
	case []any:
		for i := range m {
			m[i] = redactBase64(m[i])
		}
		return m
	case string:
		if strings.HasPrefix(m, "data:image/") && len(m) > 512 {
			return fmt.Sprintf("<data URL omitted, %d chars>", len(m))
		}
		return m
	default:
		return v
	}
}

func clip(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n]
}

// looksBase64 只用于判断长字符串是否像图片 base64，避免把普通长文本
// 误标成 base64。base64 字符集不包含空格和换行。
func looksBase64(s string) bool {
	if len(s) < 1024 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '+', r == '/', r == '=', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// ---------------- 运行进度 ----------------

type livePublisher struct {
	mu      sync.Mutex
	sess    *engine.Session
	order   []string
	entries map[string]*LiveModel
}

func liveKey(j job) string { return j.target.ID + "\x00" + j.model.Name }

func newLivePublisher(sess *engine.Session, jobs []job) *livePublisher {
	p := &livePublisher{
		sess:    sess,
		order:   make([]string, 0, len(jobs)),
		entries: make(map[string]*LiveModel, len(jobs)),
	}
	for _, j := range jobs {
		k := liveKey(j)
		p.order = append(p.order, k)
		p.entries[k] = &LiveModel{
			TargetID:   j.target.ID,
			TargetName: j.target.Name,
			Model:      j.model.Name,
			Kind:       j.model.Kind,
			Size:       j.model.Size,
			Stage:      "等待调度",
			StartedAt:  0,
		}
	}
	p.publishLocked()
	return p
}

func (p *livePublisher) publishLocked() {
	if p == nil || p.sess == nil {
		return
	}
	now := time.Now()
	out := make([]LiveModel, 0, len(p.order))
	for _, k := range p.order {
		e := p.entries[k]
		if e == nil {
			continue
		}
		cp := *e
		if cp.StartedAt > 0 {
			cp.ElapsedMs = now.Sub(time.UnixMilli(cp.StartedAt)).Milliseconds()
		}
		out = append(out, cp)
	}
	p.sess.SetExtra("imageDetectionLive", out)
}

func (p *livePublisher) setStage(j job, stage string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := p.entries[liveKey(j)]; e != nil {
		if e.StartedAt == 0 {
			e.StartedAt = time.Now().UnixMilli()
		}
		e.Stage = stage
	}
	p.publishLocked()
}

func (p *livePublisher) phaseDone(j job, pr PhaseReport) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := p.entries[liveKey(j)]; e != nil {
		switch pr.Status {
		case StatusPass:
			e.Passed++
		case StatusFail:
			e.Failed++
			if e.Error == "" {
				e.Error = pr.Reason
			}
		case StatusNA:
			e.NA++
		}
	}
	p.publishLocked()
}

func (p *livePublisher) finish(j job, errMsg string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := p.entries[liveKey(j)]; e != nil {
		e.Done = true
		e.Stage = "完成"
		if errMsg != "" {
			e.Error = errMsg
		}
	}
	p.publishLocked()
}
