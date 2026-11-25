<div align="center">

<img src="assets/AppIcon.png" width="128" height="128" alt="Colab Tools Icon" />

# Colab Tools

**面向 Google AI / Gemini 与 Colab 订阅用户的原生 macOS 菜单栏工具**

基于官方 [`google-colab-cli`](https://github.com/googlecolab/google-colab-cli) · 多订阅工作区隔离管理 · 实时 Compute Units 监控 · 自定义空闲自动关机 · 可选 LLM / Claude Code 增强

[![macOS](https://img.shields.io/badge/macOS-14%2B%20(Apple%20Silicon)-black?logo=apple)](https://www.apple.com/macos)
[![Swift](https://img.shields.io/badge/Swift-6.0-F05138?logo=swift&logoColor=white)](https://swift.org)
[![Colab CLI](https://img.shields.io/badge/google--colab--cli-0.8.x-F59E0B)](https://github.com/googlecolab/google-colab-cli)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

</div>

---

## 核心特性

### 1. 订阅档位识别 + 单/多账号自适应体验
- 支持 6 档订阅选项：**Google AI Ultra / Colab Pro+ / Google AI Pro / Colab Pro / Pay As You Go / Free**。
- 通过官方 `colab usage` 读取真实 Compute Units（CU）余额与小时费率，自动推荐订阅档位并约束可用加速器选项（例如 Free 仅开放 T4 / CPU）。
- **单账号极简模式**：仅有 1 个账号时自动隐藏账号切换条，界面纯净零噪音。
- **多账号并行管理模式**（≥ 2 个账号）：
  - 顶部出现横向胶囊切换栏 + **全局总览（Overview）**，聚合展示总点数、运行中实例数与合计小时消耗；
  - 支持各账号在独立会话与独立钥匙串凭证下并行运行、独立启停与独立 SSH 终端/隧道接入。

### 2. 官方 CLI 驱动与合规使用说明
- **官方命令封装**：账号状态、余额、会话管理、Drive 挂载、SSH、浏览器附着与续费入口均通过官方 `google-colab-cli` 子命令（`whoami`、`usage`、`sessions`、`new`、`stop`、`exec`、`download`、`drivemount`、`ssh`、`url`、`pay`）执行；多账号添加引导复用了本机已安装 `google-colab-cli 0.8.x` 的官方 OAuth2 客户端定义（`PUBLIC_SCOPES` 与 `REMOTE_REDIRECT_URI`）。
- **安全凭证隔离**：新增多账号凭证加密存于 **macOS Keychain**，每次执行通过 `0600` 独占临时 ADC 文件传递并在结束后擦除销毁；绝不覆盖终端默认 `colab` 登录态。
- **克制节流与保护**：后台同步默认 3 分钟且多账号错峰串行、手动刷新带 5 秒防抖；`COLAB_CLI_ASSIGN_RETRIES=0` 仅在明确 `503` 库存不足时有限回退，遇 `429` 限流、`412` 配额上限或超时立即停止。
- **合规边界提醒**：请严格遵守 [Google Colab 服务条款与 FAQ](https://research.google.com/colaboratory/faq.html)——请勿将多账号管理用于绕过免费额度限制，也勿在 Colab 上托管违反平台政策的长期服务；部分免费运行时按官方策略可能不支持 SSH。另需注意：`google-colab-cli 0.8.x` 的 `--config` 仅隔离会话索引，`colab exec` 历史仍统一记录在 `~/.config/colab-cli/history/<session>.jsonl`，因此本工具默认给新增账号生成唯一会话名且绝不通过 `exec` 输出明文 API Key。

### 3. 通用 Colab 伴侣 + 可选 LLM / Claude Code 增强
- **通用 Colab 能力（默认开箱即用）**：一键启停 GPU（`G4 / H100 / A100 / L4 / T4`）与 TPU（`v6e1 / v5e1`）+ `--high-mem`，展示全量活跃会话（含孤儿会话 `[?]`），一键 `colab url --open` 浏览器打开、`colab ssh` 连终端、停机后二次复查服务端确认释放。
- **可选 LLM & Claude Code 模块（需手动开启）**：
  - 支持挂载 Google Drive 启动自托管 `llama-server`（如 `Qwen3.8-27B` 256K）与 Cloudflare 公网 HTTPS 隧道；
  - **三态安全空闲看门狗**：仅在 LLM 模式且最近 90 秒内通过 `/slots` 真实观测到 `idle` 信号时才推进自定义倒计时（5–720 分钟），普通 Notebook 训练或网络失联时自动暂停计时绝不误停；
  - **CC Switch 事务安全同步**：通过 SQLite 在线备份（WAL 安全）与 `BEGIN IMMEDIATE` 事务仅写入独立待机渠道（拒绝覆盖 `is_current=1` 或官方订阅），自动写入真实上下文与自动压缩窗口环境变量。

---

## 快速安装

### 前提条件
- **系统与架构**：macOS 14.0+（当前预编译 Release 为 Apple Silicon `arm64`，Intel Mac 可通过源码 `swift build` 本地编译）
- **安装并登录官方 Google Colab CLI（已测试兼容 `0.8.0`）**：
  ```bash
  uv tool install google-colab-cli
  colab --auth=oauth2 whoami
  ```

### 从源码编译与安装
```bash
git clone https://github.com/LJMStark/Colab-Tools.git
cd Colab-Tools
chmod +x scripts/build-app.sh
./scripts/build-app.sh --install
open "/Applications/Colab Tools.app"
```

### （可选）启用 LLM 推理模块前的准备
如果你希望使用内置的 LLM / Claude Code 推理增强功能：
1. 将仓库中的 [`scripts/remote/start_server.sh`](scripts/remote/start_server.sh) 上传到你 Google Drive 的 `/MyDrive/colab-llm/scripts/start_server.sh`（也可在 App 设置中自定义路径）；
2. 在 Colab Tools 主面板打开「LLM 推理模式」开关，再点击一键启动即可。

---

## 联系作者与交流群

> 如果这个工具帮到了你，欢迎点个 ⭐ Star！有任何建议或 Bug 欢迎提 Issue / PR。

- **X (Twitter)**：[https://x.com/starkljm](https://x.com/starkljm)
- **联系方式**：X DM [`@starkljm`](https://x.com/starkljm)（如群二维码过期请私信拉群）
- **微信交流群（Colab Tools 交流群）**：

<p align="left">
  <img src="assets/wechat-qr.png" width="240" alt="Colab Tools 微信交流群二维码" />
</p>

---

## License

[MIT](LICENSE)
