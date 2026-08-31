# 8Ball 联机协议（v1.0.0）

> 权威定义见 `pkg/protocol/`（`messages.go` / `errors.go` / `table.go`）。
> 本文档是对代码的人话版，客户端（Unity）接入请以本文件 + 代码为准。

## 1. 传输与信封

- **传输**：WebSocket（`GET /ws?playerId=&name=&token=`），文本帧，UTF-8 JSON。
- **信封（扁平，所有消息共用字段）**：

| 字段 | 类型 | 方向 | 说明 |
|---|---|---|---|
| `type` | string | 双向 | 消息类型，见 §2/§3 |
| `messageId` | string | 双向 | 客户端生成的请求 ID，服务端在 ACK/ERROR 中回显 |
| `roomId` | string | 双向 | 房间内消息必带（S→C 恒带） |
| `playerId` | string | 双向 | 消息主体。**服务端以连接身份强制覆盖，客户端伪造无效** |
| `seq` | uint64 | S→C | 房间内单调递增序号，客户端可检测丢帧/乱序 |
| `timestamp` | int64 | 双向 | 发送方时钟，unix ms |
| `clientTime` | int64 | C→S | 供 RTT 计算，PONG 原样回显 |
| `serverTime` | int64 | S→C | 服务端时钟，unix ms |

- **心跳**：客户端 5s 发 `PING`；服务端 25s 发 ws 层 ping，60s 无入站流量即断开（WELCOME 中下发该参数）。
- **身份/重连令牌**：连接成功即下发 `sessionToken`（WELCOME）。断线后带 `?playerId=&token=` 重连并发送 `RECONNECT`。

## 2. 客户端 → 服务端

| type | 载荷字段 | 说明 |
|---|---|---|
| `HELLO` | `name`, `token?`, `clientVersion?` | 可选的首次自报家门；改名/换 token（连接参数亦可） |
| `PING` | （信封） | 心跳；回 `PONG`（回显 `clientTime`） |
| `QUICK_MATCH` | `name?`, `elo?` | 进入快速匹配队列（FIFO，两人成局） |
| `CANCEL_MATCH` | — | 退出匹配队列 |
| `CREATE_ROOM` | `name?`, `isPublic?` | 创建房间（好友房），返回邀请码 |
| `JOIN_ROOM` | `roomId` 或 `inviteCode`, `playerName?` | 按房间号/邀请码进房，**默认成为观众**（不占对战座位）；断线后带 token 重连时恢复原身份。固定房间号 `1000` 不存在时**自动懒创建** |
| `JOIN_GAME` | — | 观众主动抢座（2 个对战座位，满员后被拒） |
| `STAND_UP` | — | 离座让位，回到观众席；对局中离座 = 认负 |
| `LEAVE_ROOM` | — | 主动退出；对局中退出 = 认负（对手胜） |
| `READY` | `ready: bool` | 上桌玩家准备开关；双方 ready → `GAME_START`（观众不可发） |
| `RECONNECT` | `sessionToken`, `lastSeq?` | 断线重连，恢复座位并获取快照 |
| `REQUEST_SNAPSHOT` | — | 主动请求权威快照 |
| `SHOOT` | `cueAngle`(rad), `power`(0,1], `spin?{x,y,z}∈[-1,1]` | 出杆**意图**。服务端校验回合/阶段/参数后广播 |
| `STATE_FRAME` | `shotNumber`, `frame?`, `ballStates[16]` | 出杆方 20Hz 模拟帧（尽力而为，可被丢弃/忽略） |
| `SHOT_RESULT` | `shotNumber`, `firstContactBall`, `pocketedBalls`(按进袋时间排序), `outOfBoundsBalls?`, `cueBallMoved`, `ballStates[16]` | 出杆方上报静止后的**结算事实**，服务端反作弊校验后仲裁 |
| `CUE_BALL_PLACEMENT` | `position{x,y,z}` | 自由球摆位（开球阶段限定开球区 kitchen） |
| `CONCEDE` | — | 认输 |
| `DISCONNECT` | — | 优雅告别（重连窗口仍然生效） |

## 3. 服务端 → 客户端

| type | 载荷字段 | 说明 |
|---|---|---|
| `WELCOME` | `sessionToken`, `protocolVersion`, `playerNickname`, `playerAvatar?`, `heartbeatIntervalMs`, `readTimeoutMs`, `reconnectWindowMs`, `stateUpdateHz`, `table`, `resumableRoomId?` | 连接建立即下发；`table` 为几何契约（双方校验一致） |
| `PONG` | `clientTime` 回显 | 心跳应答 |
| `MATCH_QUEUED` | `queuePosition`, `queueSize`, `estimatedWaitMs` | 已入队 |
| `MATCH_FOUND` | `opponent(PlayerInfo)`, `yourSeat` | 配对成功（对手玩家先收到 `ROOM_JOINED` 后收到本条；发起方相反） |
| `MATCH_CANCELLED` | `reason?` | 已出队 |
| `ROOM_CREATED` | `inviteCode`, `yourSeat`, `role`, `gameState` | 建房成功（附带 `ROOM_JOINED`）；房主也默认观众（`role="spectator"`），需发 `JOIN_GAME` 抢座 |
| `ROOM_JOINED` | `status:"success"`, `yourSeat`, `role`, `gameState` | 进房成功（含完整权威快照）；观众 `yourSeat=0, role="spectator"` |
| `JOIN_GAME_ACK` | `status:"seated"`, `seat`, `gameState` | 抢座成功的**私密**回执（失败走 `ERROR`） |
| `STAND_UP_ACK` | `status:"stood_down"`, `gameState` | 离座成功的**私密**回执（失败走 `ERROR`） |
| `ROOM_STATE` | `roomStatus`, `players[]`, `spectators[]`, `gamePhase` | 房间成员/座位/准备状态变化广播（全员含观众） |
| `PLAYER_JOINED` / `PLAYER_LEFT` / `PLAYER_RECONNECTED` | `player(PlayerInfo)`, `reason?` | 成员事件 |
| `PLAYER_DISCONNECTED` | `player`, `reconnectWindowMs`, `reconnectDeadline` | 对手掉线；窗口内未回归则判负 |
| `GAME_START` | `gameState`, `breakerId` | 开局：完整球堆 + 开球方；初始阶段为 `BallInHand`（kitchen 内摆白球） |
| `SHOOT_ACK` | `status`("accepted"/"rejected"), `shotNumber`, `gamePhase`, `errorCode?`, `message?` | 出杆意图的**私密**回执（只有出杆方收到） |
| `SHOT_BROADCAST` | `shooterId`, `shotNumber`, `cueAngle`, `power`, `spin`, `initialSpeed`, `gamePhase`, `ballStates`(击球前权威态), `simulatorPlayerId`, `nextStateUpdateIn` | 广播给双方：以相同参数本地模拟 |
| `STATE_UPDATE` | `shotNumber`, `gamePhase`, `ballStates` | 出杆方 20Hz 帧中继（非出杆方收到） |
| `BALLS_STOPPED` | `shotNumber`, `ballStates`, `pocketedBalls`, `strikeResult`, `gamePhase`, `players[]`, `score` | **仲裁结论**：犯规/换人/胜负全在 `strikeResult` |
| `CUE_BALL_PLACEMENT_ACK` | `status`, `gamePhase`, `currentPlayerId`, `ballStates` | 摆位确认（广播双方） |
| `TURN_CHANGE` | `currentPlayerId`, `gamePhase`, `ballInHand`, `kitchenOnly`, `turnTimeoutMs` | 回合切换显式通知 |
| `GAME_OVER` | `gameStatus`, `winnerId`, `loserId`, `reason`, `score`, `durationMs`, `players[]` | 终局 |
| `SNAPSHOT` | `gameState`, `resumed` | 权威全量快照（重连恢复/校验失败强制重同步） |
| `ERROR` | `errorCode`(string), `code`(int), `message`, `fatal?` | 请求被拒。`fatal:true` 表示连接或房间不可恢复 |

## 4. 关键枚举

- **gamePhase**：`Waiting` → `BallInHand` → `Aiming` → `Moving` → `Resolving` → `Decision` → `GameOver`
- **role / PlayerInfo.role**：`seated`（上桌对战，占 1/2 座位）/ `spectator`（观众，不可出杆/准备/摆位）
- **ballType / PlayerInfo.ballType**：`solid`(1-7) / `stripe`(9-15) / `black`(8) / `cue`(0)；`null` = 未分组（开放球局）
- **roomStatus**：`WAITING` / `READY` / `IN_GAME` / `FINISHED` / `CLOSED`
- **gameStatus**：`playing` / `player1_wins` / `player2_wins` / `draw`
- **reason**（GAME_OVER）：`normal` / `concede` / `opponent_left` / `opponent_disconnect_timeout` / `illegal_eight_ball` / `legal_eight_ball` / `eight_ball_out_of_table` / `room_closed`

### 犯规码（StrikeResult.foulType）

`NO_CONTACT`（白球未碰任何球）、`WRONG_BALL`（首碰非本方球）、`CUE_BALL_POCKETED`、`BLACK_POCKETED_EARLY`（提前打进 8 → 判负）、`BLACK_WITH_CUE`（8 号与白球同落 → 判负）、`CUE_BALL_OUT_OF_BOUNDS`、`BLACK_OUT_OF_BOUNDS`（→ 判负）、`NO_SHOT`（白球未动）、`TURN_TIMEOUT`、`SHOT_RESULT_TIMEOUT`（未按时上报结算）、`ILLEGAL_RESULT_REPORT`（上报被判定伪造）。

### 错误码（ERROR.errorCode）

| 域 | 码 |
|---|---|
| 1xxx 传输 | `BAD_REQUEST`, `UNKNOWN_MESSAGE_TYPE`, `UNAUTHORIZED`, `RATE_LIMITED`, `PAYLOAD_TOO_LARGE` |
| 2xxx 房间 | `ROOM_NOT_FOUND`, `ROOM_FULL`, `ALREADY_IN_ROOM`, `NOT_IN_ROOM`, `INVALID_INVITE_CODE`, `ROOM_CLOSED`, `MATCH_ALREADY_QUEUED`, `MATCH_NOT_QUEUED`, `NOT_SEATED`, `ALREADY_SEATED` |
| 3xxx 规则 | `NOT_YOUR_TURN`, `INVALID_PHASE`, `INVALID_SHOT`, `INVALID_PLACEMENT`, `NOT_BALL_IN_HAND`, `INVALID_SHOT_RESULT`, `DUPLICATE_SHOT`, `GAME_NOT_STARTED`, `GAME_FINISHED` |
| 5xxx 会话 | `SESSION_INVALID`, `RECONNECT_EXPIRED`, `NOTHING_TO_RESUME` |
| 9xxx 内部 | `INTERNAL_ERROR` |

## 5. 一局的标准时序

```
A, B 连接 (/ws)                 → 各收 WELCOME(sessionToken)
A: QUICK_MATCH                  → A 收 MATCH_QUEUED
B: QUICK_MATCH                  → 双方收 MATCH_FOUND + ROOM_JOINED(快照)
A, B: READY{true}               → ROOM_STATE ×2 → GAME_START(breaker, phase=BallInHand)
breaker: CUE_BALL_PLACEMENT     → CUE_BALL_PLACEMENT_ACK(广播, phase=Aiming)
breaker: SHOOT                  → breaker 收 SHOOT_ACK(accepted)
                                  双方收 SHOT_BROADCAST → 双端本地模拟
breaker(模拟中): STATE_FRAME@20Hz → 对手收 STATE_UPDATE(尽力而为)
breaker(静止后): SHOT_RESULT     → 双方收 BALLS_STOPPED(仲裁) → TURN_CHANGE
... 循环 Aiming→Moving→结算 ...
(任意时刻) 断线                 → 对手收 PLAYER_DISCONNECTED(30s 窗口)
重连(/ws?playerId&token) + RECONNECT → SNAPSHOT(resumed=true) 续局
终局条件达成                     → GAME_OVER → 两人留桌 → ROOM_STATE(回 READY/Waiting)
                                     → 重新 READY ×2 → GAME_START 打下一局
```

**好友房（观众 + 抢座）流程**：

```
A: CREATE_ROOM                   → A 收 ROOM_CREATED(seat=0, role=spectator) + ROOM_JOINED —— 房主也默认观众
A: JOIN_GAME                     → A 收 JOIN_GAME_ACK(seat=1) → 全员收 ROOM_STATE(1 上桌)
B: JOIN_ROOM{roomId}             → B 收 ROOM_JOINED(seat=0, role=spectator) —— 默认观众
B: JOIN_GAME                     → B 收 JOIN_GAME_ACK(seat=2) → 全员收 ROOM_STATE(2 上桌)
C: JOIN_ROOM{roomId}             → C 收 ROOM_JOINED(spectator) —— 观战
C: JOIN_GAME                     → C 收 ERROR(ROOM_FULL) —— 座位已满
A, B: READY{true}                → 全员(含 C)收 GAME_START → 观众完整观战(收全部广播)
C: SHOOT / READY                 → C 收 ERROR(NOT_SEATED) —— 观众只读
终局                             → 全员收 GAME_OVER → ROOM_STATE(回 READY/Waiting, 两人留桌)
B: STAND_UP                      → B 收 STAND_UP_ACK → 回观众席 → 座位空出供他人 JOIN_GAME
```

## 6. 反作弊要点（客户端必须遵守，否则被拒/强制重同步）

1. 只有 `currentPlayerId` 能 `SHOOT`/`SHOT_RESULT`/`CUE_BALL_PLACEMENT`/`STATE_FRAME`；
2. `SHOT_RESULT.ballStates` 必须是完整 16 球、全部静止、位置在台面内（含容差）、两两不重叠、已落袋/出台的球不可复活；
3. `pocketedBalls` 按进袋时间**升序**（8 球"最后落袋"判定依赖此序）；
4. `power ∈ (0,1]` 映射初速 1.5~8 m/s（`table` 中下发），`spin ∈ [-1,1]`；
5. 校验失败：回 `ERROR` + 强制 `SNAPSHOT` 重同步；连续异常可进一步限制（Phase 3）。

## 7. 待对齐点（需主理人/Unity 确认）

1. **信封扁平 vs 嵌套 `data`**：本协议采用扁平结构（与 MULTIPLAYER_TECH.md §2.2 示例一致）；REQ-010 示例为 `{"type":..,"data":{..}}` 嵌套。**当前实现：扁平**。
2. **PROJECT RULE 偏离官方 WPA**（GAME_RULES.md 口径，服务端 `rules.Options` 可切换）：
   - 进任意球（含对方球）均续杆（WPA 要求进本方球）；
   - 仅白球落袋/出台给自由球，其余犯规仅换人（WPA 任意犯规均自由球）；
   - 自由球摆位限定开球区（kitchen）。
3. **认证**：暂用连接参数 `playerId` + 服务端签发的 `sessionToken`（非 JWT）；有座位玩家重连必须持有效 token，防止抢座。正式账号体系（Phase 3）接入后替换。
4. **模拟权威归属**：阶段 1 采用"出杆方客户端模拟 + 服务端校验结算"（MULTIPLAYER_TECH.md 决策）；服务端不跑物理。`SHOT_RESULT` 的物理合理性校验是启发式的（静止/越界/重叠/单调性），不含轨迹复算。
5. **MATCH_FOUND 与 ROOM_JOINED 的顺序**：等待方先收 `ROOM_JOINED` 再收 `MATCH_FOUND`；发起方相反。客户端应按 `roomId` 归并，不依赖二者顺序。
6. `estimatedWaitMs` 目前恒为 0（无历史数据，Phase 3 估算）。

## 8. 运行

```bash
go run ./cmd/server          # 默认 :8080，PORT 可覆盖
go run ./cmd/smoke           # 端到端冒烟（需 server 已启动）
```

HTTP：`GET /healthz` `GET /info` `GET /metrics` `GET /rooms`（调试）；`GET /ws` 为游戏通道。
