# DoneBot 网关改造方案

> 历史初始方案。后续已按用户要求扩展视频、音频；当前 LLM 使用渠道价 × 每模型倍率，媒体按模型配置售价，管理页统一保存开关与定价；当前实现以[模型定价说明](模型定价说明.md)、[Seedance 接入说明](Seedance-接入说明.md)和[APIMart 接入说明](APIMart-接入说明.md)为准。原文保留用于追溯阶段边界。

> 2026-11-04 更新：视频与音频独立总开关已移除；原文按总开关隐藏入口、限制调用的方案不再适用。当前由账号能力、分组模型启用状态及有效价格决定可用性。

- 版本：v0.1（2026-11-03）
- 代码基线：`/Users/zluo/Dev/donebot-api`（干净 sub2api）
- 文档性质：施工方案，可直接交给 Codex 分阶段执行
- 不在范围：DoneBot 客户端二开、`02-统一网关接入方案.md`、视频、音频、报价单

---

## 0. 项目边界

1. 本仓库是 **DoneBot 的模型网关**，不是 DoneBot 本体。网关不 import DoneBot 代码，DoneBot 不 import 网关代码。
2. 两边只通过第 6 节的 HTTP 契约耦合。契约先改，实现后改。
3. DoneBot 客户端文档 `01-开发指导/02-统一网关接入方案.md` 已冻结。本方案不读取、不修改、不引用它。
4. 视频、音频这期不做，但数据模型和路由要留位置，避免以后推倒重来。
5. 不引入“报价单 / quote / 预授权单”。LLM 按 token 计量，图片按张或按尺寸计价，提交时服务端算价、查余额、不足直接拒绝。

---

## 1. 基线盘点：已经有的，不要重写

先读代码再动手。下面这些能力在新 sub2api 里已经存在，改造应复用它，而不是另起一套。

| 能力 | 现状 | 位置 | 本方案怎么用 |
|---|---|---|---|
| 多平台 LLM 原生反代 | 已有 Anthropic / OpenAI / Gemini / Grok / Kimi / GLM / DeepSeek / MiniMax 等平台 | `backend/internal/server/routes/gateway.go`、`backend/internal/domain/platforms.go` | 直接使用，不改上游协议 |
| 聚合分组 | `platform=composite` 已是一等公民，可按模型路由到具体平台 | `backend/ent/schema/group.go`、`backend/internal/service/composite_route_resolver.go` | DoneBot 聚合分组就用 composite |
| 分组模型开关 | `groups.model_allowlist`（`{enabled, models[]}`），同时约束 `/v1/models` 和请求准入 | `backend/internal/server/middleware/group_model_allowlist.go` | 管理页的“模型开关”直接写这个字段 |
| LLM 分组倍率 | `groups.rate_multiplier`，渠道价之上乘分组倍率 | `backend/ent/schema/group.go` | 管理页只配倍率，不重复维护单价 |
| 图片计价 | `image_price_1k/2k/4k`、`image_rate_multiplier`、`image_rate_independent`、按张计费开关 | `backend/ent/schema/group.go` | 图片页直接读写这些字段 |
| 图片同步接口 | `/v1/images/generations`、`/v1/images/edits` | `backend/internal/server/routes/gateway.go` | 保持不变 |
| 图片异步任务 | `POST /v1/images/generations/async`、`POST /v1/images/edits/async`、`GET /v1/images/tasks/{id}`；Redis 存任务，结果转存对象存储 | `docs/ASYNC_IMAGE_TASKS.md`、`backend/internal/service/image_task.go` | 复用，补平台与本地存储 |
| 对象存储 | `ImageStorage.Save()` 抽象 + S3 兼容实现，支持 AWS S3 / Cloudflare R2 / 阿里云 OSS / MinIO | `backend/internal/service/image_storage.go`、`backend/internal/repository/image_storage_s3.go` | 复用它，抽出通用 Asset 存储并增加 local 驱动 |
| 管理端分组页 | `GroupsView.vue` + `components/admin/group/*` | `frontend/src/views/admin/GroupsView.vue` | 新页面复用其组件与接口风格 |
| API Key 鉴权与计费 | Key 绑定用户与分组，带配额、限流、用量 | `backend/internal/server/middleware/api_key_auth.go`、`backend/ent/schema/api_key.go` | **内部复用**，对 DoneBot 用户隐藏 |

**结论**：用户要的能力里，大约七成基线已有。真正要写的是“DoneBot 模式”这一层产品化：

1. 免手工 API Key 的安装绑定；
2. 一个自动创建的 DoneBot 聚合分组；
3. 面向 DoneBot 的 LLM / 图片管理页；
4. 上传缓存（本地 / R2 / OSS）；
5. 图片异步在聚合分组、本地存储下的兜底；
6. 计费口径统一为“渠道价 × DoneBot 倍率”。

---

## 2. 目标与非目标

### 2.1 目标

- DoneBot 用户登录自己的账户后即可使用，**不需要创建、复制、粘贴 API Key**。
- 一个 DoneBot 聚合分组聚合所有 LLM 与图片模型，模型可逐个开关。
- LLM 价格 = 渠道定价 × DoneBot 倍率；图片价格 = 分组图片价 × 倍率。
- 图片支持同步与异步两种调用，上游调用方式不变，产物可保存到本地 / R2 / OSS。
- 文件上传后按内容哈希缓存，重复上传不重复占空间。
- 管理页只有 LLM、图片两页；视频、音频入口按开关隐藏。
- 全部能力走 sub2api 已有的鉴权、计费、日志、限流，不另造账本。

### 2.2 非目标

- 不做报价单、估价单、客户端价格确认。
- 不改 Anthropic / OpenAI / Gemini 的请求与响应格式。
- 不做视频、音频页面（数据模型留字段即可）。
- 不做套餐、发票、对账单；计费直接复用 sub2api 的用户余额与用量日志，按 DoneBot 用户逐人记账。
- 不重写 sub2api 的账号调度、渠道管理、支付系统。

---

## 3. 运行模型

### 3.1 用户模型

一个网关实例服务一个 DoneBot 运营方下的全体用户。网关自己就是用户体系：DoneBot 用户的注册、登录、余额、用量都落在网关的用户表里，**不存在“一个隐藏租户代表所有用户”这层**。

```text
DoneBot 用户（真实账号，邮箱注册 / 登录）
   │  登录后领取安装凭据（每台设备一把，挂在本人名下）
   ▼
DoneBot 客户端 ──HTTPS + 安装 Key（仅服务端持有，用户不可见）
   ▼
DoneBot 网关（sub2api）
   ├── 用户账户 / 余额 / 限流 / 用量日志
   ├── 安装记录 ──► 该用户的一把隐藏 Key ──► DoneBot 聚合分组
   └── 渠道 / 账号池 ──► 各上游官方接口
```

要点：

- 余额按用户记：用户 A 余额 0.2 元，提交 0.25 元的图片任务，网关直接拒绝，不影响用户 B。
- 一个用户多台设备 = 多条安装记录、多把 Key，余额仍归到同一个用户名下。
- 运营方管理员是独立的管理账号，只负责分组、模型开关、价格、充值、封禁，不参与调用计费。
- **安装凭据只由 DoneBot 流程管理，用户和余额不隐藏。** 用户不创建或管理普通 API Key。

好处：完全复用 sub2api 现成的注册、登录、余额、配额、限流、用量日志、异步任务归属，主链路不需要重写。

### 3.2 为什么用户仍然不接触 Key

“不要 API Key”是产品要求，不是密码学要求。公网网关必须能识别调用方，否则任何人拿到地址就能烧钱。做法是：

- Key 由网关在“领取安装”时自动生成，挂在当前登录的用户名下；
- 只返回一次，DoneBot 写入自己的配置；
- 管理端不展示明文，只展示安装记录、最后在线时间、吊销按钮；
- 用户全程不接触 Key，只看到“已连接”。

---

## 4. 改造点

### 4.1 DoneBot 模式与安装绑定

#### 配置

在 `backend/internal/config/config.go` 增加：

```yaml
donebot:
  enabled: true
  group_name: "DoneBot"
  auto_create_group: true
  features:
    video: false
    audio: false
```

环境变量前缀沿用现有风格：`DONEBOT_ENABLED`、`DONEBOT_GROUP_NAME` 等。

#### 启动时

在服务启动、迁移完成之后执行 `EnsureDoneBotGroup()`：

1. `donebot.enabled=false` 时直接返回。
2. 按 `group_name` 查找分组；没有则创建：
   - `platform=composite`
   - `status=active`
   - `rate_multiplier=1.0`
   - `model_allowlist={enabled:true, models:[]}`（空表示还没选模型，管理页首屏引导“同步模型”）
   - 图片计价字段留空，表示回退到渠道/内置价
3. 把分组 ID 写入设置表 `donebot.group_id`，避免按名字反复查。
4. 不创建任何用户；用户由注册/登录产生，Key 只在领取安装时创建。

启动逻辑要幂等，重复启动不产生重复分组。

#### 领取安装接口

DoneBot 用户登录后，用会话 token 领取本机安装凭据：

```http
POST /api/v1/donebot/installations/claim
Authorization: Bearer <access_token>
{ "installation_id": "<uuid>", "install_name": "我的 Mac", "fingerprint": "..." }
```

返回：

```json
{
  "install": { "id": 12, "name": "我的 Mac", "status": "active", "created_at": "..." },
  "credential": "sk-donebot-...",
  "gateway_url": "https://gateway.example.com"
}
```

`credential` 只在这一次响应里出现，之后不可读。同一个 `installation_id` 重复领取返回同一条安装记录，不重复建 Key。

运营方管理端另有只读与吊销接口：

```http
GET    /api/v1/admin/donebot/installs
DELETE /api/v1/admin/donebot/installs/{id}
```

#### 鉴权

安装凭据复用 `api_keys` 表和现有鉴权计费链路，绑定**领取它的 DoneBot 用户**与 DoneBot 分组，但只通过安装领取接口创建，用户侧普通 API Key 管理入口在 DoneBot 模式下关闭。因此：

- `api_key_auth.go` 不改；
- 计费、限流、用量、异步任务归属全部照旧；
- 吊销安装 = 停用这把 Key；
- `last_used_at` 直接用作“最后在线”。

唯一要加的是 `/api/v1/donebot/installations/claim` 与管理端安装列表，以及把 Key 的创建包装成“安装”。

#### 数据表

```sql
CREATE TABLE donebot_installs (
  id            BIGSERIAL PRIMARY KEY,
  name          VARCHAR(100) NOT NULL,
  user_id       BIGINT NOT NULL,
  api_key_id    BIGINT NOT NULL,
  group_id      BIGINT NOT NULL,
  status        VARCHAR(20) NOT NULL DEFAULT 'active',
  fingerprint   VARCHAR(128),
  last_seen_at  TIMESTAMPTZ,
  expires_at    TIMESTAMPTZ,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_donebot_installs_status ON donebot_installs(status);
```

不给 `api_keys` 加字段，避免污染上游表结构；安装与 Key 的对应关系放在这张表。

---

### 4.2 DoneBot 聚合分组

#### 平台与路由

- 分组 `platform=composite`。
- 优先用内置识别：`claude-*`→Anthropic、`gemini-*`→Gemini、`gpt-*`/`o*`/`codex-*`/`dall-e-*`→OpenAI、`grok-*`→Grok，其余按 `backend/internal/domain/platforms.go` 的登记表。
- 同名冲突或需要改名的模型，用 `composite_model_routes` 显式配：`public_model`、`target_platform`、`upstream_model`、`endpoint`、`priority`。
- 图片模型要显式配 `endpoint=images`，避免和同名文本模型抢路由。

#### 模型清单

管理页的“同步模型”做三件事：

1. 从启用中的渠道/账号能力表拉可用模型（以现有模型目录接口为准，P0 核对具体 service）。
2. 与渠道定价表求交集，得到“有价可卖”的模型。
3. 写入 `model_allowlist.models`；已有开关状态不覆盖，只增不减，删除走人工。

`/v1/models` 返回 `目录 ∩ allowlist`；allowlist 关闭某个模型后，请求准入直接拒绝。按用户确认，错误体与错误码沿用现有中间件，不增加 DoneBot 映射。以下仅示意拒绝结构：

```json
{ "error": { "type": "invalid_request_error", "code": "model_not_enabled", "message": "Model is not enabled for this group" } }
```

实际错误码以原版实现与验收记录为准。

#### 图片的缺口

`gateway.go` 的 `imagesHandler` 目前只对 `PlatformOpenAI` 和 `PlatformGrok` 分支；composite 分组要靠 `getGroupPlatform()` 解析到具体平台后才进得去。`docs/ASYNC_IMAGE_TASKS.md` 也写明异步只支持 OpenAI 与 Grok 分组。

P0 必须验证这三条：

1. composite 分组 + `gpt-image-*` 的同步生图是否已能走通；
2. composite 分组 + 异步生图是否被分组平台判断挡住；
3. `GET /v1/images/tasks/{id}` 是 GET、不带 model，composite 解析是否会漏。

验证结果决定改法：优先让 `imagesHandler` 和 `image_task` 用 `getGroupPlatform()` 的解析结果，而不是原始 `apiKey.Group.Platform`。

---

### 4.3 DoneBot 管理页

#### 导航

新增一级菜单 `DoneBot`，两个标签页：

- `LLM`：模型开关 + 倍率；
- `图片`：模型开关 + 图片价格 + 倍率。

视频、音频标签按 `donebot.features` 隐藏，不写空页面。

#### 后端接口

```text
GET  /api/v1/admin/donebot/overview      # 分组、用户数、模型数、今日用量
GET  /api/v1/admin/donebot/llm/models    # 模型、渠道价、开关、倍率、有效价
PUT  /api/v1/admin/donebot/llm/models    # { models: [{model, enabled}] }
PUT  /api/v1/admin/donebot/llm/pricing   # { rate_multiplier: 1.25 }
GET  /api/v1/admin/donebot/images/models
PUT  /api/v1/admin/donebot/images/models
PUT  /api/v1/admin/donebot/images/pricing
GET  /api/v1/admin/donebot/installs
DELETE /api/v1/admin/donebot/installs/:id
GET  /api/v1/admin/donebot/images/tasks/:task_id # 查询任务所属安装（不返回凭据）
POST /api/v1/admin/donebot/llm/models/sync
POST /api/v1/admin/donebot/images/models/sync
```

安装凭据的创建走用户侧接口 `POST /api/v1/donebot/installations/claim`，管理端不代建。

所有写接口沿用管理端现有的 step-up / 审计中间件。

#### LLM 页字段

| 列 | 来源 | 可编辑 |
|---|---|---|
| 模型 | 模型目录 | 否 |
| 平台 | composite 路由结果 | 否 |
| 渠道单价（输入/输出） | 渠道定价 | 否 |
| 启用 | `model_allowlist.models` | 是 |
| 倍率 | `groups.rate_multiplier` | 是（整组一个） |
| 有效价 | 渠道价 × 倍率 | 否，实时算 |

页面顶部只放一个倍率输入框，避免逐模型维护单价。有效价用后端算，前端不复制公式。

#### 图片页字段

| 列 | 来源 | 可编辑 |
|---|---|---|
| 模型 | 图片模型目录 | 否 |
| 启用 | allowlist | 是 |
| 1K / 2K / 4K 单价 | `image_price_1k/2k/4k` | 是 |
| 独立倍率 | `image_rate_multiplier` + `image_rate_independent` | 是 |
| 按张计费 | `image_count_billing` | 是 |
| 有效价 | 单价 × 倍率 | 否 |

#### 前端落点

- `frontend/src/views/admin/DoneBotView.vue`：壳 + 两个 tab。
- `frontend/src/components/admin/donebot/LlmModelsPanel.vue`
- `frontend/src/components/admin/donebot/ImageModelsPanel.vue`
- `frontend/src/components/admin/donebot/InstallList.vue`
- `frontend/src/api/admin/donebot.ts`
- 路由与菜单：`frontend/src/router`、`frontend/src/components/layout` 里现有 admin 菜单注册处。

复用 `GroupsView.vue` 的表格、开关、数字输入、保存提示，不要另造一套设计语言。

---

### 4.4 上传与缓存

#### 目标

DoneBot 生成的参考图、PDF、音频片段先上传到网关，拿到一个上游可拉取的 URL，再用于图片编辑或多模态请求；相同内容只存一份。

#### 接口

```http
POST /v1/assets
Authorization: Bearer <安装凭据>
Content-Type: multipart/form-data

file=@ref.png
purpose=image
```

返回：

```json
{
  "id": "ast_01J...",
  "url": "https://assets.example.com/a3/a3f9...png",
  "mime": "image/png",
  "size": 1834921,
  "sha256": "a3f9...",
  "expires_at": "2026-11-10T12:00:00Z",
  "deduplicated": true
}
```

查询与删除：

```http
GET    /v1/assets/{id}
DELETE /v1/assets/{id}
```

#### 存储抽象

不要把 `ImageStorage` 直接改成万能接口。新增一层：

```go
// backend/internal/service/asset_storage.go
type AssetStorage interface {
    Save(ctx context.Context, key, contentType string, data []byte) (url string, err error)
    Delete(ctx context.Context, key string) error
    Kind() string // local | s3
}
```

- `s3` 驱动复用 `repository/image_storage_s3.go` 的客户端与签名逻辑，扩成通用对象；R2、OSS、MinIO 都走 S3 兼容协议。
- `local` 驱动写到 `data/assets/<前两位>/<sha256><ext>`，通过 `GET /assets/{id}` 流式返回。
- 选择逻辑：`asset_storage.driver`，缺省 `local`；配置了 S3 完整凭据时可用 `s3`。

#### 公共 URL 的硬约束

上游 OpenAI / Anthropic / Gemini 必须能匿名拉取这个 URL。因此：

- `local` 驱动只适合网关与 DoneBot 在同一台机器、或上游也在同一内网的场景；
- 公网部署必须给 `asset_storage.public_base_url`，指向对象存储或反代域名；
- 本地文件对外暴露时必须带签名 token，且 token 只绑定 asset id 与过期时间，不能是固定密钥。

管理页在切换到 `local` 且未配置公网地址时，要显示一条明确警告：“当前地址无法被上游拉取，图片编辑和多模态请求会失败”。

#### 去重与清理

- `sha256` 唯一索引；先查后写，命中直接返回原记录并 `deduplicated=true`。
- 不做引用计数。到期由清理任务按 `expires_at` 删除对象与记录；被引用的资产由 DoneBot 侧续期（`PUT /v1/assets/{id}/renew`，Phase 2）。
- 默认 TTL 7 天，配置 `asset_storage.ttl_hours`。
- 单文件上限沿用 `gateway.max_body_size`，并在 handler 单独限制 `asset_storage.max_bytes`（默认 32 MiB）。

#### 数据表

```sql
CREATE TABLE assets (
  id           VARCHAR(40) PRIMARY KEY,
  sha256       CHAR(64) NOT NULL,
  mime         VARCHAR(100) NOT NULL,
  size         BIGINT NOT NULL,
  storage      VARCHAR(20) NOT NULL,
  object_key   TEXT NOT NULL,
  url          TEXT NOT NULL,
  owner_key_id BIGINT NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  expires_at   TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX idx_assets_sha256 ON assets(sha256);
CREATE INDEX idx_assets_expires_at ON assets(expires_at);
```

---

### 4.5 图片异步任务

基线已有完整实现，改造只做四件事：

1. **平台准入**：让 composite 分组解析到具体平台后再判断是否支持异步；Gemini 原生图片若暂不支持，明确返回 `unsupported_platform`，不要静默 404。
2. **存储驱动**：`image_storage` 增加 `driver: local | s3`，复用 4.4 的 `AssetStorage`；`local` 时结果落本地并返回签名 URL。
3. **保留期**：任务 TTL 与产物 TTL 分开配置。任务默认 24 小时，产物默认 7 天；任务过期后仍可用 asset URL 取图（如果未过期）。
4. **任务归属**：`ImageTaskRecord` 现在带 `APIKeyID`，安装 Key 天然适配，不改结构；只在管理页展示“由哪个安装提交”。

不改的部分：

- 上游仍是同步调用，worker 内部等结果，再转存；
- 请求体与同步接口一致；
- 计费在任务创建时冻结、完成时结算，失败退款；
- `GET /v1/images/tasks/{id}` 的返回结构不变。

DoneBot 侧契约固定为：

```json
{
  "id": "imgtask_...",
  "task_id": "imgtask_...",
  "object": "image.generation.task",
  "status": "processing | completed | failed",
  "http_status": 200,
  "image_url": "https://...",
  "result": { "...": "上游原始响应，已把 b64 换成 URL" },
  "error": { "code": "...", "message": "..." },
  "created_at": 1730000000,
  "completed_at": 1730000012,
  "expires_at": 1730086400
}
```

轮询沿用基线 `Retry-After: 3`。处理中任务在网关进程重启后可查询，但不会自动重放上游请求。

---

### 4.6 计费

#### LLM

```text
费用 = 渠道输入价 × 输入 token + 渠道输出价 × 输出 token
最终 = 费用 × groups.rate_multiplier
```

- 渠道价是唯一单价来源，DoneBot 页只存倍率。
- 缓存命中、长上下文阶梯价沿用渠道配置，不另设。
- 不在网关里做人民币换算；sub2api 账本仍是 USD，DoneBot 展示时按自己的汇率换算。两处都存余额会导致对不上账。

#### 图片

```text
单张 = 对应尺寸单价（1K/2K/4K）或渠道价
最终 = 单张 × 图片倍率 × 张数
```

- `image_rate_independent=false` 时用分组 `rate_multiplier`；
- 异步任务创建时按估算冻结，完成后按实际张数结算，差额释放；
- 失败全额释放，不扣手续费。

#### 余额

- 每个 DoneBot 用户用自己的余额付款，没有共享余额池；管理页可给单个用户充值/调额。
- 余额不足时提交直接拒绝，HTTP 状态与错误码沿用现有鉴权和计费链路；不新增 DoneBot 专用映射。当前余额预检为 HTTP 403、`INSUFFICIENT_BALANCE`。
- 不做客户端报价确认；DoneBot 提交前可调 `GET /v1/sub2api/billing` 之类的只读余额接口展示，但不能用它下单。

---

## 5. 数据模型汇总

| 表/字段 | 类型 | 说明 |
|---|---|---|
| `donebot_installs` | 新表 | 安装记录，关联 user / api_key / group |
| `assets` | 新表 | 上传缓存，sha256 唯一 |
| `groups.platform` | 已有 | DoneBot 组固定 `composite` |
| `groups.model_allowlist` | 已有 | 模型开关 |
| `groups.rate_multiplier` | 已有 | LLM 倍率 |
| `groups.image_price_1k/2k/4k` | 已有 | 图片单价 |
| `groups.image_rate_multiplier` | 已有 | 图片倍率 |
| `settings` | 已有 | 存 `donebot.group_id` |

迁移文件放在 `backend/migrations/`（按现有编号顺延），并提供 down。

---

## 6. DoneBot 对外契约

这是两边唯一需要共同遵守的部分。

### 6.1 鉴权

```http
Authorization: Bearer <install_credential>
```

DoneBot 首次连接时由用户登录自己的账号，DoneBot 用会话调领取安装接口，拿到该用户名下的隐藏凭据并落盘；用户不填地址以外的任何东西，也不复制 Key。

### 6.2 端点

| 方法 | 路径 | 用途 |
|---|---|---|
| `POST` | `/v1/chat/completions` | OpenAI 兼容文本 |
| `POST` | `/v1/messages` | Anthropic 原生 |
| `POST` | `/v1/responses` | OpenAI Responses |
| `POST` | `/v1beta/models/{model}:generateContent` | Gemini 原生 |
| `GET` | `/v1/models` | 可用模型（已按 allowlist 过滤） |
| `POST` | `/v1/images/generations` | 同步生图 |
| `POST` | `/v1/images/edits` | 同步改图 |
| `POST` | `/v1/images/generations/async` | 异步生图 |
| `POST` | `/v1/images/edits/async` | 异步改图 |
| `GET` | `/v1/images/tasks/{id}` | 查任务 |
| `POST` | `/v1/assets` | 上传并缓存 |
| `GET` | `/v1/assets/{id}` | 查资产元数据 |
| `GET` | `/assets/{id}?token=...` | 本地驱动的签名下载 |

### 6.3 错误码

用户已确认沿用原版错误码；不执行本节早期草案中的统一映射。鉴权、allowlist、限流、余额与原生协议错误体保持现有行为；新增资产与幂等接口的实际错误见验收记录。

| HTTP | code | 场景 |
|---|---|---|
| 401 | `INVALID_API_KEY` / `API_KEY_DISABLED` | 凭据无效或已吊销 |
| 403 | `INSUFFICIENT_BALANCE` / `billing_error` | 原鉴权或图片计费准入余额不足 |
| 404 | 按入口协议输出 | allowlist 未开启模型，沿用原版 |
| 413 | `ASSET_TOO_LARGE` | 上传超限 |
| 403 | `ASSET_TOKEN_INVALID` | 本地资产签名无效或过期 |
| 409 | `IDEMPOTENCY_KEY_CONFLICT` | 相同幂等键对应不同图片请求 |
| 429 / 5xx | 原链路错误码 | 限流、上游不可用或超时 |

以下错误体仅为示意，各原生协议继续使用各自现有错误格式：

```json
{ "error": { "type": "invalid_request_error", "code": "model_not_enabled", "message": "..." } }
```

### 6.4 幂等

- 图片异步提交支持 `Idempotency-Key` 请求头，24 小时内同 Key 返回同一任务。
- LLM 请求不强制幂等，由客户端自己控制重试。
- 上传按 sha256 天然幂等。

---

## 7. 代码改动清单

| 文件 | 改动 | 风险 |
|---|---|---|
| `backend/internal/config/config.go` | 新增 `donebot`、`asset_storage` 配置块 | 低 |
| `backend/internal/setup` / 启动装配 | 调用 `EnsureDoneBotGroup()` | 中，注意幂等与迁移顺序 |
| `backend/internal/service/donebot_setup.go` | 新建：确保分组与安装绑定 | 中 |
| `backend/internal/handler/admin/donebot_handler.go` | 新建：overview / models / pricing / installs | 中 |
| `backend/internal/server/routes/admin.go` | 挂载 `/api/v1/admin/donebot/*` | 低 |
| `backend/internal/server/routes/gateway.go` | 图片 handler 支持 composite 解析；挂 `/v1/assets` | 高，先写测试 |
| `backend/internal/service/image_task.go` | 平台准入、存储驱动、TTL 拆分 | 中 |
| `backend/internal/service/asset_storage.go` | 新建接口与 local 驱动 | 中 |
| `backend/internal/repository/asset_storage_s3.go` | 复用 S3 客户端 | 中 |
| `backend/internal/repository/asset_repository.go` | 新建：assets CRUD 与 sha256 去重 | 低 |
| `backend/migrations/xxxx_donebot.sql` | 两张新表 + 索引 | 中，必须有 down |
| `frontend/src/views/admin/DoneBotView.vue` | 新建壳 | 低 |
| `frontend/src/components/admin/donebot/*` | 新建两个面板与安装列表 | 中 |
| `frontend/src/api/admin/donebot.ts` | 新建 API 封装 | 低 |
| `frontend/src/router` / 菜单 | 注册路由与入口 | 低 |
| `docs/donebot/*` | 本方案与契约 | 无 |

**不要动**：

- `backend/internal/service/openai_images*.go` 的上游请求构造；
- `backend/internal/service/gateway*.go` 的协议转换；
- `backend/ent/schema` 里已有字段的语义；
- 任何支付、订阅、兑换码逻辑。

---

## 8. 阶段与验收

### P0 现状核对（0.5–1 天）

- 本地起服务，跑通一次 LLM 与一次同步生图。
- 记录：composite 分组能否生图、异步是否被平台判断挡住、`/v1/models` 过滤行为、allowlist 拒绝的实际错误码。
- 产出：`docs/donebot/P0-核对记录.md`，只记录事实，不改代码。

**验收**：三条结论有请求/响应证据；没有结论就不进入 P1。

### P1 DoneBot 模式与聚合分组（2–3 天）

- 配置块、启动确保分组。
- 用户领取安装、吊销接口，凭据只返回一次。
- 用安装凭据跑通 LLM。

**验收**：

- 全新数据库启动后自动出现 DoneBot 分组，且不创建任何隐藏用户；
- 用户领取安装后，用返回凭据可调 `/v1/models` 与 `/v1/chat/completions`；
- 吊销后同一凭据 401；
- 重复启动不产生重复数据。

### P2 管理页（3 天）

- LLM 页、图片页、安装列表。
- 模型同步、开关、倍率保存。

**验收**：

- 关闭某模型后，`/v1/models` 不返回它，直接请求沿用原 allowlist 的 404 与协议错误格式；
- 改倍率后，新请求的计费按新倍率，历史账单不变；
- 视频/音频标签不出现；
- 页面在 1280 宽和手机宽度都不塌。

### P3 上传与缓存（3 天）

- `POST /v1/assets`、local/S3 驱动、sha256 去重、TTL 清理。
- 图片编辑用上传后的 URL 跑通。

**验收**：

- 同一文件上传两次，第二次 `deduplicated=true` 且不新增对象；
- 本地驱动下签名 URL 可下载，过期后 403；
- R2/OSS 任一驱动下，上游能拉到 URL 并成功改图；
- 超过上限返回 413，不落盘。

### P4 图片异步收口（2 天）

- composite 平台准入、本地存储、TTL 拆分、错误码。

**验收**：

- 异步提交返回 202 + task id；
- 轮询在 30 分钟内完成，结果 URL 可下载；
- 失败任务退款，余额回到提交前；
- 重启服务后任务仍可查（Redis 持久化配置正确）。

### P5 总验收（2 天）

- 按第 6 节契约逐条打勾；
- `go test ./...`、前端 lint/build 通过；
- 写一份 `docs/donebot/验收记录.md`，附请求样例与截图。

---

## 9. 给 Codex 的执行提示

把下面整段复制给 Codex。一次只让它做一个阶段，做完验收再进下一个。

~~~text
你在 /Users/zluo/Dev/donebot-api 工作。这是 sub2api 的干净基线，要做成 DoneBot 专用网关。

先读 docs/donebot/DoneBot-网关改造方案.md，再读你当前阶段涉及的源码。不要读 DoneBot 客户端的任何文档，不要改 ~/Dev/DoneBot。

硬性约束：
1. 不改上游协议：Anthropic / OpenAI / Gemini 的请求与响应格式保持原样。
2. 不引入报价单、quote、预授权单；LLM 按 token，图片按张/尺寸。
3. 复用现有 API Key 鉴权与计费链路，不要新造一套账本。
4. 复用现有 composite 分组、model_allowlist、rate_multiplier、image_price_*。
5. 视频、音频不做，只留配置开关。
6. 每个阶段先写测试，再写实现；数据库迁移必须有 down。
7. 不要顺手重构无关代码，不要升级依赖。

当前阶段：P1（DoneBot 模式与聚合分组）。

要做的事：
- 增加 donebot 配置块（enabled / group_name / features）。
- 新建 service/donebot_setup.go，实现幂等的 EnsureDoneBotGroup：composite 分组、写 settings 里的 group_id。不创建任何隐藏用户。
- 在启动装配处调用，确保迁移之后执行。
- 新增用户侧接口：POST /api/v1/donebot/installations/claim；新增管理端接口：GET/DELETE /api/v1/admin/donebot/installs。
- 安装凭据复用 api_keys 表，绑定当前登录用户与 DoneBot 分组；donebot_installs 记录映射。
- 迁移：donebot_installs 表与索引。
- 测试：全新库启动、重复启动、同一安装重复领取、创建/吊销安装、用凭据调 /v1/models、余额不足沿用原版响应。

完成后输出：
- 改了哪些文件、为什么；
- 测试命令与结果；
- 仍需人工确认的点。

不要开始 P2。
~~~

---

## 10. 风险与待定

| 项 | 现状 | 处理 |
|---|---|---|
| 本地存储的 URL 上游拉不到 | `local` 只能本机/内网访问 | 管理页警告；公网部署强制 `public_base_url`；Phase 2 可加内置反代域名 |
| composite 生图是否已通 | 代码里图片 handler 只认 OpenAI/Grok，但 composite 会解析目标平台 | P0 用真实请求验证，再决定改 handler 还是改任务服务 |
| 模型目录来源 | 各平台能力表与渠道定价可能不一致 | 以“有价可卖”为交集；同步时给出差异清单，不静默丢模型 |
| 用户级计费 | 直接复用 sub2api 用户余额与用量日志，逐用户记账 | 注册、登录、充值沿用现有账号体系，不另造账本 |
| 多安装共享一个分组 | 所有安装共用 DoneBot 分组与倍率 | Phase 1 接受；需要分开计费时按安装建子分组 |
| 图片异步的 Redis 持久化 | 依赖部署配置 | 验收时重启服务验证；文档写明必须开 AOF/RDB |

---

## 11. 变更记录

| 日期 | 版本 | 内容 |
|---|---|---|
| 2026-11-03 | v0.1 | 首版：基线盘点、按用户记账的安装绑定、聚合分组、两页管理、上传缓存、异步收口 |
