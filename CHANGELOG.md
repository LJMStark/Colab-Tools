# Changelog

All notable changes to **Colab Tools** are documented in this file.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and [Semantic Versioning](https://semver.org/).

## [0.3.22] - 2026-11-28

### Improved
- **极简状态与界面去黑话**：
  - 彻底移除「实时负载」卡片中令人困惑的「外部占用中」状态与重复徽章，收敛为清晰的「未开机 / 开机实时负载」两态；
  - 消除停机后 Google 扣费速率短暂归零延迟导致的误报黄灯与底部表格闪现；
  - 将「外部孤儿会话 [?]」「附加会话」「受管会话」「CLI」等底层技术词统一改写为普通用户一眼看懂的直白中文，并为网页端运行中的机器直接提供「打开网页」入口。

## [0.3.21] - 2026-11-28

### Fixed
- **修复 `colab drivemount` 控制终端 `/dev/tty` 阻塞导致 Drive 挂载超时**：`google-colab-cli` 在 Unix/WSL 下会优先读取 `/dev/tty` 而忽略 `stdinPipe` 写入的回车；新增跨平台 `DetachControllingTTY`（Unix `Setsid: true`、WSL `setsid -w`、Windows 独立进程组与 3s `taskkill` 超时）+ `PYTHONUNBUFFERED=1`，授权后挂载超时放宽至 90 秒，并修复用户跳过挂载时尾部输出排空竞态死锁与空 URL 误触发问题。
- **Google Drive 授权流程全面前置、自动置顶与防双开浏览器**：
  - 点击开机、部署或重挂 Drive 且开启 Drive 持久化时，第一时间（用户仍在屏幕前）即唤起主窗口并弹出清晰的 3 步准备态提醒弹窗（标明目标账号与邮箱），允许用户在第 1 秒即选择「先不挂，纯云端跑」并持久化生效至本次部署；
  - 获取到官方授权链接后原地无缝升级为 3 步就绪态弹窗，通过 `BROWSER=true` 抑制 `google-colab-cli` 重复弹窗，统一由本软件精确拉起一次浏览器并置顶窗口，且按 token 隔离旧挂载清理并避让高优先级停机/退出确认弹窗；
  - 同步升级 LLM / 自定义启动脚本路径走统一的交互式 Drive 授权，并在首次 Google 账号 OAuth 绑定界面补齐 3 步操作指引。

## [0.3.20] - 2026-11-28

### Security & Safety
- **禁止静默自动拉起无认证 `cloudflared` 公网隧道**：当账号未显式开启 `EnableCloudflared` 时，即使本机 SSH 隧道两次失败或处于免费版受限分支，不再自动暴露无密码公网链接，改为弹窗由用户显式选择重试私连、打开 Colab 官方网页或手动开启临时公网通道；同时在主界面与后台浮层提供「关公网通道」一键关闭按钮。
- **多账号操作范围收紧与高危停机/退出二次确认**：
  - `412` 恢复横幅「停掉本号旧机器」改调 `StopAccountManagedSessions`，仅停止当前账号旧会话，不再误杀其他账号；
  - 移除运行中或排队中账号时强制弹窗选择「先停机再移除」或「仅移除本地配置」，防止失去监控导致计费失控；
  - 首页会话表、菜单栏 Popover、托盘右键的停机与全部关机止损统一纳入二次确认弹窗；
  - 退出 App 时若仍有活跃付费 GPU，唤起主窗口弹出「先全部关机再退出 / 保持云端运行，仅退出 / 留在后台继续监控」选项。
- **停机单飞锁与跨账号 SSH 残留隔离**：`StopAccountSession` 增加按账号+会话单飞防重入保护；`startSSHTunnelInternal` 增加按账号防并发覆盖；`cleanupStaleColabSSH` 结合隔离 `--config` 路径与 `--auth=oauth2` 精确匹配目标账号，防止多账号同名会话互相误杀。

### Fixed
- **Drive 真实挂载态追踪、临时盘护片延缓关机与真实目录直达**：
  - 远端遥测探针回传 `DRIVE_MOUNTED`、`EFFECTIVE_OUT_DIR` 与 `CLOUDFLARED_ACTIVE`，未挂载回退 `/content/output` 时主页如实标红提示「临时盘(关机清)」并提供「重挂 Drive」按钮；
  - 开启 Drive 持久化但回退临时盘且检测到新成片视频时，看门狗到期暂缓静默自动关机并弹出强提醒，防止静默清空成片；
  - `FlushWorkspaceToDrive` 不再放过 `FLUSH_SKIP=1` 与 `FLUSH_NO_DRIVE=1`，超时放宽至 25 秒；
  - 「Drive 成片 / Drive 目录」按账号自定义 `DriveOutputDir` 与绑定邮箱 `authuser` 参数精准跳转，避免硬编码 `ColabTools` 与多 Gmail 串号。
- **计费状态防误判、活跃会话账本续算与配置/历史版本化防覆写**：
  - `ParseSessionsStrict` 区分合法空会话与 CLI 输出异常/截断，解析失败时保留原状态防止误判离线；
  - 新增 `~/.config/colab-tools/active_sessions.json` 活跃账本（每 60 秒异步及退出前原子落盘），重启后同 `Endpoint` 自动续算时长与 CU、已离线会话自动补记账单；
  - `AppSettings` 与 `HistoryStore` 新增 `schemaVersion: 1`；历史文件损坏时自动备份 `.corrupt.<ts>` 并启用只读保护拒绝覆写，且落盘错误不再静默吞掉。
- **工作流同步回收站与加锁防竞态**：双模板脚本中同步锁获取失败即中止同步不再无锁强跑；双向同步传播删除改移入同侧 `.trash/<timestamp>/` 隔离回收站，防止误删不可恢复。
- **UI 溢出、Popover 向导与流式读取修复**：
  - 修复菜单栏 Popover 空状态「打开开箱向导」被 `SetSection` 重置而无法打开引导页的缺陷；为 Popover 内容区增加滚动容器并修正多账号总览排队态误显「启」；
  - 将「任务方案与显卡选项」与「云端后台与日志」浮层最大高度收敛至 `596px`，适配 `980×660` 最小窗口不裁切；
  - `runner.go` 流式读取以 `bufio.Reader` 替代 512KiB `bufio.Scanner`，消除超长单行输出阻塞管道风险；
  - 统一引导页、账号页、设置页与 README 的 macOS 优先及页面导航口径。

## [0.3.19] - 2026-11-27

### Fixed
- **真正三向基线双向工作流同步与零静默覆盖冲突保护**:
  - 启动同步、15 秒后台轮询与关机 Flush 统一改用基于内容 `sha256` 与基线状态（`/tmp/colab-tools-wf-state.json`）的三向双向同步引擎：运行期 Drive 新增/编辑/删除与 ComfyUI 内新增/编辑/删除双向传播，消除同秒同大小修改被跳过的问题，双端并发修改或无基线重部署冲突时保留较新版本并将较旧版本另存为 `.conflict-<ts>.json` 副本，绝不静默丢稿。
- **保留所有既有与自定义 Drive 模型子目录**:
  - `extra_model_paths.yaml` 改为多行别名路径（同时兼容 `unet/` 与 `diffusion_models/`、`clip/` 与 `text_encoders/`），并动态纳入 `ColabTools/models/` 下所有既有与自定义模型目录，避免老用户升级后出现模型丢失。
- **自定义节点快照覆盖 Manager Registry (CNR) 节点**:
  - 节点快照同时采集 Git 仓库与 ComfyUI-Manager Registry（`pyproject.toml` + `cm-cli save-snapshot`）节点，开启开关后换机可一并恢复非 Git 安装节点。
- **保护 HuggingFace 共享缓存与安全加固**:
  - `h3-video.sh` 下载 HF 权重后同盘改用硬链接（`os.link`），保持零双倍占盘同时不移动或删除共享缓存 blob；
  - 停机前工作流最终 Flush 失败时仍立即止损停机，但通过 Toast 与桌面通知明确提醒用户核对 Drive；
  - 隔离 ADC 账号打开云端终端时先检查是否可复用当前控制套接字，无法复用且读取凭据/写临时文件失败时立即拒绝启动并提示，绝不无凭据回退。

## [0.3.18] - 2026-11-27

### Added
- **ComfyUI 工作流双向安全同步与示例自动导入**:
  - 启动时将 Google Drive `MyDrive/ColabTools/workflows/` 中的工作流同步至本地高速盘 `/content/ComfyUI/user/default/workflows/`，运行期间每 15 秒轻量检测变更并原子回写 Drive，主动关机前还会触发一次最终 Flush 保护（绝不将 `--user-directory` 指向 FUSE，彻底规避 SQLite `database is locked` 与软链断链）；
  - 自动收集已安装节点（如 `ComfyUI-HyperFlow-Flash`）顶层示例工作流至本地 `workflows/examples/` 只读参考，不污染用户 Drive 目录。
- **官方 ComfyUI-Manager v4 + 画布内服务端一键补模型**:
  - 部署脚本默认按官方 v4 方式启用 `--enable-manager` 并配置 `security_level = normal` 与 `network_mode = personal_cloud`，并预装 `slahiri/ComfyUI-Workflow-Models-Downloader`，支持在 ComfyUI 画布内直接扫描任意工作流缺失模型并在云端高速后台下载（绕开 ComfyUI V2 弹窗下到本机浏览器的坑），同时自带本地模型磁盘浏览与清理。
- **Drive 标准模型仓零拷贝直挂与可选自定义节点快照恢复**:
  - 自动初始化 `MyDrive/ColabTools/models/{checkpoints,diffusion_models,text_encoders,vae,loras,clip_vision,controlnet,upscale_models,embeddings}`，通过 `extra_model_paths.yaml` 零拷贝直挂 ComfyUI（移除通用 ComfyUI 全量 `rsync`，防止爆盘）；
  - 在「设置 → 高级选项」新增「Drive 模型目录」输入框与默认关闭的「换机自动恢复自定义节点快照」开关，开启后关机前自动记录第三方节点并在下次开机宽松恢复。
- **主页「后台」直达入口与会话隔离 SSH 复用终端**:
  - 将主页 Card 4 就绪态按钮「日志」升级为「后台」，在「云端后台与日志」浮层顶部露出 **`Colab 网页`**、**`>_ 云端终端`**（通过会话隔离 OpenSSH `ControlMaster` 复用现有隧道，不断 ComfyUI 且零 HTTP 429）与 **`Drive 目录`**，附带目录速查及磁盘余量/同步状态诊断。

### Fixed
- **消除 HuggingFace 模型下载双倍占盘**:
  - `scripts/templates/h3-video.sh` 下载大模型后改为从 HF 缓存原子移入目标 `models/`（跨文件系统则校验复制后清理缓存），避免 20GB+ 权重在本地临时盘占用双倍空间。

## [0.3.17] - 2026-11-27

### Fixed
- **部署准备态隔离与停机即时回收交互弹窗**:
  - 新增 `DeployPreLaunch` 部署准备态标记，在推送脚本前的云端清理与 Google Drive 挂载阶段屏蔽上一轮旧 `ServerHealthy` 遥测冲刷，避免旧健康状态误导就绪判定；
  - 主动停机或会话切换时即时取消等待中的 Google Drive 挂载引导弹窗与部署观察协程，避免停卡后残留过期弹窗。
- **H3 模型权重 `.part` 原子落盘与字节大小校验**:
  - `scripts/templates/h3-video.sh` 在 Drive → NVMe 拷贝、HuggingFace 下载及底座回存 Drive 三个环节均改为先写入临时 `.part.<pid>` 文件、核对正数文件大小一致后再原子替换目标文件，防止断流留下残缺权重文件；
  - 自定义 HuggingFace 文件列表改用纯参数展开去除首尾空白，并按 `text_encoders/`、`vae/` 前缀自动分流至对应模型子目录。
- **排队取消与分配成功并发保护**:
  - 收紧排队取消路径与 `colab new` 分配返回之间的并发锁保护与会话核验顺序，防止取消操作与刚分配成功的云端实例产生状态覆盖。
- **UI 渲染 `ViewStateSnapshot` 线程安全快照消除竞态**:
  - 为 `Service` 新增 `ViewState()` 只读快照（`ViewStateSnapshot`），统一主窗口、部署面板、引导向导、菜单栏 Popover 及多标签视图的并发状态读取，消除 UI 协程与后台轮询协程之间的数据竞态。
- **远端进程组核对 `cmdline` 与本地子进程 `Wait` 回收**:
  - 远端停止旧进程组前先核对 `/proc/<pid>/cmdline` 确认归属目标服务，避免 PID 复用误杀无关进程；本地拉起外部终端命令（macOS / Windows）后异步调用 `cmd.Wait()` 回收子进程描述符，防止产生僵尸进程。
- **历史账单 CSV 导出公式注入防护**:
  - `HistoryStore.ExportCSV` 新增 `sanitizeCSVText` 转义处理，对以 `=`、`+`、`-`、`@` 或控制字符开头的文本字段自动前置单引号，防止电子表格软件打开账单时触发公式执行。

## [0.3.16] - 2026-11-26

### Changed
- **主页从第一性原理重构为「顶部电源总控条 + 3×2 精简 6 卡」，消除所有重复按钮与死入口**:
  - **顶部电源大标志成为唯一主电源开关**：关机点此直接开机、排队点此取消、运行态点此弹出防误触确认窗（`确认关机止损 / 继续运行`），彻底移除主页卡 1 与横幅中的重复开关机和取消按钮；
  - **3×2 精简 6 卡职责严格正交**：
    1. `CU 余额与速率`（纯财务与账单跳转，无电源键）；
    2. `实时负载`（运行态合并 GPU 折线 + 显存分段条；关机/排队态仅显示灰静「休眠中」，不再画假曲线或 0% 进度条）；
    3. `当前任务`（全天候常驻 2×2 直白 4 选 1：`H3 Flash 极速版` / `H3 原版 (31B)` / `轻量 GGUF` / `大模型代理`）；
    4. `视频画布与产出`（合并原运行状态与快捷操作，保留高频 `开 ComfyUI` / `Drive 成片` / `日志`，移除重复「更多玩法」）；
    5. `空闲自动关机`（全站唯一空闲停卡控制点，预设统一为 `10m / 15m / 40m`）；
    6. `挑卡与守候`（全主页唯一策略与进阶选项浮层入口）；
  - **单账号按需隐藏冗余底部表**：单账号且无外部孤儿会话 `[?]` 或离线隐性扣费时自动隐藏底部账号表，一屏清爽收尽；多账号或检测到异常/孤儿会话时自动展开保底止损。
- **全自动托管技术选项与子页面深度减负**:
  - 彻底移除 7 个无需小白手动勾选的技术开关与重复控件（`EnableCloudflared`、`AutoOpenComfyUI`、`CacheBaseToDrive`、`H3EnableSolAttn`、`EnablePoliteQueue`、`EnableHardwareIdle`、浮层重复「跑完自动关机」及「去 Drive 看成片」），由后台按最优实践全自动托管，且保留旧 `config.json` 兼容；
  - 移除主页与菜单栏 Popover 中的「SSH 隧道 / 本机通道」与 Popover「浏览器」按钮，点「开 ComfyUI / 打开 WebUI」自动在后台打通加密通道；
  - 「抢不到就持续守候」砍掉 `4h / 8h / 12h` 三档选择，改为单一开关默认最长 8 小时封顶；
  - 菜单栏 Popover 去除顶部重复导航、待机态折叠 0% 空负载卡并仅在运行且开启空闲守护时显示「续命」；账号页收拢「导入终端已登账号」至绑定展开面板内。

## [0.3.15] - 2026-11-26

### Added
- **顶部三栏「电源与实时秒表总控条」（1 秒一眼分清开关机）**:
  - **正中醒目可点击电源标志 + 内置秒表**：关机时灰静显示 **`已关机 · 点击开机`** 与 `00:00:00`，点此标志直接尝试开机；抢卡/守候时琥珀显示等待秒表；开机后翡翠绿高亮 **`已开机`** 并每秒实时跳动 **`HH:MM:SS`** 开机时长秒表；
  - **左侧显示抢到的云端机器与内存信息**：实时采集云端 `/proc/meminfo`，同步展示显卡型号、形态（`High-RAM` / `Standard RAM`）、显存占用与系统内存容量；
  - **右侧显示可用时间**：开机时展示距离单次 24h 上限的本轮剩余可用时间、余额预计可跑时长与空闲停卡倒计时；关机时展示按余额估算的预计可用时间及 `0.00 CU/h` 未扣点状态。
- **Google 服务端回收停机主动提醒**：
  - 检测到运行中的机器被 Google 后台意外回收或达到单次上限停机（非软件主动关机）时，立刻发出带声弹窗通知，并在主页顶部保留可关闭提示条（带「重新开机 / 查看记录」一键入口）。

### Fixed
- **彻底消除关机态「伪在线」视觉误导**：顶栏右上角离线时明确显示 **`已关机`**（不再只显示版本号）；卡 1 由含糊的「已待机」改为 **`已关机 · 未扣点`**；卡 3 由「待机就绪」改为 **`未开机`**；卡 4 撤除离线 85% 假紫色进度条并改为 **`待开机`**；空闲关机关掉时明确提示 Google 仍会回收久闲机器。
- 修复 1 秒定时器在运行态仅累加时长却未触发界面重绘的问题，确保秒表每秒流畅走动。

## [0.3.14] - 2026-11-26

### Fixed
- **彻底修复 ComfyUI 本机加密通道打不开 / 卡死占坑（4 秒直通 + 自动兜底）**:
  - 修复 `startSSHTunnelProcess` 缺失 `root@` 与 `BatchMode=yes` 导致 OpenSSH 默认拿本机 Mac 用户名登录云端并卡死在密码提示、且僵尸连接占据 Google 唯一 SSH 名额（`Already-active SSH session HTTP 429`）的根本缺陷；
  - 建隧道前自动检测本机 SSH 密钥（若小白电脑无密钥自动静默生成 `ed25519` 密钥对，绝不覆盖已有密钥），连接超时放宽至 22 秒并实测 HTTP `/queue` 穿透畅通后再弹出浏览器；
  - 按钮进入「连接中…」防连点，遇旧隧道残留自动清理同会话残留进程并重连；若本机 SSH 隧道二次仍不通，自动在云端拉起 `cloudflared` 安全临时链接直接替用户打开 ComfyUI。

### Added
- **小白直达操作弹窗（需要用户操作直接弹窗，绝不甩晦涩报错）**:
  - Google Drive 挂载需要浏览器点击「允许」时，自动弹出居中操作向导并打开 Google 授权页，点完允许按「我已点完允许，继续」自动完成挂载（或一键跳过纯云端跑）；
  - 仅在所有通道全失败时弹出大白话修复弹窗（一键重连 / 重启画布 / 打开 Colab 网页）；点「开 ComfyUI」若机器未开机或裸机未装好，直接替用户一键拉起开机或自动部署。
- **低频持续守候模式（默认 8 小时封顶 · 8–12 分钟/轮防风控）**:
  - 挑卡策略与排队横幅新增 **「抢不到就持续守候」** 开关，支持 `4h / 8h（推荐）/ 12h` 封顶；前 3 轮沿用 `3m / 6m / 9m` 探路，第 4 轮起平稳保持 **8–12 分钟/轮** 随机低频探测；
  - 内置四道硬安全阀：遇 `429` / `abusive` 立刻熔断停手、遇 `401/403`/多机 `412` 立刻停手、休眠唤醒不补发积压、开启持续守候自动补开「空闲自动关机」防半夜抢到无人看管烧点。

## [0.3.13] - 2026-11-26

### Added
- **全新独立「记录」页（顶栏 4-Tab：`主页 · 记录 · 账号 · 设置`）与本地持久化账本**:
  - 顶部 `1×4` Bento 事实汇总卡（随 `近 7 天 / 近 30 天 / 全部` 与账号筛选联动）：展示**累计消耗 CU**（标明实测/估算口径）、**累计开机时长与主力显卡**、**抢卡成功率与均耗时**、以及纯事实口径的**看门狗止损拦截次数**；
  - 统一时间线支持 `全部 | 算力账单 | 抢卡记录` 三态分段过滤，记录每次抢卡顺位/轮次/结果与每次会话起止时间、运行时长、消耗 CU 及关机原因（`空闲自动关机` / `手动关机` / `24h 上限` / `外部或异常断开`）；
  - 数据持久存放在 `~/.config/colab-tools/history.json`（自动按 90 天 / 1,000 条滚动裁剪防膨胀），支持一键导出 UTF-8 BOM **CSV** 账单；主页底部表头与菜单栏 Popover 均新增快捷跳转入口。
- **分档安全保底与抢不到一键兜底**:
  - **同模型兼容任务默认开启第 3 轮安全保底**：`H3 Flash`（Pro+/Ultra）、`通用 ComfyUI`、`大模型代理` 与 `纯开机` 在前 2 轮主攻大卡未果后，第 3 轮自动纳入安全保底显卡（`L4` / `T4`）；`L4` 运行 `H3 Flash` 自动为 ComfyUI 追加 `--lowvram`；
  - **`H3 原版 (31B)` 跨模型降级可控防误伤**：默认死磕大卡不擅自换模型；在「挑卡策略」浮层提供可选开关「大卡 2 轮无货时，允许保底开 L4 并自动切轻量 GGUF」（仅当次部署临时生效，不覆盖原配置）；若 5 轮均缺货，主屏失败横幅直接提供 **「改用 L4 轻量版」** 与 **「预约 07:00 抢卡」** 一键操作。

### Fixed
- **修复 3 处导致抢卡提前放弃的隐性损耗与 2 处安全/统计缺陷**:
  - 修复 `412` 预检因查询网络抖动直接中止整条 5 轮队列的问题，改为跳过单卡继续尝试下一张；
  - 修复单卡 50s `TIMEOUT` 一票否决整轮的问题，改为先回查服务端核实是否已实际开机（防重复计费），确认未开则继续试下一张卡；
  - 新增 `--high-mem` 遇 `503` 缺货时同卡等待 2 秒后补试标准内存（Standard RAM）分配；
  - 修复 Pro/PAYG 账号选 `H3 原版 (31B)` 或 `H3 Flash` 时越界分配 L4/T4 导致爆显存的问题；
  - 修复会话离线或切换新 endpoint 时未清零 `SessionUptimeSec` 与 `IdleSeconds` 导致时长串用的问题。

## [0.3.12] - 2026-11-26

### Fixed
- **彻底消灭裸机假「启动中」状态与健康误判（Truthful Deploy State Machine）**:
  - 修复机器在线但云端尚未安装 ComfyUI 时，卡 4 误显示「启动中」导致用户干等烧点的问题；现在精准区分 **未部署（裸机）/ 部署进度 XX% / 部署失败 / 已就绪 :8188 / 渲染排队** 5 种真实状态，并同步更新卡 1 与顶部菜单栏弹窗状态词。
  - 修复遥测解析只要存在 `/tmp/colab-tools-status.json` 就一律误将 `ServerHealthy` 标为 `true` 的缺陷（即使部署已中断）；改为严格依据端口探测与 ComfyUI `/queue` 判定就绪。
  - 修复 `launchVideoDeployFlow` 在挂载 Google Drive 期间未立即进入进度态、以及推送脚本失败或云端部署中断被静默吞错的问题；将 Drive 挂载超时收敛至 35 秒并给出中文提示，部署中断自动保留错误原因并发送带声弹窗提醒。
  - 修复点击「开 ComfyUI」在未部署或部署中空等 8 秒隧道并弹出迷惑提示的问题，改为直接提示当前部署进度或引导点「立即部署」。

### Added
- **主屏一键「立即部署」与「云端部署日志」浮层**:
  - 机器在线未就绪时，卡 8「视频快捷操作」自动切换为高亮的 **「立即部署 / 重新部署」** 与 **「查看日志 / 刷新进度」** 按钮；点「查看日志」可在居中深色终端浮层内直接读取云端 `/tmp/colab-tools-deploy.log`、`/tmp/comfyui.log` 与 `:8188` 端口监听状态。
- **小显存显卡模型不匹配预警与一键切轻量版**:
  - 检测到运行中显卡为 24GB 及以下（如 L4 / T4）但选中了 `H3 原版 (31B)` 时，主屏自动提示显存不足并提供一键 **「改用轻量 GGUF」** 按钮。

## [0.3.11] - 2026-11-26

### Changed
- **深色暖石墨 4×2 Bento 统一仪表盘美学重构（Dark Warm-Graphite 4×2 Bento Unified Console）**:
  - 待机与运行态实现 **4×2 Bento 网格完全同构**与零空洞占位（主窗口默认深色主题 `dark`，兼容浅色模式切换）；
  - 升级双层金属高光 `PrecisionCard`、发光微 LED 指示灯、全套矢量 SVG 工具栏图标，并将任务方案、显卡策略与定时预约等参数面板收纳至居中 `ui.Overlay` 悬浮卡片中。
- **客观统一的 GPU 自动降级顺位（Objective GPU Auto-Fallback Priority）**:
  - 依据客观硬件规格与点数性价比将 **G4（96GB GDDR7 Blackwell）** 置为默认首选：
  - **H3 Flash 极速版 / 大模型代理 / 通用 ComfyUI / 纯开机**：统一按 **`G4 → A100 → L4`** 顺位自动降级；
  - **H3 原版（31B）**：严格仅在 **`G4 → A100 → H100`** 大显存卡间降级，不再盲目降级至 24GB L4；
  - **轻量 GGUF**：省点优先按 **`L4 → G4 → A100`** 顺位自动尝试。

### Added
- **免盯守抢卡提醒闭环（Hands-Free Queue & Notification Loop）**:
  - **顶部菜单栏实时战报**：实时显示 `抢卡 1/5` / `部署中` / `1 GPU` / `未抢到`，排队横幅同步提示可随时关掉或切走窗口去忙别的；
  - **全节点带声弹窗必达通知**：抢卡成功、ComfyUI 装好就绪、排队耗尽未抢到以及异常/限流停下均会触发带提示音弹窗通知，配合 macOS 原生通知与 Dock 图标弹跳提醒兜底；
  - **设置页「测试通知」按钮**：可在设置页随时一键测试弹窗与响铃提醒是否正常生效。

## [0.3.10] - 2026-11-26

### Changed
- **First-Principles Beginner UX Simplification (6+4 Pages → 3 Clean Tabs)**:
  - Streamlined top navigation from 6 tabs (`状态 / 部署 / 抢卡 / 接入 / 账号 / 设置` + 4 nested settings sub-tabs) down to **3 clear tabs (`主页` · `账号` · `设置`)** with zero nested settings sub-tabs.
  - Replaced the empty dashed offline dashboard with a **zero-config Home Launchpad**: defaults to **H3 Flash 极速版 + Smart GPU picking (`A100 → G4 → L4`) + Google Drive sync + 10m post-queue auto-shutdown**, letting beginners start in **1 click** without hunting through settings.
  - Consolidated GPU picking into **3 unified modes (`智能挑卡` / `省点模式` / `高级自定义`)** inside a collapsible drawer, moved LLM coding-tool integrations contextually under the **大模型代理** preset, unified auto-shutdown into a single source of truth, and removed the conflicting legacy 6-workload selector from Advanced Settings.
  - Added one-click **「恢复小白推荐默认」** and direct subscription tier selection in Accounts.

### Fixed
- **Eliminated False 15-Minute Rate-Limit Locks & Hidden Provision Errors**:
  - Updated `ClassifyProvisionFailure` to match real `google-colab-cli 0.8.x` outputs (`Backend rejected accelerator '...'` → single-card 400 prune & auto-skip; `Allocation refused (precondition failed)` → active-session check that auto-skips to the next card when 0 sessions are active or surfaces a one-click **「停掉旧机器」** button when sessions exist; `502/503/504/ReadTimeout` → retryable shortage; `401/403` and command timeouts → clear actionable retry without 15-minute lockout).
  - Restricted 15-minute cooldown strictly to genuine `429` rate limits / abuse warnings, displayed the actual plain-Chinese reason (with expandable raw CLI snippet), and added a manual **「解除等待」** escape hatch.

## [0.3.9] - 2026-11-26

### Changed
- **Redesigned Light-Bezel App Icon (`assets/AppIcon.png`)**: Replaced the legacy dark robot icon with a refined Dieter Rams–inspired anodized light-silver metallic dial icon featuring an interlocking amber-orange & graphite infinity ring with an integrated minimal play triangle — honoring Google Colab's infinity motif while reflecting one-click video model deployment and precision compute control at every icon size (`1024 × 1024`, macOS HIG padding & crisp transparent corners).
- **Updated Official Showcase Previews**: Re-exported all desktop console and onboarding screenshots with the new app icon.

## [0.3.8] - 2026-11-26

### Added
- **Zero-Hands Built-in H3 Video Model Deployment (`部署` Tab)**:
  - Embedded [`scripts/templates/h3-video.sh`](scripts/templates/h3-video.sh) and [`scripts/templates/comfyui-video.sh`](scripts/templates/comfyui-video.sh) directly into the binary (`scripts/templates/embed.go`), uploaded via Base64 over stdin on start so users never need to edit or upload scripts manually.
  - Default two-card picker for **H3 Original (31B)** and **H3 Flash (13.7B fast)**, plus expandable options for **Flash Lightweight GGUF (24GB L4 friendly)**, **custom HuggingFace repos/files** (strictly validated against injection), or **Drive-only local models** — all sharing the same 21.5 GB base weights (text encoder + video/audio VAE) without redownloading.
- **Smart Google Drive Workflow (Fast Local NVMe Copy + Real-Time Output Save)**:
  - Eliminated slow/flaky FUSE symlinks in both `h3-video.sh` and `comfyui-video.sh`: existing Drive models and prebuilt acceleration wheels (`.whl`) are copied sequentially via `rsync -ahW` onto `/content` local NVMe, new weights download directly over datacenter links (`hf_xet` / `hf_transfer`, with optional base cache back to Drive), and every rendered video saves straight to Drive `outputs/` so disconnects never lose work.
- **Auto-Open ComfyUI via Local Encrypted Tunnel & Fast Queue-Drain Shutdown**:
  - Automatically establishes a local OpenSSH encrypted tunnel (`127.0.0.1:8188`) via `colab ssh --proxy-mode` and launches the default browser once ComfyUI is ready (optional `cloudflared` link toggle available when cross-device preview is needed).
  - Status dashboard now displays live deployment progress, ComfyUI running/pending queue counts, rendered clip counts, one-click `开 ComfyUI` / `换 H3 版本` / `Drive 成片` shortcuts, and fast auto-shutdown (`10m / 15m / 40m`) after the video queue drains to zero.

### Changed
- **Plain-Language Copy Overhaul**: Rewrote onboarding, deployment UI, and bilingual `README.md` / `README.zh-CN.md` in clear, approachable everyday language focused on real video creation and LLM proxy workflows.

## [0.3.7] - 2026-11-25

### Changed
- **Cross-Platform Pure-Go + GPU UI Architecture (`macOS` + `Windows`)**: Rebuilt **Colab Tools** on top of [`egoist/mygo`](https://github.com/egoist/mygo) (`v0.10.0`, pure Go, `CGO_ENABLED=0`, zero Electron / zero WebView) while preserving the Mole-inspired desktop console (`1160 × 760`), 4×2 Bento vector micro-charts, 420pt system-tray companion popover, and full feature parity across macOS and Windows.
- **Zero-CGO Cross-Platform Persistence & Security**:
  - Replaced macOS-only `Security.framework` calls with `mygo.Secrets` (macOS Keychain / Windows Credential Manager), with transparent one-way migration for legacy `v0.3.6` `account:<UUID>` / `apikey:<UUID>` entries and `config.json` Swift reference dates (`scheduledNextFireAt`, `circuitBreakUntilByAccount`, `isolated_adc`).
  - Replaced C `sqlite3_backup_*` in CC Switch sync with pure-Go `modernc.org/sqlite` + `VACUUM INTO` WAL-safe backups and `BEGIN IMMEDIATE` transactions.
- **Windows Native-First + Seamless WSL2 CLI Bridge**: Automatically prefers a healthy native `colab.exe` (direct argv invocation without PowerShell quoting bugs) and transparently falls back to `wsl.exe` bridging official `google-colab-cli` inside WSL2 with automatic `C:\...` → `/mnt/c/...` path translation, CRLF normalization, and `taskkill /T /F` process-tree cleanup.
- **Dual-Platform Packaging & CI/CD**: Replaced `scripts/build-app.sh` with [`scripts/package.sh`](scripts/package.sh) (`mygo build` + `-trimpath` + local-path leakage verification) producing signed `Colab-Tools-v0.3.7-macOS-arm64.zip` and `Colab-Tools-v0.3.7-Windows-amd64.zip`, backed by `macos-15` + `windows-latest` GitHub Actions CI & Release workflows.

### Fixed
- **State Machine, SSH Forwarding & Watchdog Hardening (Codex Review)**:
  - Settled transitional `PhaseStarting` / `PhaseStopping` phases cleanly on authoritative server snapshots and independent post-stop `colab sessions` verification (never staying stuck or misreporting unverified stops).
  - Switched SSH tunneling on both macOS and Windows to local OpenSSH `-N -L` backed by `ProxyCommand="colab ... ssh --proxy-mode -s <session>"` instead of passing `-L` after `colab ssh --`.
  - Reset idle timer when LLM `/slots` `LatestTaskID` advances between 20s polls, marked signal `.unknown` on probe failures, and only incremented idle seconds while fresh idle evidence is observed.
  - Moved stale ephemeral ADC file cleanup behind `RequestSingleInstanceLock()`, guarded allocation heatmap UI reads with lock-safe snapshots, scoped polite-queue cancel cleanup by queue token, and hardened `package.sh` local-path auditing against `pipefail` SIGPIPE.

## [0.3.6] - 2026-11-25

### Added
- **Mole-Style Desktop Main Console (`⌘0` / `⌘1–⌘5` / `⌘,`)**: Upgraded the standalone Settings window into a full responsive macOS desktop console (`1160 × 760 pt`, min `980 × 660 pt`) with a centered floating capsule navigation bar (`Status`, `Compute & Queue`, `Integrations`, `Accounts`, `Settings`) and dynamic Dock visibility (appears in Dock while open for `⌘Tab`, automatically returns to quiet menu-bar-only accessory mode when closed).
- **4×2 Bento Status Dashboard & Live Session Table**: At-a-glance status screen inside the main window featuring CU balance + status aura orb, 10-segment VRAM meter, GPU utilization sparkline, context/workload horizon bar, session runtime ring gauge (with smart target-GPU standby estimation), interactive `15m / 40m / 60m` idle watchdog selector, queue/schedule summary, one-click sync & quick actions, plus a bottom live table covering all accounts, active runtimes, extra-session billing warnings, and full-session stop-all.
- **First-Launch Environment Doctor & 3-Step Onboarding Wizard (`EnvironmentDoctor`)**:
  - Automatically checks `google-colab-cli` via official `colab version` (distinguishing healthy vs. broken binaries), local package managers (`uv`, `pipx`, `Homebrew`, `python3`), and Google Colab account authorization on first launch (silently skipped for upgraded users who already have bound accounts, and re-openable anytime under **Settings → Environment**).
  - Supports transparent, cancellable one-click `google-colab-cli` installation (`uv tool install` → `pipx install` → `brew install` → explicit-consent official Astral `uv` installer) with POSIX process-group cleanup (`POSIX_SPAWN_SETPGROUP`), live log streaming, post-install verification, and a copy-command manual fallback.
- **4 New Core Unit Test Suites (29/29 total passing)**: Added regression tests for `EnvironmentDoctor` package-manager selection, `colab version` parsing, POSIX process-group cancellation/timeout child-tree cleanup, target-GPU offline runtime estimation, and legacy `config.json` onboarding migration.

### Changed
- **Apple macOS HIG + CJK Typography Overhaul (`ColabTypography`)**: Systematically scaled up typography across the main console and menu bar popover to follow macOS HIG & Chinese legibility standards (`13–13.5pt` body, `14pt` section headers, `22pt` page titles, `12pt` captions, `>= 11pt` badge floor), restored native `.controlSize(.regular)` in the main window, and widened the menu bar companion popover to `420pt` with a top-bar button to expand the full desktop console.

## [0.3.5] - 2026-11-25

### Added
- **Standalone Native macOS Settings App (`Cmd + ,`)**: Moved complex configuration out of the narrow menu bar popover into a dedicated Apple System Settings–style sidebar window (`General`, `Compute & Queue`, `Integrations`, `Accounts`, `Advanced`, `About`) with instant-apply controls and native `SMAppService.mainApp` Launch-at-Login support.
- **Visual Compute & Queue Workbench**:
  - **2-Column Hardware Target Grid**: Select GPUs/TPUs (`G4 / H100 / A100 / L4 / T4 / v6e-1 / v5e-1`) with VRAM & estimated `CU/h` readouts and `↑ / ↓` priority ordering preserved across saves.
  - **Two-Stage Auto-Fallback**: Focus rounds `1..N` exclusively on primary scarce cards (e.g., `G4 → H100`), then automatically append a safe fallback card (e.g., `L4`) from round `N+1` onward.
  - **Off-Peak Scheduled Start**: Schedule one-shot or daily off-peak launches (e.g., `07:00` local morning presets) with automatic de-duplication, cooldown checks, and global queue mutex compliance.
  - **Local Allocation Hit-Rate Heatmap (`AllocationInsights`)**: Strictly local, privacy-preserving 6-slot (`00–04` .. `20–24`) hit-rate chart in `~/.config/colab-tools/allocation-stats.json` with automatic golden-hour recommendations after 3+ samples.
- **Multi-Client Integrations Hub (`ClientIntegrations`)**:
  - Automatically scans installed AI coding clients (`CC Switch`, `OpenCode`, `Codex CLI`, `Claude Code`) and floats detected tools to the top.
  - **Native Safe Standby Sync**: Extends safe standby writes beyond CC Switch to **OpenCode** (`~/.config/opencode/opencode.json` using per-account `{env:COLAB_TOOLS_API_KEY_<UUID>}`, never storing plaintext keys) and **Codex CLI** (`~/.codex/config.toml` idempotent per-account `# BEGIN COLAB-TOOLS MANAGED <key>` block without touching top-level defaults), with serialized writes, symlink/ownership verification, unmanaged conflict rejection, and private `0700`/`0600` timestamped backups under `~/.config/colab-tools/backups/`.
  - **Universal Dual-Protocol Copy Hub**: Safe-by-default `OpenAI (/v1)` and `Anthropic` template copy cards (never embedding Keychain secrets into offline placeholders; explicit online-only button to copy raw secret) for Cherry Studio, Cline, Roo Code, Cursor, Claude Code Router, `ccm`, and any standard client.
- **9 New Core Unit Test Suites (25/25 total passing)**: Full regression coverage for per-account client isolation, unmanaged TOML/JSON conflict protection, invalid UTF-8 non-overwrite + `0700/0600` backup permissions, two-stage fallback & 10-minute schedule grace/expiry windows, corrupted stats clamping & unknown enum field-level decoding, and safe default snippet export.

### Changed
- **Streamlined Menu Bar Popover**: Removed the top 3-tab segmented switcher (`Control / Settings / About`) in favor of crisp `+ / Refresh / ⚙` action icons; kept high-frequency workload picker & queue toggle in the popover while summarizing queue priority, fallback, and schedule into a clean single-line status row with a one-click `Configure ›` deep link into the Settings window.
- **Multi-Client Bottom Action**: Upgraded the LLM dock button to sync all enabled integrations in one click and added notification toggle support (`enableNotifications`) while keeping critical billing and circuit-breaker alerts active.

## [0.3.4] - 2026-11-25

### Security & Anti-False-Shutdown Hardening (Codex Review Follow-Up)
- **LLM API Key Origin Isolation**: Strictly separated workload `status.json` `ui_url` (`displayUIURL`) from credential-bearing LLM `publicURL`, bound LLM `/slots` polling to the current active session endpoint, and blocked cross-origin HTTP redirects from forwarding `x-api-key`.
- **Safe In-Flight Queue Cancellation**: Cancelling a polite queue while `colab new` is already in flight now verifies server-side sessions before declaring offline, alerting immediately if an allocation succeeded so billing runtimes are never hidden.
- **Per-Round Identity Verification & Persisted Cooldowns**: Re-verifies default OAuth2 CLI login before every queue round, persists 15-minute anti-abuse circuit-breaker cooldowns across app restarts and all start paths, enforces an absolute 40-minute queue deadline, and permanently prunes HTTP `400` unsupported targets across rounds.
- **Watchdog Hardening**: Made remote GPU & CPU telemetry optional so missing readings or TPU runtimes return `.unknown` instead of `0%` idle; enforced remote mtime freshness (`<= 5m`) on `/tmp/colab-tools-status.json` so stale files never override a live busy ComfyUI `/queue`; aggregated all llama-server `/slots` entries instead of checking only slot 0; preserved legacy `enableHardwareIdleFallback = false` on old `config.json` upgrades.
- **Concurrency & Entitlement Safeguards**: Added per-run queue mutex tokens, synchronous busy-state acquisition against double-clicks, immediate post-allocation 10-minute reminder scheduling, custom script execution for `.general` workloads, and Google Colab FAQ compliance guardrails for free-tier runtimes without positive CU balance.
- **Template & Build Script Fixes**: Switched ComfyUI and Gradio starter templates to `python3 json.dumps` with explicit `--output-directory` verification; fixed `pipefail` + `grep -q` SIGPIPE false-clean check in `build-app.sh` so path leaks hard-fail before replacing the app bundle; aligned context menu header status and non-zero failure exit in `PreviewExporter`.

## [0.3.3] - 2026-11-25

### Added
- **Multimodal Workload System (`WorkloadKind`)**: Replaced the binary LLM toggle with 6 workload profiles (`LLM`, `Video`, `Image`, `Audio / Speech`, `Music`, `Training / General`) with seamless migration for existing configs.
- **Open Workload Status Protocol & Auto-Detection**: Reads `/tmp/colab-tools-status.json` for custom model names, WebUI URLs, progress ratios, and busy states; automatically probes ComfyUI (`:8188/queue`), Gradio (`:7860`), host CPU utilization, and active CUDA compute processes.
- **Dual Low-Load Anti-False-Shutdown Watchdog**: Extends idle auto-shutdown beyond LLM `/slots` to all multimodal and training workloads — prioritizing app signals (`status.json` / ComfyUI `/queue`) and falling back to **GPU < 5% AND CPU < 25%** so 20GB model downloads or FFmpeg CPU transcoding are never mistakenly killed.
- **Dynamic Port Tunneling & WebUI Launcher**: SSH tunnels automatically forward the active workload port (`18080`, `8188`, `7860`, `8888`, or custom) and bottom HUD quick actions adapt to one-click WebUI/preview opening.
- **Starter Templates & Protocol Spec**: Added `docs/WORKLOAD_PROTOCOL.md` and starter scripts under `scripts/templates/` (`comfyui-video.sh`, `gradio-audio-music.sh`, `training-job.sh`).

## [0.3.2] - 2026-11-25

### Added
- **Polite Anti-Abuse GPU/TPU Queue (`PoliteQueuePolicy`)**:
  - Select 1 target (single-card wait) or multiple targets (high-to-low priority chain) filtered automatically by your subscription tier (`G4 / H100 / A100 / L4 / T4 / v6e-1 / v5e-1`).
  - Enforces single-layer retries (`COLAB_CLI_ASSIGN_RETRIES=0`), minute-scale AWS/Google Equal Jitter backoff (`3m → 6m → 9m → 12m` ceilings, 12–20s inter-target spacing), hard caps (`5` rounds / `40` minutes), global single-account queue mutex, and pre-flight session verification.
  - Immediate hard circuit-breaker + 15-minute cooldown on `412 / 429 / 401 / 403`, abuse messages, timeouts, or unknown errors (only explicit `503` capacity shortages continue queueing).
  - Automatic post-allocation workflow: macOS notification with hardware & estimated `CU/hr`, LLM auto-start + CC Switch sync when enabled, and a 10-minute anti-forget reminder for non-LLM compute sessions (auto-cancelled on browser/SSH open or stop).
- **3 New Unit Test Suites**: Coverage for Equal Jitter caps, tier filtering, circuit-breaker classification, and backwards-compatible `config.json` decoding (11/11 total tests).

### Changed
- **Cleaner In-App About Panel**: Removed the embedded WeChat QR code from the in-app About tab and `.app` bundle resources (kept in GitHub README for community onboarding).

## [0.3.1] - 2026-11-25

### Fixed
- **Settings Panel Horizontal Clipping**: Locked inner scroll content width to the popover frame (`396pt`) and wrapped the CC Switch toggle into a clean two-line layout so switching menu bar display mode (`仅图标 / 紧凑 / 详细`) never shifts or clips controls at the left edge.
- **Popover Height & Sizing Stability**: Enabled `NSHostingController.sizingOptions = .preferredContentSize` and replaced the fixed `620pt` scroll cap with dynamic screen-aware height measurement (up to `860pt` tall), allowing the full vertical HUD dashboard to display in one uninterrupted tall view like native macOS monitoring utilities while gracefully scrolling on smaller displays.

### Added
- **CC Switch Standby Row**: Added a dedicated CC Switch channel status line inside the Active Sessions & Endpoint card so users can confirm official subscription isolation at a glance.

## [0.3.0] - 2026-11-25

### Added
- **Redesigned macOS HUD Interface**: High-contrast hero Compute Units readout, metadata spec pills, 2×2 micro-chart telemetry cards (10-segment VRAM bar, live GPU utilization sparkline, horizon context bar, and interactive `15m / 40m / 60m` idle watchdog selector), wide quota/accelerator focus card, table-style session list, and 3-column bottom summary strip.
- **External / Orphan Session Warning Banner**: Surfaces non-managed or `[?]` orphan server-side sessions to help catch forgotten billing.
- **Google AI Ultra 5x / 20x Tier Details**: Updated subscription tier descriptions for `5x (1,000 CU)` and `20x (2,000 CU)` allowances.
- **Open-Source Infrastructure**: Bilingual `README.md` + `README.zh-CN.md` with `@2x` Retina UI previews, GitHub Actions CI & automated release workflows, `CONTRIBUTING.md`, `SECURITY.md`, and issue templates.

### Changed
- **Formalized Packaging**: `build-app.sh` now strips host build paths (`-file-prefix-map` + `strip -x`), verifies zero `/Users/` leakage, and performs atomic staging installation.

## [0.2.0] - 2026-11-25

### Added
- Initial public release of **Colab Tools** (native Swift 6 + SwiftUI + AppKit menu bar app).
- Multi-account Keychain ADC isolation (`--auth=adc` + `--config`), three-state idle shutdown watchdog, WAL-safe CC Switch standby sync, deadlock-free process runner, and 8 core regression unit tests.
