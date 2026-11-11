# API Unit · LLM 接口测试台

一个**独立运行**的 Go + React 前后端分离测试系统，面向 New API / One API / OpenAI 兼容网关，提供四类核心能力：

| 模块 | 说明 |
| --- | --- |
| **并发压测** | 固定请求数、持续时长、并发爬坡；统计 QPS、RPM、延迟分位、TTFT、TPOT、真实并发、Token 与成本 |
| **Token 审计** | 同一提示词发往多个上游/模型，对比输入 Token、重复波动与本地分词器偏差，定位额外 Token 注入 |
| **请求差异审计** | 保存完整请求快照与 SHA-256；对 `systemInstruction`、`contents`、`tools`、`generationConfig`、`cachedContent`、`safetySettings` 等做 JSONPath 结构化 Diff；扫描隐藏指令 |
| **提示词注入** | 内置攻击用例 + 规则判定 + LLM-as-judge，输出 ASR、分类风险与逐用例明细 |

系统不修改、不侵入 New API 源码，只通过标准 HTTP 接口访问网关。

---

## 1. 技术栈与目录

```
API_Unit/
├── backend/                  Go 1.26+（标准库 + chi + SQLite）
│   ├── cmd/server/           服务入口
│   ├── cmd/mock-upstream/    本地模拟上游（联调用）
│   └── internal/
│       ├── api/              HTTP API、SSE、HTML/JSON/CSV 导出
│       ├── config/           环境变量
│       ├── engine/           调度、重试、指标、实时快照、SQLite 落库
│       ├── model/            领域模型（Run/Item/Snapshot/Aggregate...）
│       ├── modules/          stress / tokenaudit / injection
│       ├── protocol/         OpenAI / Claude / Gemini 协议适配与 SSE 解析
│       ├── requestdiff/      请求快照、指纹、JSON Diff、隐藏指令扫描、审计模块
│       ├── secret/           AES-256-GCM 密钥加密、会话签名
│       ├── seed/             内置提示词与攻击用例
│       ├── store/            SQLite 存储
│       └── tokens/           tiktoken / Gemini SentencePiece / 在线计数 / 启发式
└── frontend/                 React 18 + TypeScript + Vite + Ant Design 5 + ECharts
```

- 数据保存在本机 SQLite（WAL 模式），无需外部数据库。
- 前端构建产物默认输出到 `backend/web`，Go 服务自动托管，也支持前后端分别部署。
- 默认监听 `127.0.0.1:8788`；开发模式前端 `http://127.0.0.1:5173`，Vite 代理 `/api`。

---

## 2. 快速开始

### 2.1 环境要求

- Go 1.26+
- Node.js 20+ / npm 10+
- Windows / Linux / macOS 均可

### 2.2 开发模式

```bash
# 终端 1：后端
cd backend
go run ./cmd/server

# 终端 2：前端
cd frontend
npm install
npm run dev
```

打开 `http://127.0.0.1:5173`。

### 2.3 一键构建

```bash
./build.sh          # Linux / macOS / Git Bash
.\build.ps1         # Windows PowerShell
```

产物：

- `apiunit` / `apiunit.exe`
- `backend/web/`（前端静态文件）

运行：

```bash
DATA_DIR=./backend/data ./apiunit
# 或 Windows PowerShell:
# $env:DATA_DIR="D:\workspace\API_Unit\backend\data"; .\apiunit.exe
```

`WEB_DIR` 未显式设置时，会在 `web/` 与 `backend/web/` 中自动探测。

### 2.4 本地模拟上游（可选）

```bash
cd backend
go run ./cmd/mock-upstream -addr 127.0.0.1:9999
```

在系统中添加上游：

- Base URL：`http://127.0.0.1:9999`
- API Key：`sk-test`
- 协议：`自动`

模拟模型：

- `mock-gpt-4o-mini` / `mock-gpt-4o`：普通响应
- `mock-safe-model`：拒绝（用于注入防御）
- `mock-unsafe-model`：输出攻击标记（用于注入成功）
- `mock-inflate-model`：输入 Token 虚增 50%（用于审计）
- `mock-broken-model`：HTTP 500
- `mock-rate-model`：HTTP 429
- `mock-claude-sonnet` / `mock-gemini-2.5-flash`：原生 Claude / Gemini 路径

---

## 3. 对接 New API

1. 在 New API 创建令牌（`sk-...`）。
2. 本系统「上游 / 供应商」→ 添加上游：填写网关 Base URL、令牌、协议（New API 建议 `openai` 或 `自动`）。
3. 点击「取模型」或「测试连接并获取模型」，确认模型列表可见。
4. 新建测试时选择上游与模型即可。

请求路径遵循标准协议：

- OpenAI：`POST {base}/v1/chat/completions`，`Authorization: Bearer <key>`
- Claude：`POST {base}/v1/messages`，`x-api-key` + `anthropic-version`
- Gemini：`POST {base}/v1beta/models/{model}:generateContent`，`x-goog-api-key`

Base URL 支持带或不带 `/v1`、`/v1beta`、`/v1/chat/completions` 等后缀，保存时自动规范化。

---

## 4. 环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `ADDR` | `127.0.0.1:8788` | 监听地址 |
| `DATA_DIR` | `./data` | SQLite、加密密钥、分词器模型目录 |
| `WEB_DIR` | 自动探测 | 前端构建目录 |
| `AUTH_PASSWORD` | 空 | 设置后启用登录密码；接口使用 HMAC 签名 Cookie |
| `RETENTION_DAYS` | `30` | 历史测试保留天数，启动与每日清理 |
| `MAX_CONCURRENT_RUNS` | `4` | 同时运行的测试数上限 |
| `MAX_REQUEST_BODY_MB` | `32` | API 请求体上限 |
| `TOKENIZER_DIR` | `$DATA_DIR/tokenizers` | 分词器文件目录 |
| `OFFLINE` | `false` | `true` 时禁止联网下载 Gemini 分词器 |
| `API_UNIT_VERSION` | `1.0.0` | 展示版本 |
| `HTTP_DIAL_TIMEOUT_SEC` | `15` | TCP 连接超时（秒） |
| `HTTP_TLS_TIMEOUT_SEC` | `30` | TLS 握手超时（秒） |
| `HTTP_RESPONSE_TIMEOUT_SEC` | `60` | 等待响应头超时（秒） |
| `DISCOVER_TIMEOUT_SEC` | `45` | 获取模型列表的单次请求超时（秒） |
| `DISCOVER_TOTAL_TIMEOUT_SEC` | `120` | 获取模型列表的总超时（秒），含重试 |
| `DISCOVER_RETRIES` | `2` | 取模型失败时的重试次数（仅重试超时/网络错误/429/5xx） |
| `HTTPS_PROXY` / `HTTP_PROXY` | 空 | 访问上游的 HTTP 代理；需在启动前设置 |

> 若访问 HTTPS 上游频繁出现 `TLS handshake timeout`，通常是到该域名的链路不稳定或被限速。优先设置 `HTTPS_PROXY` 后重启服务，或调大 `HTTP_TLS_TIMEOUT_SEC` / `DISCOVER_TIMEOUT_SEC` / `DISCOVER_RETRIES`。

安全说明：

- API Key 使用随机 32 字节密钥 AES-256-GCM 加密后入库，接口只返回掩码；不会写入浏览器 `localStorage`。
- 默认只监听本机。若暴露到网络，请务必设置 `AUTH_PASSWORD` 并放在可信反向代理之后。
- 本系统不再提供任意 URL 代理接口，避免 SSRF 面。

---

## 5. 核心设计

### 5.1 并发压测

- 模式：`fixed`（固定请求数）、`duration`（持续时长）、`ramp`（并发爬坡）。
- 调度：闭环保并发；可选 RPS 上限；可选多模型并行。
- 指标：成功/失败/取消、成功率、QPS、RPM（成功请求/分钟）、延迟 P50/P90/P95/P99、TTFT、TPOT、输出 tok/s、Token、缓存 Token、成本、状态码、错误分组、逐秒序列。
- **成功判定**：HTTP 2xx + 可解析的协议信封 + 实际输出（文本/推理/工具调用）。仅 usage、空 candidates、无法解析的 SSE 数据块、缺少终止标记的流都会计为失败（`empty` / `stream` / `blocked`）。
- 请求/响应正文捕获默认关闭；开启后落库上限为请求 8MB、响应 1MB、输出文本 256KB，超出部分标记 `truncated`。JSON/HTML/CSV 导出均内嵌正文。
- **真实并发**：250ms 采样在途请求数，不再把"配置并发"当作实际并发。
- **延迟分位只统计成功请求**，失败请求单独保留错误延迟，避免污染 P95/P99。
- 状态码 `0` 统一标记为“网络/无响应”，不再显示为 HTTP 0。
- 重试可选（默认关闭），支持 429/5xx/流中断/网络错误与退避、`Retry-After`。
- 实时 SSE：每 500ms 推送**有界快照**（序列下采样至 600 点、最近 50 条），避免长测试消息无限增长。
- 结果落库：批量写入，大结果集分页查询；支持 JSON/HTML/CSV 导出。

### 5.2 Token 审计

- 多目标 × 多提示词 × 重复次数，矩阵展示。
- 每个目标可单独选择分词器策略：
  - `auto`：OpenAI → tiktoken；Gemini → 本地 SentencePiece；Claude → 启发式
  - `tiktoken`：内置 `o200k_base` / `cl100k_base`，离线可用
  - `gemini`：官方 Gemma 3 / Gemma 2 SentencePiece 模型（首次下载并 SHA-256 校验；CDN / GitHub 官方源 / 代理镜像依次回退）
  - `online`：调用 OpenAI `POST /v1/responses/input_tokens`（失败自动回退）
  - `heuristic` / `none`
- 判定：
  - `DeltaPct`：相对各目标中位基线（`min` 或 `median`）
  - `SpreadPct`：同一目标重复请求的最大/最小波动
  - `OverheadPct`：上游上报输入 Token 相对本地分词器的偏差（**判断注入的主证据**）
  - 缺少 `usage` 会标记 `no_usage`，避免把估算值当真实计费。
- 跨模型家族（GPT vs Gemini）的 Token 差异天然存在，UI 和报告中会明确提示。

### 5.3 请求差异审计（本次重点）

**快照保存字段**：测试 ID、请求 ID、模型、端点、请求时间、完整请求正文、正文 SHA-256、正文大小、用例、目标标签、重复序号、canonical 请求。

**分区指纹**（每区独立 SHA-256，便于识别新增/删除/替换/篡改）：

- System Prompt
- User Prompt（最后一个 user 消息）
- 历史对话
- Tools / Function Declarations / Tool Choice / 内置工具
- Function Response
- Generation Config
- Safety Settings
- Cached Content
- 图片 / 音频 / 视频 / 文件（多模态）
- Web Search
- 其他额外字段

**结构化 Diff**：

- 操作：`add` / `remove` / `replace`
- 定位：标准 JSONPath（如 `$.contents[0].parts[1].inlineData`）
- 分类：System Prompt、User Prompt、历史、Tools、Function Response、生成参数、安全设置、缓存、多模态、Web Search、其他
- 模式：
  - `wire`：协议原生请求（重点覆盖 `systemInstruction`、`contents`、`tools`、`generationConfig`、`cachedContent`、`safetySettings`、`image_url`、`inlineData` 等）
  - `canonical`：协议无关的逻辑请求，适合跨协议/跨上游比较
- 大字段处理：base64/超大字符串在 Diff 中以长度 + SHA-256 摘要呈现，避免页面卡死。
- 左右 JSON Tree 高亮 + 差异明细表（修改前/后、JSONPath、分类、级别）。
- 支持选择任意历史快照对比（跨测试），并导出 JSON / 独立 HTML 报告。

**隐藏指令扫描**（确定性规则，无额外成本）：

- 忽略/覆盖先前指令（中英文）
- 角色覆盖、DAN/越狱式设定
- 引用系统提示词、开发者消息
- 诱导泄露密钥/口令/校验码
- “不要告诉用户”等隐蔽指令
- 伪造角色标记（`<|im_start|>`、`[system]`、`BEGIN SYSTEM PROMPT` 等）
- HTML 注释
- 零宽字符、双向控制字符
- Tools 描述/Function Response 中的劫持与外发指令、外部 URL
- 长 base64 文本（提示可能存在编码隐藏指令）
- 多模态 URL/文本同样纳入扫描

**重要边界**：快照记录的是**本工具实际发出的请求**。如果 New API 在服务端进一步改写请求，本系统无法直接观测；此时需要结合 New API 日志核对。本模块不读取也不修改 New API 源码。

### 5.4 提示词注入

- 内置 30+ 用例，覆盖：系统提示词泄露、指令覆盖、越狱、数据外泄、编码绕过、角色扮演、间接注入、工具滥用。
- 判定策略：
  - `rule`：canary 泄露、关键词/正则命中、拒绝话术；零成本、可复现
  - `hybrid`：规则无法判定时才调用 LLM 裁判，降低裁判成本
  - `llm`：全部交给裁判模型
- 裁判输出严格 JSON（`success` / `blocked` / `unclear`），解析失败自动降级。
- ASR = 注入成功 /（成功 + 防御 + 无法判定），执行失败不计入分母。
- 记录裁判 Token 与成本。
- 内置用例只使用无害标记（canary、`OVERRIDE-OK`），用于授权安全测试。

---

## 6. API 摘要

所有接口位于 `/api`；设置 `AUTH_PASSWORD` 后除健康检查与登录外均需 Cookie。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/health` | 健康检查 |
| GET | `/api/auth/status` | 登录状态 |
| POST | `/api/auth/login` / `logout` | 登录/退出 |
| GET/POST/PUT/DELETE | `/api/targets...` | 上游管理、测试连接、获取模型 |
| GET/POST/PUT/DELETE | `/api/prompts...` | 提示词库 |
| GET/POST/PUT/DELETE | `/api/attacks...` | 攻击用例、导入导出、恢复内置 |
| GET/POST/PUT/DELETE | `/api/prices...` | 模型定价 |
| GET | `/api/tokenizers/status` | 分词器状态 |
| POST | `/api/tokenizers/count` | Token 计数预览 |
| POST | `/api/tokenizers/ensure` | 下载/校验 Gemini 分词器 |
| POST | `/api/tokenizers/reset` | 清除本地分词器文件与缓存错误；`{download:true}` 时重新下载 |
| POST | `/api/runs` | 创建测试 `{kind, config}` |
| GET | `/api/runs...` | 列表、详情、停止、删除 |
| GET | `/api/runs/{id}/events` | SSE：`snapshot` / `update` / `done` |
| GET | `/api/runs/{id}/items` | 结果分页 |
| GET | `/api/runs/{id}/snapshots` | 请求快照列表 |
| GET | `/api/snapshots/{id}` | 快照详情（wire + canonical） |
| POST | `/api/diffs` | `{leftId, rightId, mode}` 结构化 Diff |
| GET | `/api/diffs/export` | Diff 报告 JSON/HTML |
| GET | `/api/runs/{id}/export` | 测试报告 JSON/HTML/CSV |

测试类型 `kind`：`stress`、`token_audit`、`request_diff`、`injection`。

---

## 7. 扩展性：新增测试类型

后端通过 `engine.Module` 注册测试类型：

```go
type Module interface {
    Kind() model.RunKind
    Title() string
    Validate(raw json.RawMessage) (any, error)
    Run(ctx context.Context, sess *engine.Session, cfg any) error
    Summary(sess *engine.Session) model.RunSummary
}
```

新增测试类型的最小步骤：

1. 在 `backend/internal/modules/<kind>/` 实现 `Module`，复用 `engine.Session.Exec`（自动获得超时、重试、限速、Token 统计、成本、落库、SSE）。
2. 在 `cmd/server/main.go` 调用 `manager.Register(...)`。
3. 前端新增表单页面与结果视图；如需新字段，扩展 `RunSummary.data`（模块自定义 JSON）即可，无需改引擎。
4. 如果测试涉及新协议（如图片生成），在 `internal/protocol` 增加请求构建/响应解析；`ChatRequest.ExtraBody` 可直接注入未建模的协议字段。

例如后续生图测试：

- 请求：`POST /v1/images/generations`，参数 `model/prompt/n/size/quality/response_format`。
- 可复用调度器、并发、延迟/TTFT 指标、成本、SSE、导出。
- 新增 `image` 分类的请求快照与差异字段（如 `size`、`quality`、`n`）。

---

## 8. 验证与测试

```bash
cd backend
go test ./...
go vet ./...

cd ../frontend
npx tsc --noEmit
npm run build
```

已覆盖的测试：

- Base URL 规范化
- OpenAI / Claude / Gemini 富请求构建（tools、历史、Function Response、多模态、safety、cached、generationConfig）
- SSE 流式解析与 usage 合并
- API 错误体解析（JSON / HTML）
- JSONPath Diff 与分类
- 分区指纹与隐藏指令规则
- tiktoken / 启发式 / Gemini 规格选择
- 注入规则判定与裁判 JSON 解析

端到端已验证：

- 固定压测、持续压测与 SSE 实时更新、停止
- Token 审计矩阵与异常检测
- 请求差异审计：快照、wire/canonical Diff、隐藏指令、HTML/JSON 导出
- 注入测试：规则模式、混合模式、LLM 裁判
- 可选密码登录
- Mock 上游下的全部页面

---

## 9. 设计取舍（相较旧系统的优化）

- 去掉 localStorage 存密钥与纯前端配置，统一服务端存储与加密。
- 去掉任意 URL 代理接口，避免 SSRF。
- SSE 由“每次全量、无限增长”改为“有界快照 + 序列下采样 + 最近 N 条”。
- 实际并发采样，不再把配置并发当实际并发。
- 延迟分位只统计成功请求；补充 TPOT。
- 持久化 SQLite；结果分页查询，不再把前 20k 条塞进一次性 JSON。
- usage 缺失时区分 `estimated` / `mixed` / `no_usage`，避免把估算当计费。
- 请求差异审计复用 Token 审计的统计与报告结构，不重复实现 Token 统计。
- Claude 本地分词器暂不集成（需要注册与授权成本），以 usage 为主、启发式为辅。
- 修复旧系统的成功徽标硬编码 200、网络错误显示 HTTP 0、耗时统计软截止、限流/重试缺失等问题。

---

## 10. 已知限制

- 请求快照反映的是本工具发出的请求；服务端改写需结合网关日志。
- 本地分词器对 tools、多模态、System Prompt 模板的估算存在误差，审计结论以 usage + 偏差方向为主。
- New API 的 `RetryTimes > 0` 会让一次客户端请求命中多次上游，压测延迟/Token 可能不对应单次调用，建议压测时将网关重试设为 0。
- base64 大字段在 Diff 中以摘要展示，原始值仍保存在快照中。
- 超大规模压测受 SQLite 写入与本机资源限制；结果落库采用批量与缓冲，极端 QPS 下可能丢弃部分明细（会记录 `persistDropped` 计数）。
