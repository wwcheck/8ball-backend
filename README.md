# 8Ball Backend

美式8球联机对战后端服务

## 项目信息

- **项目名称**: 8Ball Backend
- **版本**: 0.0.1
- **语言**: Go 1.21+
- **框架**: Gin + WebSocket + gRPC

## 快速开始

### 1. 环境要求

- Go 1.21 或更新版本
- MySQL 8.0+
- Redis 6.0+
- Docker & Docker Compose (可选)

### 2. 安装依赖

```bash
go mod download
go mod tidy
```

### 3. 运行服务器

```bash
# 方式 1: 使用 Makefile
make run

# 方式 2: 直接运行
go run cmd/server/main.go

# 方式 3: 构建后运行
make build
./bin/server.exe
```

### 4. 测试 API

```bash
# 健康检查
curl http://localhost:8080/healthz

# Hello 端点
curl http://localhost:8080/hello

# 服务器信息
curl http://localhost:8080/info
```

## 项目结构

```
8Ball-Backend/
├── cmd/
│   └── server/           # 服务器主程序
│       └── main.go
├── pkg/
│   ├── auth/             # 认证模块
│   ├── room/             # 房间管理
│   ├── match/            # 匹配系统
│   ├── transport/        # 网络传输
│   ├── storage/          # 数据存储
│   ├── cache/            # 缓存层
│   ├── monitor/          # 监控日志
│   ├── physics/          # 物理引擎
│   └── config/           # 配置管理
├── api/
│   └── proto/            # Protobuf 定义
├── tests/                # 测试文件
├── deployments/          # 部署配置
├── config/               # 配置文件
├── scripts/              # 脚本
├── docs/                 # 文档
├── go.mod               # Go 模块定义
├── Makefile             # Make 构建文件
└── README.md            # 本文件
```

## 命令行工具

### 使用 Makefile

```bash
make run     # 运行开发服务器
make build   # 构建二进制文件
make test    # 运行测试
make tidy    # 整理依赖
make fmt     # 格式化代码
make clean   # 清理构建文件
make help    # 显示帮助
```

### 直接使用 Go 命令

```bash
# 运行
go run cmd/server/main.go

# 构建
go build -o bin/server.exe cmd/server/main.go

# 测试
go test ./...

# 整理依赖
go mod tidy

# 格式化代码
go fmt ./...
```

## 核心依赖

- **Gin** - HTTP Web 框架
- **WebSocket** - 实时双向通信
- **gRPC** - 高性能 RPC 框架
- **JWT** - 身份认证
- **MySQL** - 数据库驱动
- **Redis** - 缓存客户端
- **Prometheus** - 性能指标
- **Logrus** - 日志库

## API 端点

### 已实现的端点

| 方法 | 路径 | 描述 |
|------|------|------|
| GET | /healthz | 健康检查 |
| GET | /hello | Hello 消息 |
| GET | /info | 服务器信息 |

### 待实现的端点

- POST /api/v1/auth/register - 用户注册
- POST /api/v1/auth/login - 用户登录
- GET /api/v1/rooms - 列出房间
- POST /api/v1/rooms - 创建房间
- WS /ws - WebSocket 连接

## 开发指南

### 参考文档

- `../claude.md` - 项目规划和职责
- `../ARCHITECTURE.md` - 架构设计
- `../DEVELOPMENT_GUIDE.md` - 开发指南

### 代码风格

- 遵循 Go 官方编码规范
- 使用 `gofmt` 格式化代码
- 编写测试用例
- 添加必要的注释

## 故障排除

### 端口被占用

```powershell
# 查看占用 8080 端口的进程
netstat -ano | findstr :8080

# 杀死进程
taskkill /PID <PID> /F
```

### 找不到依赖

```bash
go mod download
go mod tidy
```

### 编译错误

```bash
# 检查 Go 版本
go version

# 更新依赖
go get -u ./...
```

## 下一步

1. ✅ Go 环境已安装
2. ✅ 依赖已安装
3. ✅ 第一个程序已创建
4. 📖 查看 claude.md 了解项目规划
5. 🏗️ 参考 ARCHITECTURE.md 开始开发模块
6. 💻 按照 DEVELOPMENT_GUIDE.md 编写代码

## 许可证

MIT License

## 联系方式

如有问题，请查阅项目文档或联系开发团队。
