# Implementation Plan: fileId 直链图片访问（新架构）

**Branch**: `001-fileid-direct-link` | **Date**: 2026-09-01 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/001-fileid-direct-link/spec.md`

## Summary

图片访问链接由随机 UUID 改为 Telegram fileId + 后缀（`/file/{fileId}.{filename}`），访问链路与数据库彻底解耦：从 URL 切分 fileId → bot 获取直链（内存缓存）→ 流式代理。数据库降级为管理增强（登记/统计/禁用），不可用时启动降级、访问无感。上传两处入口统一链接生成，登记失败不阻断上传。P3 附频道历史重建（User API 遍历 + Bot 转发桥接）恢复管理数据。**零数据库 schema 迁移**（`file_id` 列与索引已存在）。决策详情见 [research.md](research.md)。

## Technical Context

**Language/Version**: Go 1.26（`go.mod`: `module hosting`）  
**Primary Dependencies**: `go-telegram-bot-api/v5 v5.5.1`（Bot API）、`gotd/td v0.142.0`（User API/MTProto，P3 重建用）、`gorilla/mux v1.8.1`（路由）、`gorilla/sessions`、`modernc.org/sqlite`（纯 Go SQLite）  
**Storage**: SQLite（`images`/`documents` 表；本特性仅改 `images` 的使用方式，schema 零变更）+ Telegram 频道（文件本体）+ 内存 `URLCache`（key 由 telegram_url 改为 fileId）  
**Testing**: Go 标准 `testing` + `net/http/httptest`；bot 直链获取通过包级函数变量注入 mock（见 research.md D8）；项目当前无测试，本特性建立首批测试  
**Target Platform**: Linux 服务器（systemd + Nginx，见 README）；开发环境为 Windows  
**Project Type**: web-service（自托管图床）  
**Performance Goals**: 与现状持平——URLCache 命中路径零 bot 调用；DB 不可用时访问路径少一次查询，不低于现状  
**Constraints**: 内存 <10MB（README 承诺）；Bot API `getFile` 单文件 ≤20MB（图片上传限制 10MB，天然满足）；bot 获取有平台频率限制（沿用 23h 缓存对冲）  
**Scale/Scope**: 个人自用单管理员；改动集中在 3 个文件（`handlers.go`、`api.go`、`db.go`）+ 新增重建功能与测试

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

`.specify/memory/constitution.md` 存在但为**未填写的安装模板**（全部为占位符 `[PRINCIPLE_1_NAME]` 等），无任何生效的约束条目。

- 首次评估（Phase 0 前）：无 gates 可校验 → **PASS（无约束）**
- 复评（Phase 1 后）：设计未引入任何违反常识性原则的内容（无新仓库、无新服务、无过度抽象；复用既有表结构与索引）→ **PASS**

> 建议后续运行 `/speckit-constitution` 制定实际宪法；当前以 spec 的 Assumptions 与 research.md 决策记录代行约束。

## Project Structure

### Documentation (this feature)

```text
specs/001-fileid-direct-link/
├── plan.md              # This file (/speckit-plan command output)
├── research.md          # Phase 0 output — 9 项技术决策（URL 格式/链路/降级/重建桥接等）
├── data-model.md        # Phase 1 output — 实体语义变化（file_id 升格为访问主键），零 schema 迁移
├── quickstart.md        # Phase 1 output — 6 个验证场景（含删库访问核心场景）
├── contracts/
│   └── http-api.md      # Phase 1 output — HTTP 接口契约（直链行为矩阵/上传响应/重建端点）
└── tasks.md             # Phase 2 output (/speckit-tasks command - NOT created by /speckit-plan)
```

### Source Code (repository root)

```text
internal/
├── db/
│   └── db.go                  # [改造] InitDB 可选化：失败降级不 Fatal；新增可用性守卫
├── global/
│   └── global.go              # [改造] 新增 DBAvailable 标志；URLCache key 语义改 fileId
├── handlers/
│   ├── handlers.go            # [改造] HandleImage 重写（fileId 直取+可选增强）；HandleUpload 链接生成；GetTelegramFileURL 改为可注入函数变量
│   ├── api.go                 # [改造] HandleAPIUpload 链接生成统一
│   ├── handlers_test.go       # [新增] URL 切分/类型推断单测 + httptest 集成（DB 可用/不可用两态）
│   └── rebuild.go             # [新增·P3] 频道历史重建 handler（可裁剪）
├── telegram/
│   ├── telegram.go            # [不动] bot 初始化
│   └── user_api.go            # [扩展·P3] messages.GetHistory 频道遍历 + Bot ForwardMessage 桥接
└── middleware/middleware.go   # [改造] 管理操作定位键 id → file_id（配合路由）

cmd/server/main.go             # [改造] 路由：/admin/toggle/image/{fileId}、/admin/rebuild；启动流程适配降级模式

templates/upload.tmpl          # [确认] URL 渲染无需改动（后端传值变化）
templates/admin.tmpl           # [改造] 禁用按钮定位键改 file_id；降级模式提示
```

**Structure Decision**: 既有单体服务的就地改造，不新建顶层目录。改动集中在 `internal/handlers`（访问与上传）、`internal/db`（可选化）、`cmd/server/main.go`（路由）；P3 重建新增 `rebuild.go` 与 `user_api.go` 扩展，物理隔离便于裁剪。

## Complexity Tracking

> **Fill ONLY if Constitution Check has violations that must be justified**

无 Constitution 违规，无需填写。
