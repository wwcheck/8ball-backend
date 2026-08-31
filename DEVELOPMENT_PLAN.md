# 8Ball 后端 - 周计划与任务分解

> 详细的每周开发任务和交付物清单

---

## 📅 Week 1: 认证系统 & JWT

**目标**: 完成用户认证系统，支持用户注册、登录、Token 管理

### 🎯 关键成果
- ✅ 用户注册 API 可用
- ✅ 用户登录 API 可用
- ✅ JWT Token 生成和验证
- ✅ 数据库用户表创建
- ✅ 单元测试覆盖 >= 80%

### 📋 详细任务

#### 任务 1.1: 数据库设计 (1 天)
**内容**: 设计用户表结构，编写 MySQL 初始化脚本

**交付物**:
```
scripts/init-db.sql
├── users 表 (用户基本信息)
├── user_profiles 表 (用户扩展信息)
└── sessions 表 (会话记录)
```

**SQL 表结构**:
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

CREATE TABLE user_stats (
    player_id BIGINT PRIMARY KEY,
    total_games INT DEFAULT 0,
    wins INT DEFAULT 0,
    elo_rating INT DEFAULT 1000,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
);
```

#### 任务 1.2: JWT 实现 (1 天)
**内容**: 实现 JWT Token 的生成、验证、刷新

**代码位置**: `pkg/auth/jwt.go`

**实现功能**:
```go
- GenerateToken(userID, username string) (string, error)
- ValidateToken(tokenString string) (*Claims, error)
- RefreshToken(refreshToken string) (string, error)
- RevokeToken(token string) error
```

**测试用例**:
- ✅ Token 生成正确
- ✅ Token 验证通过
- ✅ 过期 Token 验证失败
- ✅ Token 刷新正常

#### 任务 1.3: 数据库 DAO 层 (1 天)
**内容**: 实现用户数据的增删改查

**代码位置**: `pkg/storage/user_dao.go`

**实现方法**:
```go
- CreateUser(ctx, username, email, password) (*User, error)
- GetUserByID(ctx, id int64) (*User, error)
- GetUserByUsername(ctx, username string) (*User, error)
- UpdateUser(ctx, user *User) error
- DeleteUser(ctx, id int64) error
```

#### 任务 1.4: 认证业务逻辑 (1.5 天)
**内容**: 实现用户注册、登录、密码验证等业务逻辑

**代码位置**: `pkg/auth/service.go`

**实现方法**:
```go
type AuthService interface {
    Register(ctx, username, email, password string) (*User, error)
    Login(ctx, username, password string) (*LoginResponse, error)
    ValidateToken(ctx, token string) (*Claims, error)
    Logout(ctx, token string) error
}
```

#### 任务 1.5: REST API 实现 (1.5 天)
**内容**: 实现认证相关的 HTTP 端点

**代码位置**: `api/handlers/auth.go`

**实现端点**:
```
POST   /api/v1/auth/register
       请求: { username, email, password }
       响应: { user_id, token, expires_in }
       
POST   /api/v1/auth/login
       请求: { username, password }
       响应: { token, refresh_token, expires_in }
       
POST   /api/v1/auth/logout
       请求: { token }
       响应: { success }
       
POST   /api/v1/auth/refresh
       请求: { refresh_token }
       响应: { token, expires_in }
       
GET    /api/v1/auth/me
       响应: { user_id, username, email, created_at }
```

#### 任务 1.6: 单元测试 (1 天)
**内容**: 为认证模块编写完整的测试用例

**测试覆盖**:
```
pkg/auth/
├── jwt_test.go
│   ├── TestGenerateToken
│   ├── TestValidateToken
│   └── TestExpiredToken
├── service_test.go
│   ├── TestRegister
│   ├── TestLogin
│   └── TestLoginInvalidPassword
└── integration_test.go
    ├── TestRegisterAndLogin
    └── TestTokenRefresh
```

**目标覆盖率**: >= 90%

#### 任务 1.7: 文档更新 (0.5 天)
**内容**: 更新 API 文档和认证协议说明

**文档位置**: `docs/auth-api.md`

**文档内容**:
- API 端点说明
- 请求/响应示例
- 错误处理
- Token 刷新流程

### 📊 Week 1 交付物清单

- [ ] `scripts/init-db.sql` - 数据库初始化脚本
- [ ] `pkg/auth/jwt.go` - JWT 实现
- [ ] `pkg/auth/password.go` - 密码加密（bcrypt）
- [ ] `pkg/auth/service.go` - 认证业务逻辑
- [ ] `pkg/storage/user_dao.go` - 用户 DAO
- [ ] `api/handlers/auth.go` - 认证 API
- [ ] `pkg/auth/*_test.go` - 完整的测试用例
- [ ] `docs/auth-api.md` - API 文档
- [ ] ✅ 单元测试通过率 >= 90%
- [ ] ✅ 所有 API 端点可用

### ⏱️ 时间估计
- 总耗时: **5-7 天**
- 每天工作量: **5-6 小时**

---

## 📅 Week 2: 房间管理

**目标**: 完成游戏房间的创建、加入、状态管理

### 🎯 关键成果
- ✅ 房间创建 API 可用
- ✅ 房间加入 API 可用
- ✅ 房间状态查询 API 可用
- ✅ 房间超时自动清理
- ✅ 单元测试覆盖 >= 80%

### 📋 详细任务

#### 任务 2.1: 房间数据模型 (1 天)
**代码位置**: `pkg/room/room.go`

```go
type Room struct {
    ID        string        // 房间 ID
    Status    RoomStatus    // 状态
    Player1   *Player       // 玩家1
    Player2   *Player       // 玩家2
    GameState *GameState    // 游戏状态
    CreatedAt time.Time
    UpdatedAt time.Time
    ExpiresAt time.Time
}

type GameState struct {
    Balls        []BallPhysics
    CueBall      BallPhysics
    CurrentTurn  string
    Score        map[string]int
    PhysicsFrame int64
    Events       []GameEvent
}
```

#### 任务 2.2: 房间管理器 (1.5 天)
**代码位置**: `pkg/room/manager.go`

```go
type RoomManager struct {
    rooms map[string]*Room
    mu    sync.RWMutex
}

方法:
- CreateRoom(player1ID string) (*Room, error)
- JoinRoom(roomID, playerID string) (*Room, error)
- LeaveRoom(roomID, playerID string) error
- FinishRoom(roomID string) error
- GetRoom(roomID string) (*Room, error)
- ListActiveRooms() []*Room
```

#### 任务 2.3: REST API (1.5 天)
**代码位置**: `api/handlers/room.go`

```
GET    /api/v1/rooms              # 列出所有房间
POST   /api/v1/rooms              # 创建房间
GET    /api/v1/rooms/{id}         # 获取房间信息
DELETE /api/v1/rooms/{id}         # 关闭房间
POST   /api/v1/rooms/{id}/join    # 加入房间
POST   /api/v1/rooms/{id}/leave   # 离开房间
```

#### 任务 2.4: 房间数据库持久化 (1 day)
**代码位置**: `pkg/storage/room_dao.go`

- 房间信息持久化
- 游戏记录保存

#### 任务 2.5: 测试 (1 day)
- 房间创建测试
- 房间加入测试
- 房间超时测试
- 集成测试

### ⏱️ 时间估计: **5-6 天**

---

## 📅 Week 3: WebSocket 实时通信

**目标**: 实现客户端与服务器的实时双向通信

### 🎯 关键成果
- ✅ WebSocket 服务器可用
- ✅ 消息路由正常
- ✅ 心跳检测正常
- ✅ 连接管理完善
- ✅ 性能测试通过

### 📋 详细任务

#### 任务 3.1: WebSocket 服务器 (1.5 天)
**代码位置**: `pkg/transport/ws.go`

#### 任务 3.2: 消息协议 (1 day)
**代码位置**: `api/proto/messages.proto`

#### 任务 3.3: 消息路由 (1 day)
**代码位置**: `pkg/transport/dispatcher.go`

#### 任务 3.4: 心跳检测 (0.5 day)
**代码位置**: `pkg/transport/heartbeat.go`

#### 任务 3.5: 性能测试 (1 day)

### ⏱️ 时间估计: **5 天**

---

## 📅 Week 4: 游戏核心逻辑

**目标**: 实现游戏状态同步和基本游戏规则

### 🎯 关键成果
- ✅ 出杆逻辑可用
- ✅ 碰撞检测正常
- ✅ 得分规则正确
- ✅ 状态同步准确
- ✅ 压力测试通过

### 📋 详细任务

#### 任务 4.1: 游戏引擎 (2 天)
#### 任务 4.2: 游戏规则 (1.5 day)
#### 任务 4.3: 物理同步 (2 day)
#### 任务 4.4: 压力测试 (1.5 day)

### ⏱️ 时间估计: **7 天**

---

## 📅 Week 5-6: 高级功能

### Week 5: 对战匹配 & 排行榜
- 匹配队列实现
- Elo 等级系统
- 排行榜 API

### Week 6: 监控 & 优化
- Prometheus 集成
- 日志系统集成
- 性能优化
- 部署优化

---

## 🎯 总体时间表

```
Week 1: 认证系统        ████████░░ 80% (5-7 天)
Week 2: 房间管理        ░░░░░░░░░░  0% (5-6 天)
Week 3: WebSocket       ░░░░░░░░░░  0% (5 天)
Week 4: 游戏逻辑        ░░░░░░░░░░  0% (7 天)
Week 5: 匹配 & 排行     ░░░░░░░░░░  0% (4 天)
Week 6: 监控 & 优化     ░░░░░░░░░░  0% (4 天)
       ─────────────────────────────────
总计：                   约 30-35 天 (6 周)
```

---

## ✅ 每周 Checklist

### 周末 Review 要点
- [ ] 所有代码已提交
- [ ] 单元测试覆盖率 >= 80%
- [ ] API 文档已更新
- [ ] 性能指标通过
- [ ] 代码审查通过

---

## 📞 卡点处理

如遇到以下问题，及时调整计划：
- 🔴 依赖问题 → 更新依赖版本
- 🔴 性能问题 → 优化算法或缓存
- 🔴 测试失败 → 调试并修复
- 🔴 需求变更 → 更新本文档

---

**现在准备开始 Week 1 了吗？** 🚀

选择一个任务开始：
1. 📝 生成数据库脚本?
2. 💻 生成 JWT 实现代码?
3. 🗄️ 生成 User DAO 代码?
4. 🧪 生成测试代码?
