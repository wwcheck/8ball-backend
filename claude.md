# 8Ball_PhysX - 美式8球联机对战后端开发指南

> **项目代称**: 8Ball  
> **开发语言**: Go 1.21+  
> **主要角色**: 后端服务开发  
> **最后更新**: 2026-08-26  
> **当前状态**: 🟢 基础框架完成，准备开发核心模块  
> **项目进度**: ████████░░░░░░░░░░░░ 40% 
> **最近完成**: ✅ Go环境搭建 ✅ 项目初始化 ✅ 第一个程序运行

---

## 📋 项目概述

8Ball_PhysX 是一款基于 Unity 引擎的美式8球（Pool/Billiards）多人在线对战游戏。后端负责实现实时联机服务，支持两个玩家的游戏状态同步、对战匹配、持久化存储等核心功能。

### 核心目标
- ✅ 支持两人实时对战，网络延迟 <100ms
- ✅ 游戏画面状态同步精度达到毫秒级
- ✅ 高可用、可扩展的服务架构
- ✅ 支持断线重连机制

---

## 👨‍💻 开发角色划分

### Claude AI 开发人员职责（本人）

**身份**: 后端开发人员，全栈开发支持

**主要职责**:
- 💻 **代码编写** - 生成所有后端 Go 代码实现
- 🏗️ **架构设计** - 设计系统架构和模块结构
- 📝 **文档编写** - 编写项目文档、API 文档、技术文档
- 🧪 **测试代码** - 编写单元测试、集成测试、压力测试
- 🔧 **问题解决** - 调试 bug、性能优化、问题排查
- 📚 **方案设计** - 设计数据库表结构、协议设计、算法设计
- 💡 **建议提供** - 提供技术建议、架构优化建议

**工作内容**:
- ✅ 生成实现代码（pkg/ 目录下的所有模块）
- ✅ 生成测试代码（tests/ 目录）
- ✅ 生成部署脚本（deployments/ 目录）
- ✅ 生成和维护文档（.md 文件）
- ✅ 生成 API 定义（api/ 目录）
- ✅ 生成配置文件（config/ 目录）

### 用户职责

**身份**: 项目负责人，需求提供方

**主要职责**:
- 📋 提供产品需求和业务需求
- ✅ 测试和验证代码功能
- 🔄 反馈和确认设计方案
- 📊 监督项目进度
- 🎯 指导开发方向

**协作流程**:
```
产品需求 → 我设计方案 → 我生成代码 → 你验证测试 → 反馈迭代 → 完成交付
```

---

## 🎯 后端开发职责（技术范围）

### 1. **游戏服务核心**
- 设计并实现实时游戏服务器（Game Server）
- 管理游戏房间、对战匹配逻辑
- 处理物理引擎同步（球体位置、速度、碰撞）
- 实现游戏状态机（等待开局 → 进行中 → 结束）

### 2. **网络通信层**
- 构建低延迟通信协议（WebSocket / gRPC）
- 设计高效的消息序列化（Protocol Buffers / MessagePack）
- 实现心跳检测、超时处理、断线重连
- 版本管理和向后兼容性

### 3. **用户系统**
- 用户认证与授权（JWT Token）
- 用户信息管理与资料存储
- 在线状态追踪
- 好友系统与黑名单管理

### 4. **数据持久化**
- 游戏记录保存（对战历史、排行榜）
- 玩家统计数据（胜率、积分、等级）
- 配置数据管理
- 数据备份与恢复机制

### 5. **对战匹配与房间管理**
- 实现排队匹配系统（基于等级/排行分）
- 房间创建、加入、退出逻辑
- 超时管理与自动清理
- 观战模式支持（可选）

### 6. **游戏状态同步**
- 实现 authoritative server 模式（服务器权威）
- 物理状态同步（球位置、速度、角速度）
- 指令/事件同步（出杆、碰撞、得分）
- 处理网络抖动和延迟补偿

### 7. **监控与日志**
- 性能指标收集（TPS、延迟、带宽使用）
- 业务日志分析（匹配情况、游戏时长）
- 错误追踪与告警
- 实时服务健康检查

### 8. **测试与文档**
- 单元测试与集成测试
- 压力测试（模拟并发玩家）
- 完整的API文档与协议文档
- 部署与运维文档

---

## 🏗️ 技术架构设计

### 整体架构图
```
┌─────────────────────────────────────────────────────────────┐
│                     Unity 客户端                             │
│              (WebSocket / gRPC 连接)                         │
└──────────────────────┬──────────────────────────────────────┘
                       │
        ┌──────────────┼──────────────┐
        ▼              ▼              ▼
┌──────────────┐ ┌──────────────┐ ┌──────────────┐
│  Gateway     │ │  Gateway     │ │  Gateway     │  (负载均衡)
│  (LB)        │ │  (LB)        │ │  (LB)        │
└──────────────┘ └──────────────┘ └──────────────┘
        │              │              │
        └──────────────┼──────────────┘
                       │
        ┌──────────────┴──────────────┐
        ▼                             ▼
┌─────────────────────┐      ┌─────────────────────┐
│  Game Server Pool   │      │  Service Cluster    │
│  ┌─────────────────┐│      │ ┌─────────────────┐ │
│  │ Game Server 1   ││      │ │ Account Service │ │
│  │ Game Server 2   ││      │ │ Rank Service    │ │
│  │ Game Server N   ││      │ │ Match Service   │ │
│  └─────────────────┘│      │ └─────────────────┘ │
└─────────────────────┘      └─────────────────────┘
        │                             │
        └──────────────┬──────────────┘
                       ▼
        ┌──────────────────────────────┐
        │   Cache Layer (Redis)        │
        │  - Session Cache             │
        │  - Room State Cache          │
        │  - Ranking Cache             │
        └──────────────────────────────┘
                       │
        ┌──────────────┼──────────────┐
        ▼              ▼              ▼
    ┌────────┐    ┌────────┐    ┌────────┐
    │  DB    │    │  DB    │    │  DB    │
    │ MySQL  │    │ Mongo  │    │ Graph  │
    └────────┘    └────────┘    └────────┘
```

### 关键设计原则

**1. Authoritative Server 模式**
- 服务器作为唯一真实源（SSOT）
- 客户端预测，服务器验证
- 减少客户端作弊风险

**2. 消息驱动架构**
```go
// 消息流向
Client1 ──(action)──> Server ──(game state update)──> Client2
Client2 ──(action)──> Server ──(game state update)──> Client1
```

**3. 房间隔离**
- 每个对战房间独立运行
- 房间内维护独立的物理引擎状态
- 支持平滑的房间生命周期管理

**4. 分布式一致性**
- 使用 Raft / Paxos 保证关键数据一致性
- 事件溯源（Event Sourcing）记录游戏过程
- 最终一致性原则应用于排行榜等非关键数据

---

## 🛠️ 技术栈详解

### 后端框架与库

| 组件 | 选型 | 原因 |
|------|------|------|
| **Web 框架** | `Gin` / `Echo` | 轻量高效，API路由清晰 |
| **RPC 框架** | `gRPC + Protobuf` | 低延迟，高效序列化 |
| **实时通信** | `WebSocket (Gorilla)` | 浏览器兼容，双向通信 |
| **消息队列** | `RabbitMQ` / `Kafka` | 异步处理，系统解耦 |
| **缓存** | `Redis` | 高速缓存，会话管理 |
| **数据库** | `MySQL` + `MongoDB` | MySQL关系数据，Mongo文档灵活性 |
| **监控** | `Prometheus` + `Grafana` | 时间序列数据，可视化 |
| **日志** | `ELK Stack` (Elasticsearch+Logstash+Kibana) | 集中日志管理，快速查询 |
| **服务框架** | `Micros` / `Kratos` | 微服务治理，服务发现 |
| **测试** | `testing` + `testify` | Go 原生，断言库 |

### 依赖包列表
```go
// go.mod 核心依赖
require (
    github.com/gin-gonic/gin v1.9.x          // Web 框架
    google.golang.org/grpc v1.5x.x            // gRPC 框架
    google.golang.org/protobuf v1.31.x        // Protobuf
    github.com/gorilla/websocket v1.5.x       // WebSocket
    github.com/redis/go-redis/v9 v9.x.x       // Redis 客户端
    github.com/go-sql-driver/mysql v1.7.x     // MySQL 驱动
    go.mongodb.org/mongo-go-driver v1.12.x    // MongoDB 驱动
    github.com/golang-jwt/jwt/v5 v5.x.x       // JWT 认证
    github.com/prometheus/client_golang v1.x  // Prometheus 指标
    github.com/sirupsen/logrus v1.9.x         // 日志库
    github.com/stretchr/testify v1.8.x        // 测试工具
)
```

---

## 📦 核心功能模块设计

### 1. **用户认证与授权模块** (`pkg/auth/`)
```
auth/
├── jwt.go           # JWT Token 生成与验证
├── password.go      # 密码加密（bcrypt）
├── middleware.go    # 认证中间件
└── rbac.go          # 基于角色的访问控制
```

**关键接口**：
```go
type AuthService interface {
    Register(ctx, username, password) (userID, error)
    Login(ctx, username, password) (token, error)
    ValidateToken(ctx, token) (claims, error)
    Refresh(ctx, token) (newToken, error)
    Logout(ctx, token) error
}
```

### 2. **游戏房间模块** (`pkg/room/`)
```
room/
├── room.go          # 房间核心逻辑
├── state.go         # 房间状态管理
├── message.go       # 消息定义与处理
└── physics.go       # 物理状态同步
```

### 3. **对战匹配模块** (`pkg/match/`)
```
match/
├── queue.go         # 匹配队列管理
├── matcher.go       # 匹配算法
└── rating.go        # 等级/排分系统（Elo/Glicko2）
```

### 4. **通信协议模块** (`pkg/protocol/`)
```
protocol/
├── messages.proto   # Protobuf 定义
├── codec.go         # 序列化/反序列化
├── handler.go       # 消息处理器
└── types.go         # Go 消息类型定义
```

### 5. **网络通信层** (`pkg/transport/`)
```
transport/
├── ws_server.go     # WebSocket 服务器
├── grpc_server.go   # gRPC 服务器
├── connection.go    # 连接管理
└── heartbeat.go     # 心跳检测
```

### 6. **数据持久化模块** (`pkg/storage/`)
```
storage/
├── user.go          # 用户数据 DAO
├── game_record.go   # 游戏记录 DAO
├── ranking.go       # 排行榜 DAO
└── migration.go     # 数据库迁移
```

### 7. **监控与日志模块** (`pkg/monitor/`)
```
monitor/
├── metrics.go       # Prometheus 指标
├── logger.go        # 日志记录
├── tracer.go        # 链路追踪（Jaeger）
└── healthcheck.go   # 健康检查
```

---

## 🔄 游戏状态同步方案

### 同步策略

**1. 混合同步模式**
```
客户端预测 + 服务器权威 + 状态校正
│
├─ 客户端预测：立即响应玩家操作，本地预演物理
├─ 服务器处理：计算真实物理，验证合法性
└─ 校正反馈：定期向客户端发送校正信号
```

**2. 消息频率**
- **关键消息**（得分、犯规）：立即发送
- **状态更新**：每 50ms 发送一次（20Hz）
- **心跳**：每 5s 发送一次

**3. 断线重连**
```go
type ReconnectHandler struct {
    SessionID  string        // 会话标识
    GameState  GameState     // 快照
    Timestamp  int64         // 快照时间戳
}

// 重连流程
1. 客户端发送 SessionID + LastKnownState
2. 服务器验证会话有效性
3. 发送当前完整状态 + 快照后的所有事件
4. 客户端重演本地预测 + 接收补充消息
5. 同步完成
```

---

## 📋 实现路线（Roadmap）

### **Phase 1: 核心基础设施** (周 1-2)
- [ ] 项目结构搭建 & Go Module 初始化 ✅
- [ ] gRPC 基础框架 & Protocol Buffers 定义
- [ ] WebSocket 连接层实现 ✅
- [ ] 基础的用户认证（简化版 JWT）
- [ ] 简单的房间管理逻辑
- [ ] 单元测试框架搭建 ✅

**交付物**：
- 可连接的 WebSocket 服务 ✅
- 用户注册/登录接口
- 创建/加入房间接口

### **Phase 2: 游戏核心逻辑** (周 3-4)
- [ ] 游戏状态机实现
- [ ] 物理状态同步协议
- [ ] 出杆、碰撞检测逻辑
- [ ] 得分、犯规规则实现
- [ ] 断线重连机制
- [ ] 集成测试

### **Phase 3: 对战系统** (周 5-6)
- [ ] 玩家匹配队列
- [ ] Elo 等级系统
- [ ] 排行榜模块
- [ ] 游戏记录持久化
- [ ] 统计数据计算
- [ ] 性能优化

### **Phase 4: 运维与优化** (周 7-8)
- [ ] 监控指标集成 (Prometheus)
- [ ] 日志系统集成 (ELK/Loki)
- [ ] 链路追踪 (Jaeger)
- [ ] 压力测试 & 性能优化
- [ ] 部署脚本 & Docker 化
- [ ] 文档完善

### **Phase 5: 高级特性** (后续)
- [ ] 观战模式
- [ ] 回放系统
- [ ] 社交功能（好友、聊天）
- [ ] 成就系统
- [ ] 多种游戏模式支持

---

## 📐 API 设计规范

### REST API 端点设计
```
# 用户相关
POST   /api/v1/auth/register       # 注册
POST   /api/v1/auth/login          # 登录
POST   /api/v1/auth/logout         # 登出
POST   /api/v1/auth/refresh        # 刷新 Token
GET    /api/v1/users/{id}          # 获取用户信息
PUT    /api/v1/users/{id}          # 更新用户信息

# 房间相关
GET    /api/v1/rooms               # 列出房间
POST   /api/v1/rooms               # 创建房间
GET    /api/v1/rooms/{id}          # 获取房间信息
DELETE /api/v1/rooms/{id}          # 关闭房间

# 对战相关
POST   /api/v1/match/queue         # 加入匹配队列
DELETE /api/v1/match/queue         # 离开匹配队列

# 排行榜
GET    /api/v1/ranking             # 获取排行榜
GET    /api/v1/ranking/{id}        # 获取玩家排名

# 游戏记录
GET    /api/v1/records             # 获取游戏记录
GET    /api/v1/records/{id}        # 获取游戏详情
```

---

## 🧪 测试策略

### 单元测试
```bash
# 测试覆盖率目标 >= 80%
go test -v -cover ./...

# 特定包测试
go test -v -cover ./pkg/room/
```

### 集成测试
```bash
# 启动测试环境
docker-compose -f docker-compose.test.yml up

# 运行集成测试
go test -v -tags=integration ./tests/integration/...
```

### 压力测试
```bash
# 模拟 N 个并发玩家
# 测试指标：
# - 吞吐量 (TPS)
# - 延迟 (P99)
# - 内存占用
# - CPU 使用率

go test -v -bench=. -benchmem ./tests/benchmark/...
```

---

## 🐳 部署与运维

### Docker 容器化
```dockerfile
# Dockerfile
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 go build -o game-server ./cmd/server

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/game-server .
EXPOSE 8080 50051
CMD ["./game-server"]
```

---

## 📚 目录结构
```
8Ball-Backend/
├── cmd/
│   └── server/
│       └── main.go              # 服务器入口
├── pkg/
│   ├── auth/                    # 认证模块
│   ├── room/                    # 房间模块
│   ├── match/                   # 匹配模块
│   ├── protocol/                # 通信协议
│   ├── transport/               # 网络层
│   ├── storage/                 # 数据存储
│   ├── monitor/                 # 监控日志
│   ├── physics/                 # 物理引擎适配
│   └── config/                  # 配置管理
├── api/
│   ├── gameservice/
│   │   └── service.proto        # gRPC 定义
│   └── messages.proto           # 消息定义
├── tests/
│   ├── unit/                    # 单元测试
│   ├── integration/             # 集成测试
│   └── benchmark/               # 性能测试
├── deployments/
│   ├── docker-compose.yml       # 本地部署
│   ├── k8s/                     # Kubernetes
│   └── helm/                    # Helm Charts
├── docs/
│   ├── architecture.md          # 架构文档
│   ├── api.md                   # API 文档
│   ├── protocol.md              # 协议文档
│   └── deployment.md            # 部署文档
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

---

## 🔐 安全考虑

### 数据安全
- [ ] HTTPS / TLS 加密传输
- [ ] 密码使用 bcrypt + salt 加密存储
- [ ] JWT Token 使用 RS256 签名算法
- [ ] API 速率限制（防止暴力破解）
- [ ] SQL 注入防护（使用参数化查询）

### 游戏安全
- [ ] 服务器权威架构（防止客户端作弊）
- [ ] 出杆参数范围验证
- [ ] 物理计算结果验证
- [ ] 异常行为检测（超速、穿墙等）

### 网络安全
- [ ] 输入验证与清理
- [ ] CORS 配置限制
- [ ] DDoS 防护
- [ ] 日志审计

---

## 📖 参考文档与资源

### 游戏同步相关
- [Source Engine 网络同步](https://developer.valvesoftware.com/wiki/Networking)
- [GaffeGames - Snapshot Interpolation](https://www.gabrielgambetta.com/entity-interpolation.html)
- [Unity Netcode 官方文档](https://docs.unity.com/netcode/)

### Go 相关
- [Go 官方文档](https://golang.org/doc/)
- [Effective Go](https://golang.org/doc/effective_go)
- [gRPC Go 教程](https://grpc.io/docs/languages/go/)

### 架构参考
- 《游戏引擎架构》
- 《分布式系统设计》
- 《网络游戏同步技术》

---

## 🎯 成功标准

**Alpha 版本**
- ✅ 支持两个玩家实时对战
- ✅ 消息延迟 < 100ms (P99)
- ✅ 99.9% 连接可用性

**Beta 版本**
- ✅ 支持 50 并发房间
- ✅ 完整的排行榜系统
- ✅ 自动匹配功能
- ✅ 完整的 API 文档

**Release 版本**
- ✅ 支持 500+ 并发房间
- ✅ 多种游戏模式
- ✅ 完整的监控告警
- ✅ 可观测性达到生产级别

---

**版本**: 1.0  
**最后更新**: 2026-08-26  
**维护者**: 后端开发团队
