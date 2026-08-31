# 8Ball 后端架构设计文档

> 系统架构、模块设计、数据库设计的完整说明

---

## 整体架构

```
┌─────────────────────────────────────────────┐
│         Unity 客户端（WebSocket/gRPC）      │
└────────────────┬────────────────────────────┘
                 │
        ┌────────┴─────────┐
        ▼                  ▼
   ┌─────────┐      ┌─────────────┐
   │ Gateway │      │   Gateway   │  (负载均衡)
   └────┬────┘      └──────┬──────┘
        │                  │
        └────────┬─────────┘
                 ▼
    ┌────────────────────────────┐
    │  Game Server Pool          │
    │  ├─ Game Server 1          │
    │  ├─ Game Server 2          │
    │  └─ Game Server N          │
    └────────────┬───────────────┘
                 │
    ┌────────────┴──────────────┐
    ▼                           ▼
┌─────────┐            ┌──────────────────┐
│ Redis   │            │ MySQL / MongoDB  │
│ (Cache) │            │  (Database)      │
└─────────┘            └──────────────────┘
```

---

## 核心模块设计

### 1. 认证模块 (pkg/auth/)

**职责**：用户认证、JWT Token 管理、权限控制

```go
type AuthService interface {
    Register(ctx, username, email, password string) (*User, error)
    Login(ctx, username, password string) (*LoginResponse, error)
    ValidateToken(ctx, token string) (*Claims, error)
    Refresh(ctx, refreshToken string) (*LoginResponse, error)
    Logout(ctx, token string) error
}
```

**关键文件**：
- `jwt.go` - JWT Token 生成和验证
- `password.go` - 密码加密（bcrypt）
- `service.go` - 认证业务逻辑
- `middleware.go` - 认证中间件

---

### 2. 房间管理模块 (pkg/room/)

**职责**：游戏房间的创建、加入、状态管理

**房间状态转移**：
```
CREATED → WAITING → PLAYING → FINISHED → CLOSED
```

**关键结构**：
```go
type Room struct {
    ID        string              // 房间 ID
    Status    RoomStatus          // 房间状态
    Player1   *Player             // 玩家1
    Player2   *Player             // 玩家2
    GameState *GameState          // 游戏状态
    CreatedAt time.Time
    UpdatedAt time.Time
    ExpiresAt time.Time           // 房间过期时间
}

type GameState struct {
    Balls        []BallPhysics    // 所有球的物理状态
    CueBall      BallPhysics      // 主球
    CurrentTurn  string           // 当前出杆方
    Score        map[string]int   // 积分
    Events       []GameEvent      // 事件序列
    PhysicsFrame int64            // 物理帧计数
}
```

---

### 3. 对战匹配模块 (pkg/match/)

**职责**：玩家排队、自动匹配、Elo 等级管理

**匹配算法**：
```
1. 玩家进入队列
2. 获取 Elo 评分
3. 设置初始匹配范围（±100分）
4. 每 15 秒扩大范围（±50分）
5. 找到匹配对手 → 创建房间
```

**Elo 计算公式**：
```
新Elo = 旧Elo + K × (实际结果 - 预期概率)

K = 32（标准值）
实际结果 = 1（赢）/ 0（输）
预期概率 = 1 / (1 + 10^((对手Elo - 你的Elo)/400))
```

---

### 4. 网络通信层 (pkg/transport/)

**职责**：连接管理、消息路由、心跳检测

**连接生命周期**：
```
TCP建立 → WebSocket握手 → 认证 → 加入房间 → 游戏通信 → 离开房间 → 关闭
```

**心跳机制**：
```
客户端：每 5 秒发送心跳
服务器：
  - 收到心跳，重置超时计时器
  - 30 秒未收到 → 视为连接断开
  - 自动清理资源
```

---

### 5. 数据存储层 (pkg/storage/)

**数据库设计**：

#### 用户表
```sql
CREATE TABLE users (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    username VARCHAR(255) UNIQUE NOT NULL,
    email VARCHAR(255) UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL
);
```

#### 游戏记录表
```sql
CREATE TABLE game_records (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    room_id VARCHAR(255) NOT NULL,
    player1_id BIGINT NOT NULL,
    player2_id BIGINT NOT NULL,
    winner_id BIGINT NOT NULL,
    player1_score INT DEFAULT 0,
    player2_score INT DEFAULT 0,
    duration_seconds INT,
    started_at TIMESTAMP,
    ended_at TIMESTAMP,
    FOREIGN KEY (player1_id) REFERENCES users(id),
    FOREIGN KEY (player2_id) REFERENCES users(id),
    FOREIGN KEY (winner_id) REFERENCES users(id)
);
```

#### 玩家统计表
```sql
CREATE TABLE player_stats (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    player_id BIGINT UNIQUE NOT NULL,
    total_games INT DEFAULT 0,
    wins INT DEFAULT 0,
    losses INT DEFAULT 0,
    elo_rating INT DEFAULT 1000,
    max_elo_rating INT DEFAULT 1000,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    FOREIGN KEY (player_id) REFERENCES users(id),
    INDEX idx_elo_rating (elo_rating DESC)
);
```

---

## 网络通信设计

### 消息流向

```
Unity 客户端
    ↓ (Protobuf编码)
WebSocket / gRPC
    ↓ (TCP传输)
后端服务器
    ↓ (消息解析)
业务逻辑处理
    ↓ (状态计算)
房间引擎
    ↓ (物理模拟)
存储层
    ↓ (数据持久化)
MySQL / Redis
```

### 同步策略

**1. 状态快照** (每 50ms)
- 所有球体位置
- 所有球体速度
- 当前回合信息
- 积分信息

**2. 增量更新**
- 仅发送变化的属性
- 使用位掩码标识变化

**3. 事件通知** (立即)
- 球进袋
- 犯规
- 回合结束
- 游戏结束

**4. 客户端预测**
```go
func PredictBallPosition(pos, vel Vector3, timeDelta float32) Vector3 {
    return pos.Add(vel.MultiplyScalar(timeDelta))
}
```

---

## 性能优化

### 1. 消息压缩
- Protobuf 减少消息大小（相比 JSON 减少 70%）
- gzip 压缩大型消息

### 2. 缓存策略

| 数据 | 缓存层 | TTL | 更新策略 |
|------|--------|-----|---------|
| 用户信息 | Redis | 1小时 | 写穿 |
| 排行榜 | Redis | 5分钟 | 异步更新 |
| 房间状态 | 内存 | 实时 | - |
| 会话 | Redis | 24小时 | 延迟写 |

### 3. 连接池
- Redis 连接池
- 数据库连接池
- 对象复用（减少 GC）

---

## 故障恢复

### 短暂中断 (<30秒)
```
1. 客户端检测断开
2. 自动重连（指数退避）
3. 发送 SessionID + LastKnownSeq
4. 服务器补发缺失消息
5. 同步完成
```

### 长连接断开 (>30秒)
```
1. 服务器清理会话
2. 自动退出房间
3. 客户端重连需重新加入房间
```

---

## 监控指标

### 业务指标
- `game.active_rooms` - 活跃房间数
- `game.total_players_online` - 在线玩家数
- `game.matches_per_hour` - 每小时匹配数
- `game.average_game_duration` - 平均游戏时长

### 技术指标
- `network.message_latency_ms` - P50/P99 消息延迟
- `network.message_throughput_mps` - 每秒消息数
- `server.cpu_usage_percent` - CPU 使用率
- `server.memory_usage_mb` - 内存使用量
- `server.connection_count` - 活跃连接数

### 告警规则
- P99 延迟 > 200ms
- 消息丢包率 > 0.1%
- CPU 使用率 > 80%
- 内存使用率 > 85%

---

## 部署架构

### Docker 部署
```dockerfile
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

### Kubernetes 部署
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: game-server
spec:
  replicas: 3
  selector:
    matchLabels:
      app: game-server
  template:
    metadata:
      labels:
        app: game-server
    spec:
      containers:
      - name: game-server
        image: 8ball/game-server:latest
        ports:
        - containerPort: 8080
        - containerPort: 50051
        resources:
          requests:
            memory: "256Mi"
            cpu: "250m"
          limits:
            memory: "512Mi"
            cpu: "500m"
```

---

## 总结

该架构设计遵循以下原则：

✅ **Authoritative Server** - 服务器权威，防止作弊  
✅ **模块化设计** - 清晰的职责划分，易于扩展  
✅ **高可用** - 连接池、缓存、故障恢复  
✅ **可观测** - 完整的监控和日志  
✅ **可扩展** - 支持水平扩展和负载均衡  

---

**版本**: 1.0  
**最后更新**: 2026-08-26
