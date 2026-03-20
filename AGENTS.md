# GoImage 项目上下文文档

## 项目概览

**GoImage** 是一个基于 Go 语言开发的轻量级图片托管服务，使用 Telegram 作为存储后端。该项目采用服务器/客户端分离架构，提供 Web 界面和 RESTful API，支持图片上传、管理和访问。

**当前版本**: v0.1.9

**主要特性**:
- 无限容量，利用 Telegram 频道存储图片
- 轻量级设计，内存占用小于 10MB
- 提供 Web 界面和独立命令行客户端
- RESTful API 支持，便于第三方集成
- 用户认证和访问控制
- 缩略图支持（v0.1.5+）
- CORS 支持，可嵌入其他网站
- 速率限制和安全防护

## 项目架构

### 技术栈

- **语言**: Go 1.26.0
- **Web 框架**: Gorilla Mux (路由)
- **会话管理**: Gorilla Sessions
- **存储后端**: Telegram Bot API
- **数据库**: SQLite (modernc.org/sqlite)
- **前端**: 原生 HTML/CSS/JavaScript + Go 模板

### 目录结构

```
goImage/
├── cmd/                    # 可执行程序入口
│   ├── server/            # 服务器端主程序
│   │   └── main.go
│   └── client/            # 客户端工具
│       └── main.go
├── internal/               # 内部包（不对外暴露）
│   ├── config/           # 配置管理
│   ├── db/               # 数据库操作
│   ├── global/           # 全局变量和状态
│   ├── handlers/         # HTTP 处理器
│   ├── middleware/       # 中间件（认证、日志等）
│   ├── telegram/         # Telegram 集成
│   ├── template/         # 模板渲染
│   ├── utils/            # 工具函数
│   └── logger/           # 日志系统
├── templates/             # HTML 模板
│   ├── home.tmpl         # 首页
│   ├── login.tmpl        # 登录页
│   ├── upload.tmpl       # 上传结果页
│   └── admin.tmpl        # 管理后台
├── static/                # 静态资源
│   ├── favicon.ico
│   ├── robots.txt
│   └── deleted.jpg       # 已删除图片占位符
├── tools/                 # 工具脚本
│   └── generate_apikey.go # API 密钥生成工具
├── config.json           # 主配置文件
├── go.mod                # Go 模块定义
├── README.md             # 项目文档
└── API.md                # API 文档
```

### 核心组件

#### 1. 服务器端 (cmd/server/main.go)
- **职责**: 提供 Web 服务和 RESTful API
- **端口**: 默认 18080
- **依赖**: Telegram Bot、SQLite 数据库
- **关键功能**:
  - 图片上传处理
  - 用户认证（基于 session）
  - 图片代理和缓存
  - 访问统计和管理

#### 2. 客户端工具 (cmd/client/main.go)
- **职责**: 命令行上传工具
- **版本**: v0.1.4
- **支持平台**: Linux、Windows、macOS
- **功能**: 通过 RESTful API 上传图片到服务器

#### 3. 内部模块

**config/config.go**:
- 加载配置文件 (config.json)
- 支持环境变量覆盖配置
- 配置验证

**db/db.go**:
- SQLite 数据库连接管理
- 提供带超时的数据库操作
- 主要表: `images` (存储图片元数据)

**handlers/handlers.go**:
- HandleHome: 首页渲染
- HandleUpload: 图片上传处理
- HandleImage: 图片访问代理
- HandleLoginPage/HandleLogin: 登录处理
- HandleAdmin: 管理后台
- HandleToggleStatus: 切换图片状态

**handlers/api.go**:
- HandleAPIUpload: RESTful API 上传端点
- HandleAPIHealthCheck: API 健康检查

**handlers/status.go**:
- HandleStatus: 服务状态监控端点
- HandleHealthCheck: 健康检查端点

**middleware/middleware.go**:
- RequireAuth: 管理员认证中间件
- RequireAPIKey: API 密钥认证中间件
- RequireAuthForUpload: 上传权限检查中间件
- LoggingMiddleware: 请求日志记录

**telegram/telegram.go**:
- Telegram Bot 初始化和配置
- 图片上传到 Telegram 频道
- 获取 Telegram 文件 URL

**template/template.go**:
- 模板加载和缓存
- 模板渲染

## 构建和运行

### 环境要求

- Go 1.26.0+
- Telegram Bot Token
- Telegram 频道 Chat ID
- SQLite (自动包含)

### 配置文件

**主配置文件**: `config.json`

```json
{
  "telegram": {
    "token": "your-bot-token",
    "chatId": -123456789
  },
  "admin": {
    "username": "admin",
    "password": "password"
  },
  "site": {
    "name": "Site Name",
    "maxFileSize": 10,
    "port": 18080,
    "host": "127.0.0.1",
    "favicon": "favicon.ico"
  },
  "database": {
    "path": "./images.db",
    "maxOpenConns": 25,
    "maxIdleConns": 10,
    "connMaxLifetime": "5m"
  },
  "security": {
    "rateLimit": {
      "enabled": true,
      "limit": 60,
      "window": "1m"
    },
    "allowedHosts": ["localhost", "127.0.0.1"],
    "sessionSecret": "",
    "statusKey": "status_key",
    "apiKeys": ["your-api-key"],
    "requireAPIKey": false,
    "requireLoginForUpload": false
  },
  "environment": "production"
}
```

### 编译服务器端

```bash
cd G:/server/goImage
go build -o imagehosting.exe ./cmd/server
```

### 编译客户端工具

```bash
cd G:/server/goImage
go build -o imagehosting-client.exe ./cmd/client
```

### 编译 API 密钥生成工具

```bash
cd G:/server/goImage/tools
go build -o generate_apikey.exe generate_apikey.go
```

### 运行服务器

```bash
# 使用默认配置
./imagehosting.exe

# 指定配置文件
./imagehosting.exe -config /path/to/config.json

# 指定工作目录
./imagehosting.exe -workdir /path/to/workdir

# 查看版本
./imagehosting.exe -version

# 查看帮助
./imagehosting.exe -help
```

### 使用客户端上传

```bash
# 基本用法
./imagehosting-client.exe -url http://localhost:18080/api/v1/upload -file ./image.jpg

# 使用 API 密钥
./imagehosting-client.exe -url http://localhost:18080/api/v1/upload -file ./image.jpg -key your-api-key

# 详细输出
./imagehosting-client.exe -url http://localhost:18080/api/v1/upload -file ./image.jpg -verbose

# 设置超时
./imagehosting-client.exe -url http://localhost:18080/api/v1/upload -file ./image.jpg -timeout 120
```

### 生成 API 密钥

```bash
# 生成一个密钥（默认32字节）
./generate_apikey.exe

# 生成多个密钥
./generate_apikey.exe -count 5

# 指定密钥长度
./generate_apikey.exe -length 64
```

## RESTful API

### 上传图片

**端点**: `POST /api/v1/upload`

**请求**:
- Content-Type: `multipart/form-data`
- 参数: `image` (图片文件)

**认证** (可选):
- Header: `X-API-Key: your-api-key`
- 或 Header: `Authorization: Bearer your-api-key`

**成功响应**:
```json
{
  "success": true,
  "message": "上传成功",
  "data": {
    "url": "https://example.com/file/abc123.jpg",
    "filename": "example.jpg",
    "contentType": "image/jpeg",
    "size": 123456,
    "uploadTime": "2025-05-22T12:00:00Z"
  }
}
```

**失败响应**:
```json
{
  "success": false,
  "message": "错误消息",
  "data": null
}
```

### 健康检查

**端点**: `GET /api/v1/health`

**响应**:
```json
{
  "status": "healthy"
}
```

### 状态监控

**端点**: `GET /status?key={statusKey}`

**响应**:
```json
{
  "status": "ok",
  "startTime": "2025-12-07T14:47:33.151149269+08:00",
  "uptime": "725h46m49.280276368s",
  "goVersion": "go1.25.5",
  "numGoroutine": 8,
  "numCPU": 1,
  "memStats": {
    "alloc": 1607912,
    "totalAlloc": 4917031680,
    "sys": 22370568,
    "numGC": 20707,
    "pauseTotalNs": 10890448805
  },
  "urlCacheSize": 208
}
```

## 数据库结构

### images 表

```sql
CREATE TABLE images (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    telegram_url TEXT NOT NULL,
    proxy_url TEXT NOT NULL UNIQUE,
    ip_address TEXT,
    user_agent TEXT,
    filename TEXT,
    content_type TEXT,
    file_id TEXT NOT NULL,
    upload_time DATETIME DEFAULT CURRENT_TIMESTAMP,
    is_active BOOLEAN DEFAULT 1,
    view_count INTEGER DEFAULT 0
);
```

## 开发约定

### 代码风格

- 使用 Go 标准格式化: `go fmt ./...`
- 遵循 Go 官方代码规范
- 使用有意义的变量和函数名
- 添加必要的注释（特别是复杂的逻辑）

### 错误处理

- 所有错误都必须处理，不要忽略
- 使用日志记录关键错误
- 返回适当的 HTTP 状态码
- 使用 defer 确保资源清理

### 并发控制

- 使用 channel 进行并发控制 (如 global.UploadSemaphore)
- 使用互斥锁保护共享资源 (如 global.URLCacheMux)
- 使用 context 控制超时和取消

### 安全考虑

- 验证所有用户输入
- 使用参数化 SQL 查询防止注入
- 实施速率限制防止滥用
- 使用安全的 session 管理
- CORS 配置支持跨域访问

### 测试

- TODO: 添加单元测试
- TODO: 添加集成测试
- 测试前备份数据库

## 已知限制

1. **Telegram 存储限制**:
   - 动态 WebP 会被转换为静态图片
   - GIF 会被转换为 MP4 视频
   - 这是 Telegram 服务端的限制，无法规避

2. **登录 Bug**:
   - 输入错误密码后需要在新标签页重新打开登录页面
   - 直接刷新页面会一直报错

3. **删除操作**:
   - 删除图片只是禁止访问，数据仍保留在 Telegram 中
   - 无法真正删除 Telegram 中的文件

## 常见任务

### 添加新的图片格式支持

1. 修改 `internal/utils/utils.go` 中的 `AllowedMimeTypes` 映射
2. 修改 `internal/handlers/handlers.go` 中的文件类型检测逻辑
3. 更新文档

### 修改上传文件大小限制

修改 `config.json`:
```json
{
  "site": {
    "maxFileSize": 20  // 单位：MB
  }
}
```

同时确保 Nginx 配置中的 `client_max_body_size` 大于此值。

### 启用 API 认证

1. 生成 API 密钥: `./generate_apikey.exe`
2. 修改 `config.json`:
```json
{
  "security": {
    "requireAPIKey": true,
    "apiKeys": ["generated-key-here"]
  }
}
```
3. 重启服务

### 要求登录后才能上传

修改 `config.json`:
```json
{
  "security": {
    "requireLoginForUpload": true
  }
}
```

### 查看服务日志

日志输出到标准输出，可以使用以下方式查看：

```bash
# Windows PowerShell
./imagehosting.exe | Tee-Object -FilePath app.log

# 或者使用重定向
./imagehosting.exe > app.log 2>&1
```

### 数据库维护

```bash
# 备份数据库
copy images.db images.db.backup

# 查看数据库结构（需要 SQLite 工具）
sqlite3 images.db ".schema"

# 查询所有图片记录
sqlite3 images.db "SELECT * FROM images;"
```

## 环境变量

以下配置可以通过环境变量覆盖：

- `TELEGRAM_BOT_TOKEN`: Telegram Bot 令牌
- `TELEGRAM_CHAT_ID`: Telegram 频道 ID
- `DATABASE_PATH`: 数据库文件路径
- `SERVER_PORT`: 服务器端口
- `DEBUG`: 设置为 "true" 启用调试日志

## 性能优化

- URL 缓存: 缓存 Telegram 文件 URL，减少 API 调用
- 缓存清理: 每 12 小时自动清理过期缓存
- 数据库连接池: 使用连接池提高性能
- 并发控制: 限制同时上传数量
- 流式传输: 使用流式传输处理大文件

## 依赖项

主要依赖（从 go.mod）：

- `github.com/go-telegram-bot-api/telegram-bot-api/v5 v5.5.1`: Telegram Bot API
- `github.com/google/uuid v1.6.0`: UUID 生成
- `github.com/gorilla/mux v1.8.1`: HTTP 路由
- `github.com/gorilla/sessions v1.4.0`: Session 管理
- `modernc.org/sqlite v1.46.1`: SQLite 驱动

## 贡献指南

1. Fork 项目
2. 创建功能分支: `git checkout -b feature/your-feature`
3. 提交更改: `git commit -m 'Add some feature'`
4. 推送到分支: `git push origin feature/your-feature`
5. 创建 Pull Request

## 联系方式

- 项目主页: https://github.com/nodeseeker/goImage
- Issues: https://github.com/nodeseeker/goImage/issues

## 许可证

请查看 LICENSE 文件了解详细信息。