---
description: "Task list for fileId 直链图片访问（新架构）"
---

# Tasks: fileId 直链图片访问（新架构）

**Input**: Design documents from `/specs/001-fileid-direct-link/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/http-api.md, quickstart.md

**Tests**: 测试任务已包含（T008/T011/T012）——依据 plan.md Technical Context 与 research.md D8 明确规划的测试策略，非 spec 强制要求；如不需要可在执行时跳过，不影响任务链。

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

## Path Conventions

既有单体 Go 服务就地改造，无新顶层目录；路径均为仓库相对路径（Go module `hosting`）。

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: 确认改造基线（既有服务可构建、可运行）

- [x] T001 验证基线：在仓库根执行 `go build ./...` 与 `go vet ./...` 通过，并手动跑通一次现状上传/访问，记录基准行为（对照 internal/handlers/handlers.go 现有流程）

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: 三个用户故事共同依赖的基础设施：数据库可选化、URL 切分、测试注入点

**⚠️ CRITICAL**: 未完成本阶段前不得开始任何用户故事

- [x] T002 数据库可选化（US2/US3 共用）：在 internal/global/global.go 新增 `DBAvailable bool` 标志与轻量守卫（如 `db.IsAvailable()`）；改写 internal/db/db.go 的 `InitDB`——初始化失败（打开/Ping/建表任一错误）时记录警告、置 `DBAvailable=false` 并正常返回，不再 `log.Fatal`
- [x] T003 [P] URL 切分纯函数：在 internal/utils/utils.go 新增 `SplitFileIDURL(pathSegment string) (fileID, filename string, err error)`——按第一个 `.` 切分，fileId 为空返回错误，文件名允许为空（依据 research.md D1 / data-model.md 解析规则）
- [x] T004 [P] 测试注入点：将 internal/handlers/handlers.go 中 `GetTelegramFileURL` 由普通函数改为可替换的包级函数变量，供集成测试 mock bot 直链获取（research.md D8）

**Checkpoint**: 基础就绪，用户故事可以开始（US1 与 US2 可并行启动）

---

## Phase 3: User Story 1 - 上传返回 fileId 直链 (Priority: P1) 🎯 MVP(上)

**Goal**: 两处上传入口统一返回 `/file/{fileId}.{filename}` 格式链接

**Independent Test**: 上传图片后检查返回 URL 的格式与编码；⚠️ 本故事的"立即访问可成功"验收需 US2 的访问逻辑配合完成（见 Dependencies——P1 双故事构成原子 MVP）

### Implementation for User Story 1

- [x] T005 [P] [US1] 链接构造辅助函数：在 internal/utils/utils.go 新增 `BuildFileIDURL(fileID, filename string) string`——生成 `/file/{fileID}.{url.PathEscape(filename)}`，与 T003 的切分函数互为逆操作
- [x] T006 [US1] 改造网页上传：internal/handlers/handlers.go 的 `HandleUpload`——用 `BuildFileIDURL` 替换现 `proxyUUID` 生成段（约 298-307 行），proxy_url 落库新格式；telegram_url 获取失败不再阻断（用 file_id 兜底登记空串并继续，因访问已不依赖该列）
- [x] T007 [US1] 改造 API 上传：internal/handlers/api.go 的 `HandleAPIUpload`——图片分支同样改用 `BuildFileIDURL`（约 246 行），`/doc` 分支保持不变
- [x] T008 [P] [US1] 单元测试：新建 internal/utils/utils_test.go——覆盖 `SplitFileIDURL`/`BuildFileIDURL` 的 roundtrip、中文/特殊字符文件名、多级 `.`（`a.b.png`）、无 `.`、空 fileId、空文件名（`/file/{fileId}.`）边界

**Checkpoint**: 上传侧完成。与 Phase 4 联合验证后方可宣布 P1 交付

---

## Phase 4: User Story 2 - 零数据库依赖的图片访问 (Priority: P1) 🎯 MVP(下)

**Goal**: `HandleImage` 重写为 fileId 直取——访问结果与数据库状态完全解耦

**Independent Test**: 上传取得直链 → 停服删除 images.db → 重启 → 直链仍返回 200 与正确 Content-Type（quickstart.md 场景 2）

### Implementation for User Story 2

- [x] T009 [US2] 重写访问主路径：internal/handlers/handlers.go 的 `HandleImage`——①`SplitFileIDURL` 切分（失败直接 404）②`global.URLCache` 改以 fileId 为 key（同步调整 global.go 注释与写入点）③缓存未命中/过期经 T004 函数变量刷新直链 ④流式代理返回，原样保留 GIF→MP4 检测、Range/206、CORS、缓存头、HEAD 逻辑（contracts/http-api.md 行为矩阵）
- [x] T010 [US2] 接入可选增强路径：internal/handlers/handlers.go 的 `HandleImage` 内，在获取文件前——`db.IsAvailable()` 时按 `WHERE file_id = ?` 查询（走 idx_file_id）：命中且 `is_active=0` 返回占位图；命中且活跃则尽力累加 `view_count`（失败仅日志）；查询出错/无记录/库不可用一律静默继续直取（FR-002/003/007）
- [x] T011 [US2] 集成测试：新建 internal/handlers/handlers_test.go——httptest 驱动，mock T004 函数变量返回固定直链；覆盖：DB 可用+记录活跃 / DB 可用+禁用（占位图+X-Image-Status）/ DB 不可用（行为与可用一致）/ 无效 fileId（404）/ 切分失败（404）
- [x] T012 [P] [US2] 降级行为测试：新建 internal/db/db_test.go——坏路径/坏文件初始化不 Fatal、`DBAvailable=false`、`IsAvailable()` 语义正确

**Checkpoint**: 🎯 **P1 MVP 完成（US1+US2 原子交付）**——执行 quickstart.md 场景 1/2/3/6 全量验证后方可继续

---

## Phase 5: User Story 3 - 管理功能作为可选增强 (Priority: P2)

**Goal**: 管理功能（禁用/统计/登记）恢复一致体验且失效不影响访问；上传登记失败不阻断

**Independent Test**: 库可用时登录 /admin 禁用图片 → 访问返回占位图；将 database.path 指向坏路径上传 → 仍返回可用链接（quickstart.md 场景 3/4）

### Implementation for User Story 3

- [x] T013 [US3] 禁用定位键改 file_id：internal/handlers/handlers.go 的 `HandleToggleStatus` 图片分支改 `UPDATE images SET is_active = NOT is_active WHERE file_id = ?`；cmd/server/main.go 路由改 `/admin/toggle/image/{fileId}`（doc 分支维持 id 不变）
- [x] T014 [P] [US3] 管理页模板适配：templates/admin.tmpl——图片视图的禁用按钮/操作路径由行 id 改传 file_id（从查询结果集中带出）；图片视图数据查询在 internal/handlers/handlers.go 的 `HandleAdmin` 中补充返回 file_id 字段
- [x] T015 [US3] 登记失败不阻断上传：internal/handlers/handlers.go 的 `HandleUpload` 与 internal/handlers/api.go 的 `HandleAPIUpload`——DB insert 失败时降级为日志警告并照常返回成功响应与链接（FR-006）
- [x] T016 [US3] 管理页降级提示：internal/handlers/handlers.go 的 `HandleAdmin` 传入 `DBAvailable` 状态，templates/admin.tmpl 在库不可用时展示降级提示条（管理功能受限、访问不受影响）

**Checkpoint**: 管理增强完成，访问解耦特性不受影响

---

## Phase 6: User Story 4 - 从频道历史重建管理数据 (Priority: P3，可整体裁剪)

**Goal**: 管理员触发重建，从频道历史恢复管理登记；访问全程不受影响

**Independent Test**: 清空数据库 → 触发 POST /admin/rebuild → 管理页出现频道全部图片；重建前后直链访问始终正常（quickstart.md 场景 5）

### Implementation for User Story 4

- [x] T017 [US4] 频道历史遍历：internal/telegram/user_api.go 新增 `IterateChannelHistory`——gotd/td `messages.GetHistory` 倒序分页拉取存储频道全部消息，回调产出（消息时间、caption/文件名、photo 引用）；User API 未就绪时返回明确错误
- [x] T018 [US4] Bot 转发桥接：internal/telegram/user_api.go（或新建 internal/telegram/rebuild.go）——对每条图片消息用 Bot API `ForwardMessage` 转发到同一频道，从返回 Message 取最大尺寸 photo 的 file_id，随即 `DeleteMessage` 清理；失败计数并继续
- [x] T019 [US4] 重建 handler：新建 internal/handlers/rebuild.go——`POST /admin/rebuild`（登录态）：组合 T017/T018，按 file_id 去重后 INSERT（已存在跳过），响应 `{ inserted, skipped, scanned }`；可重入幂等（contracts/http-api.md §5）
- [x] T020 [US4] 路由注册：cmd/server/main.go 挂载 `/admin/rebuild`（RequireAuth 中间件）

**Checkpoint**: 全部故事完成

---

## Phase 7: Polish & Cross-Cutting Concerns

**Purpose**: 跨故事收尾

- [x] T021 [P] 文档更新：README.md（新链接格式、数据库损坏恢复指引"重新部署即可"）与 API.md（`/api/v1/upload` 响应 url 新格式）
- [x] T022 按 quickstart.md 6 个场景全量回归验证并记录结果
- [x] T023 终检：`go build ./...`、`go vet ./...`、`go test ./...` 全绿；清理废弃的 proxyUUID 相关死代码（internal/handlers/handlers.go、api.go 中不再引用的 uuid 逻辑，注意 uuid 包在上传追踪 ID 处仍在使用，勿误删）

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: 无依赖，立即开始
- **Foundational (Phase 2)**: 依赖 Phase 1；**阻塞所有用户故事**（T002/T003/T004）
- **User Stories (Phase 3-6)**: 均依赖 Phase 2 完成
- **Polish (Phase 7)**: 依赖全部所需故事完成

### User Story Dependencies

- **US1 (P1)**: 依赖 T003/T005。⚠️ **US1 与 US2 构成原子 MVP**——US1 的验收场景"立即访问链接成功"依赖 US2 的访问逻辑；反之 US2 的真实链接测试依赖 US1 的上传改造。两者须联合交付、联合验证
- **US2 (P1)**: 依赖 T002（可选化）/T004（注入点）。US1↔US2 之外无跨故事依赖
- **US3 (P2)**: 软依赖 US1+US2 完成（toggle 定位键与登记时机与新链路对齐才可端到端验收）；代码上无硬阻塞
- **US4 (P3)**: 仅依赖 Phase 2；与 US1-3 无文件冲突（rebuild.go/新函数），可完全并行开发；可整体裁剪不影响其他故事

### Within Each User Story

- 辅助函数 → handler 改造 → 测试验证
- 同文件任务（T009→T010、T013→T014 部分）按序执行，勿并行
- P1 MVP 验收门（quickstart 场景 1/2/3/6）通过前不得进入 US3

### Parallel Opportunities

- Phase 2：T003 与 T004 可并行（不同文件）
- Phase 3：T005 完成后，T006（handlers.go）与 T007（api.go）可并行；T008 独立可并行
- Phase 4：T009→T010 串行；T011 在 T009 后可写；T012 独立可并行
- Phase 5：T014/T016（模板）与 T015（handler）可并行；T013 先行
- Phase 6：T017 与 T018 部分并行（同文件时串行），T019/T020 依赖前者
- 跨故事：US4（rebuild.go，全新文件）可与 US1/US2/US3 完全并行

---

## Parallel Example: User Story 1

```bash
# T005 完成后，两个上传入口改造并行（不同文件）：
Task: "T006 改造网页上传 internal/handlers/handlers.go"
Task: "T007 改造 API 上传 internal/handlers/api.go"
# 测试独立并行：
Task: "T008 单元测试 internal/utils/utils_test.go"
```

---

## Implementation Strategy

### MVP First (US1 + US2 原子交付)

1. Complete Phase 1: Setup
2. Complete Phase 2: Foundational（T002/T003/T004——CRITICAL）
3. Complete Phase 3 + Phase 4（US1 与 US2 一起）
4. **STOP and VALIDATE**: quickstart.md 场景 1/2/3/6——尤其场景 2（删库重启访问）是本特性的存在意义
5. Deploy——至此数据库损坏不再是灾难

### Incremental Delivery

1. Setup + Foundational → 基础就绪
2. US1 + US2 → **MVP！核心价值交付（直链 + 零依赖访问）**
3. US3 → 管理体验补全（禁用/统计/登记健壮性）
4. US4 → 管理数据灾备（可选，随时可加）
5. Polish → 文档与回归

### Parallel Team Strategy

1. 共同完成 Setup + Foundational
2. 分工：A=US1+US2（主链路）、B=US4（重建，全新文件零冲突）、US3 由 A 在 MVP 后接续
3. Polish 合流

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- US1/US2 为原子 MVP 对（互相构成对方验收前提），任何"只做 US1"的中间态不可部署——新链接在旧访问逻辑下会 404
- US4（Phase 6）可整体裁剪：删除 T017-T020 不影响其他任务
- 测试任务（T008/T011/T012）源自 plan.md 规划，非 spec 强制，可按需裁剪
- 验证基准：quickstart.md 6 场景 + contracts/http-api.md 行为矩阵
- 旧 uuid 链接逻辑删除后注意 `google/uuid` 包仍被上传追踪 ID 使用（T023）
- Commit after each task or logical group; stop at any checkpoint to validate
