# SUSE 定时打卡

一个本地运行的 Go 后端 + 内嵌前端定时打卡工具，打卡链路参考 [SUSE-OAA-APP](https://github.com/suse-edu-cn/SUSE-OAA-APP) 的 UIAS / 652 实现。页面登录复用 SUSE OAA 后端的账号体系，通过 `/v2/auth/*` 与 `/v2/user/me` 完成 access + refresh 双 token 登录和自动续期；打卡账号既支持 UIAS 密码 + 图形验证码，也支持 qfhy 微信扫码登录。定时任务在重试后仍有账号失败时，可以选用共用的 SMTP 邮箱发送摘要通知。验证码识别使用 [GoCaptcha](https://github.com/YN1753/GoCaptcha) 的公开 Go SDK，在打卡进程内完成：

- 不调用 `ddddocr` CLI；
- 不启动额外的 OCR HTTP 服务；
- 不引入 Python / OpenCV / 远程 OCR API。

> 这是个人自动化工具，不是学校官方应用。位置使用源码内置的校区点位，账号只选择校区；请只在自己的账号上使用，并遵守学校考勤规定。

## 功能

- OAA 账号登录：与 suseoaa 后端共用账号系统，调用 `/v2/auth/login`、`/v2/auth/refresh`、`/v2/auth/logout`，并用 `/v2/user/me` 校验登录状态；access token 过期后自动用 refresh token 续期。
- 多账号管理：学号、密码、校区（宜宾/李白河/汇东）。
- 位置数据：内置源码 141 个点位（宜宾 74、李白河 47、汇东 20），每次打卡从账号所选校区随机选择一个。
- 可选失败邮件通知：定时任务重试耗尽后，如果仍有账号失败，向设置的通知邮箱发送摘要；所有通知共用一个服务端 SMTP 发信邮箱。
- UIAS 密码登录：登录页 `execution`、图形验证码、反转密码 + 原始 RSA。
- 微信扫码登录：获取 qfhy 二维码，后端通过 SSE 接收扫码状态，扫码确认后用 `_sop_session_` 换 `SESSION`；扫码凭证只保存在服务端。
- 验证码自动识别：GoCaptcha 进程内推理，固定要求 4 位 ASCII 字母/数字。
- rememberMe 快速登录：成功后加密保存 UIAS Cookie，下次优先免验证码登录。
- 扫码账号重新扫码：会话过期后在账号菜单点“重新扫码”即可更新会话，不需要删号重建。
- 扫码账号参与定时：每次运行前自动用 `_sop_session_` 刷新 SESSION，直到扫码会话本身过期。
- 652 打卡：拉取待签到任务、读取详情、提交位置签到。
- 定时执行：每天固定时间（默认 `Asia/Shanghai`）、选择账号、失败重试 N 次、间隔 M 分钟。
- 手动运行：立即对定时绑定的账号执行一次。
- 验证码留样：登录失败时保存验证码图片 + 我们填入的 OCR 文本 + 登录结果，便于后续评估与人工纠正。
- 运行记录：账号结果、任务、OCR 结果、失败原因、样本入口。
- 本地安全：默认只监听 `127.0.0.1`，打卡密码和 UIAS 登录会话 AES-GCM 加密落盘，API 不返回密码/Cookie；OAA 双 token 只保存在浏览器本地，服务端不落盘。

## 账号与登录

- 账号密码由 suseoaa 后端校验，打卡项目只做转发，不直接访问 OAA 数据库。
- 使用 OAA 同一套双 token：短期 access token（JWT）用于访问打卡 API；过期后前端调用 `/api/auth/refresh`，由打卡服务转发到 OAA `/v2/auth/refresh`，refresh token 保持不变。
- 打卡服务校验 Bearer token 时调用 OAA `/v2/user/me`，并做不超过 30 秒的短缓存。OAA 不可用时页面接口返回 502，已经启动的打卡任务不受影响。
- 退出登录会调用 OAA `/v2/auth/logout` 注销当前设备的 refresh token，并清空浏览器本地 token。
- 设备标识由浏览器生成并保存在 localStorage（形如 `checkin-5fb7c12968`），同一浏览器的多个标签页共享。
- 默认 `CHECKIN_OAA_BASE=http://127.0.0.1:8080`，部署时改成实际的 OAA 后端地址。
- 当前**不做 OAA 用户之间的数据隔离**：任何能登录 OAA 的账号都可以访问本实例的打卡账号、定时设置和运行记录。不要把本实例直接暴露给不信任的用户。

### 打卡账号的登录方式

- **账号密码**：保存 UIAS 密码；运行时走图形验证码 + RSA 密码登录，成功后保存 UIAS rememberMe Cookie。
- **微信扫码**：不保存 UIAS 密码；扫码成功后保存 qfhy 的 `_sop_session_` / `SESSION` Cookie（加密落盘），用于后续打卡。会话过期后需要在账号菜单点击“重新扫码”。
- 两种账号共用同一套校区和随机点位逻辑；定时任务可以同时包含两种账号。

## 打卡链路

参考实现的位置：

- 登录页、验证码、任务接口：[CheckinApiService.kt](https://github.com/suse-edu-cn/SUSE-OAA-APP/blob/main/shared/src/commonMain/kotlin/com/suseoaa/projectoaa/shared/data/remote/api/CheckinApiService.kt)
- UIAS 登录 / rememberMe / 短信挑战：[UiasLoginRepository.kt](https://github.com/suse-edu-cn/SUSE-OAA-APP/blob/main/shared/src/commonMain/kotlin/com/suseoaa/projectoaa/shared/data/repository/checkin/UiasLoginRepository.kt)
- 微信扫码接口 / SSE / SSO：[QrCodeCheckinApiService.kt](https://github.com/suse-edu-cn/SUSE-OAA-APP/blob/main/shared/src/commonMain/kotlin/com/suseoaa/projectoaa/shared/data/remote/api/QrCodeCheckinApiService.kt)
- `_sop_session_` JWT 解析：[SopSessionParser.kt](https://github.com/suse-edu-cn/SUSE-OAA-APP/blob/main/shared/src/commonMain/kotlin/com/suseoaa/projectoaa/shared/data/repository/checkin/SopSessionParser.kt)
- 扫码账号保存 / 会话续期：[QrCodeCheckinRepositoryImpl.kt](https://github.com/suse-edu-cn/SUSE-OAA-APP/blob/main/shared/src/commonMain/kotlin/com/suseoaa/projectoaa/shared/data/repository/QrCodeCheckinRepositoryImpl.kt)
- 任务选择 / 详情 / 提交：[CheckinTaskRepository.kt](https://github.com/suse-edu-cn/SUSE-OAA-APP/blob/main/shared/src/commonMain/kotlin/com/suseoaa/projectoaa/shared/data/repository/checkin/CheckinTaskRepository.kt)
- 签到请求体：[CheckinSignData.kt](https://github.com/suse-edu-cn/SUSE-OAA-APP/blob/main/shared/src/commonMain/kotlin/com/suseoaa/projectoaa/shared/data/repository/checkin/CheckinSignData.kt)

本项目的等价流程：

1. `GET https://uias.suse.edu.cn/sso/login?service=...`，从页面提取 `execution`。
2. `GET https://uias.suse.edu.cn/sso/captcha.jpg`，用 GoCaptcha 识别 4 位验证码。
3. 密码先反转，再做无填充 RSA 幂模运算，输出 256 位十六进制。
4. `POST` 登录表单（`username`、`password`、`authcode`、`execution`、`encrypted=true`、`_eventId=submit`、`loginType=1`、`rememberMe=true`）。
5. 跟随 302 到 qfhy，确认拿到 `SESSION` Cookie。
6. `GET /site/qddk/qdrw/api/myList.rst?status=1`，优先选今天且“进行中”的任务，否则取第一条。
7. `GET /site/qddk/qdrw/qdxx/api/detailList.rst?qdrwId=...`，检查 `qdzt`。
8. 从账号所选校区的内置点位中随机取一个，使用源码里原样的 `location` JSON 和地址。
9. `POST /site/qddk/qdrw/api/checkSignLocationWithPhoto.rst`，提交 `id/qdzt/qdsj/isOuted/isLated/dkddPhoto/qdddjtdz/location/txxx`。

短信二次验证目前**明确失败并停止**，不会尝试自动绕过；验证码失败会自动换一张图重试。

微信扫码账号的链路（参考项目的 `QrCodeCheckinApiService`、`SopSessionParser`、`QrCodeCheckinRepositoryImpl`）：

1. `GET https://qfhy.suse.edu.cn/edu/v2/weixin/getClientId` 获取 `client_id`。
2. `POST /edu/v2/weixin/getQrCodeUrl` 获取二维码（`app_id=wx130c9f0196e29149`）。
3. 打卡后端连接 `GET /edu/v1/wechat/stream?clientId=...` SSE：`1` 表示已扫码，`2` 表示确认成功，其他状态视为取消。
4. 成功后访问 `/callback/edu/?<event-data>` 获取 `_sop_session_` JWT，解析 `uid`、`extra.openId`、`extra.userName`。
5. `GET /site/appware/system/sso/loginUrl?service=<编码后的 /xg/app/qddk/admin?open_id=...>` 换取 qfhy `SESSION`。
6. 保存 `_sop_session_` + `SESSION`，后续任务列表、详情、签到与密码账号共用同一套提交逻辑。
7. 每次扫码账号运行前，用保存的 `_sop_session_` 重新换取 SESSION；`_sop_session_` 过期后提示重新扫码。

## 环境要求

- Go 1.27.3+（`go.mod` 要求 1.27.3，低于此版本会由 Go 自动下载对应工具链；本项目在 Go 1.27.3 / macOS ARM64 上验收）。
- 能访问 suseoaa 后端（默认 `http://127.0.0.1:8080`，需与后端版本匹配）。
- 能访问 `uias.suse.edu.cn` 和 `qfhy.suse.edu.cn`（校外通常需要学校 VPN）。
- 如需失败邮件通知，需要可用的 SMTP 服务；默认按 QQ 邮箱（`smtp.qq.com:465`）配置。
- GoCaptcha 平台限制见其 README：
  - macOS ARM64 已验证；macOS Intel 通过 Rosetta 验证；
  - Linux x64 已验证，要求 glibc 2.27+，Alpine/musl 不支持；
  - Windows x64 需要系统安装 Microsoft Visual C++ 2015-2022 Redistributable；
  - Linux ARM64、Windows ARM64 上游尚未完成独立验收。

## 快速开始

```bash
# 1. 安装依赖（go.mod 已固定 alpha.8）
GOPROXY=https://proxy.golang.org,direct \
  go get github.com/YN1753/GoCaptcha@v0.1.0-alpha.8
go mod tidy

# 2. 配置 OAA 后端地址（本地联调默认 http://127.0.0.1:8080）
export CHECKIN_OAA_BASE=https://your-oaa-backend.example.com

# 3. 可选：配置失败通知的共用发信邮箱
export CHECKIN_SMTP_USER=suse_oaa@qq.com
export CHECKIN_SMTP_PASS='SMTP 授权码'

# 4. 启动
go run ./cmd/server

# 5. 打开后用 OAA 账号登录
open http://127.0.0.1:8099
```

首次启动会把内嵌的 ONNX Runtime 释放到 `data/gocaptcha/`，之后复用缓存，不需要联网下载模型。

构建单文件二进制：

```bash
# 本机构建；版本号由 -ldflags 注入，未指定时为 dev
make build
./suse-checkin -version

# 等价的手写命令
CGO_ENABLED=0 go build -trimpath -buildvcs=false \
  -ldflags "-s -w -X suse-checkin/internal/version.Version=v0.1.0" \
  -o suse-checkin ./cmd/server
./suse-checkin
```

打发布包（默认 `linux/amd64`、`linux/arm64`、`darwin/arm64`，输出到 `dist/`）：

```bash
make release VERSION=v0.1.0
# dist/suse-checkin_v0.1.0_linux_amd64.tar.gz
# dist/suse-checkin_v0.1.0_linux_arm64.tar.gz
# dist/suse-checkin_v0.1.0_darwin_arm64.tar.gz
# dist/sha256sums.txt
```

只打包指定平台时可以直接调脚本：`PLATFORMS="linux/amd64" ./scripts/build-release.sh v0.1.0`。

## 发布与 CI

- `.github/workflows/ci.yml`：向 `main` 推送或提交 PR 时执行 gofmt 检查、`go vet`、`go test`，并在 Ubuntu runner 上交叉编译 `linux/amd64` 做一次 `-version` 冒烟测试。
- `.github/workflows/release.yml`：推送 `v*` tag 时自动跑测试、按 tag 打包三个平台、校验 Linux 二进制能启动，然后用 `gh release create` 创建 GitHub Release 并上传 `*.tar.gz` 与 `sha256sums.txt`；重复执行会用 `--clobber` 覆盖同名产物。
- 版本号只有一个来源：git tag。`Version` 变量位于 [internal/version/version.go](internal/version/version.go)，由 `-ldflags -X suse-checkin/internal/version.Version=<tag>` 注入，`-version` 与启动日志、`/api/healthz` 返回的 `version` 字段都读取它。

发版步骤：

```bash
git tag v0.1.0
git push origin v0.1.0   # 触发 release 工作流
```

Linux 产物是动态链接 glibc 的可执行文件（GoCaptcha 通过 purego dlopen 运行时），要求 glibc 2.27+，不支持 Alpine/musl。

验证真实验证码抓取和识别（不登录、不提交任何凭据）：

```bash
go run ./cmd/server -captcha-check
# 输出示例：验证码识别: "0430" (17 ms)
```

## 使用流程

1. 用 SUSE OAA 账号登录页面（学号/用户名/邮箱 + 密码）。
2. 点击“添加账号”，选择登录方式：
   - **账号密码**：填写 UIAS 学号、密码、姓名和校区。
   - **微信扫码**：生成二维码后用微信扫描并确认，服务端会自动获取学号和姓名；确认校区后保存。
   - 账号只选择校区，不能选择具体点位，也不能填写自定义坐标。
   - 每次打卡由后端从该校区内置点位中随机选择一个；本次实际使用的地址和坐标会显示在运行记录的“位置”列。
3. 点击“编辑定时”，在弹窗中勾选账号、启用定时、设置时间与重试参数，保存。
   - 可选开启“失败时邮件通知”，通知邮箱默认填入当前 OAA 账号的邮箱，也可以修改。
   - 卡片本身只展示状态：顶部“运行状态与定时”一张卡片汇总了启用徽章、下次执行时间与“距今”、上次结果、重试与通知统计，以及参与定时的账号列表。
4. 需要立刻执行时，点击“运行定时账号”；该操作是异步的，页面轮询显示进度。
5. 运行完成后在“最近运行”里查看每个账号的状态、实际位置、OCR 文本和验证码样本。
6. 如果识别失败，样本位于 `data/captchas/YYYY-MM-DD/`，图片旁的 `.json` 记录了 OCR 文本和结果。

## 位置数据

位置目录来自 [CheckinModels.kt](https://github.com/suse-edu-cn/SUSE-OAA-APP/blob/main/shared/src/commonMain/kotlin/com/suseoaa/projectoaa/shared/domain/model/checkin/CheckinModels.kt) 的 `CheckinLocations`，已提取到 `internal/locations/locations.go`：

| 校区 | 点位数 |
|---|---:|
| 宜宾 | 74 |
| 李白河 | 47 |
| 汇东 | 20 |
| **合计** | **141** |

- 账号只保存校区，不保存具体点位或自定义坐标。
- 每次打卡都会重新随机选点，等价于源码的 `randomLocationForCampus(selectedLocation)`。
- `location` 字段直接使用源码中的原始 JSON 字符串；`qdddjtdz` 使用同一地址，保证两者一致。
- 测试会校验 141 个点的地址、经纬度与 `location` JSON 完全一致；服务层测试会校验实际提交的点来自所选校区。
- 如果学校后续更新了源码点位，替换/更新 `internal/locations/locations.go` 并同步测试计数即可。

## 定时与重试规则

- 时区固定为 `Asia/Shanghai`，每天执行一次。
- 如果当天设定时间已经过去，下一次是明天；服务器在设定时间处于关闭状态时**不会补跑**，请手动运行。
- 每个账号顺序执行，账号之间间隔 1 秒。
- 失败重试只针对可重试错误：验证码识别错误、验证码登录错误、会话失效、网络/上游临时错误。
- 密码错误、短信二次验证、签到业务失败不会重试。
- 定时执行完成后记录 `lastRunDate`，当天不会重复触发。
- 手动运行不受“启用定时”开关限制。
- 失败邮件通知只针对定时运行；手动运行不会发送通知。
- 如果服务在设定时间未运行，不会补跑，也不会发送通知。
- 扫码账号可以参与定时。每次运行前会用保存的 `_sop_session_` 刷新 SESSION；如果扫码会话已经过期，本次运行会以“扫码会话已过期”失败，需要重新扫码。
- 密码账号和扫码账号可以在同一次定时任务中混合执行。

## 失败邮件通知

- 在“编辑定时”弹窗中勾选“失败时邮件通知”并填写通知邮箱后生效；邮箱默认填入当前 OAA 账号的邮箱。
- 触发条件：定时任务执行完配置的失败重试次数后，仍有至少一个账号状态不属于“签到成功 / 已签到 / 无任务”。每个账号各自的失败原因会汇总到一封邮件。
- 所有通知共用一套服务端 SMTP 发信配置，发件邮箱由 `CHECKIN_SMTP_USER` / `CHECKIN_SMTP_FROM` 决定；收件邮箱是每个实例保存的通知邮箱。
- 未配置 SMTP 时，页面上的通知开关不可用，服务端也会拒绝开启通知。
- 邮件正文只包含账号名、学号、状态、失败原因、任务名和运行时间，不包含密码、Cookie、验证码图片或完整请求。
- SMTP 发送失败不会影响打卡结果，只会在服务日志和定时结果中标记“失败通知发送失败”。

QQ 邮箱示例（465 端口隐式 TLS，`CHECKIN_SMTP_PASS` 填 SMTP 授权码而不是登录密码）：

```bash
export CHECKIN_SMTP_HOST=smtp.qq.com
export CHECKIN_SMTP_PORT=465
export CHECKIN_SMTP_USER=suse_oaa@qq.com
export CHECKIN_SMTP_PASS='你的 SMTP 授权码'
export CHECKIN_SMTP_TLS=true
```

使用 587 端口 STARTTLS 时设置 `CHECKIN_SMTP_PORT=587`、`CHECKIN_SMTP_TLS=false`；不支持 STARTTLS 的明文 SMTP 会被拒绝。

## 验证码接入

核心代码在 `internal/captcha/recognizer.go`：

```go
recognizer, err := captcha.New()      // 进程启动时加载一次
text, err := recognizer.Recognize(img) // 并发安全，失败自动换图重试
```

- 模型、字符集、ONNX Runtime 都是 GoCaptcha 模块内嵌的，构建阶段不需要 Python/OpenCV/ORT。
- 识别结果只接受 4 位 ASCII 字母或数字；长度或字符不符按失败处理。
- 每次登录尝试使用全新的 UIAS 会话和验证码；失败后由服务层按配置重试。
- 每次登录尝试都会留样验证码图片，包含我们填入的 OCR 文本、耗时、最终状态和错误信息；不保存账号密码、Cookie 或完整请求头。页面不再提供验证码诊断入口，识别效果通过 `-captcha-check` 和留样确认。
- 当前只验证了通用模型在目标站点验证码上的少量样本，不承诺通用准确率。目标站点改版或验证码风格变化后需要重新评估；留样就是为此准备的。

## 安全与隐私

- 默认监听 `127.0.0.1:8099`，页面需要 OAA 登录，但**没有按 OAA 用户隔离数据**：任何有效 OAA 账号都能查看和操作本实例的全部打卡账号。不要把实例暴露给不信任的用户，也不要直接暴露到公网。
- 需要远程访问时，建议 SSH 隧道：
  ```bash
  ssh -L 8099:127.0.0.1:8099 user@your-host
  ```
  或在前面加带 TLS 和独立访问控制的反向代理。
- OAA 的 access token 与 refresh token 保存在浏览器 localStorage，退出登录会清空并调用 OAA 登出。CSP 只允许同源脚本，但 localStorage 仍可能被 XSS 读取，请只在可信浏览器环境中使用。
- SMTP 密码只从环境变量读取，不写入 `data/state.json`；通知邮件不包含打卡密码、Cookie 或验证码图片。
- 打卡密码、UIAS rememberMe Cookie 和扫码账号的 `_sop_session_` / `SESSION` Cookie 都使用 AES-256-GCM 加密写入 `data/state.json`；密钥在 `data/key`（`0600`），状态文件也是 `0600`，数据目录 `0700`。
- 扫码过程中的二维码和待登录状态只保存在服务端内存（默认最多保留 5 分钟），不会把 qfhy Cookie 暴露给浏览器；浏览器只拿到二维码图片和学号/姓名。
- 前端按 `core / services / ui / views` 模块拆分，放在 `web/static/`，由 Go `embed` 打包进单文件二进制；页面只加载本机 `/static` 资源。
- 密钥和密文在同一个目录，只能防止误传明文，不能抵御已控制本机账户的攻击者。不要把 `data/` 提交到 Git。
- API 只返回 `AccountView`，不返回打卡密码或 UIAS Cookie；运行日志不打印密码/Cookie。
- 修改类接口要求 `Content-Type: application/json`；不开启 CORS；有 Host/回环地址校验、CSP、`nosniff` 等基础防护。
- 验证码样本只在本机 `data/captchas/`，其中不含凭据；请不要把样本目录随意公开。

## 配置

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `CHECKIN_ADDR` | `127.0.0.1:8099` | HTTP 监听地址 |
| `CHECKIN_DATA_DIR` | `data` | 数据、密钥、样本目录 |
| `CHECKIN_TZ` | `Asia/Shanghai` | 调度时区 |
| `CHECKIN_UIAS_BASE` | `https://uias.suse.edu.cn` | UIAS 地址（测试可覆盖） |
| `CHECKIN_QFHY_BASE` | `https://qfhy.suse.edu.cn` | 652 地址（测试可覆盖） |
| `CHECKIN_OAA_BASE` | `http://127.0.0.1:8080` | suseoaa 后端地址，用于登录、刷新和校验 token |
| `CHECKIN_TIMEOUT` | `25s` | 单次 HTTP 请求超时（1s–2m），也用于 OAA 调用 |
| `CHECKIN_SMTP_HOST` | `smtp.qq.com` | 共用发信 SMTP 服务器 |
| `CHECKIN_SMTP_PORT` | `465` | SMTP 端口 |
| `CHECKIN_SMTP_USER` | 空 | 发信邮箱；与密码同时为空时禁用邮件通知 |
| `CHECKIN_SMTP_PASS` | 空 | SMTP 密码/授权码 |
| `CHECKIN_SMTP_FROM` | 同 `USER` | 发件人地址 |
| `CHECKIN_SMTP_TLS` | `true` | `true` 使用隐式 TLS（465）；`false` 使用 STARTTLS（如 587） |
| `GOCAPTCHA_CACHE_DIR` | `$DATA/gocaptcha` | ONNX Runtime 缓存目录 |

启动参数：

- `-addr 127.0.0.1:8099`：覆盖监听地址。
- `-captcha-check`：抓取一张真实验证码并识别后退出。

## 数据目录

```text
data/
├── state.json                 # 加密密码/Cookie、调度配置、运行记录
├── key                        # 32 字节 AES 密钥（0600）
├── gocaptcha/                 # GoCaptcha 释放的 ONNX Runtime
└── captchas/
    └── 2026-10-26/
        ├── 1-20261026-070000.000-1.jpg
        └── 1-20261026-070000.000-1.json
```

删除 `data/key` 会导致已有密码和会话无法解密，需要重新录入账号。OAA 的 access/refresh token 不写入 `data/`，SMTP 凭据只从环境变量读取。

## HTTP API

所有响应均为 JSON；修改类请求必须使用 `application/json`。除健康检查、登录、刷新和登出外，所有 `/api/*` 请求都必须携带 `Authorization: Bearer <access token>`。

| 方法 | 路径 | 鉴权 | 说明 |
|---|---|---|---|
| `POST` | `/api/auth/login` | 公开 | OAA 登录，JSON：`account`、`password`、`device`；返回 `token`、`refresh_token`、`user` |
| `POST` | `/api/auth/refresh` | 公开 | 用 `refresh_token` 换取新的 access token |
| `POST` | `/api/auth/logout` | 凭 `refresh_token` | 注销当前设备并清除 OAA refresh token |
| `GET` | `/api/auth/me` | Bearer | 当前 OAA 用户信息 |
| `POST` | `/api/wechat/login` | Bearer | 创建微信扫码登录，返回 `id`、二维码 Data URL、过期时间 |
| `GET` | `/api/wechat/login/{id}` | Bearer | 查询扫码状态：`waiting/scanned/success/expired/error` |
| `POST` | `/api/wechat/login/{id}/complete` | Bearer | 用扫码结果新增账号，或带 `accountId` 更新已有扫码账号 |
| `DELETE` | `/api/wechat/login/{id}` | Bearer | 取消扫码登录并释放服务端会话 |
| `GET` | `/api/state` | Bearer | 账号（脱敏）、校区点位数、调度、进度、最近 50 条运行记录、通知可用性 |
| `GET` | `/api/healthz` | 公开 | 健康检查 |
| `POST` | `/api/accounts` | Bearer | 新增**密码**打卡账号（`loginType=password`） |
| `PUT` | `/api/accounts/{id}` | Bearer | 修改打卡账号；`password` 留空表示不修改；扫码账号需带 `loginType=wechat` |
| `DELETE` | `/api/accounts/{id}` | Bearer | 删除账号，同时从定时目标中移除 |
| `PUT` | `/api/schedule` | Bearer | 保存定时配置；包含 `notifyEnabled`、`notifyEmail` |
| `POST` | `/api/run` | Bearer | 异步运行指定账号，返回 `202`；用 `/api/state` 轮询进度 |
| `GET` | `/api/samples/{date}/{file}` | Bearer | 查看留样图片（仅 `data/captchas/` 下的安全文件名） |

示例：

```bash
curl -s http://127.0.0.1:8099/api/healthz

# 登录 OAA（device 最长 20 个字符）
TOKEN=$(curl -s -X POST http://127.0.0.1:8099/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"account":"2024xxxx","password":"OAA 密码","device":"checkin-curl"}' \
  | jq -r '.token')

curl -s http://127.0.0.1:8099/api/state \
  -H "Authorization: Bearer $TOKEN"

curl -s -X POST http://127.0.0.1:8099/api/accounts \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"studentId":"2024xxxx","password":"UIAS 密码","name":"姓名","campus":"宜宾"}'

curl -s -X PUT http://127.0.0.1:8099/api/schedule \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"enabled":true,"timeOfDay":"07:00:00","accountIds":[1],"maxRetries":3,"retryIntervalMinutes":5,"notifyEnabled":true,"notifyEmail":"you@example.com"}'

curl -s -X POST http://127.0.0.1:8099/api/run \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"accountIds":[1]}'
```

## 前端结构

```text
web/
├── index.html                 # 页面骨架，只加载 /static 资源
├── embed.go                   # //go:embed index.html static
└── static/
    ├── css/
    │   ├── tokens.css         # 变量、reset、动画
    │   ├── base.css           # 按钮、表单、下拉、徽章、菜单
    │   ├── layout.css         # 页面骨架、栅格、卡片、登录页
    │   ├── components.css     # 状态与定时合并卡片、账号、定时列表、运行记录
    │   ├── dialogs.css        # 账号弹窗、扫码面板、定时弹窗、样本弹窗
    │   └── responsive.css     # 响应式断点
    └── js/
        ├── app.js             # 入口、轮询、模块编排
        ├── core/              # state / session / api / utils
        ├── services/          # 微信扫码 API
        ├── ui/                # toast、下拉菜单
        └── views/             # auth / status / accounts / schedule / runs
```

- 前端没有构建步骤，浏览器直接加载原生 ES modules。
- Go 使用 `//go:embed index.html static` 把整个 `web/static` 打进二进制；服务端在 `/static/` 下提供内嵌静态资源。
- 修改前端后重新 `go build` / `go run` 即可，不需要额外的前端 dev server。

## 测试与验收状态

验收日期：2026-10-27；环境：macOS ARM64、Go 1.27.3、GoCaptcha v0.1.0-alpha.8。

已实测：

- `go test ./...`：通过。覆盖 9 个测试包、83 个顶层测试（store、RSA、上游客户端、微信扫码、打卡编排、调度、HTTP API、位置目录、OAA 认证客户端、邮件通知）。
- `go vet ./...`：通过。
- `go test -race ./...`：通过。
- 位置目录：从源码提取 141 个点（宜宾 74、李白河 47、汇东 20）；逐点校验地址、经纬度与原始 `location` JSON 一致；服务层断言实际提交的点来自所选校区。
- 真实 UIAS 验证码抓取 + 识别：6 张人工核对样本 `6800 / 0570 / 6321 / 3399 / 9846 / 7536` 全部正确；`-captcha-check` 再测 `0430`（17 ms）。页面上的验证码诊断卡片已移除，识别确认统一用 `-captcha-check` 和登录失败留样。
- Mock UIAS/QFHY 全链路：登录 → rememberMe、任务列表 → 详情 → 随机位置提交、验证码重试、短信中断、错误密码、业务失败、异步运行、留样与日志。
- Mock OAA 认证链路：登录成功/密码错误、refresh、logout、Bearer 保护、无效 token、OAA 503、token 校验短缓存、定时通知开关的服务端校验。
- Mock qfhy 微信扫码链路：`getClientId` → 二维码 → SSE `1/2` 状态 → callback 设置 `_sop_session_` → SSO 换 `SESSION` → 用户信息 → 扫码账号保存/重扫/打卡；覆盖过期扫码会话、扫码取消和二维码过期。
- 邮件通知单元测试：失败账号筛选、HTML 转义、SMTP 配置校验、连接失败处理、调度器在重试耗尽后调用通知并回写“失败通知已发送”。
- 浏览器 UI：OAA 登录、扫码添加账号（mock qfhy 自动完成）、扫码账号重新扫码、扫码账号实际执行签到、账号操作菜单、用户下拉菜单、通知设置保存、响应式布局和表单下拉样式。
- 浏览器 UI（界面重构）：页面只有三张卡片——“运行状态与定时”（hero 显示下次执行时间、日期 · 每天 HH:MM · 距今 x、调度消息、上次结果、进度与“运行定时账号”，下面是打卡账号/最近结果/失败重试/失败通知统计和定时账号列表）、打卡账号、最近运行。定时设置弹窗打开时回填服务端配置，编辑后出现“未保存”，取消与右上角关闭都会清除脏状态，保存后卡片与统计行同步；`/api/captcha/preview` 返回 404，页面已无验证码诊断卡片。

未实测 / 边界：

- **没有使用真实 OAA 账号和真实 UIAS 密码账号完成登录与真实打卡提交**。OAA、qfhy 扫码和 UIAS 登录均用本地 mock 验收；真实的扫码需要你打开页面用微信扫描，真实打卡需要当天存在的待签到任务。
- **没有用真实微信完成扫码确认**。qfhy 的 `getClientId` / `getQrCodeUrl` 接口已实际连通验证，但扫码确认、callback、SSO 和真实 `_sop_session_` 会话由 mock qfhy 覆盖；如果学校调整扫码页面或接口，需要以真实扫码结果为准。
- **没有向真实 SMTP 服务器投递测试邮件**。通知逻辑、模板和失败路径有单元测试覆盖，实际 QQ 邮箱/企业邮箱的授权码、限频和投递结果需要你配置后自行验证。
- 无法验证学校端是否接受随机选出的具体点位；如果签到任务限定某个区域，随机点可能落在范围外，需要以真实结果为准。
- 短信二次验证不会自动处理；遇到时本次运行会失败并记录原因。
- 微信扫码登录依赖 qfhy 的二维码/SSE/SSO 链路；扫码会话 `_sop_session_` 过期后必须重新扫码，无法完全免人工。
- 当前没有 OAA 用户级数据隔离；如果部署为多人共用实例，需要额外设计归属和权限。
- 登录 token 存在浏览器 localStorage；高安全场景建议只在受信终端使用，或在反向代理层再加一层访问控制。
- 学校接口、OAA 接口或页面结构变更后可能需要调整解析逻辑。
- Windows/Linux ARM64 的上游 GoCaptcha 运行时限制同样适用于本项目。

## 常见问题

**`go get` 超时**

```bash
GOPROXY=https://proxy.golang.org,direct \
  go get github.com/YN1753/GoCaptcha@v0.1.0-alpha.8
# 或
GOPROXY=https://goproxy.cn,direct \
  go get github.com/YN1753/GoCaptcha@v0.1.0-alpha.8
```

**启动时报 GoCaptcha 初始化失败**

先确认系统和架构在支持范围内；检查 `data/gocaptcha` 是否有写权限；Windows 安装 VC++ 运行库；Linux 不要使用 Alpine/musl。

**页面提示 OAA 认证服务暂不可用**

确认 `CHECKIN_OAA_BASE` 指向正确的 suseoaa 后端、后端进程和数据库/Redis 正常；打卡服务本身不代理 OAA 的数据库。认证服务短暂不可用时，已经启动的定时任务仍会继续执行。

**登录提示密码错误或请求过于频繁**

这里校验的是 OAA 账号密码，不是 UIAS 打卡密码。OAA 登录接口按 IP 限制 10 次/分钟，连续失败时请稍后再试。

**登录过期后没有自动续期**

access token 过期时页面会自动调用 refresh token。若 refresh token 也已过期、被其他设备顶号或 OAA 不可用，会回到登录页；重新登录即可。

**收不到失败邮件**

按顺序检查：

1. “编辑定时”弹窗里是否勾选了“失败时邮件通知”，邮箱是否正确；
2. 服务启动日志是否显示“邮件失败通知已启用”；
3. `CHECKIN_SMTP_USER` / `CHECKIN_SMTP_PASS` 是否配置，QQ 邮箱是否使用 SMTP 授权码；
4. 邮件只在定时任务重试耗尽后仍有失败时发送，手动运行和全部成功不会发送；
5. 查看服务日志中的 `[scheduler] 发送失败通知失败` 和 `[notify]` 记录；
6. 465 端口应使用 `CHECKIN_SMTP_TLS=true`，587 端口使用 `CHECKIN_SMTP_TLS=false`。

**“失败时邮件通知”开关是灰色的**

服务端没有配置 SMTP 发信邮箱，或当前实例没有启用通知能力。配置 `CHECKIN_SMTP_USER` 和 `CHECKIN_SMTP_PASS` 后重启服务。

**抓取不到验证码**

确认能直接访问 UIAS；校外先连学校 VPN；检查代理、DNS 和系统时间。可以用 `go run ./cmd/server -captcha-check` 单测一次抓取与识别；运行时的识别结果和失败原因会记在 `data/captchas/` 留样的 `.json` 里。

**登录失败：验证码错误**

服务会自动换图重试，最多达到配置次数。连续失败时先降低频率，避免触发站点风控；用 `-captcha-check` 或留样 `.json` 中的 OCR 文本确认识别是否正常。

**登录失败但原因不明确**

可以设置 `CHECKIN_LOGIN_DEBUG_DIR=/path/to/debug` 后重新运行一次，程序会把 UIAS 返回的登录失败页面保存为 HTML，页面里通常包含真实错误（如“账号或密码错误”）。该文件可能包含学号，不包含密码，请只保存在本机。

注意：UIAS 正常登录页本身就带“短信登录”标签和 `smscode` 输入框。程序只在页面不是普通密码登录页时，才将 `smscode` 等标记判定为短信二次验证；普通页面上的登录失败会显示真实原因，不再误报“需短信验证”。

**检测到短信二次验证**

自动流程会停止。当前不支持自动填写短信码；请在手机端完成验证或改用其他方式打卡。

**定时没有执行**

- 确认定时开关已启用、至少选中一个账号、保存成功；
- 服务器在设定时间必须处于运行状态，错过后不会补跑；
- 查看“最近运行”和“运行状态”的提示；
- 如果账号被删除，定时目标会同步移除，需要重新添加并勾选。

**删除账号后密码/会话还能恢复吗**

不能。删除操作会从 `state.json` 中移除该账号记录；如需保留请先备份 `data/`。

**端口被占用**

```bash
go run ./cmd/server -addr 127.0.0.1:8100
```

## 合规声明

本项目用于个人学习和个人账号的自动化辅助，不是学校官方产品。自动打卡可能违反学校的考勤管理规定，使用前请自行确认授权与合规性；源码内置点位是校园坐标，如果你本人不在该校区，使用它可能构成伪造考勤，请不要这样使用。开发者不对账号风控、考勤结果或数据丢失承担责任。

## 第三方组件

- [GoCaptcha](https://github.com/YN1753/GoCaptcha)（MIT）：Go 版 ddddocr 推理封装、模型、ONNX Runtime 运行时。
- 模型与字符集来自 [ddddocr](https://github.com/sml2h3/ddddocr) 1.5.6；其训练数据授权链请以原项目说明为准。
- 打卡链路参考 [SUSE-OAA-APP](https://github.com/suse-edu-cn/SUSE-OAA-APP)。

本仓库自身尚未指定开源许可证；如需再分发，请先补充项目许可证并核对上述第三方许可。
