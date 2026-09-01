# Data Model: fileId 直链图片访问（新架构）

**Branch**: `001-fileid-direct-link` | **Date**: 2026-09-01 | **Spec**: [spec.md](spec.md)

## 总体结论：零 Schema 迁移

现有 `images` 表已含 `file_id` 列与 `idx_file_id` 索引，本特性**不新增/不修改任何表结构**。变化的是字段的**语义地位**：`file_id` 从登记信息升格为访问主键，`proxy_url` 从访问主键降格为登记信息。

## 实体

### 1. fileId 直链（无存储，纯派生）

图片访问的唯一凭据，由 URL 规则派生，不落库、不可变。

| 组成 | 规则 | 示例 |
|------|------|------|
| 路径前缀 | 固定 `/file/` | `/file/` |
| fileId | Telegram Bot API file_id，字符集 `[A-Za-z0-9_-]`，不含 `.` | `AgACAgUAAx0CPQAB1w...` |
| 分隔符 | 第一个 `.` | `.` |
| 文件名 | 原始文件名（URL 编码），可含多个 `.` | `photo.v2.png` |

**解析规则（权威）**：路径段中第一个 `.` 之前为 fileId，其余为文件名。fileId 段为空或文件名段为空均视为非法请求（400/404）。

**校验规则**：
- 切分出的 fileId 必须非空；不对其字符集做强校验（宽松透传给 bot，由 bot 判定有效性，失败→404）
- 文件名段允许为空（此时内容类型走内容检测）

### 2. 图片记录（images 表，既有）

| 字段 | 类型 | 语义变化 |
|------|------|----------|
| `id` | INTEGER PK | 不变（管理页行标识） |
| `file_id` | TEXT NOT NULL | **升格：访问主键**。禁用检查、统计累加、重建去重均按此列精确匹配（`idx_file_id` 已存在） |
| `proxy_url` | TEXT NOT NULL | **降格：登记信息**。存新格式 `/file/{fileId}.{filename}`，仅供管理页展示与跳转；访问链路不再查询此列 |
| `telegram_url` | TEXT NOT NULL | 不变：最近一次获取的 Telegram 下载直链（自然过期，仅作缓存参考值） |
| `content_type` | TEXT NOT NULL | 不变：内容类型判定的第一优先级（仅 DB 命中时使用） |
| `filename` | TEXT NOT NULL | 不变 |
| `ip_address` / `user_agent` | TEXT | 不变：上传审计 |
| `upload_time` | DATETIME | 不变 |
| `is_active` | BOOLEAN | 不变：禁用标志。**生效条件**：DB 可用且该 file_id 有记录 |
| `view_count` | INTEGER | 不变：尽力而为累加，DB 不可用时丢弃 |

**状态迁移**：`is_active` 1→0（管理页禁用）→ 访问返回占位图；0→1（启用）→ 恢复。数据库丢失后所有记录状态归零（重新登记后恢复可用态）。

**写入路径**：
1. 上传成功 → 登记（best-effort，失败仅日志）
2. 重建（P3）→ 批量登记（`file_id` 已存在则跳过，不覆盖统计）

### 3. documents 表

不在本特性范围，维持现状（`/doc/{uuid}-{filename}` 旧格式与查库逻辑原样保留）。

## 运行时状态（内存，不持久化）

| 状态 | 现状 | 变化 |
|------|------|------|
| `global.URLCache` | key = telegram_url（会过期的直链） | **key 改为 fileId**；value 不变（最新直链 + 过期时间 23h） |
| `global.DB` / 新增 `global.DBAvailable` | DB 指针，初始化失败即 Fatal | 增加可用性标志；失败→降级模式，DB 相关调用全部经此守卫 |

## 数据生命周期

- **正常**：上传登记 → 访问统计/禁用 → 无限存活
- **数据库损坏/删除**：记录全灭，访问不受影响；重建（P3）可从频道历史恢复登记
- **重建产生的新库**：file_id 去重合并，upload_time 取频道消息时间（可获得时），统计从零开始
