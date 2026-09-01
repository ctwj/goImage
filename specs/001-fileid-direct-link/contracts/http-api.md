# HTTP Interface Contract: fileId 直链图片访问（新架构）

**Branch**: `001-fileid-direct-link` | **Date**: 2026-09-01 | **Spec**: [spec.md](spec.md)

本特性对外暴露/变更的 HTTP 接口契约。未列出的接口（登录、管理页、文档 `/doc/*`、健康检查等）行为不变。

## 1. 图片直链访问（变更核心）

### `GET|HEAD|OPTIONS /file/{fileId}.{filename}`

| 项 | 契约 |
|----|------|
| `{fileId}` | Telegram Bot API file_id，字符集 `[A-Za-z0-9_-]`，不含 `.` |
| `.{filename}` | 分隔符 + 原始文件名（URL 编码，可含多级 `.`）；文件名段允许缺失（`/file/{fileId}.`） |
| 解析规则 | 路径段中**第一个 `.`** 之前为 fileId，其余为文件名 |

**行为矩阵**：

| 服务端状态 | 响应 |
|-----------|------|
| fileId 有效 | `200`，图片字节流，`Content-Type` 按 DB 记录 → 扩展名 → 内容检测三级判定 |
| fileId 有效但记录被禁用（DB 可用且命中） | `200`，`image/jpeg` 占位图，`X-Image-Status: deleted` |
| fileId 无效 / bot 获取失败 | `404`，"Image not found" |
| 数据库不可用 / 无记录 | **与正常完全一致**（返回图片；仅统计不累加） |

**响应头契约**（与现行为一致）：

- `Cache-Control: public, max-age=31536000` + `Expires`（+1 年）
- CORS：`Access-Control-Allow-Origin: *`，`Allow-Methods: GET, HEAD, OPTIONS`，`Allow-Headers: Range`，`Expose-Headers: Content-Length, Content-Range, Accept-Ranges`
- `OPTIONS` 预检 → `200` 空体
- `Accept-Ranges: bytes`；`Range` 请求 → `206` + `Content-Range`（透传）
- GIF 被 Telegram 转码为 MP4 时动态修正 `Content-Type: video/mp4`
- 流式传输，不缓冲整个文件

## 2. 网页上传（变更：返回链接格式）

### `POST /upload`

请求不变（multipart 表单字段 `image`）。响应模板（upload.tmpl）渲染的 `{URL}` 变更为：

```
{scheme}://{host}/file/{fileId}.{urlEscape(filename)}
```

**契约要点**：bot 上传成功即视为整体成功；数据库登记失败**不改变**成功响应（仅服务端日志）。

## 3. API 上传（变更：返回链接格式）

### `POST /api/v1/upload`

请求不变（multipart 表单，鉴权方式不变）。图片类响应 JSON 变更为：

```json
{
  "success": true,
  "url": "https://img.example.com/file/AgACAgUAAx0CPQAB1w.xXXXX.jpg"
}
```

`url` 字段格式同上（`/file/{fileId}.{filename}`）；错误响应结构不变。文档类（`/doc/...`）响应格式不变。

## 4. 管理操作（变更：定位键）

### `POST /admin/toggle/image/{fileId}`

定位键由数据库行 `{id}` 改为 **`{fileId}`**（`WHERE file_id = ?`）。响应不变（`200`）。行 id 定位方式废弃。

## 5. 频道重建（新增，P3，可裁剪）

### `POST /admin/rebuild`

| 项 | 契约 |
|----|------|
| 鉴权 | 管理员登录态（与 `/admin` 一致） |
| 请求体 | 无（或可选 `{ "dryRun": true }` 仅统计不写入） |
| 行为 | User API 遍历存储频道历史 → Bot 转发桥接取得 file_id → 去重登记；期间与之后的直链访问不受任何影响 |
| 成功 | `200`，`{ "inserted": N, "skipped": M, "scanned": T }` |
| User API 未配置/未认证 | `503`，`{ "error": "User API not available" }` |
| 中途失败 | `207` 语义（已登记条目保留，响应含错误与进度）；可重入（去重保证幂等） |

## 6. 错误语义汇总

| 场景 | 状态码 | 说明 |
|------|--------|------|
| fileId 无法解析（路径段无 `.` 或 fileId 为空） | `404` | 不区分原因，统一未找到 |
| bot 判定 file_id 无效 | `404` | 对外不暴露内部错误细节 |
| 数据库任何故障 | **不影响访问响应** | 仅日志；管理接口返回降级提示 |

## 兼容性声明

- 旧 `/file/{uuid}-{filename}` 链接：uuid 非 file_id，bot 获取失败 → `404`。无迁移路径（存量已灭）
- `/doc/*` 全部行为不变
