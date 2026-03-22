# goImage 图床

基于 Go 语言开发的图片托管服务，使用 Telegram 作为存储后端。

## 功能特性
- 无限容量，上传图片和文档到 Telegram 频道
- 轻量级要求，内存占用小于 10MB
- 支持图片格式：JPG、PNG、GIF、WebP
- 支持文档格式：PDF、ZIP、DOC、DOCX、XLS、XLSX、PPT、PPTX 等
- 支持管理员登录，查看上传记录和删除文件
- 提供 RESTful API 接口，支持第三方集成
- 包含独立的命令行客户端工具
- 支持跨域资源共享（CORS），可嵌入其他网站使用
- 多线程并行上传，提升上传速度


## 页面展示
首页支持点击、拖拽或者剪贴板上传文件。

![首页](https://github.com/nodeseeker/goImage/blob/main/images/index.png?raw=true)

上传进度展示和后台处理显示。

![进度](https://github.com/nodeseeker/goImage/blob/main/images/home.png?raw=true)

登录页面，输入用户名和密码登录。

![登录](https://github.com/nodeseeker/goImage/blob/main/images/login.png?raw=true)

管理页面，查看访问统计和删除图片。`v0.1.5`版本新增了缩略图功能，以便快速检索和查找、管理等。
注意：删除操作为禁止访问文件，数据依旧存留在telegram频道中。

![管理](https://github.com/nodeseeker/goImage/blob/main/images/admin.png?raw=true)


## 前置准备

1. Telegram 准备工作：
   - 创建 Telegram Bot（通过 @BotFather）
   - 记录获取的 Bot Token
   - 创建一个频道用于存储文件
   - 将 Bot 添加为频道管理员
   - 获取频道的 Chat ID（可通过 @getidsbot 获取）

2. 如需上传文档文件（PDF、ZIP、Office 等），还需：
   - 获取 Telegram API ID 和 API Hash（通过 [my.telegram.org](https://my.telegram.org)）
   - 准备一个已加入目标频道的 Telegram 个人账号

3. 系统要求：
   - 使用 Systemd 的 Linux 系统
   - 已安装并配置 Nginx
   - 域名已配置 SSL 证书（必需）

## 安装步骤

**注意文件名称和路径，以实际文件为准**

1. 创建服务目录：
```bash
sudo mkdir -p /opt/imagehosting
cd /opt/imagehosting
```

2. 下载并解压程序：
   从 [releases页面](https://github.com/nodeseeker/goImage/releases) 下载最新版本并解压到 `/opt/imagehosting` 目录。
```bash
# 下载程序包
wget https://github.com/nodeseeker/goImage/releases/download/v0.2.0/imagehosting-linux-amd64.zip
# 解压文件
unzip imagehosting-linux-amd64.zip
# 移动到目标目录
mv imagehosting-linux-amd64/* /opt/imagehosting/
```
解压后的目录结构：
```
/opt/imagehosting/
├── imagehosting           # 服务器程序文件
├── config.json.example    # 配置文件示例
├── static/                # 静态资源目录
│   ├── favicon.ico
│   ├── robots.txt
│   └── deleted.jpg
├── templates/             # 模板目录
│   ├── home.tmpl
│   ├── login.tmpl
│   ├── upload.tmpl
│   └── admin.tmpl
├── README.md
├── API.md
└── LICENSE
```

3. 创建配置文件：
```bash
cp config.json.example config.json
vim config.json
```

4. 设置权限：
```bash
sudo chown -R root:root /opt/imagehosting
sudo chmod 755 /opt/imagehosting/imagehosting
```

## 配置说明

### 1. 程序配置文件

编辑 `/opt/imagehosting/config.json`，示例如下：

```json
{
    "telegram": {
        "token": "1234567890:ABCDEFG_ab1-asdfghjkl12345",
        "chatId": -123456789
    },
    "telegramUser": {
        "apiId": 12345678,
        "apiHash": "your-api-hash-here",
        "phoneNumber": "+8613800138000",
        "sessionFile": "./session.tg",
        "chatId": 123456789
    },
    "admin": {
        "username": "nodeseeker",
        "password": "nodeseeker@123456"
    },
    "site": {
        "name": "NodeSeek",
        "maxFileSize": 10,
        "maxDocumentSize": 1024,
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
        "statusKey": "nodeseek_status",
        "apiKeys": ["your-secret-api-key-here"],
        "requireAPIKey": false,
        "requireLoginForUpload": false
    },
    "environment": "production"
}
```
详细的说明如下：

**Telegram Bot 配置**（用于图片上传）
- `telegram.token`：电报机器人的 Bot Token
- `telegram.chatId`：频道的 Chat ID（负数，如 -1001234567890）

**Telegram User API 配置**（用于文档上传，可选）
- `telegramUser.apiId`：Telegram API ID（从 my.telegram.org 获取）
- `telegramUser.apiHash`：Telegram API Hash
- `telegramUser.phoneNumber`：已加入目标频道的手机号
- `telegramUser.sessionFile`：会话文件路径，默认 `./session.tg`
- `telegramUser.chatId`：频道 Chat ID（正数，如 1234567890）

**管理员配置**
- `admin.username`：网站管理员用户名
- `admin.password`：网站管理员密码

**站点配置**
- `site.name`：网站名称
- `site.favicon`：网站图标文件名
- `site.maxFileSize`：图片文件最大上传大小（单位：MB），默认 10MB
- `site.maxDocumentSize`：文档文件最大上传大小（单位：MB），默认 1024MB
- `site.port`：服务端口，默认 18080
- `site.host`：服务监听地址，默认 127.0.0.1 本地监听

**数据库配置**
- `database.path`：SQLite 数据库文件路径，默认为 "./images.db"
- `database.maxOpenConns`：最大数据库连接数，默认 25
- `database.maxIdleConns`：最大空闲连接数，默认 10
- `database.connMaxLifetime`：连接最大生存时间，格式为时间字符串，如 "5m" 表示 5 分钟

**安全配置**
- `security.rateLimit.enabled`：是否启用请求速率限制
- `security.rateLimit.limit`：时间窗口内允许的最大请求数，默认 60
- `security.rateLimit.window`：速率限制的时间窗口，如 "1m" 表示 1 分钟
- `security.allowedHosts`：允许访问的主机名列表
- `security.sessionSecret`：会话密钥，留空将自动生成
- `security.statusKey`：状态页面访问密钥
- `security.apiKeys`：API 密钥列表
- `security.requireAPIKey`：是否要求 API 密钥认证
- `security.requireLoginForUpload`：是否要求登录后才能上传

**环境配置**
- `environment`：运行环境，"development" 或 "production"

### 2. Telegram User API 认证

如需上传文档文件，首次使用需要进行认证：

```bash
cd /opt/imagehosting
./imagehosting -auth
```

按提示输入验证码完成认证，认证成功后会生成 `session.tg` 文件。

### 3. Systemd 服务配置

创建服务文件：
```bash
sudo vim /etc/systemd/system/imagehosting.service
```

服务文件内容：
```ini
[Unit]
Description=Image Hosting Service
After=network.target
StartLimitIntervalSec=0

[Service]
Type=simple
Restart=always
RestartSec=5
User=root
WorkingDirectory=/opt/imagehosting
ExecStart=/opt/imagehosting/imagehosting

[Install]
WantedBy=multi-user.target
```

### 4. Nginx 配置示例

在你的网站配置文件中添加：
```nginx
server {
    listen 443 ssl;
    server_name your-domain.com;
    
    ssl_certificate /path/to/cert.pem;
    ssl_certificate_key /path/to/key.pem;
    
    client_max_body_size 1100m;  # 文档上传大小限制
    
    location / {
        proxy_pass http://127.0.0.1:18080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        
        # 大文件上传超时设置
        proxy_connect_timeout 300;
        proxy_send_timeout 300;
        proxy_read_timeout 300;
    }
}
```

## 启动和维护

### 命令行参数

| 参数 | 说明 |
|------|------|
| `-config` | 指定配置文件路径（默认: ./config.json） |
| `-workdir` | 指定工作目录 |
| `-auth` | 执行 Telegram User API 认证 |
| `-help` | 显示帮助信息 |
| `-version` | 显示版本信息 |

**使用示例：**
```bash
# 使用默认配置
./imagehosting

# 指定配置文件
./imagehosting -config /etc/goimage/config.json

# 指定工作目录
./imagehosting -workdir /opt/imagehosting

# User API 认证
./imagehosting -auth
```

### Systemd 服务管理

```bash
# 启动服务
sudo systemctl daemon-reload
sudo systemctl enable imagehosting
sudo systemctl start imagehosting

# 查看状态
sudo systemctl status imagehosting

# 查看日志
sudo journalctl -u imagehosting -f

# 重启服务
sudo systemctl restart imagehosting
```

## 支持的文件类型

| 类型 | 格式 | 大小限制 |
|------|------|----------|
| 图片 | JPG, PNG, GIF, WebP | maxFileSize (默认 10MB) |
| 文档 | PDF, ZIP, DOC, DOCX, XLS, XLSX, PPT, PPTX, TXT, ODT, ODS, ODP | maxDocumentSize (默认 1GB) |

## 安全建议

1. **文件类型验证**：服务器会验证上传的文件类型
2. **限制上传大小**：合理设置 `maxFileSize` 和 `maxDocumentSize`
3. **速率限制**：启用内置速率限制功能
4. **API 访问控制**：启用 API Key 认证（`requireAPIKey: true`）
5. **定期审查**：检查上传记录和日志

## 常见问题

1. **上传失败**：
   - 检查 Bot Token 和 Chat ID 是否正确
   - 确认 Bot 具有频道管理员权限
   - 文档上传需配置 User API 并完成认证

2. **文档上传提示 User API not ready**：
   - 确认 `telegramUser` 配置正确
   - 运行 `./imagehosting -auth` 完成认证
   - 检查 `session.tg` 文件是否存在

3. **上传速度慢**：
   - v0.2.0 已启用多线程并行上传
   - 检查网络连接质量

4. **API 认证失败**：
   - 确保 API Key 配置正确
   - 使用 `-key` 参数传递正确的密钥
   - 修改配置后重启服务

5. **已知限制**：
   - 动态 WebP 会被转换为静态图片
   - GIF 会被转换为 MP4 视频
   - 这是 Telegram 服务端的限制

---

## 客户端和API

程序提供符合RESTful规范的API接口，方便第三方集成和自动化上传。具体内容参考 [API.md](API.md) 文件。
