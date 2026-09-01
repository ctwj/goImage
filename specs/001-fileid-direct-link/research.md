# Research: fileId 直链图片访问（新架构）

**Branch**: `001-fileid-direct-link` | **Date**: 2026-09-01 | **Spec**: [spec.md](spec.md)

## 现状调研结论

- 访问链路：`/file/{uuid}-{filename}` → `proxy_url LIKE` 查库 → `Bot.GetFileDirectURL(fileID)`（内存缓存 23h）→ 流式代理。**记录定位完全依赖数据库**，库损坏即全失效
- 两处独立生成链接：网页上传 `internal/handlers/handlers.go:298-307`（HandleUpload）、API 上传 `internal/handlers/api.go:246,276`（HandleAPIUpload），格式 `/file/{uuid}-{filename}`
- `images.file_id` 字段与 `idx_file_id` 索引已存在——数据库 schema **无需任何迁移**
- `InitDB`（`internal/db/db.go:14`）在库不可用时 `log.Fatal` 退出——可选化的主要障碍
- 直链缓存 `global.URLCache` 以 `telegram_url`（会过期的下载 URL）为 key，语义绕
- User API（gotd/td）已具备：peer 缓存、消息发送、文档流式下载，未使用频道历史遍历
- 项目当前无任何测试文件

## 决策记录

### D1: 直链 URL 格式 — `/file/{fileId}.{filename}`

- **Decision**: 路径为 `/file/{fileId}.{原始文件名}`，如 `/file/AgACAgUAAx0CPQAB1w.xXXXX.jpg`。解析规则：路径段中**第一个 `.` 之前为 fileId**，之后为文件名
- **Rationale**: Telegram Bot API file_id 字符集为 `[A-Za-z0-9_-]`（base64url 变体，约 40-120 字符），**不含 `.`**，以 `.` 切分无歧义且符合"扩展名"直觉；保留完整文件名维持下载/展示语义与现有格式一致
- **Alternatives**:
  - `-` 作分隔符（现有格式沿用）——否决：file_id 内部含 `-`，无法切分
  - 仅扩展名 `/file/{fileId}.png`——否决：丢失原始文件名，降低下载体验
  - 查询参数 `/file?id={fileId}`——否决：部分网站/CDN 对查询参数处理不一致，路径形式更通用

### D2: 访问链路 — fileId 直取，数据库降级为可选增强

- **Decision**: `HandleImage` 重写为：①从 URL 切分 fileId → ②（仅当 DB 可用且记录存在）检查禁用状态、累加统计 → ③经 URLCache（key 改为 fileId）获取 Telegram 直链，过期则 `GetFileDirectURL` 刷新 → ④流式代理返回（保留 GIF→MP4 检测、Range、CORS、缓存头）。DB 任何失败（不可用、无记录、写入失败）一律**静默跳过，不阻断访问**
- **Rationale**: 满足 FR-002/FR-003（零依赖访问）与 FR-007（禁用/统计在数据可用时生效）。"零依赖"的准确含义是 *DB 故障不改变访问结果*，而非访问路径绝不碰库——禁用检查是尽力而为的增强
- **Alternatives**:
  - 访问完全不碰库、禁用仅作用于管理页显示——否决：禁用功能实质失效，违背 FR-007 场景 3
  - 保留两段式（查库命中走旧流程/未命中走 bot）——否决：用户明确按新项目处理，旧流程无存在必要

### D3: 数据库可选化 — 启动降级而非退出

- **Decision**: `InitDB` 改为尽力初始化：失败时记录警告并进入**降级模式**（`global.DBAvailable = false`），服务照常启动，上传/访问正常，管理功能返回降级提示。所有 DB 调用点经可用性检查包裹
- **Rationale**: 满足"无数据库的部署与启动"边界情况；现状 `log.Fatal` 是最大的单点
- **Alternatives**: 延迟初始化（首次用时再连）——否决：启动时探测一次即可，运行中反复重连徒增复杂度；如库在运行中被删除，访问不受影响（查询报错按跳过处理）

### D4: 上传登记失败不阻断 — 链接优先

- **Decision**: 两处上传（网页/API）在 bot 上传成功取得 file_id 后，**先构造并返回链接**；随后的 DB 登记失败仅记日志，不影响响应。链接格式统一提取为单一辅助函数，两处共用
- **Rationale**: FR-006（登记 SHOULD、失败不阻断上传）；bot 已成功意味着文件已安全，登记只是管理增强
- **Alternatives**: 登记失败回滚（删除频道消息）——否决：过度设计，违背解耦初衷

### D5: 旧格式处理 — 直接废弃

- **Decision**: 不再按 uuid `LIKE` 查询；`/file/{...}` 路由保持单一 handler，一律按"切分 fileId → bot 直取"处理。旧 UUID 链接中的 uuid 不是有效 file_id，bot 获取失败自然返回 404
- **Rationale**: FR-009（旧格式废弃）；无需额外路由或格式判断逻辑，代码更简
- **Alternatives**: 过渡期双格式兼容——否决：存量数据已全灭，无兼容对象

### D6: 统计与禁用的查询方式 — 按 file_id 精确匹配

- **Decision**: 禁用检查与 `view_count` 累加均按 `WHERE file_id = ?`（走已有 `idx_file_id`），弃用 `proxy_url LIKE`
- **Rationale**: fileId 是新架构的访问主键，索引已有，精确匹配快于前缀 LIKE；管理页禁用操作同样按 file_id 定位（前端传 file_id 而非行 id 或 proxy_url）

### D7: P3 重建实现 — User API 遍历 + Bot 转发桥接

- **Decision**: 管理员触发（登录态下 `POST /admin/rebuild`）。流程：①User API（gotd/td `messages.GetHistory`）倒序遍历存储频道全部历史消息；②对每条含图片的消息，用 Bot API `ForwardMessage` 转发到**同一频道**，从返回的 Message 中取得 Bot 体系 file_id，随即 `DeleteMessage` 清理转发消息；③按 file_id 登记（去重：已存在则跳过）；④进度写日志/页面反馈
- **Rationale**: MTProto 的 photo 引用与 Bot API file_id 是两个体系，`ForwardMessage` 是唯一可靠的转换桥；gotd/td 与会话管理项目已具备。重建仅恢复管理数据，与访问无关（FR-010）
- **Alternatives**:
  - 用 User API 直接下载并重新上传——否决：会生成新 file_id，与已分发链接脱钩，且耗费流量
  - 登记时不存 Bot file_id、存 message 引用——否决：访问与禁用检查均以 file_id 为 key，存别的 key 无法联动
- **依赖**: 需 User API 已配置并认证（`session.tg`）；未配置时重建入口提示不可用，不影响其他功能

### D8: 测试策略 — 纯函数单测 + httptest 集成

- **Decision**: ①URL 切分函数（fileId/文件名/边界）单测；②内容类型推断（后缀缺失/未知）单测；③`httptest` 集成测试覆盖 DB 可用与不可用两态下的访问行为（mock 直链获取函数——将 `GetTelegramFileURL` 改为可替换的包级函数变量）；④`InitDB` 降级行为单测（坏路径不 Fatal）
- **Rationale**: bot 是外部依赖，注入点最小化（仅直链获取一处）即可覆盖核心分支；项目从零建立测试，先保关键路径
- **Alternatives**: 引入完整接口抽象层——否决：为单一外部调用引入抽象层过度

### D9: 内容类型判定 — 后缀优先，内容检测兜底

- **Decision**: 兜底顺序：①登记记录的 content_type（DB 可用且命中时）②扩展名映射（`AllowedMimeTypes` 反查）③`http.DetectContentType`（读前 512 字节）。GIF→MP4 动态修正逻辑原样保留于代理层
- **Rationale**: FR-004；三级判定覆盖后缀缺失/未知/DB 不可用全部情形

## 遗留风险（接受）

- Bot API `getFile` 单文件 ≤20MB：图片上传限制 10MB（photo 通道实际约 10MB），天然满足
- `ForwardMessage` 重建会在频道产生瞬时重复消息（随即删除）：一次性管理操作，可接受
- 数据库损坏期间禁用失效、统计丢失：spec 已声明为接受的取舍
