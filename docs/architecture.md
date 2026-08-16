# Mijia Archive 架构

## 目标和边界

应用从一个部署时配置的只读媒体库中管理多个 Mijia 录像文件夹，并在同一套响应式 Web UI 中提供登录、文件夹入口、用户管理、日期浏览、事件定位、seek、跨片段连续播放和倍速切换。

媒体目录是不可变输入。SQLite、转码缓存、临时文件和运行日志统一位于独立的 `STATE_DIR`。部署只允许把专用媒体库根目录只读挂载到容器，不修改媒体或宿主机网络配置。

## 组件

```text
只读媒体根 ──> Go scanner ──> SQLite
      │                         │
      └──── ID 映射媒体读取 <── 认证 Go HTTP API ──> React UI
                                  │
                                  └──> STATE_DIR/转码缓存
```

- 单个 Go 二进制提供 `scan` 和 `serve` 子命令。
- scanner 负责目录校验、命名解析、增量元数据读取、`ffprobe` 和 MOTION 聚合。
- SQLite 是唯一查询索引，不把客户端输入转换成文件路径。
- HTTP API 提供日期、时间线、事件、缩略图、Range 媒体和兼容转码接口。
- React + TypeScript + Vite 构建单一响应式应用，由 Go 服务托管生产构建产物。
- SCS 管理服务端 session，Argon2id 用于密码哈希；用户、session 和文件夹配置与索引共用受保护的 SQLite。

## 配置和运行目录

必要配置：

- `MEDIA_LIBRARY_ROOT`：宿主机专用录像库根目录，只读挂载到容器 `/media-library`。
- `INITIAL_FOLDER_PATH`、`INITIAL_FOLDER_NAME`：迁移现有单目录索引时使用的初始文件夹；路径必须在媒体库根内。
- `STATE_DIR`：可写状态目录，必须与媒体根物理分离。
- `MIJIA_TIMEZONE`：IANA 时区，第一版默认 `Asia/Shanghai`。
- `LISTEN_ADDR`：默认 `127.0.0.1:8080`，公网访问必须经认证反向代理。
- `FFPROBE_PATH`、`FFMPEG_PATH`：显式工具路径；缺失时扫描/转码给出清晰错误，不尝试安装。
- `TRANSCODE_MAX_BYTES`：兼容缓存上限，默认 20 GiB。
- `TRANSCODE_WORKERS`：默认 1。

管理员新增文件夹时，服务将服务器绝对路径映射为媒体库挂载内的相对位置，并拒绝越界、symlink 逃逸、非目录和缺少 `MIJIA_RECORD_VIDEO` 的目标。媒体库根本身只有修改部署配置并重建容器后才会改变。

## SQLite 模型

迁移由应用内嵌 SQL 管理，至少包含：

- `schema_migrations(version, applied_at)`。
- `users`：随机公开 ID、唯一用户名、Argon2id 哈希、`admin`/`user` 角色、启用状态和创建时间。
- `sessions`：由 SCS 管理的不透明 token、序列化 session 数据和过期时间。
- `folders`：随机公开 ID、名称、仅管理员可见的服务器路径、解析后的容器路径、扫描状态和时间。
- `scan_runs(id, started_at, finished_at, status, error_summary)`。
- `segments`：内部整数主键、文件夹外键、随机 128 位 `public_id`、文件夹内唯一相对 MP4 路径、相对 JPEG 路径、UTC 起止毫秒、时长、大小、mtime、视频/音频编码、尺寸、帧率、探测状态、可用状态、最后扫描 ID、缺失时间。
- `events`：内部主键、随机 128 位 `public_id`、片段外键、UTC 起止毫秒、`raw_record_count`、`has_unknown_flag`，并按片段/起始时间唯一。现有字段名为保持数据库兼容继续保留；展示层将其映射为画面变动（false）或有人移动（true）。
- `transcode_entries`：片段、配置版本、源指纹、缓存相对路径、状态、大小、最后访问时间和脱敏错误。

公开 ID 使用加密安全随机数生成，编码为不带路径含义的 URL-safe 字符串。媒体文件路径只保存为相对于所属录像文件夹的路径；文件夹的宿主机绝对路径仅保存在受保护的管理配置中，并且只向管理员 API 返回。

所有业务时间以 UTC 毫秒存储，日期聚合和文件名一致性检查使用配置时区。

## 只读增量扫描

1. 校验根目录，打开预期的 VIDEO 和 MOTION 子目录。
2. 只接受实测命名规则的普通文件，拒绝 symlink、设备、socket 和未知路径形态。
3. 解析文件名 Unix 秒，并检查小时目录和分秒字段在配置时区中是否一致。
4. 以相对路径、大小和 mtime 对比现有记录。未变化片段复用探测结果；新增或变化片段才运行 `ffprobe`。
5. `ffprobe` 通过可替换接口调用，设置超时并只读取 JSON 输出。失败片段保存脱敏状态并从可播放时间线排除。
6. 解析每个 `motion_record_msg` 的固定布局，验证头、长度和记录字段；按 Unix 秒去重后关联片段。
7. 在事务中 upsert 本次结果。只有完整扫描成功后，才把未见到的旧片段软标记为缺失。

扫描是幂等的：相同输入重复扫描不得改变公开 ID、增加重复事件或重新探测未变化媒体。

## API

所有端点使用 `/api/v1`，JSON 错误包含稳定错误码和安全描述，不包含命令行或底层数据库错误。除登录、静态资源和健康检查外，所有页面、API 与媒体请求都要求有效 session。

- `POST /auth/login`、`GET /auth/me`、`POST /auth/logout` 提供登录生命周期；`POST /auth/password` 在校验旧密码后更新当前用户密码并使现有 session 失效。
- `GET/POST /users` 由管理员列出和新增系统用户；新增用户必须提交两次一致的密码。
- `GET/POST/PUT /folders` 提供文件夹入口和管理员配置；合法保存后异步启动只读扫描。

- `GET /days?folder=<opaque>&from=YYYY-MM-DD&to=YYYY-MM-DD` 返回指定文件夹每日覆盖秒数、片段数和事件数。
- `GET /timeline?folder=<opaque>&start=<RFC3339>&end=<RFC3339>` 返回指定文件夹窗口内片段与事件。
- `GET /events?folder=<opaque>&date=YYYY-MM-DD&cursor=<opaque>&limit=<n>` 返回指定文件夹游标分页事件。
- `GET /media/{publicID}/source` 通过数据库映射并重新验证文件边界，使用 `http.ServeContent` 支持 HEAD 和 Range。
- `GET /media/{publicID}/thumbnail` 只读取该片段已索引的 JPEG。
- `POST /media/{publicID}/compat` 幂等创建或查询转码任务，返回 `ready`、`pending` 或 `failed` 及重试建议。
- `GET /media/{publicID}/compat` 仅在缓存完成时支持 HEAD 和 Range；未完成时返回稳定的冲突状态，不返回部分文件。

日期和时间参数有严格格式、跨度和分页上限。媒体端点不接受 `path`、URL 或文件名参数。

## 播放和转码

前端总是先实际尝试源 MP4，不把 `video.canPlayType` 的结果作为硬性门槛，因为 Windows 浏览器可能在系统和显卡具备 HEVC 解码能力时仍给出不确定结果。部分浏览器会在视频轨无法解码时继续播放音频且不抛出媒体错误，因此源播放使用 `requestVideoFrameCallback` 做短时首帧看门狗：播放时间前进但没有任何视频帧时，在该会话中改用兼容版本，避免黑屏和每个片段重复失败。

兼容转码配置固定版本化：

- `libx264`、`yuv420p`、最长边不超过 1920 且不放大。
- AAC 单声道，64 kbit/s。
- 约 3 秒 GOP、`faststart` MP4、保留源时间长度。
- 默认一个工作进程，相同源指纹和配置只运行一个任务。

输出先写 `STATE_DIR` 内随机临时文件，成功校验后原子改名；失败时删除临时文件并记录脱敏错误。源指纹由相对路径、大小、mtime 和配置版本组成。缓存超过上限时只按 LRU 删除兼容副本，永不接触媒体根。

## 响应式 Web UI

同一状态模型驱动桌面、平板和手机布局，不使用 User-Agent 分叉。登录后默认进入文件夹管理页；普通用户只能打开可用文件夹，管理员还可维护文件夹和新增用户：

- 受控 URL 参数保存 `date`、`time` 和 `event`，不包含路径。
- 日期选择器按月查询 `/days`，只有包含可用录像片段的日期可选；无录像日期显示为禁用状态，不能触发空时间线请求。
- 桌面同时显示播放器、时间线和事件面板；窄屏将事件列表排列在播放器和时间线下方，点击事件后平滑返回播放器并开始对应位置的播放。
- 时间线按可见窗口加载，清楚区分录像、空档、画面变动和有人移动。定位滑块直接叠加在彩色事件轨道上；轻触按触点比例立即 seek，拖动中仅更新预览并在释放后 seek。
- seek 到空档时优先跳到未来最近片段；没有未来片段时跳到前一片段末尾，并显示反馈。
- `ended` 时切到下一片段并保留倍速；仅在正在播放时预热下一片段的兼容任务。
- 支持 1x/2x/4x、上一/下一事件、键盘播放暂停和快进后退。
- 播放器使用 `playsinline`，不强制有声自动播放；旋转和 resize 后保留日期、位置、倍速和事件选择。
- 使用动态视口、安全区、44×44 CSS px 触控目标、语义控件、可见焦点和减少动画设置。

## 失败和运维

- SQLite 迁移失败、媒体根不可读或配置边界不安全时拒绝启动。
- 单个损坏文件、缩略图缺失或 MOTION 异常只影响对应记录并进入可观察状态。
- 日志使用公开 ID 和安全错误码，不输出媒体绝对路径或完整 ffmpeg 命令。
- 默认监听回环地址并由应用自身实施全路由认证；公网部署还必须通过受信任的 TLS 反向代理或隧道建立传输和访问边界。
- 真机 iPhone Safari 和 Android Chrome 验证属于发布前人工验收，模拟器结果不能替代真机结论。
