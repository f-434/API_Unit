# Token 口径与手工 Token 基准 · 进度文档

- 日期：2026-11-26
- 状态：功能已实现并通过测试，**代码尚未提交**；运行中的 8788 后端需要重启
- 术语：原“Token 家族”已统一改名为 **Token 口径**

---

## 1. 需求背景

Token 审计的流程是：提示词库维护提示词 → 审计页选择提示词和目标 → 对比上游返回的输入 Token 与本地 Token 数。

本地 Token 原先完全依赖分词器估算，与实际值常有偏差。本次增加：

1. 渠道（上游）可标注 Token 口径（家族 / 平台，任意层级）。
2. 提示词可按口径维护实际输入 Token 数。
3. 未维护时回退本地分词器；维护后以手工值为准。
4. 审计页自动继承渠道的协议、分词器，不再逐行手选。
5. 审计报告可把实测值一键写回提示词基准。

---

## 2. 已完成的改动

### 2.1 数据模型

| 对象 | 字段 | 说明 |
| --- | --- | --- |
| `targets` | `token_family` | 渠道口径路径，如 `gemini/vt` |
| `prompts` | `token_overrides_json` | 路径 → 手工 Token 数，如 `{"gemini":31,"gemini/vt":34}` |
| `prompts` | `token_overrides_hash` | 维护基准时的内容 SHA-256，用于自动失效 |

- 设置表 `settings` 的键 `token.families` 保存口径树。
- 旧数据库通过 `ensureColumn` 自动补列，已在真实数据库副本上验证迁移。

**默认口径树**（可在设置页任意增删、改名称；标识创建后不可改）：

```text
gemini  → as, vt
gpt     → openai, az
claude  → an, awsb
```

> `12`、`3` 只是用户举例，已确认**不内置**。

### 2.2 匹配规则

提示词基准按以下顺序匹配渠道口径：

```text
完整路径 → 逐级父路径 → 本地分词器
```

示例：

```json
{
  "gemini": 31,
  "gemini/vt": 34
}
```

- 渠道口径 `gemini/vt` → 命中 34。
- 渠道口径 `gemini/as` → 回退父级，命中 31。
- 没有命中 → 使用分词器。

### 2.3 内容变更保护

- 保存基准时记录当前提示词内容的 SHA-256。
- 内容变更后摘要不一致，基准自动失效并回退分词器。
- 普通保存不会把旧数值重新启用。
- 以下操作会重新校准并更新摘要：
  - 修改任意一个基准数值；
  - 编辑弹窗勾选“沿用现有校准值，并按新内容重新校准”；
  - 审计报告点击“存基准”。
- 报告“存基准”时，如果原基准已失效，只保留本次写入的值，避免把其他过时的口径值一并启用。

### 2.4 审计运行时

- `engine.ExecRequest` 新增 `LocalTokensOverride`。
- 命中手工基准时：
  - `LocalPromptTokens` 使用手工值；
  - 来源标记为 `manual`；
  - 响应缺少 usage 时也用它兜底。
- 报告判定：
  - `OverheadPct = 上游上报输入 Token - 本地基准`；
  - `localSource = manual` 参与注入判定；
  - 原因显示“上报值比手工基准高 X%”。
- 渠道口径存在时，本地可比性判定优先信任口径，而不是模型名（适配中转站重命名模型）。
- 口径为空或无法识别时，回退按模型名推断。

### 2.5 审计页自动映射

Token 审计的对比目标行已移除「协议」「分词器」两个手动选择框：

- 协议：直接使用所选渠道的协议；渠道为 `auto` 时保持自动推断。
- 分词器：按渠道口径根节点推导：
  - `gemini` / `google` → `gemini`
  - `gpt` / `openai` / `azure` / `az` → `tiktoken`
  - 其他或未标注 → `auto`
- 预算估算与实际提交使用同一套推导逻辑。
- 请求差异审计页面**仍保留**逐行手动选择，未改动。

### 2.6 一键回填

审计报告矩阵中每个成功单元格都有“存基准”：

- 写入值：本次重复请求的输入 Token 中位数。
- 写入路径：该目标渠道的口径。
- 目标渠道未标注口径时不显示该按钮。
- 写入后刷新提示词列表。

### 2.7 前端页面

| 页面 | 改动 |
| --- | --- |
| `/prompts` | 新增“本地 Token”列；编辑弹窗按口径树逐级填写校准值；失效提示；新增/编辑切换时清空表单，修复数据串 |
| `/targets` | 上游表单新增“Token 口径”级联选择；列表新增对应列 |
| `/settings` | 新增“Token 口径”树编辑器：名称 / 标识 / 操作三列，支持任意层级新增、删除、保存 |
| `/audit/new` | 移除协议、分词器选择；自动按渠道映射 |
| 审计报告 | 显示“手工偏差”；单元格增加“存基准” |

### 2.8 修复记录

- **数据串**：编辑不同提示词时复用同一个 Form 实例，AntD 默认 `preserve: true` 导致上一条提示词的校准值残留。已通过 `form.resetFields()` + `Form preserve={false}` 修复并验证。
- **示例被内置**：`gemini/12`、`gemini/3` 已从默认口径树移除。

---

## 3. 主要代码位置

### 后端

- `backend/internal/tokenmeta/tokenmeta.go`
  - 口径树默认值、加载/保存/校验；
  - 基准归一化、失效判断、路径匹配。
- `backend/internal/model/model.go`
  - `Target.TokenFamily`；
  - `Prompt.TokenOverrides / TokenOverridesHash / TokenOverridesStale`；
  - `HashText`。
- `backend/internal/store/store.go`
  - 迁移与 `targets` / `prompts` 读写。
- `backend/internal/api/handlers.go`
  - 渠道口径校验；
  - 提示词基准创建/更新/导入；
  - `POST /api/prompts/{id}/token-reference`；
  - `GET/PUT /api/token-families`；
  - `/api/tokenizers/count` 支持 `promptId`；
  - `/api/meta` 返回 `tokenFamilies`。
- `backend/internal/api/server.go`
  - 新增路由。
- `backend/internal/engine/session.go`
  - 手工基准注入。
- `backend/internal/modules/tokenaudit/audit.go`
  - 审计时解析基准；
  - 报告字段 `TokenFamily`；
  - 判定逻辑与说明文案。
- `backend/internal/protocol/protocol.go`、`modules/modcommon/target.go`
  - `TargetInfo.TokenFamily` 传递。

### 前端

- `frontend/src/pages/Prompts.tsx`
- `frontend/src/pages/Settings.tsx`
- `frontend/src/pages/Targets.tsx`
- `frontend/src/pages/AuditNew.tsx`
- `frontend/src/components/TargetFormModal.tsx`
- `frontend/src/components/TargetModel.tsx`
- `frontend/src/components/AuditReportView.tsx`
- `frontend/src/types.ts`
- `frontend/src/api.ts`
- `frontend/src/styles.css`（口径树样式）

### 测试

- `backend/internal/tokenmeta/tokenmeta_test.go`
- `backend/internal/api/tokenreference_test.go`
- `backend/internal/modules/tokenaudit/audit_test.go`

### 文档

- `README.md`：Token 审计、接口表已同步。
- 本文档。

---

## 4. 接口速查

```http
GET    /api/token-families
PUT    /api/token-families            { "items": [ ...树... ] }
POST   /api/prompts/{id}/token-reference
       { "family": "gemini/vt", "tokens": 123 }   // tokens<=0 表示删除该路径
POST   /api/tokenizers/count
       { "model":"...", "strategy":"gemini", "targetId":"...", "promptId":"...",
         "messages":[{"role":"user","content":"..."}] }
```

`prompts` 创建/更新请求新增：

```json
{
  "tokenOverrides": { "gemini/vt": 34 },
  "recalibrate": false
}
```

`targets` 创建/更新请求新增：

```json
{ "tokenFamily": "gemini/vt" }
```

---

## 5. 验证情况

- `go test ./...` 全部通过。
- `npm run build` 通过。
- 浏览器实测：
  - `/prompts` 编辑弹窗口径树、数值保存；
  - 不同提示词之间不再串数据；
  - `/targets` 口径级联选择；
  - `/settings` 口径树新增、保存、刷新保留、删除；
  - `/audit/new` 目标行不再显示协议/分词器；
  - 渠道 `gemini/vt` 时估算请求实际发送 `strategy: "gemini"`；
  - 提交后的运行配置为 `protocol` 取渠道值、`tokenizer` 取口径推导值；
  - 浏览器无控制台错误（测试页的模型列表 400 来自临时假地址，与本次功能无关）。
- 用 130MB 真实数据库副本验证迁移，旧提示词和渠道无损。

构建产物：

- `backend/web` 已更新；
- 根目录 `apiunit` / `apiunit.exe` 已重新生成。

---

## 6. 当前注意事项

1. **必须重启 8788 后端**：当前运行的是旧的 `go run` 进程，不包含新接口；否则新前端请求 `/api/token-families` 会 404。
2. 现有渠道的 `token_family` 默认为空，需要在上游表单逐个标注口径；未标注时仍按分词器工作。
3. 审计页的“对比本地分词器”开关（`CheckLocal`）原本就没有接入后端判定，本次未处理。
4. 请求差异审计仍使用逐行手动选择协议/分词器。
5. 口径树的“标识”是路径的一部分，创建后不建议修改；删除节点不会删除提示词里已有的基准数据，那些数据会保留但不再匹配。
6. 代码未提交 git。

---

## 7. 明天可继续的方向

按优先级参考：

1. **渠道口径批量维护**
   - 现有 65 个渠道手动标注较繁琐；
   - 可增加列表批量设置，或根据名称/域名给出建议值（如名称含 `gemini-as` → `gemini/as`）。
2. **请求差异审计是否也自动映射**
   - 如果确认要统一，可复用 `tokenizerForCaliber`。
3. **接上“对比本地分词器”开关**
   - 关闭时不做本地/手工偏差判定，避免当前开关无效。
4. **审计报告展示口径与来源**
   - 目标表头显示口径路径和本地来源（manual / tiktoken / gemini），方便排查。
5. **模型级基准**
   - 当前支持到平台级；如果同一渠道下不同模型 Token 差异明显，可扩展到第三级 `gemini/vt/gemini-2.5-pro`。

---

## 8. 常用命令

```bash
# 后端
cd backend
go test ./...
go run ./cmd/server

# 前端
cd frontend
npm run dev
npm run build

# 完整构建
./build.sh          # Linux / macOS / Git Bash
.\build.ps1         # Windows PowerShell
```
