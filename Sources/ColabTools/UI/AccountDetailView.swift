import ColabToolsCore
import SwiftUI

struct AccountDetailView: View {
    @ObservedObject var store: WorkspaceStore
    let account: AccountProfile

    private var rt: AccountRuntimeState {
        store.state(for: account.id)
    }

    var body: some View {
        VStack(spacing: 10) {
            subscriptionHeaderCard
            powerControlCard
            metricsGrid

            if !rt.sessions.isEmpty {
                activeSessionsCard
            }

            idleWatchdogCard

            if account.enableLLMModule {
                llmIntegrationCard
            }
        }
    }

    // MARK: - Subscription & Identity Strip
    private var subscriptionHeaderCard: some View {
        HStack(spacing: 8) {
            Circle()
                .fill(ColabTheme.color(for: account.colorTag))
                .frame(width: 8, height: 8)

            VStack(alignment: .leading, spacing: 1) {
                Text(account.email.isEmpty ? account.displayName : account.email)
                    .font(.system(size: 11.5, weight: .medium))
                    .lineLimit(1)
                    .truncationMode(.middle)
                Text(account.tier.summaryNote)
                    .font(.system(size: 10))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }

            Spacer()

            Menu {
                ForEach(SubscriptionTier.allCases) { tier in
                    Button {
                        var copy = account
                        copy.tier = tier
                        store.updateAccount(copy)
                    } label: {
                        HStack {
                            Text(tier.displayName)
                            if tier == account.tier {
                                Image(systemName: "checkmark")
                            }
                        }
                    }
                }
                Divider()
                Button("管理订阅 / 购点 (colab pay)…") {
                    store.openPayPage()
                }
            } label: {
                HStack(spacing: 3) {
                    Text(account.tier.shortBadge)
                        .font(.system(size: 10.5, weight: .semibold))
                    Image(systemName: "chevron.up.chevron.down")
                        .font(.system(size: 8.5))
                }
                .foregroundStyle(.primary)
                .padding(.horizontal, 8)
                .padding(.vertical, 4)
                .background(Color.primary.opacity(0.06), in: Capsule())
            }
            .menuStyle(.borderlessButton)
            .fixedSize()
        }
        .padding(.horizontal, 11)
        .padding(.vertical, 9)
        .refinedCard()
    }

    // MARK: - Power & Accelerator Selection
    private var powerControlCard: some View {
        VStack(spacing: 8) {
            HStack(spacing: 8) {
                VStack(alignment: .leading, spacing: 3) {
                    Text("算力分配策略")
                        .font(.system(size: 10.5))
                        .foregroundStyle(.secondary)
                    Picker("", selection: Binding(
                        get: { account.preferredAccelerator },
                        set: { newVal in
                            var copy = account
                            copy.preferredAccelerator = newVal
                            store.updateAccount(copy)
                        }
                    )) {
                        ForEach(account.tier.allowedAccelerators) { opt in
                            Text(opt.label).tag(opt)
                        }
                    }
                    .labelsHidden()
                    .controlSize(.small)
                    .frame(maxWidth: 205, alignment: .leading)
                    .disabled(rt.phase.isRunning || rt.phase.isBusy)
                }

                Spacer()

                if rt.phase.isBusy {
                    HStack(spacing: 5) {
                        ProgressView().controlSize(.small)
                        Text("执行中")
                            .font(.system(size: 11.5, weight: .medium))
                            .foregroundStyle(.secondary)
                    }
                    .padding(.horizontal, 11)
                    .padding(.vertical, 6)
                    .background(Color.primary.opacity(0.05), in: RoundedRectangle(cornerRadius: 7))
                } else if rt.phase.isRunning {
                    Button {
                        Task { await store.stopAccountSession(account.id) }
                    } label: {
                        Label("关机止损", systemImage: "power")
                            .font(.system(size: 11.5, weight: .medium))
                            .foregroundStyle(Color(nsColor: .systemRed))
                            .padding(.horizontal, 11)
                            .padding(.vertical, 6)
                            .background(Color(nsColor: .systemRed).opacity(0.09), in: RoundedRectangle(cornerRadius: 7))
                            .overlay(
                                RoundedRectangle(cornerRadius: 7)
                                    .strokeBorder(Color(nsColor: .systemRed).opacity(0.28), lineWidth: 0.75)
                            )
                    }
                    .buttonStyle(.plain)
                } else {
                    Button {
                        Task { await store.startAccountSession(account.id) }
                    } label: {
                        Label("一键启动", systemImage: "play.fill")
                            .font(.system(size: 11.5, weight: .medium))
                            .foregroundStyle(Color(nsColor: .windowBackgroundColor))
                            .padding(.horizontal, 13)
                            .padding(.vertical, 6)
                            .background(Color.primary, in: RoundedRectangle(cornerRadius: 7))
                    }
                    .buttonStyle(.plain)
                }
            }

            HStack(spacing: 12) {
                if account.tier.supportsHighMem {
                    Toggle("高内存形态 (--high-mem)", isOn: Binding(
                        get: { account.highMem },
                        set: { v in
                            var copy = account
                            copy.highMem = v
                            store.updateAccount(copy)
                        }
                    ))
                    .font(.system(size: 10.5))
                    .foregroundStyle(.secondary)
                    .controlSize(.mini)
                    .disabled(rt.phase.isRunning || rt.phase.isBusy)
                }

                Spacer()

                Toggle("LLM 推理模式", isOn: Binding(
                    get: { account.enableLLMModule },
                    set: { v in
                        var copy = account
                        copy.enableLLMModule = v
                        store.updateAccount(copy)
                    }
                ))
                .font(.system(size: 10.5))
                .foregroundStyle(.secondary)
                .controlSize(.mini)
                .disabled(rt.phase.isBusy)
            }

            if let err = rt.lastError, !err.isEmpty {
                HStack(spacing: 5) {
                    Image(systemName: "exclamationmark.circle")
                        .font(.system(size: 10.5))
                    Text(err)
                        .font(.system(size: 10.5))
                        .lineLimit(2)
                    Spacer()
                }
                .foregroundStyle(Color(nsColor: .systemOrange))
                .padding(.top, 2)
            }
        }
        .padding(11)
        .refinedCard()
    }

    // MARK: - 2x2 Live Telemetry Grid
    private var metricsGrid: some View {
        let cols = [GridItem(.flexible(), spacing: 9), GridItem(.flexible(), spacing: 9)]
        return LazyVGrid(columns: cols, spacing: 9) {
            MetricTile(
                title: "已运行时间",
                value: rt.phase.isRunning ? Formatters.duration(rt.sessionUptimeSeconds) : "—",
                subtitle: rt.phase.isRunning
                    ? "距 \(account.tier.maxSessionHours)h 上限约 \(Formatters.shortMinutes(rt.remainingCapSeconds(for: account.tier)))"
                    : "当前未计费",
                progress: rt.phase.isRunning
                    ? min(1.0, Double(rt.sessionUptimeSeconds) / Double(account.tier.maxSessionHours * 3600))
                    : 0
            )

            MetricTile(
                title: "预计可用时长",
                value: rt.estimatedHoursRemaining(for: account.tier).map { String(format: "%.0fh", $0) }
                    ?? (account.tier == .free ? "免费动态" : "—"),
                subtitle: String(
                    format: "点数 %@ CU · %.2f/hr",
                    Formatters.cu(rt.cuBalance),
                    rt.phase.isRunning ? rt.cuRatePerHour : 0
                ),
                progress: nil
            )

            MetricTile(
                title: "显存占用",
                value: rt.vramTotalMB > 0
                    ? String(format: "%.1f / %.1f G", Double(rt.vramUsedMB) / 1024.0, Double(rt.vramTotalMB) / 1024.0)
                    : (rt.phase.isRunning ? rt.shortGPUName : "—"),
                subtitle: rt.phase.isRunning
                    ? (rt.vramTotalMB > 0 ? "GPU 利用率 \(rt.gpuUtilPercent)%" : "官方会话已分配")
                    : "等待实例上线",
                progress: rt.vramTotalMB > 0 ? rt.vramRatio : 0,
                accentHigh: true
            )

            if account.enableLLMModule {
                MetricTile(
                    title: "上下文槽位",
                    value: rt.phase.isRunning && rt.serverHealthy
                        ? "\(Formatters.tokens(rt.promptTokensUsed)) / \(rt.contextLimit / 1024)K"
                        : "—",
                    subtitle: rt.phase.isRunning
                        ? (rt.isProcessing ? "推理生成中…" : (rt.serverHealthy ? "待命 · Task #\(max(0, rt.lastTaskID))" : "LLM 未启动"))
                        : "支持自动压缩对齐",
                    progress: rt.phase.isRunning && rt.serverHealthy ? rt.contextRatio : 0
                )
            } else {
                MetricTile(
                    title: "活跃会话",
                    value: "\(rt.activeAssignments)",
                    subtitle: rt.phase.isRunning ? "会话：\(account.sessionName)" : "官方 CLI 直连管理",
                    progress: nil
                )
            }
        }
    }

    // MARK: - Official Active Sessions List (Universal Colab Companion Feature)
    private var activeSessionsCard: some View {
        VStack(alignment: .leading, spacing: 7) {
            HStack {
                Text("活跃会话 (\(rt.sessions.count))")
                    .font(.system(size: 11, weight: .medium))
                    .foregroundStyle(.secondary)
                Spacer()
                Text("全部源自官方 colab sessions")
                    .font(.system(size: 9.5))
                    .foregroundStyle(.tertiary)
            }

            ForEach(rt.sessions) { sess in
                HStack(spacing: 7) {
                    Circle()
                        .fill(sess.isOrphan ? ColabTheme.amber : Color(nsColor: .systemGreen))
                        .frame(width: 6, height: 6)
                    VStack(alignment: .leading, spacing: 1) {
                        Text(sess.isOrphan ? "孤儿会话 [?]" : sess.name)
                            .font(.system(size: 11, weight: .medium))
                            .lineLimit(1)
                        Text("\(sess.hardware) · \(sess.shape)")
                            .font(.system(size: 10))
                            .foregroundStyle(.secondary)
                    }
                    Spacer()

                    if !sess.isOrphan {
                        Button {
                            store.openColabInBrowser(accountId: account.id, sessionName: sess.name)
                        } label: {
                            Image(systemName: "safari")
                                .font(.system(size: 11))
                        }
                        .buttonStyle(.borderless)
                        .help("在浏览器打开此 Colab 会话 (colab url --open)")

                        Button {
                            store.openRemoteSSHShell(accountId: account.id, sessionName: sess.name)
                        } label: {
                            Image(systemName: "terminal")
                                .font(.system(size: 11))
                        }
                        .buttonStyle(.borderless)
                        .help("在终端连接 SSH (colab ssh)")

                        Button {
                            Task { await store.stopAccountSession(account.id, specificSession: sess.name) }
                        } label: {
                            Image(systemName: "stop.circle")
                                .font(.system(size: 11))
                                .foregroundStyle(Color(nsColor: .systemRed).opacity(0.85))
                        }
                        .buttonStyle(.borderless)
                        .help("停止此会话并释放算力")
                    }
                }
                .padding(.vertical, 3)
            }
        }
        .padding(10)
        .refinedCard()
    }

    // MARK: - Custom Idle Shutdown Watchdog
    private var idleWatchdogCard: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                HStack(spacing: 5) {
                    Image(systemName: "timer")
                        .font(.system(size: 11.5))
                        .foregroundStyle(.secondary)
                    Text("无使用自动关机")
                        .font(.system(size: 12, weight: .medium))
                }
                Spacer()
                Toggle("", isOn: Binding(
                    get: { account.autoShutdownEnabled },
                    set: { v in
                        var copy = account
                        copy.autoShutdownEnabled = v
                        store.updateAccount(copy)
                    }
                ))
                .toggleStyle(.switch)
                .controlSize(.mini)
                .labelsHidden()
            }

            if account.autoShutdownEnabled {
                let wdStatus = store.watchdogStatus(for: account)
                HStack {
                    if !rt.phase.isRunning {
                        Text("空闲满 \(account.idleTimeoutMinutes) 分钟自动执行 colab stop 止损")
                            .font(.system(size: 11))
                            .foregroundStyle(.secondary)
                    } else if wdStatus == .pausedNoSignal {
                        Text(account.enableLLMModule ? "等待 /slots 空闲信号 · 计时暂停（防误停）" : "仅 LLM 模式支持精准空闲检测（防误停训练）")
                            .font(.system(size: 10.5))
                            .foregroundStyle(ColabTheme.amber)
                    } else if rt.isProcessing {
                        Text("正在推理中 · 空闲计时已暂停")
                            .font(.system(size: 11, weight: .medium))
                            .foregroundStyle(Color(nsColor: .systemGreen))
                    } else {
                        Text("已空闲 \(Formatters.duration(rt.idleSeconds)) · 剩 \(Formatters.duration(rt.remainingIdleSeconds(timeoutMinutes: account.idleTimeoutMinutes))) 关机")
                            .font(.system(size: 11))
                            .monospacedDigit()
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
                    if rt.phase.isRunning && wdStatus != .pausedNoSignal {
                        Button("续命") {
                            store.resetIdleTimer(for: account.id)
                        }
                        .font(.system(size: 10.5, weight: .medium))
                        .buttonStyle(.bordered)
                        .controlSize(.mini)
                    }
                }

                HStack(spacing: 5) {
                    ForEach([15, 30, 40, 60, 120], id: \.self) { mins in
                        Button {
                            var copy = account
                            copy.idleTimeoutMinutes = mins
                            store.updateAccount(copy)
                        } label: {
                            Text("\(mins)m")
                                .font(.system(size: 10.5, weight: account.idleTimeoutMinutes == mins ? .semibold : .regular))
                                .monospacedDigit()
                                .padding(.horizontal, 7)
                                .padding(.vertical, 3)
                                .background(
                                    account.idleTimeoutMinutes == mins
                                        ? Color.primary.opacity(0.14)
                                        : Color.primary.opacity(0.04),
                                    in: RoundedRectangle(cornerRadius: 5)
                                )
                        }
                        .buttonStyle(.plain)
                    }
                    Spacer()
                    Stepper(value: Binding(
                        get: { account.idleTimeoutMinutes },
                        set: { v in
                            var copy = account
                            copy.idleTimeoutMinutes = v
                            store.updateAccount(copy)
                        }
                    ), in: 5...720, step: 5) {
                        Text("\(account.idleTimeoutMinutes)分")
                            .font(.system(size: 10.5))
                            .monospacedDigit()
                            .foregroundStyle(.secondary)
                    }
                    .controlSize(.mini)
                }
            }
        }
        .padding(11)
        .refinedCard()
    }

    // MARK: - LLM & Claude Code Integration Card
    private var llmIntegrationCard: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text("LLM 推理与 Claude Code 接入")
                    .font(.system(size: 11, weight: .medium))
                    .foregroundStyle(.secondary)
                Spacer()
                if rt.serverHealthy {
                    Text("在线")
                        .font(.system(size: 9.5, weight: .medium))
                        .foregroundStyle(Color(nsColor: .systemGreen))
                        .padding(.horizontal, 6)
                        .padding(.vertical, 1.5)
                        .background(Color(nsColor: .systemGreen).opacity(0.12), in: Capsule())
                }
            }

            KeyValueRow(label: "模型", value: rt.shortModelName)
            KeyValueRow(label: "上下文 / KV", value: "\(rt.contextLimit / 1024)K · \(rt.kvQuant) · FlashAttn")

            HStack(spacing: 6) {
                Text("公网 HTTPS")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                    .frame(width: 68, alignment: .leading)
                Text(rt.publicURL.isEmpty ? "未启动" : rt.publicURL)
                    .font(.system(size: 10.5, design: .monospaced))
                    .lineLimit(1)
                    .truncationMode(.middle)
                Spacer()
                Button {
                    store.copyToClipboard(rt.publicURL, label: "公网 HTTPS 地址")
                } label: {
                    Image(systemName: "doc.on.doc").font(.system(size: 10.5))
                }
                .buttonStyle(.borderless)
                .disabled(rt.publicURL.isEmpty)
                .help("复制公网地址")
            }

            Divider().opacity(0.35).padding(.vertical, 1)

            HStack(spacing: 7) {
                ActionChip(
                    title: rt.ccSwitchSynced ? "已同步 CC Switch" : "同步 CC Switch",
                    icon: rt.ccSwitchSynced ? "checkmark.circle" : "arrow.triangle.2.circlepath",
                    disabled: !CCSwitchSync.isInstalled || rt.publicURL.isEmpty,
                    active: rt.ccSwitchSynced
                ) {
                    store.syncToCCSwitch(accountId: account.id, silent: false)
                }

                ActionChip(
                    title: "Claude 终端",
                    icon: "terminal",
                    disabled: !rt.phase.isRunning || rt.publicURL.isEmpty
                ) {
                    store.launchClaudeTerminal(accountId: account.id)
                }

                ActionChip(
                    title: rt.sshTunnelActive ? "断开隧道" : "SSH 兜底",
                    icon: rt.sshTunnelActive ? "lock.fill" : "lock.open",
                    disabled: !rt.phase.isRunning && !rt.sshTunnelActive,
                    active: rt.sshTunnelActive
                ) {
                    store.toggleSSHTunnel(for: account.id)
                }
            }
        }
        .padding(11)
        .refinedCard()
    }
}
