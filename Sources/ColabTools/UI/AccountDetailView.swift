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

    private var effectiveKind: WorkloadKind {
        account.workloadKind == .general ? (rt.detectedWorkloadKind ?? .general) : account.workloadKind
    }

    private var activeServicePort: Int {
        rt.detectedServicePort ?? account.remoteServicePort
    }

    var body: some View {
        let _ = store.queueTickVersion
        VStack(spacing: 9) {
            heroHeaderSection

            if case .queuing(let attempt, let maxAttempts, let nextRetryAt, let summary) = rt.phase {
                activeQueueBanner(attempt: attempt, maxAttempts: maxAttempts, nextRetryAt: nextRetryAt, summary: summary)
            } else if let until = rt.queueCircuitBreakUntil, until > Date() {
                circuitBreakerBanner(until: until)
            }

            if !orphanSessions.isEmpty {
                externalSessionBanner
            }

            telemetryGrid2x2
            quotaAndStrategyWideCard
            sessionsAndConfigTableCard
            bottomDockCard
        }
        .frame(maxWidth: .infinity, alignment: .leading)
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
        case .queuing: return "clock.arrow.circlepath"
        case .error: return "exclamationmark.triangle.fill"
        case .offline: return "moon.zzz.fill"
        }
    }

    private var heroStatusWord: String {
        switch rt.phase {
        case .running: return rt.isProcessing ? "推理中" : "运行良好"
        case .starting(let s): return s
        case .queuing(let attempt, let maxAttempts, _, _): return "排队 \(attempt)/\(maxAttempts)"
        case .stopping: return "关机中…"
        case .error: return "需检查"
        case .offline: return "已关闭"
        }
    }

    @ViewBuilder
    private var powerActionButton: some View {
        let isCoolingDown = (rt.queueCircuitBreakUntil ?? .distantPast) > Date()
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
        } else if rt.phase.isQueuing {
            Button {
                store.cancelQueue(for: account.id)
            } label: {
                Label("取消排队", systemImage: "xmark.circle")
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
            let queueBlocked = account.enablePoliteQueue && isCoolingDown
            Button {
                Task { await store.startAccountSession(account.id) }
            } label: {
                Label(account.enablePoliteQueue ? "排队启动" : "一键启动", systemImage: account.enablePoliteQueue ? "clock.arrow.circlepath" : "play.fill")
                    .font(.system(size: 12, weight: .semibold))
                    .foregroundStyle(queueBlocked ? Color.secondary : Color(nsColor: .windowBackgroundColor))
                    .padding(.horizontal, 14)
                    .padding(.vertical, 7)
                    .background(queueBlocked ? Color.primary.opacity(0.08) : Color.primary, in: RoundedRectangle(cornerRadius: 9, style: .continuous))
            }
            .buttonStyle(.plain)
            .disabled(queueBlocked)
        }
    }

    private func activeQueueBanner(attempt: Int, maxAttempts: Int, nextRetryAt: Date?, summary: String) -> some View {
        let countdownText: String = {
            guard let nextRetryAt else { return "正在探测目标库存…" }
            let rem = max(0, Int(ceil(nextRetryAt.timeIntervalSinceNow)))
            return String(format: "距下次探测 %02d:%02d", rem / 60, rem % 60)
        }()
        return HStack(spacing: 8) {
            Image(systemName: "clock.badge.checkmark")
                .font(.system(size: 13))
                .foregroundStyle(ColabTheme.amber)
            VStack(alignment: .leading, spacing: 2) {
                Text("合规排队中 · 第 \(attempt)/\(maxAttempts) 轮（\(summary)）")
                    .font(.system(size: 11.5, weight: .semibold))
                    .foregroundStyle(.primary)
                    .lineLimit(1)
                Text("\(countdownText) · 遇 412/429 自动熔断")
                    .font(.system(size: 10.5))
                    .monospacedDigit()
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
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

    private func circuitBreakerBanner(until: Date) -> some View {
        let remMin = max(1, Int(ceil(until.timeIntervalSinceNow / 60.0)))
        return HStack(spacing: 7) {
            Image(systemName: "hand.raised.fill")
                .font(.system(size: 12))
                .foregroundStyle(ColabTheme.coral)
            Text("已触发防风控硬熔断保护，排队功能冷却剩余约 \(remMin) 分钟。")
                .font(.system(size: 11))
                .foregroundStyle(.primary.opacity(0.88))
            Spacer()
        }
        .padding(.horizontal, 11)
        .padding(.vertical, 8)
        .background(ColabTheme.coral.opacity(0.11), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .strokeBorder(ColabTheme.coral.opacity(0.28), lineWidth: 0.6)
        )
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
                badgeText: rt.phase.isRunning
                    ? rt.shortGPUName
                    : (account.enablePoliteQueue ? "排队等待" : account.preferredAccelerator.shortChipLabel)
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
        let kind = effectiveKind
        let tint = ColabTheme.color(for: kind)

        return VStack(alignment: .leading, spacing: 8) {
            if kind == .llm {
                TileHeader(
                    icon: kind.iconName,
                    iconTint: tint,
                    title: kind.metricCardTitle,
                    badgeText: "\(rt.contextLimit / 1024)K"
                )

                BigMetricValue(
                    number: (rt.phase.isRunning && rt.serverHealthy) ? Formatters.tokens(rt.promptTokensUsed) : "—",
                    unit: (rt.phase.isRunning && rt.serverHealthy) ? "tok" : ""
                )

                HorizonBar(progress: rt.serverHealthy ? rt.contextRatio : 0, tint: tint)
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
            } else if !rt.workloadMetricValue.isEmpty {
                // Open `/tmp/colab-tools-status.json` custom metric display (video steps / music tracks / audio latency)
                TileHeader(
                    icon: kind.iconName,
                    iconTint: tint,
                    title: rt.workloadMetricLabel.isEmpty ? kind.metricCardTitle : rt.workloadMetricLabel,
                    badgeText: ":\(activeServicePort)"
                )

                BigMetricValue(
                    number: rt.phase.isRunning ? rt.workloadMetricValue : "—",
                    unit: rt.phase.isRunning ? rt.workloadMetricUnit : ""
                )

                HorizonBar(
                    progress: rt.phase.isRunning ? (rt.workloadMetricRatio ?? (rt.isProcessing ? 0.65 : 0.08)) : 0,
                    tint: tint
                )
                .padding(.vertical, 5)

                Spacer(minLength: 0)

                Text(
                    rt.workloadDetail.isEmpty
                        ? "CPU \(rt.cpuUtilPercent)% · CUDA \(rt.cudaProcessCount) 进程"
                        : rt.workloadDetail
                )
                .font(.system(size: 11))
                .monospacedDigit()
                .foregroundStyle(.secondary)
                .lineLimit(1)
            } else if kind == .video || kind == .image {
                let totalJobs = rt.queueRunning + rt.queuePending
                TileHeader(
                    icon: kind.iconName,
                    iconTint: tint,
                    title: kind.metricCardTitle,
                    badgeText: ":\(activeServicePort)"
                )

                BigMetricValue(
                    number: rt.phase.isRunning ? "\(totalJobs)" : "—",
                    unit: rt.phase.isRunning ? "任务" : ""
                )

                HorizonBar(
                    progress: rt.phase.isRunning ? (totalJobs > 0 ? min(1.0, Double(totalJobs) / 5.0) : Double(rt.cpuUtilPercent) / 100.0) : 0,
                    tint: tint
                )
                .padding(.vertical, 5)

                Spacer(minLength: 0)

                Text(
                    rt.phase.isRunning
                        ? "运行 \(rt.queueRunning) · 排队 \(rt.queuePending) · CPU \(rt.cpuUtilPercent)%"
                        : "支持 ComfyUI / 状态协议"
                )
                .font(.system(size: 11))
                .monospacedDigit()
                .foregroundStyle(.secondary)
                .lineLimit(1)
            } else {
                // Audio / Music / General compute mode
                TileHeader(
                    icon: kind.iconName,
                    iconTint: tint,
                    title: kind.metricCardTitle,
                    badgeText: kind == .general ? (account.highMem ? "High-RAM" : "Std") : ":\(activeServicePort)"
                )

                BigMetricValue(
                    number: rt.phase.isRunning ? "\(rt.cpuUtilPercent)" : "—",
                    unit: rt.phase.isRunning ? "% CPU" : ""
                )

                HorizonBar(
                    progress: rt.phase.isRunning ? min(1.0, Double(rt.cpuUtilPercent) / 100.0) : 0,
                    tint: tint
                )
                .padding(.vertical, 5)

                Spacer(minLength: 0)

                Text(
                    rt.phase.isRunning
                        ? "CUDA \(rt.cudaProcessCount) 进程 · \(rt.serverHealthy ? "服务在线" : "端口 :\(activeServicePort)")"
                        : "\(kind.displayName) · 端口 :\(activeServicePort)"
                )
                .font(.system(size: 11))
                .monospacedDigit()
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
            if account.enableLLMModule { return "等待 /slots 信号" }
            return account.enableHardwareIdleFallback ? "等待 GPU+CPU 采样" : "已关闭硬件兜底"
        }
        let src = rt.signalSource == .none ? "" : " · \(rt.signalSource.shortLabel)"
        if rt.isProcessing { return "活跃中\(src)" }
        return "空闲 \(Formatters.shortMinutes(rt.idleSeconds))\(src)"
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

                if account.enablePoliteQueue && !rt.phase.isRunning {
                    let cleanTargets = PoliteQueuePolicy.sanitizedTargets(selected: account.queueTargets, for: account.tier)
                    CardBadge(
                        text: "顺位 \(PoliteQueuePolicy.targetsSummary(cleanTargets))",
                        tint: ColabTheme.amber
                    )
                } else {
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
                    .disabled(rt.phase.isRunning || rt.phase.isBusy || rt.phase.isQueuing)
                }
            }

            HStack(spacing: 10) {
                Menu {
                    ForEach(WorkloadKind.allCases) { kind in
                        Button {
                            var copy = account
                            copy.workloadKind = kind
                            store.updateAccount(copy)
                        } label: {
                            HStack {
                                Text("\(kind.displayName) (:\(String(kind.defaultRemotePort)))")
                                if kind == account.workloadKind { Image(systemName: "checkmark") }
                            }
                        }
                    }
                } label: {
                    HStack(spacing: 4) {
                        Image(systemName: effectiveKind.iconName)
                            .font(.system(size: 10))
                            .foregroundStyle(ColabTheme.color(for: effectiveKind))
                        Text(account.workloadKind.displayName)
                            .font(.system(size: 10.5, weight: .medium))
                        Image(systemName: "chevron.up.chevron.down")
                            .font(.system(size: 8))
                            .opacity(0.65)
                    }
                    .padding(.horizontal, 7)
                    .padding(.vertical, 3)
                    .background(Color.primary.opacity(0.065), in: RoundedRectangle(cornerRadius: 6, style: .continuous))
                }
                .menuStyle(.borderlessButton)
                .menuIndicator(.hidden)
                .fixedSize()
                .disabled(rt.phase.isBusy || rt.phase.isQueuing)

                if account.tier.supportsHighMem {
                    Toggle("高内存", isOn: Binding(
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
                    .disabled(rt.phase.isRunning || rt.phase.isBusy || rt.phase.isQueuing)
                }

                Toggle("缺卡排队", isOn: Binding(
                    get: { account.enablePoliteQueue },
                    set: { v in
                        var copy = account
                        copy.enablePoliteQueue = v
                        store.updateAccount(copy)
                    }
                ))
                .font(.system(size: 11))
                .foregroundStyle(account.enablePoliteQueue ? ColabTheme.amber : .secondary)
                .controlSize(.mini)
                .disabled(rt.phase.isRunning || rt.phase.isBusy || rt.phase.isQueuing)

                Spacer(minLength: 0)
            }

            if account.enablePoliteQueue {
                Divider().opacity(0.32)
                VStack(alignment: .leading, spacing: 6) {
                    let allowed = account.tier.queueableAccelerators
                    let selectedSet = Set(PoliteQueuePolicy.sanitizedTargets(selected: account.queueTargets, for: account.tier))
                    HStack(spacing: 5) {
                        ForEach(allowed) { opt in
                            let isOn = selectedSet.contains(opt)
                            Button {
                                store.toggleQueueTarget(opt, for: account.id)
                            } label: {
                                Text(opt.shortChipLabel)
                                    .font(.system(size: 10.5, weight: isOn ? .semibold : .medium))
                                    .monospacedDigit()
                                    .lineLimit(1)
                                    .padding(.horizontal, 7)
                                    .padding(.vertical, 3.5)
                                    .background(
                                        isOn ? ColabTheme.amber.opacity(0.20) : Color.primary.opacity(0.055),
                                        in: RoundedRectangle(cornerRadius: 6, style: .continuous)
                                    )
                                    .overlay(
                                        RoundedRectangle(cornerRadius: 6, style: .continuous)
                                            .strokeBorder(isOn ? ColabTheme.amber.opacity(0.50) : Color.primary.opacity(0.08), lineWidth: 0.6)
                                    )
                                    .foregroundStyle(isOn ? .primary : .secondary)
                            }
                            .buttonStyle(.plain)
                            .disabled(rt.phase.isRunning || rt.phase.isBusy || rt.phase.isQueuing)
                        }
                        Spacer(minLength: 0)
                    }

                    Text("可单选死磕或多选按高到低顺位等待 · 最多 5 轮/40m · 遇 412/429 自动熔断")
                        .font(.system(size: 10))
                        .foregroundStyle(.tertiary)
                        .lineLimit(1)
                }
            }
        }
        .padding(11)
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

            if effectiveKind == .llm {
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

                HStack(spacing: 6) {
                    Text("CC 渠道")
                        .font(.system(size: 11.5))
                        .foregroundStyle(.secondary)
                        .frame(width: 48, alignment: .leading)
                    Text(account.ccSwitchProviderName)
                        .font(.system(size: 11))
                        .lineLimit(1)
                        .truncationMode(.tail)
                    Spacer()
                    CardBadge(
                        text: rt.ccSwitchSynced ? "已就绪 · 官方安全" : "待机未同步",
                        tint: rt.ccSwitchSynced ? ColabTheme.emerald : nil
                    )
                }
            } else {
                Divider().opacity(0.35)

                HStack(spacing: 6) {
                    Text("负载")
                        .font(.system(size: 11.5))
                        .foregroundStyle(.secondary)
                        .frame(width: 48, alignment: .leading)
                    Text(rt.workloadName.isEmpty ? effectiveKind.displayName : rt.workloadName)
                        .font(.system(size: 11.5, weight: .medium))
                        .lineLimit(1)
                        .truncationMode(.middle)
                    Spacer()
                    CardBadge(
                        text: rt.serverHealthy ? "\(effectiveKind.shortBadge) · 在线" : effectiveKind.shortBadge,
                        tint: rt.serverHealthy ? ColabTheme.emerald : ColabTheme.color(for: effectiveKind)
                    )
                }

                let endpointText: String = {
                    if !rt.publicURL.isEmpty { return rt.publicURL }
                    if rt.sshTunnelActive { return "http://127.0.0.1:\(String(account.localSSHTunnelPort))" }
                    return "远端端口 :\(String(activeServicePort))（可开隧道）"
                }()
                let copyableEndpoint: String = !rt.publicURL.isEmpty
                    ? rt.publicURL
                    : (rt.sshTunnelActive ? "http://127.0.0.1:\(String(account.localSSHTunnelPort))" : "")

                HStack(spacing: 6) {
                    Text("端点")
                        .font(.system(size: 11.5))
                        .foregroundStyle(.secondary)
                        .frame(width: 48, alignment: .leading)
                    Text(endpointText)
                        .font(.system(size: 11, design: .monospaced))
                        .foregroundStyle(copyableEndpoint.isEmpty ? .tertiary : .primary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                    Spacer()
                    Button {
                        store.copyToClipboard(copyableEndpoint, label: "服务访问地址")
                    } label: {
                        Image(systemName: "doc.on.doc").font(.system(size: 11.5))
                    }
                    .buttonStyle(.borderless)
                    .disabled(copyableEndpoint.isEmpty)
                    .help("复制访问地址")
                }

                HStack(spacing: 6) {
                    Text("守护")
                        .font(.system(size: 11.5))
                        .foregroundStyle(.secondary)
                        .frame(width: 48, alignment: .leading)
                    Text(rt.signalSource == .none ? (account.enableHardwareIdleFallback ? "GPU+CPU 双低载" : "仅手动关机") : rt.signalSource.shortLabel)
                        .font(.system(size: 11))
                        .lineLimit(1)
                    Spacer()
                    CardBadge(text: "CPU \(rt.cpuUtilPercent)% · CUDA \(rt.cudaProcessCount)")
                }
            }
        }
        .padding(11)
        .hudCard()
    }

    // MARK: - 5. Bottom Utility Strip + 3-Column Big Summary
    private var bottomDockCard: some View {
        VStack(spacing: 10) {
            if effectiveKind == .llm {
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
                HStack(spacing: 6) {
                    QuickActionButton(
                        title: effectiveKind.webUIButtonTitle,
                        icon: "safari",
                        disabled: !rt.phase.isRunning
                    ) {
                        store.openWorkloadWebUI(accountId: account.id)
                    }

                    QuickActionButton(
                        title: rt.sshTunnelActive ? "断隧道" : "隧道",
                        icon: rt.sshTunnelActive ? "lock.fill" : "arrow.left.arrow.right",
                        disabled: !rt.phase.isRunning && !rt.sshTunnelActive,
                        active: rt.sshTunnelActive
                    ) {
                        store.toggleSSHTunnel(for: account.id)
                    }

                    QuickActionButton(
                        title: "SSH",
                        icon: "terminal",
                        disabled: !rt.phase.isRunning
                    ) {
                        store.openRemoteSSHShell(accountId: account.id)
                    }

                    QuickActionButton(title: "续费", icon: "creditcard") {
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
                    value: effectiveKind == .llm
                        ? "\(rt.contextLimit / 1024)K"
                        : (rt.phase.isRunning ? "\(rt.cpuUtilPercent)%" : ":\(activeServicePort)"),
                    label: effectiveKind == .llm ? "上下文上限" : (rt.phase.isRunning ? "整机 CPU" : "服务端口")
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
