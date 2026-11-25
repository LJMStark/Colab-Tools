import ColabToolsCore
import SwiftUI

struct AccountDetailView: View {
    @ObservedObject var store: WorkspaceStore
    let account: AccountProfile

    private var rt: AccountRuntimeState {
        store.state(for: account.id)
    }

    private var orphanSessions: [ColabSessionItem] {
        rt.sessions.filter { $0.isOrphan || $0.name != account.sessionName }
    }

    var body: some View {
        VStack(spacing: 11) {
            heroHeaderSection

            if !orphanSessions.isEmpty {
                externalSessionBanner
            }

            telemetryGrid2x2
            quotaAndStrategyWideCard
            sessionsAndConfigTableCard
            bottomDockCard
        }
    }

    // MARK: - 1. Hero Header (Big Headline Metric + System Spec Pills)
    private var heroHeaderSection: some View {
        VStack(alignment: .leading, spacing: 9) {
            HStack(alignment: .center, spacing: 10) {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Image(systemName: heroIconName)
                        .font(.system(size: 21, weight: .semibold))
                        .foregroundStyle(ColabTheme.statusColor(for: rt.phase))
                        .symbolRenderingMode(.hierarchical)

                    Text(Formatters.cu(rt.cuBalance))
                        .font(.system(size: 29, weight: .bold, design: .rounded))
                        .monospacedDigit()
                        .foregroundStyle(.primary)

                    VStack(alignment: .leading, spacing: 1) {
                        Text(heroStatusWord)
                            .font(.system(size: 13.5, weight: .semibold))
                            .foregroundStyle(rt.phase.isRunning ? .primary : .secondary)
                        Text("CU 点数")
                            .font(.system(size: 10.5))
                            .foregroundStyle(.tertiary)
                    }
                }

                Spacer()

                powerActionButton
            }

            // Crisp metadata pill strip (clickable tier badge)
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 6) {
                    Menu {
                        ForEach(SubscriptionTier.allCases) { tier in
                            Button {
                                var copy = account
                                copy.tier = tier
                                store.updateAccount(copy)
                            } label: {
                                HStack {
                                    Text(tier.displayName)
                                    if tier == account.tier { Image(systemName: "checkmark") }
                                }
                            }
                        }
                        Divider()
                        Button("管理订阅 / 购点 (colab pay)…") {
                            store.openPayPage()
                        }
                    } label: {
                        HStack(spacing: 4) {
                            Text(account.tier.shortBadge)
                            Image(systemName: "chevron.up.chevron.down")
                                .font(.system(size: 8.5, weight: .semibold))
                                .opacity(0.7)
                        }
                        .hudPill()
                    }
                    .menuStyle(.borderlessButton)
                    .menuIndicator(.hidden)
                    .fixedSize()

                    if rt.phase.isRunning {
                        Text(rt.gpuName.isEmpty ? rt.shortGPUName : rt.gpuName)
                            .hudPill()
                        if rt.vramTotalMB > 0 {
                            Text(String(format: "%.0f GB", Double(rt.vramTotalMB) / 1024.0))
                                .hudPill()
                        }
                        Text("已运行 \(Formatters.duration(rt.sessionUptimeSeconds))")
                            .monospacedDigit()
                            .hudPill()
                    } else {
                        Text(account.email.isEmpty ? account.displayName : account.email)
                            .hudPill()
                        Text("会话: \(account.sessionName)")
                            .hudPill()
                    }
                }
            }

            if let err = rt.lastError, !err.isEmpty {
                HStack(spacing: 6) {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .font(.system(size: 11))
                    Text(err)
                        .font(.system(size: 11))
                        .lineLimit(2)
                    Spacer()
                }
                .foregroundStyle(ColabTheme.amber)
                .padding(.horizontal, 10)
                .padding(.vertical, 7)
                .background(ColabTheme.amber.opacity(0.11), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
            }
        }
        .padding(.horizontal, 2)
    }

    private var heroIconName: String {
        switch rt.phase {
        case .running: return rt.isProcessing ? "bolt.circle.fill" : "sun.max.fill"
        case .starting, .stopping: return "arrow.triangle.2.circlepath.circle.fill"
        case .error: return "exclamationmark.triangle.fill"
        case .offline: return "moon.zzz.fill"
        }
    }

    private var heroStatusWord: String {
        switch rt.phase {
        case .running: return rt.isProcessing ? "推理中" : "运行良好"
        case .starting(let s): return s
        case .stopping: return "关机中…"
        case .error: return "需检查"
        case .offline: return "已关闭"
        }
    }

    @ViewBuilder
    private var powerActionButton: some View {
        if rt.phase.isBusy {
            HStack(spacing: 6) {
                ProgressView().controlSize(.small)
                Text("处理中")
                    .font(.system(size: 12, weight: .medium))
                    .foregroundStyle(.secondary)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 7)
            .background(Color.primary.opacity(0.06), in: RoundedRectangle(cornerRadius: 9, style: .continuous))
        } else if rt.phase.isRunning {
            Button {
                Task { await store.stopAccountSession(account.id) }
            } label: {
                Label("关机止损", systemImage: "power")
                    .font(.system(size: 12, weight: .semibold))
                    .foregroundStyle(ColabTheme.coral)
                    .padding(.horizontal, 12)
                    .padding(.vertical, 7)
                    .background(ColabTheme.coral.opacity(0.12), in: RoundedRectangle(cornerRadius: 9, style: .continuous))
                    .overlay(
                        RoundedRectangle(cornerRadius: 9, style: .continuous)
                            .strokeBorder(ColabTheme.coral.opacity(0.32), lineWidth: 0.75)
                    )
            }
            .buttonStyle(.plain)
        } else {
            Button {
                Task { await store.startAccountSession(account.id) }
            } label: {
                Label("一键启动", systemImage: "play.fill")
                    .font(.system(size: 12, weight: .semibold))
                    .foregroundStyle(Color(nsColor: .windowBackgroundColor))
                    .padding(.horizontal, 14)
                    .padding(.vertical, 7)
                    .background(Color.primary, in: RoundedRectangle(cornerRadius: 9, style: .continuous))
            }
            .buttonStyle(.plain)
        }
    }

    private var externalSessionBanner: some View {
        HStack(spacing: 7) {
            Image(systemName: "exclamationmark.shield.fill")
                .font(.system(size: 12))
                .foregroundStyle(ColabTheme.amber)
            Text("检测到 \(orphanSessions.count) 个非主控/外部运行中会话，请注意避免遗忘计费。")
                .font(.system(size: 11))
                .foregroundStyle(.primary.opacity(0.88))
            Spacer()
        }
        .padding(.horizontal, 11)
        .padding(.vertical, 8)
        .background(ColabTheme.amber.opacity(0.11), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .strokeBorder(ColabTheme.amber.opacity(0.28), lineWidth: 0.6)
        )
    }

    // MARK: - 2. 2x2 Micro-Chart Telemetry Grid
    private var telemetryGrid2x2: some View {
        let cols = [GridItem(.flexible(), spacing: 10), GridItem(.flexible(), spacing: 10)]
        return LazyVGrid(columns: cols, spacing: 10) {
            // Card 1: VRAM (Segmented Equalizer Bar)
            vramCard
            // Card 2: GPU Load (Sparkline)
            gpuLoadCard
            // Card 3: Context / Assignments (Horizon Bar)
            contextCard
            // Card 4: Idle Auto-Shutdown (Fan-style interactive preset pills)
            idleWatchdogCard
        }
    }

    private var vramCard: some View {
        let pct = Int((rt.vramRatio * 100).rounded())
        let pressureText = rt.vramTotalMB == 0 ? "未加载" : (pct >= 94 ? "高占用" : "压力正常")
        return VStack(alignment: .leading, spacing: 8) {
            TileHeader(
                icon: "memorychip",
                iconTint: ColabTheme.emerald,
                title: "显存",
                badgeText: pressureText,
                badgeTint: pct >= 94 ? ColabTheme.amber : nil
            )

            BigMetricValue(
                number: rt.vramTotalMB > 0 ? "\(pct)" : "—",
                unit: rt.vramTotalMB > 0 ? "%" : ""
            )

            MicroSegmentsBar(progress: rt.vramRatio, tint: pct >= 94 ? ColabTheme.amber : ColabTheme.emerald)

            Text(
                rt.vramTotalMB > 0
                    ? String(format: "%.1f GB / %.1f GB", Double(rt.vramUsedMB) / 1024.0, Double(rt.vramTotalMB) / 1024.0)
                    : "等待 GPU 分配"
            )
            .font(.system(size: 11))
            .monospacedDigit()
            .foregroundStyle(.secondary)
            .lineLimit(1)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .padding(12)
        .hudCard()
    }

    private var gpuLoadCard: some View {
        let rateStr = rt.phase.isRunning ? String(format: "%.2f/h", rt.cuRatePerHour) : "离线"
        return VStack(alignment: .leading, spacing: 8) {
            TileHeader(
                icon: "cpu",
                iconTint: ColabTheme.amber,
                title: "GPU",
                badgeText: rt.phase.isRunning ? rt.shortGPUName : account.preferredAccelerator.rawValue
            )

            BigMetricValue(
                number: rt.phase.isRunning ? "\(rt.gpuUtilPercent)" : "—",
                unit: rt.phase.isRunning ? "%" : ""
            )

            MicroSparkline(
                points: rt.gpuUtilHistory.isEmpty ? [Double(rt.gpuUtilPercent), Double(rt.gpuUtilPercent)] : rt.gpuUtilHistory,
                tint: ColabTheme.amber
            )

            Text(rt.phase.isRunning ? (rt.isProcessing ? "推理高负载 · \(rateStr)" : "待命低负载 · \(rateStr)") : "当前未计费")
                .font(.system(size: 11))
                .monospacedDigit()
                .foregroundStyle(.secondary)
                .lineLimit(1)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .padding(12)
        .hudCard()
    }

    private var contextCard: some View {
        let pct = Int((rt.contextRatio * 100).rounded())
        return VStack(alignment: .leading, spacing: 8) {
            if account.enableLLMModule {
                TileHeader(
                    icon: "square.stack.3d.up",
                    iconTint: ColabTheme.sky,
                    title: "上下文",
                    badgeText: "\(rt.contextLimit / 1024)K"
                )

                BigMetricValue(
                    number: (rt.phase.isRunning && rt.serverHealthy) ? Formatters.tokens(rt.promptTokensUsed) : "—",
                    unit: (rt.phase.isRunning && rt.serverHealthy) ? "tok" : ""
                )

                HorizonBar(progress: rt.serverHealthy ? rt.contextRatio : 0, tint: ColabTheme.sky)
                    .padding(.vertical, 5)

                Spacer(minLength: 0)

                Text(
                    rt.serverHealthy
                        ? "已用 \(pct)% · \(rt.kvQuant) KV · #\(max(0, rt.lastTaskID))"
                        : "自动压缩窗口已对齐"
                )
                .font(.system(size: 11))
                .monospacedDigit()
                .foregroundStyle(.secondary)
                .lineLimit(1)
            } else {
                TileHeader(
                    icon: "server.rack",
                    iconTint: ColabTheme.sky,
                    title: "活跃会话",
                    badgeText: account.highMem ? "High-RAM" : "Standard"
                )

                BigMetricValue(number: "\(rt.activeAssignments)", unit: "个")

                HorizonBar(progress: min(1.0, Double(rt.activeAssignments) / 3.0), tint: ColabTheme.sky)
                    .padding(.vertical, 5)

                Spacer(minLength: 0)

                Text(rt.phase.isRunning ? "官方 CLI 直连托管" : "通用算力管理模式")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .padding(12)
        .hudCard()
    }

    private var idleWatchdogCard: some View {
        let wdStatus = store.watchdogStatus(for: account)
        let remMin = max(0, (rt.remainingIdleSeconds(timeoutMinutes: account.idleTimeoutMinutes) + 59) / 60)

        return VStack(alignment: .leading, spacing: 7) {
            HStack(spacing: 5) {
                Image(systemName: "timer")
                    .font(.system(size: 11.5, weight: .medium))
                    .foregroundStyle(account.autoShutdownEnabled ? ColabTheme.cyan : .secondary)
                Text("空闲关机")
                    .font(.system(size: 12, weight: .medium))
                    .foregroundStyle(.secondary)
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
                .tint(ColabTheme.emerald)
                .controlSize(.mini)
                .labelsHidden()
            }

            HStack(alignment: .firstTextBaseline) {
                if !account.autoShutdownEnabled {
                    BigMetricValue(number: "OFF", unit: "", fontSize: 22)
                } else if !rt.phase.isRunning {
                    BigMetricValue(number: "\(account.idleTimeoutMinutes)", unit: "min", fontSize: 22)
                } else if wdStatus == .pausedNoSignal {
                    BigMetricValue(number: "暂停", unit: "", fontSize: 20)
                } else if rt.isProcessing {
                    BigMetricValue(number: "活跃", unit: "", fontSize: 20)
                } else {
                    BigMetricValue(number: "\(remMin)", unit: "min", fontSize: 22)
                }

                Spacer()
                if rt.phase.isRunning && account.autoShutdownEnabled && wdStatus != .pausedNoSignal {
                    Button("续命") {
                        store.resetIdleTimer(for: account.id)
                    }
                    .font(.system(size: 10.5, weight: .medium))
                    .buttonStyle(.bordered)
                    .controlSize(.mini)
                }
            }

            Text(watchdogCaption(status: wdStatus))
                .font(.system(size: 10.5))
                .foregroundStyle(wdStatus == .pausedNoSignal ? ColabTheme.amber : .secondary)
                .lineLimit(1)

            // Reference-inspired 3-pill selector right inside card
            HStack(spacing: 4) {
                ForEach([15, 40, 60], id: \.self) { mins in
                    let selected = account.idleTimeoutMinutes == mins
                    Button {
                        var copy = account
                        copy.idleTimeoutMinutes = mins
                        copy.autoShutdownEnabled = true
                        store.updateAccount(copy)
                    } label: {
                        Text("\(mins)m")
                            .font(.system(size: 10.5, weight: selected ? .semibold : .medium))
                            .monospacedDigit()
                            .frame(maxWidth: .infinity)
                            .padding(.vertical, 3.5)
                            .background(
                                selected ? ColabTheme.amber.opacity(0.22) : Color.primary.opacity(0.06),
                                in: RoundedRectangle(cornerRadius: 6, style: .continuous)
                            )
                            .foregroundStyle(selected ? .primary : .secondary)
                    }
                    .buttonStyle(.plain)
                }
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .padding(12)
        .hudCard()
    }

    private func watchdogCaption(status: WatchdogAction) -> String {
        guard account.autoShutdownEnabled else { return "手动控制关机" }
        guard rt.phase.isRunning else { return "启动后自动守护" }
        if status == .pausedNoSignal {
            return account.enableLLMModule ? "等待 /slots 信号" : "需开启 LLM 监测"
        }
        if rt.isProcessing { return "推理中 · 计时暂停" }
        return "已空闲 \(Formatters.shortMinutes(rt.idleSeconds)) 后关机"
    }

    // MARK: - 3. Wide Focus Card (Quota / Remaining Hours + Accelerator Strategy)
    private var quotaAndStrategyWideCard: some View {
        VStack(alignment: .leading, spacing: 9) {
            HStack {
                HStack(spacing: 5) {
                    Image(systemName: "bolt.batteryblock.fill")
                        .font(.system(size: 12))
                        .foregroundStyle(ColabTheme.emerald)
                    Text("算力续航")
                        .font(.system(size: 12, weight: .medium))
                        .foregroundStyle(.secondary)
                    if rt.phase.isRunning {
                        Text("单次上限 \(account.tier.maxSessionHours)h")
                            .font(.system(size: 11))
                            .foregroundStyle(.tertiary)
                    }
                }
                Spacer()
                CardBadge(
                    text: rt.phase.isRunning
                        ? "本轮剩余 \(Formatters.shortMinutes(rt.remainingCapSeconds(for: account.tier)))"
                        : account.tier.displayName
                )
            }

            HStack(alignment: .center, spacing: 10) {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    if let hrs = rt.estimatedHoursRemaining(for: account.tier) {
                        BigMetricValue(number: String(format: "%.0f", hrs), unit: "小时可用", fontSize: 22)
                    } else {
                        BigMetricValue(number: account.tier == .free ? "免费池" : "—", unit: "", fontSize: 20)
                    }
                }

                Spacer()

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
                .frame(maxWidth: 185)
                .disabled(rt.phase.isRunning || rt.phase.isBusy)
            }

            HStack(spacing: 14) {
                if account.tier.supportsHighMem {
                    Toggle("高内存 (--high-mem)", isOn: Binding(
                        get: { account.highMem },
                        set: { v in
                            var copy = account
                            copy.highMem = v
                            store.updateAccount(copy)
                        }
                    ))
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                    .controlSize(.mini)
                    .disabled(rt.phase.isRunning || rt.phase.isBusy)
                }

                Toggle("LLM 推理与 Claude 接入", isOn: Binding(
                    get: { account.enableLLMModule },
                    set: { v in
                        var copy = account
                        copy.enableLLMModule = v
                        store.updateAccount(copy)
                    }
                ))
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
                .controlSize(.mini)
                .disabled(rt.phase.isBusy)

                Spacer()
            }
        }
        .padding(12)
        .hudCard()
    }

    // MARK: - 4. Table-Style Sessions & Endpoint List (Inspired by Top Processes Table)
    private var sessionsAndConfigTableCard: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text("活跃会话与端点")
                    .font(.system(size: 11.5, weight: .medium))
                    .foregroundStyle(.secondary)
                Spacer()
                Text("配置")
                    .font(.system(size: 11))
                    .foregroundStyle(.tertiary)
                    .frame(width: 82, alignment: .trailing)
                Text("操作")
                    .font(.system(size: 11))
                    .foregroundStyle(.tertiary)
                    .frame(width: 66, alignment: .trailing)
            }

            if rt.sessions.isEmpty {
                HStack {
                    Text("当前无运行中 Colab 实例")
                        .font(.system(size: 12))
                        .foregroundStyle(.tertiary)
                    Spacer()
                }
                .padding(.vertical, 4)
            } else {
                ForEach(rt.sessions) { sess in
                    HStack(spacing: 6) {
                        Circle()
                            .fill(sess.isOrphan ? ColabTheme.amber : ColabTheme.emerald)
                            .frame(width: 6.5, height: 6.5)
                        Text(sess.isOrphan ? "外部会话 [?]" : sess.name)
                            .font(.system(size: 12, weight: .medium))
                            .lineLimit(1)
                        Spacer()
                        Text("\(sess.hardware) · \(sess.shape == "Standard" ? "Std" : sess.shape)")
                            .font(.system(size: 11.5))
                            .monospacedDigit()
                            .foregroundStyle(.secondary)
                            .frame(width: 82, alignment: .trailing)
                            .lineLimit(1)
                        HStack(spacing: 8) {
                            if !sess.isOrphan {
                                Button {
                                    store.openColabInBrowser(accountId: account.id, sessionName: sess.name)
                                } label: {
                                    Image(systemName: "safari").font(.system(size: 11.5))
                                }
                                .buttonStyle(.borderless)
                                .help("浏览器打开 (colab url --open)")

                                Button {
                                    store.openRemoteSSHShell(accountId: account.id, sessionName: sess.name)
                                } label: {
                                    Image(systemName: "terminal").font(.system(size: 11.5))
                                }
                                .buttonStyle(.borderless)
                                .help("打开 SSH 终端")

                                Button {
                                    Task { await store.stopAccountSession(account.id, specificSession: sess.name) }
                                } label: {
                                    Image(systemName: "stop.circle").font(.system(size: 11.5)).foregroundStyle(ColabTheme.coral)
                                }
                                .buttonStyle(.borderless)
                                .help("停止此会话")
                            }
                        }
                        .frame(width: 66, alignment: .trailing)
                    }
                    .padding(.vertical, 2)
                }
            }

            if account.enableLLMModule {
                Divider().opacity(0.35)

                HStack(spacing: 6) {
                    Text("模型")
                        .font(.system(size: 11.5))
                        .foregroundStyle(.secondary)
                        .frame(width: 48, alignment: .leading)
                    Text(rt.shortModelName)
                        .font(.system(size: 11.5, design: .monospaced))
                        .lineLimit(1)
                        .truncationMode(.middle)
                    Spacer()
                    if rt.serverHealthy {
                        CardBadge(text: "在线", tint: ColabTheme.emerald)
                    }
                }

                HStack(spacing: 6) {
                    Text("HTTPS")
                        .font(.system(size: 11.5))
                        .foregroundStyle(.secondary)
                        .frame(width: 48, alignment: .leading)
                    Text(rt.publicURL.isEmpty ? "未启动" : rt.publicURL)
                        .font(.system(size: 11, design: .monospaced))
                        .foregroundStyle(rt.publicURL.isEmpty ? .tertiary : .primary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                    Spacer()
                    Button {
                        store.copyToClipboard(rt.publicURL, label: "公网 HTTPS 地址")
                    } label: {
                        Image(systemName: "doc.on.doc").font(.system(size: 11.5))
                    }
                    .buttonStyle(.borderless)
                    .disabled(rt.publicURL.isEmpty)
                    .help("复制公网地址")
                }
            }
        }
        .padding(12)
        .hudCard()
    }

    // MARK: - 5. Bottom Utility Strip + 3-Column Big Summary
    private var bottomDockCard: some View {
        VStack(spacing: 10) {
            if account.enableLLMModule {
                HStack(spacing: 6) {
                    QuickActionButton(
                        title: rt.ccSwitchSynced ? "已同步" : "同步 CC",
                        icon: rt.ccSwitchSynced ? "checkmark.circle.fill" : "arrow.triangle.2.circlepath",
                        disabled: !CCSwitchSync.isInstalled || rt.publicURL.isEmpty,
                        active: rt.ccSwitchSynced
                    ) {
                        store.syncToCCSwitch(accountId: account.id, silent: false)
                    }

                    QuickActionButton(
                        title: "Claude",
                        icon: "terminal",
                        disabled: !rt.phase.isRunning || rt.publicURL.isEmpty
                    ) {
                        store.launchClaudeTerminal(accountId: account.id)
                    }

                    QuickActionButton(
                        title: rt.sshTunnelActive ? "断隧道" : "SSH",
                        icon: rt.sshTunnelActive ? "lock.fill" : "lock.open",
                        disabled: !rt.phase.isRunning && !rt.sshTunnelActive,
                        active: rt.sshTunnelActive
                    ) {
                        store.toggleSSHTunnel(for: account.id)
                    }

                    QuickActionButton(title: "续费", icon: "creditcard") {
                        store.openPayPage()
                    }
                }
            } else {
                HStack(spacing: 7) {
                    QuickActionButton(
                        title: "网页打开",
                        icon: "safari",
                        disabled: !rt.phase.isRunning
                    ) {
                        store.openColabInBrowser(accountId: account.id)
                    }

                    QuickActionButton(
                        title: "SSH 终端",
                        icon: "terminal",
                        disabled: !rt.phase.isRunning
                    ) {
                        store.openRemoteSSHShell(accountId: account.id)
                    }

                    QuickActionButton(title: "订阅 / 购点", icon: "creditcard") {
                        store.openPayPage()
                    }
                }
            }

            Divider().opacity(0.35)

            HStack(spacing: 0) {
                FooterStatCell(
                    value: rt.phase.isRunning ? Formatters.duration(rt.sessionUptimeSeconds) : "0m",
                    label: "已运行"
                )
                FooterStatCell(
                    value: rt.phase.isRunning ? String(format: "%.2f", rt.cuRatePerHour) : "0.00",
                    label: "CU / 小时"
                )
                FooterStatCell(
                    value: account.enableLLMModule ? "\(rt.contextLimit / 1024)K" : "\(rt.activeAssignments)",
                    label: account.enableLLMModule ? "上下文上限" : "运行会话"
                )
            }
        }
        .padding(12)
        .hudCard()
    }
}

private struct FooterStatCell: View {
    let value: String
    let label: String

    var body: some View {
        VStack(spacing: 2) {
            Text(value)
                .font(.system(size: 14.5, weight: .semibold, design: .rounded))
                .monospacedDigit()
                .lineLimit(1)
            Text(label)
                .font(.system(size: 10.5))
                .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity)
    }
}
