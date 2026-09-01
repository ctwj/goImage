# Quickstart: fileId 直链图片访问（新架构）

**Branch**: `001-fileid-direct-link` | **Date**: 2026-09-01

面向开发者的最小构建、运行与验证指引。前置条件：Go 1.26+；已配置 `config.json`（bot token、chatId）。

## 构建与运行

```bash
# 构建（仓库根目录）
go build -o imagehosting ./cmd/server

# 运行（需要 config.json；session.tg 仅 P3 重建功能需要）
./imagehosting

# 跑测试
go test ./...
```

## 核心验证场景

### 场景 1 — 上传返回 fileId 直链（P1）

```bash
curl -F "image=@test.png" http://localhost:8080/upload
# 或 API 方式：
curl -X POST -F "image=@test.png" http://localhost:8080/api/v1/upload
```

✅ 预期：返回的 URL 形如 `/file/{fileId}.test.png`（fileId 为 `[A-Za-z0-9_-]+`，不含 `.`），立即访问该 URL 返回原图片。

### 场景 2 — 零数据库依赖访问（P1，核心）

```bash
# 1. 正常上传，保存返回的直链
URL=$(curl -X POST -F "image=@test.png" http://localhost:8080/api/v1/upload | jq -r .url)

# 2. 模拟数据库彻底损坏：停服 → 删除库文件 → 重启
rm images.db && ./imagehosting

# 3. 再次访问
curl -I "$URL"
```

✅ 预期：步骤 3 返回 `200` 与正确 `Content-Type`，服务启动不因缺库而退出（日志出现降级警告）。管理页此时显示降级提示。

### 场景 3 — 登记失败不阻断上传（P1）

使数据库不可写（如将 `database.path` 指向只读/坏路径），上传图片。

✅ 预期：上传成功并返回可用直链；日志仅记录登记失败。

### 场景 4 — 管理功能可用时行为不变（P2）

库可用时登录 `/admin`：可见记录、可禁用；禁用后访问直链返回 `X-Image-Status: deleted` 占位图，启用后恢复。

### 场景 5 — 频道历史重建（P3，可选）

配置并认证 User API（`-auth` 生成 `session.tg`）后，登录态触发：

```bash
curl -X POST -b "admin-session=..." http://localhost:8080/admin/rebuild
```

✅ 预期：返回 `{ inserted, skipped, scanned }`；管理页出现频道内全部图片；重建前后直链访问始终正常。

### 场景 6 — 旧链接废弃确认

访问任一旧 `/file/{uuid}-{filename}` 链接。

✅ 预期：`404`（uuid 非有效 file_id）。

## 边界快速回归

- `HEAD` / `OPTIONS` 预检 / `Range` 断点续传 / CORS 头 — 与旧版一致
- GIF 上传 → 播放正常（含转码 MP4 修正）
- 文件名含中文/特殊字符/多级 `.`（如 `a.b.png`）→ 链接正确编码、切分正确
- `/doc/*` 文档功能不受影响
