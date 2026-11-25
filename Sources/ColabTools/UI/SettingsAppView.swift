import AppKit
import ColabToolsCore
import ServiceManagement
import SwiftUI

struct SettingsAppView: View {
    @ObservedObject var store: WorkspaceStore
    var isPreviewMode: Bool = false

    @State private var loginItemStatus: SMAppService.Status = .notRegistered
    @State private var selectedCopyProtocol: CopyProtocolKind = .openAI
    @State private var selectedInsightHW: String = ""
    @State private var showingAddAccountSheet: Bool = false
    @State private var remotePortDraft: String = ""
    @State private var localPortDraft: String = ""

    private var selectedAccount: AccountProfile? {
        store.selectedAccount
    }

    var body: some View {
        let _ = store.queueTickVersion
        HStack(alignment: .top, spacing: 0) {
            sidebar
            Divider().opacity(0.45)
            detailCanvas
        }
        .frame(
            minWidth: isPreviewMode ? 820 : 720,
            idealWidth: 820,
            maxWidth: isPreviewMode ? 820 : .infinity,
            minHeight: isPreviewMode ? 600 : 520,
            idealHeight: isPreviewMode ? nil : 640,
            maxHeight: isPreviewMode ? nil : .infinity,
            alignment: .topLeading
        )
        .overlay(alignment: .bottomTrailing) {
            if let toast = store.toastMessage {
                HStack(spacing: 6) {
                    Image(systemName: "checkmark.circle.fill")
                        .font(.system(size: 12))
                        .foregroundStyle(ColabTheme.emerald)
                    Text(toast)
                        .font(.system(size: 11.5, weight: .medium))
                        .lineLimit(1)
                }
                .padding(.horizontal, 12)
                .padding(.vertical, 8)
                .background(.regularMaterial, in: Capsule())
                .overlay(Capsule().strokeBorder(Color.primary.opacity(0.12), lineWidth: 0.6))
                .shadow(color: .black.opacity(0.16), radius: 10, y: 4)
                .padding(16)
                .transition(.opacity)
            }
        }
        .sheet(isPresented: $showingAddAccountSheet) {
            AddAccountSheetView(store: store) {
                showingAddAccountSheet = false
            }
            .padding(18)
            .frame(width: 400)
        }
        .onAppear {
            refreshLoginItemStatus()
            syncPortDrafts()
        }
        .onChange(of: store.settings.selectedAccountId) { _, _ in
            syncPortDrafts()
        }
        .onChange(of: selectedAccount?.remoteServicePort) { _, _ in
            syncPortDrafts()
        }
        .onChange(of: selectedAccount?.localSSHTunnelPort) { _, _ in
            syncPortDrafts()
        }
        .onReceive(NotificationCenter.default.publisher(for: NSWindow.didBecomeKeyNotification)) { _ in
            refreshLoginItemStatus()
        }
    }

    // MARK: - Sidebar
    private var sidebar: some View {
        VStack(alignment: .leading, spacing: 6) {
            // App identity header (leaves clean breathing room under traffic lights)
            HStack(spacing: 9) {
                appIconView(size: 28, radius: 7)
                VStack(alignment: .leading, spacing: 1) {
                    Text("Colab Tools")
                        .font(.system(size: 13.5, weight: .semibold))
                    Text("v0.3.5 偏好设置")
                        .font(.system(size: 10.5))
                        .foregroundStyle(.secondary)
                }
            }
            .padding(.horizontal, 12)
            .padding(.top, isPreviewMode ? 18 : 34)
            .padding(.bottom, 10)

            // Account context switcher inside Settings window
            if store.settings.accounts.count >= 2 {
                Menu {
                    ForEach(store.settings.accounts) { acc in
                        Button {
                            store.settings.selectedAccountId = acc.id
                        } label: {
                            HStack {
                                Text("\(acc.displayName) · \(acc.tier.shortBadge)")
                                if acc.id == selectedAccount?.id { Image(systemName: "checkmark") }
                            }
                        }
                    }
                } label: {
                    HStack(spacing: 6) {
                        Circle()
                            .fill(ColabTheme.color(for: selectedAccount?.colorTag ?? .amber))
                            .frame(width: 7, height: 7)
                        Text(selectedAccount?.displayName ?? "选择账号")
                            .font(.system(size: 11.5, weight: .medium))
                            .lineLimit(1)
                        Spacer()
                        if let tier = selectedAccount?.tier {
                            Text(tier.shortBadge)
                                .font(.system(size: 9.5, weight: .medium))
                                .foregroundStyle(.secondary)
                        }
                        Image(systemName: "chevron.up.chevron.down")
                            .font(.system(size: 8.5, weight: .semibold))
                            .foregroundStyle(.secondary)
                    }
                }
                .menuStyle(.borderlessButton)
                .menuIndicator(.hidden)
                .padding(.horizontal, 10)
                .padding(.vertical, 6)
                .background(Color.primary.opacity(0.055), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: 8, style: .continuous).strokeBorder(Color.primary.opacity(0.08), lineWidth: 0.5))
                .padding(.horizontal, 10)
                .padding(.bottom, 6)
            } else if let acc = selectedAccount {
                HStack(spacing: 6) {
                    Circle()
                        .fill(ColabTheme.color(for: acc.colorTag))
                        .frame(width: 7, height: 7)
                    Text(acc.displayName)
                        .font(.system(size: 11.5, weight: .medium))
                        .lineLimit(1)
                    Spacer()
                    CardBadge(text: acc.tier.shortBadge)
                }
                .padding(.horizontal, 10)
                .padding(.vertical, 5)
                .background(Color.primary.opacity(0.045), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
                .padding(.horizontal, 10)
                .padding(.bottom, 6)
            }

            VStack(spacing: 2) {
                ForEach(WorkspaceStore.SettingsSection.allCases) { sec in
                    sidebarRow(sec)
                }
            }
            .padding(.horizontal, 8)

            Spacer()

            HStack(spacing: 5) {
                Image(systemName: "checkmark.shield")
                    .font(.system(size: 10.5))
                    .foregroundStyle(ColabTheme.emerald)
                Text("所有修改即时生效")
                    .font(.system(size: 10.5))
                    .foregroundStyle(.tertiary)
            }
            .padding(.horizontal, 14)
            .padding(.bottom, 14)
        }
        .frame(width: 192)
        .frame(maxHeight: .infinity)
        .background(Color.primary.opacity(0.025))
    }

    private func sidebarRow(_ sec: WorkspaceStore.SettingsSection) -> some View {
        let isSelected = store.selectedSettingsSection == sec
        return Button {
            withAnimation(.snappy(duration: 0.14)) {
                store.selectedSettingsSection = sec
            }
        } label: {
            HStack(spacing: 9) {
                Image(systemName: sec.iconName)
                    .font(.system(size: 12, weight: .semibold))
                    .foregroundStyle(.white)
                    .frame(width: 22, height: 22)
                    .background(sectionColor(sec).opacity(isSelected ? 1.0 : 0.78), in: RoundedRectangle(cornerRadius: 6, style: .continuous))

                Text(sec.rawValue)
                    .font(.system(size: 12.5, weight: isSelected ? .semibold : .regular))
                    .foregroundStyle(isSelected ? .primary : .secondary)

                Spacer()
            }
            .padding(.horizontal, 8)
            .padding(.vertical, 6)
            .background(
                isSelected ? Color.primary.opacity(0.09) : Color.clear,
                in: RoundedRectangle(cornerRadius: 8, style: .continuous)
            )
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }

    private func sectionColor(_ sec: WorkspaceStore.SettingsSection) -> Color {
        switch sec {
        case .general: return ColabTheme.sky
        case .compute: return ColabTheme.amber
        case .integrations: return ColabTheme.emerald
        case .accounts: return ColabTheme.cyan
        case .advanced: return Color(red: 0.48, green: 0.52, blue: 0.58)
        case .about: return ColabTheme.indigo
        }
    }

    // MARK: - Detail Canvas
    @ViewBuilder
    private var detailCanvas: some View {
        let content = VStack(alignment: .leading, spacing: 14) {
            switch store.selectedSettingsSection {
            case .general: generalPage
            case .compute: computeAndQueuePage
            case .integrations: integrationsPage
            case .accounts: accountsPage
            case .advanced: advancedPage
            case .about: aboutPage
            }
        }
        .padding(.horizontal, 24)
        .padding(.top, isPreviewMode ? 20 : 28)
        .padding(.bottom, 24)
        .frame(maxWidth: .infinity, alignment: .topLeading)

        if isPreviewMode {
            content
                .frame(maxWidth: .infinity, alignment: .topLeading)
        } else {
            ScrollView(.vertical, showsIndicators: true) {
                content
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }

    private func pageHeader(title: String, subtitle: String) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            Text(title)
                .font(.system(size: 19, weight: .semibold))
            Text(subtitle)
                .font(.system(size: 12))
                .foregroundStyle(.secondary)
        }
        .padding(.bottom, 2)
    }

    private func settingsSectionCard<Content: View>(title: String, trailing: String? = nil, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text(title)
                    .font(.system(size: 12.5, weight: .semibold))
                    .foregroundStyle(.primary.opacity(0.9))
                Spacer()
                if let trailing, !trailing.isEmpty {
                    Text(trailing)
                        .font(.system(size: 11))
                        .foregroundStyle(.tertiary)
                }
            }
            content()
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .hudCard(cornerRadius: 12)
    }

    // MARK: - Page 1: General
    @ViewBuilder
    private var generalPage: some View {
        pageHeader(title: "通用", subtitle: "控制开机行为、菜单栏外观与防忘关自动停机。")

        settingsSectionCard(title: "应用与菜单栏") {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 6) {
                        Text("开机自动启动")
                            .font(.system(size: 12.5, weight: .medium))
                        if loginItemStatus == .requiresApproval {
                            CardBadge(text: "待系统批准", tint: ColabTheme.amber)
                        }
                    }
                    Text(
                        loginItemStatus == .requiresApproval
                            ? "已提交开机自启申请，请在 macOS「系统设置 → 登录项」中允许启用"
                            : "登录 macOS 后自动常驻顶部菜单栏"
                    )
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                }
                Spacer()
                if loginItemStatus == .requiresApproval {
                    Button("打开登录项设置") {
                        SMAppService.openSystemSettingsLoginItems()
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                }
                Toggle("", isOn: Binding(
                    get: { loginItemStatus == .enabled || loginItemStatus == .requiresApproval },
                    set: { toggleLaunchAtLogin($0) }
                ))
                .toggleStyle(.switch)
                .labelsHidden()
                .controlSize(.small)
            }

            Divider().opacity(0.35)

            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text("菜单栏显示信息")
                        .font(.system(size: 12.5, weight: .medium))
                    Text("在顶部状态栏实时查看显卡与剩余空闲时间")
                        .font(.system(size: 11))
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Picker("", selection: $store.settings.menuBarMode) {
                    ForEach(MenuBarDisplayMode.allCases) { mode in
                        Text(mode.label).tag(mode)
                    }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .controlSize(.small)
                .frame(width: 220)
            }

            Divider().opacity(0.35)

            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text("桌面通知提醒")
                        .font(.system(size: 12.5, weight: .medium))
                    Text("排到卡、预约触发、空闲预警与自动关机时推送通知（计费止损警告始终保留）")
                        .font(.system(size: 11))
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Toggle("", isOn: $store.settings.enableNotifications)
                    .toggleStyle(.switch)
                    .labelsHidden()
                    .controlSize(.small)
            }
        }

        if let acc = selectedAccount {
            settingsSectionCard(title: "空闲自动关机（防止忘记关机浪费点数）", trailing: acc.displayName) {
                HStack {
                    VStack(alignment: .leading, spacing: 2) {
                        Text("开启空闲自动关机")
                            .font(.system(size: 12.5, weight: .medium))
                        Text("确认长时间无任务运行后自动释放实例停止计费")
                            .font(.system(size: 11))
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
                    Toggle("", isOn: binding(for: acc, \.autoShutdownEnabled))
                        .toggleStyle(.switch)
                        .tint(ColabTheme.emerald)
                        .labelsHidden()
                        .controlSize(.small)
                }

                if acc.autoShutdownEnabled {
                    Divider().opacity(0.35)

                    HStack(spacing: 8) {
                        Text("连续空闲多久关机")
                            .font(.system(size: 12))
                        Spacer()
                        HStack(spacing: 5) {
                            ForEach([15, 30, 40, 60], id: \.self) { m in
                                let active = acc.idleTimeoutMinutes == m
                                Button {
                                    var copy = acc
                                    copy.idleTimeoutMinutes = m
                                    store.updateAccount(copy)
                                } label: {
                                    Text("\(m) 分钟")
                                        .font(.system(size: 11, weight: active ? .semibold : .regular))
                                        .monospacedDigit()
                                        .padding(.horizontal, 8)
                                        .padding(.vertical, 4)
                                        .background(
                                            active ? ColabTheme.amber.opacity(0.20) : Color.primary.opacity(0.055),
                                            in: RoundedRectangle(cornerRadius: 6, style: .continuous)
                                        )
                                        .foregroundStyle(active ? .primary : .secondary)
                                }
                                .buttonStyle(.plain)
                            }
                        }
                        TextField("", value: binding(for: acc, \.idleTimeoutMinutes), format: .number.grouping(.never))
                            .textFieldStyle(.roundedBorder)
                            .frame(width: 52)
                            .multilineTextAlignment(.trailing)
                            .controlSize(.small)
                        Text("分")
                            .font(.system(size: 11))
                            .foregroundStyle(.secondary)
                    }

                    HStack {
                        Text("关机前提前提醒")
                            .font(.system(size: 12))
                        Spacer()
                        Stepper(value: binding(for: acc, \.warnBeforeMinutes), in: 0...15) {
                            Text(acc.warnBeforeMinutes == 0 ? "不提前提醒" : "提前 \(acc.warnBeforeMinutes) 分钟")
                                .font(.system(size: 12))
                                .monospacedDigit()
                        }
                        .controlSize(.small)
                    }

                    if !acc.enableLLMModule {
                        Divider().opacity(0.35)
                        HStack {
                            VStack(alignment: .leading, spacing: 2) {
                                Text("无应用信号时启用硬件双低载检测")
                                    .font(.system(size: 12))
                                Text("仅在 GPU < 5% 且整机 CPU < 25% 同时满足时计时，避免下载权重或转码时误关")
                                    .font(.system(size: 10.5))
                                    .foregroundStyle(.secondary)
                            }
                            Spacer()
                            Toggle("", isOn: binding(for: acc, \.enableHardwareIdleFallback))
                                .toggleStyle(.switch)
                                .labelsHidden()
                                .controlSize(.small)
                        }
                    }
                }
            }
        }
    }

    // MARK: - Page 2: Compute & Queue Workbench
    @ViewBuilder
    private var computeAndQueuePage: some View {
        pageHeader(title: "算力与抢卡", subtitle: "选好目标显卡顺位、保底降级与清晨错峰预约，让排队更省心且不触发官方风控。")

        if let acc = selectedAccount {
            let rt = store.state(for: acc.id)
            let locked = rt.phase.isRunning || rt.phase.isBusy || rt.phase.isQueuing
            let primaryTargets = PoliteQueuePolicy.sanitizedTargets(selected: acc.queueTargets, for: acc.tier)

            // 1. Target Hardware & Queue Mode
            settingsSectionCard(title: "目标显卡与缺货排队", trailing: acc.tier.displayName) {
                HStack {
                    VStack(alignment: .leading, spacing: 2) {
                        Text("目标显卡缺货时自动排队")
                            .font(.system(size: 12.5, weight: .medium))
                        Text("遇到官方暂无库存（503）自动按合规节奏重试，不必反复手动点击启动")
                            .font(.system(size: 11))
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
                    Toggle("", isOn: binding(for: acc, \.enablePoliteQueue))
                        .toggleStyle(.switch)
                        .tint(ColabTheme.amber)
                        .labelsHidden()
                        .controlSize(.small)
                        .disabled(locked)
                }

                Divider().opacity(0.35)

                if acc.enablePoliteQueue {
                    HStack {
                        Text("点击卡片勾选目标显卡，用 ↑ ↓ 调整尝试顺位")
                            .font(.system(size: 11.5))
                            .foregroundStyle(.secondary)
                        Spacer()
                        CardBadge(text: "顺位 \(PoliteQueuePolicy.targetsSummary(primaryTargets))", tint: ColabTheme.amber)
                    }

                    let allowed = acc.tier.queueableAccelerators
                    let cols = [GridItem(.flexible(), spacing: 9), GridItem(.flexible(), spacing: 9)]
                    LazyVGrid(columns: cols, spacing: 9) {
                        ForEach(allowed) { opt in
                            let rankIdx = primaryTargets.firstIndex(of: opt)
                            hardwareTargetCard(
                                option: opt,
                                rankIndex: rankIdx,
                                totalSelected: primaryTargets.count,
                                locked: locked,
                                accountId: acc.id
                            )
                        }
                    }

                    if acc.tier.supportsHighMem {
                        HStack {
                            Toggle("申请时同时请求高内存形态（High-RAM）", isOn: binding(for: acc, \.highMem))
                                .font(.system(size: 11.5))
                                .foregroundStyle(.secondary)
                                .toggleStyle(.checkbox)
                                .controlSize(.small)
                                .disabled(locked)
                            Spacer()
                        }
                        .padding(.top, 2)
                    }
                } else {
                    HStack {
                        VStack(alignment: .leading, spacing: 2) {
                            Text("单次启动默认显卡")
                                .font(.system(size: 12))
                            Text("未开启排队时，点击启动后直接按此选项尝试分配一次")
                                .font(.system(size: 11))
                                .foregroundStyle(.secondary)
                        }
                        Spacer()
                        Picker("", selection: binding(for: acc, \.preferredAccelerator)) {
                            ForEach(acc.tier.allowedAccelerators) { opt in
                                Text(opt.label).tag(opt)
                            }
                        }
                        .labelsHidden()
                        .controlSize(.small)
                        .frame(width: 230)
                        .disabled(locked)
                    }
                    if acc.tier.supportsHighMem {
                        Toggle("请求高内存形态（High-RAM）", isOn: binding(for: acc, \.highMem))
                            .font(.system(size: 11.5))
                            .foregroundStyle(.secondary)
                            .toggleStyle(.checkbox)
                            .controlSize(.small)
                            .disabled(locked)
                    }
                }
            }

            // 2. Two-Stage Auto Fallback
            if acc.enablePoliteQueue && acc.tier.queueableAccelerators.count >= 2 {
                let eligibleFallbacks = PoliteQueuePolicy.eligibleFallbackTargets(excluding: primaryTargets, for: acc.tier)
                settingsSectionCard(title: "两阶段自动降级（先冲高配，再保底）") {
                    HStack {
                        VStack(alignment: .leading, spacing: 2) {
                            Text("前几轮专注抢主力卡，仍缺货时自动加入保底卡")
                                .font(.system(size: 12.5, weight: .medium))
                            Text(
                                eligibleFallbacks.isEmpty
                                    ? "当前订阅下所有可用硬件已全部加入主力顺位，如需独立保底请先取消勾选部分主力卡"
                                    : "既不错过稀缺旗舰卡，又避免苦等 40 分钟一无所获"
                            )
                            .font(.system(size: 11))
                            .foregroundStyle(.secondary)
                        }
                        Spacer()
                        Toggle("", isOn: binding(for: acc, \.queueFallbackEnabled))
                            .toggleStyle(.switch)
                            .tint(ColabTheme.amber)
                            .labelsHidden()
                            .controlSize(.small)
                            .disabled(locked || eligibleFallbacks.isEmpty)
                    }

                    if acc.queueFallbackEnabled && !eligibleFallbacks.isEmpty {
                        Divider().opacity(0.35)
                        HStack(spacing: 8) {
                            Text("前")
                                .font(.system(size: 12))
                            Picker("", selection: binding(for: acc, \.queueFallbackAfterRounds)) {
                                ForEach(1..<PoliteQueuePolicy.maxAttempts, id: \.self) { r in
                                    Text("\(r) 轮").tag(r)
                                }
                            }
                            .labelsHidden()
                            .controlSize(.small)
                            .frame(width: 68)
                            .disabled(locked)

                            Text("仅尝试主力卡，第 \(acc.queueFallbackAfterRounds + 1) 轮起加入保底")
                                .font(.system(size: 12))
                                .foregroundStyle(.secondary)

                            Picker("", selection: binding(for: acc, \.queueFallbackTarget)) {
                                ForEach(eligibleFallbacks) { opt in
                                    Text("\(opt.shortChipLabel) (\(opt.roleTag))").tag(opt)
                                }
                            }
                            .labelsHidden()
                            .controlSize(.small)
                            .frame(width: 138)
                            .disabled(locked)

                            Spacer()
                        }
                    }
                }
            }

            // 3. Off-Peak Scheduled Start
            settingsSectionCard(title: "错峰定时预约（推荐清晨 06:00–08:00 低峰）") {
                HStack {
                    VStack(alignment: .leading, spacing: 2) {
                        Text("按指定时间自动启动申请")
                            .font(.system(size: 12.5, weight: .medium))
                        Text("避开欧美工作高峰，到点自动唤醒并按上方策略拿卡（错过超过 10 分钟自动过期不误启）")
                            .font(.system(size: 11))
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
                    Toggle("", isOn: binding(for: acc, \.scheduledStartEnabled))
                        .toggleStyle(.switch)
                        .tint(ColabTheme.emerald)
                        .labelsHidden()
                        .controlSize(.small)
                }

                if acc.scheduledStartEnabled {
                    Divider().opacity(0.35)

                    HStack(spacing: 10) {
                        Text("预约时间")
                            .font(.system(size: 12))

                        HStack(spacing: 6) {
                            Picker("", selection: binding(for: acc, \.scheduledStartHour)) {
                                ForEach(0..<24, id: \.self) { h in
                                    Text(String(format: "%02d 时", h)).tag(h)
                                }
                            }
                            .labelsHidden()
                            .controlSize(.small)
                            .frame(width: 74)

                            Picker("", selection: binding(for: acc, \.scheduledStartMinute)) {
                                ForEach([0, 15, 30, 45], id: \.self) { m in
                                    Text(String(format: "%02d 分", m)).tag(m)
                                }
                            }
                            .labelsHidden()
                            .controlSize(.small)
                            .frame(width: 74)
                        }

                        Spacer()

                        Picker("", selection: binding(for: acc, \.scheduledStartRepeatDaily)) {
                            Text("仅一次").tag(false)
                            Text("每天重复").tag(true)
                        }
                        .pickerStyle(.segmented)
                        .labelsHidden()
                        .controlSize(.small)
                        .frame(width: 140)
                    }

                    HStack(spacing: 6) {
                        Text("低峰快捷")
                            .font(.system(size: 11))
                            .foregroundStyle(.secondary)
                        ForEach([(6, 30, "06:30"), (7, 0, "07:00"), (7, 30, "07:30")], id: \.2) { h, m, label in
                            let active = acc.scheduledStartHour == h && acc.scheduledStartMinute == m
                            Button {
                                var copy = acc
                                copy.scheduledStartHour = h
                                copy.scheduledStartMinute = m
                                store.updateAccount(copy)
                            } label: {
                                Text(label)
                                    .font(.system(size: 11, weight: active ? .semibold : .regular))
                                    .monospacedDigit()
                                    .fixedSize(horizontal: true, vertical: false)
                                    .padding(.horizontal, 8)
                                    .padding(.vertical, 3.5)
                                    .background(
                                        active ? ColabTheme.emerald.opacity(0.18) : Color.primary.opacity(0.055),
                                        in: RoundedRectangle(cornerRadius: 6, style: .continuous)
                                    )
                                    .foregroundStyle(active ? .primary : .secondary)
                            }
                            .buttonStyle(.plain)
                        }
                        Spacer()
                        let nextDate = acc.scheduledNextFireAt ?? PoliteQueuePolicy.nextScheduledFireDate(hour: acc.scheduledStartHour, minute: acc.scheduledStartMinute)
                        HStack(spacing: 4) {
                            Image(systemName: "alarm")
                                .font(.system(size: 10.5))
                                .foregroundStyle(ColabTheme.emerald)
                            Text("下次：\(nextDate.formatted(date: .abbreviated, time: .shortened))")
                                .font(.system(size: 11))
                                .monospacedDigit()
                                .foregroundStyle(.secondary)
                        }
                    }
                }
            }

            // 4. Local Allocation Hit-Rate Heatmap
            let legalInsightKeys = acc.tier.queueableAccelerators.map { AllocationInsights.normalizeHardwareKey($0.shortChipLabel) }
            let defaultHW = primaryTargets.first?.shortChipLabel ?? (acc.tier.queueableAccelerators.first?.shortChipLabel ?? "G4")
            let focusHW: String = {
                if !selectedInsightHW.isEmpty,
                   legalInsightKeys.contains(AllocationInsights.normalizeHardwareKey(selectedInsightHW)) {
                    return selectedInsightHW
                }
                return defaultHW
            }()
            let slots = store.insights.sixSlotHeatmap(for: focusHW)
            let recText = store.insights.recommendedWindowSummary(for: focusHW)

            settingsSectionCard(title: "本机时段命中率参考（纯本地统计 · 绝不上传）") {
                HStack(spacing: 6) {
                    ScrollView(.horizontal, showsIndicators: false) {
                        HStack(spacing: 5) {
                            ForEach(acc.tier.queueableAccelerators) { opt in
                                let key = opt.shortChipLabel
                                let isSel = AllocationInsights.normalizeHardwareKey(focusHW) == AllocationInsights.normalizeHardwareKey(key)
                                Button {
                                    selectedInsightHW = key
                                } label: {
                                    Text(key)
                                        .font(.system(size: 11, weight: isSel ? .semibold : .regular))
                                        .fixedSize(horizontal: true, vertical: false)
                                        .padding(.horizontal, 8)
                                        .padding(.vertical, 3)
                                        .background(
                                            isSel ? ColabTheme.sky.opacity(0.18) : Color.primary.opacity(0.055),
                                            in: RoundedRectangle(cornerRadius: 6, style: .continuous)
                                        )
                                        .foregroundStyle(isSel ? .primary : .secondary)
                                }
                                .buttonStyle(.plain)
                            }
                        }
                    }
                    Spacer(minLength: 6)
                    Text("共 \(store.insights.totalAttempts(for: focusHW)) 次记录")
                        .font(.system(size: 10.5))
                        .monospacedDigit()
                        .foregroundStyle(.tertiary)
                        .fixedSize(horizontal: true, vertical: false)
                }

                HStack(alignment: .bottom, spacing: 8) {
                    ForEach(slots) { slot in
                        VStack(spacing: 5) {
                            Text(slot.rate.map { "\(Int(($0 * 100).rounded()))%" } ?? "—")
                                .font(.system(size: 10, weight: .medium))
                                .monospacedDigit()
                                .foregroundStyle(slot.attempts > 0 ? .primary : .tertiary)

                            ZStack(alignment: .bottom) {
                                RoundedRectangle(cornerRadius: 5, style: .continuous)
                                    .fill(Color.primary.opacity(0.06))
                                    .frame(height: 44)
                                if let rate = slot.rate {
                                    let barH = max(6, CGFloat(rate) * 44)
                                    RoundedRectangle(cornerRadius: 5, style: .continuous)
                                        .fill(rate >= 0.5 ? ColabTheme.emerald.opacity(0.82) : ColabTheme.amber.opacity(0.75))
                                        .frame(height: barH)
                                }
                            }

                            Text(slot.label)
                                .font(.system(size: 10))
                                .monospacedDigit()
                                .foregroundStyle(.secondary)
                        }
                        .frame(maxWidth: .infinity)
                    }
                }
                .padding(.top, 4)

                HStack(spacing: 6) {
                    Image(systemName: "sparkles")
                        .font(.system(size: 11))
                        .foregroundStyle(ColabTheme.sky)
                    Text(recText ?? "每次申请会在本机静默记录成功/缺货，累积 3 次后自动为你推荐 \(focusHW) 黄金时段。")
                        .font(.system(size: 11))
                        .foregroundStyle(.secondary)
                }
            }

            // 5. Plain-language safety strip
            HStack(spacing: 8) {
                Image(systemName: "shield.lefthalf.filled")
                    .font(.system(size: 13))
                    .foregroundStyle(ColabTheme.emerald)
                Text("防风控护栏始终开启：低频随机间隔重试（上限 5 轮 / 40 分钟），遇到限流（429）或超额（412）立即自动熔断并冷却 15 分钟。")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
            }
            .padding(.horizontal, 4)
        }
    }

    private func hardwareTargetCard(
        option: AcceleratorOption,
        rankIndex: Int?,
        totalSelected: Int,
        locked: Bool,
        accountId: UUID
    ) -> some View {
        let isSelected = rankIndex != nil
        return HStack(spacing: 8) {
            Button {
                store.toggleQueueTarget(option, for: accountId)
            } label: {
                HStack(spacing: 8) {
                    ZStack {
                        RoundedRectangle(cornerRadius: 6, style: .continuous)
                            .fill(isSelected ? ColabTheme.amber.opacity(0.22) : Color.primary.opacity(0.055))
                            .frame(width: 24, height: 24)
                        if let idx = rankIndex {
                            Text("#\(idx + 1)")
                                .font(.system(size: 11, weight: .bold, design: .rounded))
                                .monospacedDigit()
                                .foregroundStyle(ColabTheme.amber)
                        } else {
                            Image(systemName: "plus")
                                .font(.system(size: 10, weight: .medium))
                                .foregroundStyle(.tertiary)
                        }
                    }

                    VStack(alignment: .leading, spacing: 2) {
                        HStack(spacing: 5) {
                            Text(option.shortChipLabel)
                                .font(.system(size: 12.5, weight: .semibold))
                                .foregroundStyle(isSelected ? .primary : .secondary)
                            if !option.roleTag.isEmpty {
                                Text(option.roleTag)
                                    .font(.system(size: 9.5, weight: .medium))
                                    .foregroundStyle(.secondary)
                                    .padding(.horizontal, 5)
                                    .padding(.vertical, 1.5)
                                    .background(Color.primary.opacity(0.06), in: RoundedRectangle(cornerRadius: 4))
                            }
                        }
                        Text(option.specSubtitle)
                            .font(.system(size: 10.5))
                            .foregroundStyle(.tertiary)
                            .lineLimit(1)
                    }
                    Spacer(minLength: 0)
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .disabled(locked)

            if let idx = rankIndex, totalSelected >= 2 {
                VStack(spacing: 2) {
                    Button {
                        store.moveQueueTarget(option, direction: -1, for: accountId)
                    } label: {
                        Image(systemName: "chevron.up")
                            .font(.system(size: 9, weight: .semibold))
                            .frame(width: 18, height: 14)
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(idx == 0 ? .quaternary : .secondary)
                    .disabled(locked || idx == 0)

                    Button {
                        store.moveQueueTarget(option, direction: 1, for: accountId)
                    } label: {
                        Image(systemName: "chevron.down")
                            .font(.system(size: 9, weight: .semibold))
                            .frame(width: 18, height: 14)
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(idx == totalSelected - 1 ? .quaternary : .secondary)
                    .disabled(locked || idx == totalSelected - 1)
                }
            }
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 8)
        .background(
            isSelected ? ColabTheme.amber.opacity(0.08) : Color.primary.opacity(0.03),
            in: RoundedRectangle(cornerRadius: 10, style: .continuous)
        )
        .overlay(
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .strokeBorder(isSelected ? ColabTheme.amber.opacity(0.45) : Color.primary.opacity(0.08), lineWidth: 0.75)
        )
    }

    // MARK: - Page 3: Integrations Hub
    @ViewBuilder
    private var integrationsPage: some View {
        HStack(alignment: .top) {
            pageHeader(title: "接入工具", subtitle: "自动检测本机 AI 编程客户端，支持无感安全写入或双协议一键复制。")
            Spacer()
            Button {
                store.refreshClientDetection()
                store.showToast("已重新扫描本机已安装的客户端")
            } label: {
                Label("重新检测", systemImage: "arrow.clockwise")
                    .font(.system(size: 11.5))
            }
            .buttonStyle(.bordered)
            .controlSize(.small)
        }

        if let acc = selectedAccount {
            let rt = store.state(for: acc.id)
            let det = store.clientDetection
            let hasEndpoint = !rt.publicURL.isEmpty

            // 1. Native Safe Auto-Sync Clients (detected installed floated first)
            settingsSectionCard(title: "原生安全同步（写前备份 · 绝不改官方默认）") {
                let orderedClients: [(key: String, installed: Bool)] = [
                    ("ccswitch", det.ccSwitchInstalled),
                    ("opencode", det.openCodeInstalled),
                    ("codex", det.codexInstalled),
                    ("claude", det.claudeCodeInstalled)
                ].sorted { ($0.installed ? 0 : 1) < ($1.installed ? 0 : 1) }

                ForEach(Array(orderedClients.enumerated()), id: \.element.key) { idx, item in
                    if idx > 0 { Divider().opacity(0.35) }
                    switch item.key {
                    case "ccswitch":
                        nativeClientRow(
                            title: "CC Switch",
                            installed: det.ccSwitchInstalled,
                            synced: rt.ccSwitchSynced,
                            subtitle: "写入独立待机渠道（WAL 安全事务 + 备份），不改动当前激活的官方订阅",
                            toggleBinding: binding(for: acc, \.autoSyncCCSwitch),
                            canSyncNow: det.ccSwitchInstalled && hasEndpoint,
                            onSyncNow: { store.syncToCCSwitch(accountId: acc.id, silent: false) }
                        )
                        if acc.autoSyncCCSwitch {
                            HStack {
                                Text("渠道显示名称")
                                    .font(.system(size: 11.5))
                                    .foregroundStyle(.secondary)
                                Spacer()
                                TextField("Colab Tools", text: binding(for: acc, \.ccSwitchProviderName))
                                    .textFieldStyle(.roundedBorder)
                                    .controlSize(.small)
                                    .frame(width: 240)
                            }
                            .padding(.leading, 4)
                        }

                    case "opencode":
                        let ocIds = ClientIntegrations.identifiers(for: acc.id)
                        nativeClientRow(
                            title: "OpenCode",
                            installed: det.openCodeInstalled,
                            synced: rt.openCodeSynced,
                            subtitle: "按账号独立合并至 opencode.json（不存明文 Key，引用环境变量 \(ocIds.envKey.prefix(28))…）",
                            toggleBinding: binding(for: acc, \.autoSyncOpenCode),
                            canSyncNow: hasEndpoint,
                            onSyncNow: { store.syncToOpenCode(accountId: acc.id, silent: false) }
                        )

                    case "codex":
                        nativeClientRow(
                            title: "Codex CLI",
                            installed: det.codexInstalled,
                            synced: rt.codexSynced,
                            subtitle: "按账号独立托管区块写入 ~/.codex/config.toml（仅 env_key，不改动官方默认渠道）",
                            toggleBinding: binding(for: acc, \.autoSyncCodex),
                            canSyncNow: hasEndpoint,
                            onSyncNow: { store.syncToCodex(accountId: acc.id, silent: false) }
                        )

                    default:
                        HStack(alignment: .center, spacing: 10) {
                            VStack(alignment: .leading, spacing: 2) {
                                HStack(spacing: 6) {
                                    Text("Claude Code")
                                        .font(.system(size: 12.5, weight: .semibold))
                                    CardBadge(
                                        text: det.claudeCodeInstalled ? "已检测到" : "未安装",
                                        tint: det.claudeCodeInstalled ? ColabTheme.emerald : nil
                                    )
                                }
                                Text("通过一次性自清理脚本安全注入环境变量拉起终端，API Key 不留命令历史")
                                    .font(.system(size: 11))
                                    .foregroundStyle(.secondary)
                                    .fixedSize(horizontal: false, vertical: true)
                            }
                            Spacer()
                            Button("安全拉起终端") {
                                store.launchClaudeTerminal(accountId: acc.id)
                            }
                            .buttonStyle(.bordered)
                            .controlSize(.small)
                            .disabled(!rt.phase.isRunning || !hasEndpoint)
                        }
                    }
                }
            }

            // 2. Universal Dual-Protocol Copy Hub (Cherry Studio / Cline / Roo Code / Cursor / CCR / ccm)
            settingsSectionCard(title: "通用接入（一键复制给其他客户端）") {
                let ids = ClientIntegrations.identifiers(for: acc.id)
                let liveURL: String? = {
                    if hasEndpoint { return rt.publicURL }
                    if rt.sshTunnelActive, let bp = rt.activeSSHTunnelPort {
                        return "http://127.0.0.1:\(String(bp))"
                    }
                    return nil
                }()

                Text("适用于 Cherry Studio · Cline · Roo Code · Cursor · Claude Code Router · ccm 等任意支持标准 API 的工具")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)

                HStack(spacing: 8) {
                    HStack(spacing: 3) {
                        ForEach(CopyProtocolKind.allCases) { proto in
                            let active = selectedCopyProtocol == proto
                            Button {
                                selectedCopyProtocol = proto
                            } label: {
                                Text(proto.rawValue)
                                    .font(.system(size: 11, weight: active ? .semibold : .regular))
                                    .padding(.horizontal, 10)
                                    .padding(.vertical, 4)
                                    .background(
                                        active ? Color.primary.opacity(0.14) : Color.clear,
                                        in: RoundedRectangle(cornerRadius: 6, style: .continuous)
                                    )
                                    .foregroundStyle(active ? .primary : .secondary)
                            }
                            .buttonStyle(.plain)
                        }
                    }
                    .padding(2.5)
                    .background(Color.primary.opacity(0.055), in: RoundedRectangle(cornerRadius: 8, style: .continuous))

                    Spacer()

                    if liveURL == nil {
                        CardBadge(text: "启动后自动填入实测地址", tint: ColabTheme.amber)
                    } else {
                        CardBadge(text: "实时端点已就绪", tint: ColabTheme.emerald)
                    }
                }

                let previewURL = liveURL ?? "https://your-colab-endpoint.trycloudflare.com"
                let previewText = ClientIntegrations.exportEnvSnippet(
                    protocolKind: selectedCopyProtocol,
                    baseURL: previewURL,
                    modelAlias: rt.modelAlias,
                    apiKey: "",
                    envKey: ids.envKey,
                    includeRawSecret: false
                )

                Text(previewText)
                    .font(.system(size: 11, design: .monospaced))
                    .foregroundStyle(.primary.opacity(0.88))
                    .lineSpacing(3)
                    .fixedSize(horizontal: false, vertical: true)
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(10)
                    .background(Color.primary.opacity(0.045), in: RoundedRectangle(cornerRadius: 8, style: .continuous))

                HStack(spacing: 8) {
                    Button {
                        store.copyClientSnippet(accountId: acc.id, protocolKind: selectedCopyProtocol, format: .env, includeRawSecret: false)
                    } label: {
                        Label("复制安全模板", systemImage: "terminal")
                            .font(.system(size: 11.5))
                    }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.small)

                    Button {
                        store.copyClientSnippet(accountId: acc.id, protocolKind: selectedCopyProtocol, format: .json, includeRawSecret: false)
                    } label: {
                        Label("复制 JSON", systemImage: "curlybraces")
                            .font(.system(size: 11.5))
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)

                    Button {
                        let urlToCopy = selectedCopyProtocol == .openAI
                            ? ClientIntegrations.openAIBaseURL(from: previewURL)
                            : previewURL
                        store.copyToClipboard(urlToCopy, label: "\(selectedCopyProtocol.rawValue) 地址")
                    } label: {
                        Label("仅复制地址", systemImage: "link")
                            .font(.system(size: 11.5))
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)

                    Spacer()

                    Button {
                        store.copyClientSnippet(accountId: acc.id, protocolKind: selectedCopyProtocol, format: .env, includeRawSecret: true)
                    } label: {
                        Label("复制含真实密钥", systemImage: "key")
                            .font(.system(size: 11))
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                    .disabled(liveURL == nil)
                    .help("仅在线时可用：将 Keychain 中的真实 API Key 写入剪贴板")
                }
            }
        }
    }

    private func nativeClientRow(
        title: String,
        installed: Bool,
        synced: Bool,
        subtitle: String,
        toggleBinding: Binding<Bool>,
        canSyncNow: Bool,
        onSyncNow: @escaping () -> Void
    ) -> some View {
        HStack(alignment: .center, spacing: 10) {
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(title)
                        .font(.system(size: 12.5, weight: .semibold))
                    CardBadge(
                        text: installed ? "已检测到" : "未检测到",
                        tint: installed ? ColabTheme.emerald : nil
                    )
                    if synced {
                        CardBadge(text: "已同步", tint: ColabTheme.sky)
                    }
                }
                Text(subtitle)
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 8)
            Button("立即同步", action: onSyncNow)
                .buttonStyle(.bordered)
                .controlSize(.small)
                .disabled(!canSyncNow)

            Toggle("", isOn: toggleBinding)
                .toggleStyle(.switch)
                .tint(ColabTheme.emerald)
                .labelsHidden()
                .controlSize(.small)
        }
    }

    // MARK: - Page 4: Accounts
    @ViewBuilder
    private var accountsPage: some View {
        HStack(alignment: .top) {
            pageHeader(title: "账号管理", subtitle: "多订阅账号彼此钥匙串隔离，可并行监控点数与独立配置算力。")
            Spacer()
            Button {
                showingAddAccountSheet = true
            } label: {
                Label("添加账号", systemImage: "person.badge.plus")
                    .font(.system(size: 11.5))
            }
            .buttonStyle(.borderedProminent)
            .controlSize(.small)
        }

        settingsSectionCard(title: "已绑定账号 (\(store.settings.accounts.count))") {
            ForEach(Array(store.settings.accounts.enumerated()), id: \.element.id) { idx, acc in
                if idx > 0 { Divider().opacity(0.35) }
                let st = store.state(for: acc.id)
                let isSel = acc.id == selectedAccount?.id
                HStack(spacing: 10) {
                    Circle()
                        .fill(ColabTheme.color(for: acc.colorTag))
                        .frame(width: 9, height: 9)

                    VStack(alignment: .leading, spacing: 2) {
                        HStack(spacing: 6) {
                            Text(acc.displayName)
                                .font(.system(size: 12.5, weight: .semibold))
                            CardBadge(text: acc.tier.shortBadge)
                            CardBadge(text: acc.authMode == .systemDefaultOAuth2 ? "默认 OAuth2" : "独立钥匙串")
                            if isSel {
                                CardBadge(text: "当前编辑", tint: ColabTheme.sky)
                            }
                        }
                        Text(acc.email.isEmpty ? "默认官方 CLI 账号" : acc.email)
                            .font(.system(size: 11))
                            .foregroundStyle(.secondary)
                    }

                    Spacer()

                    Text("\(Formatters.cu(st.cuBalance)) CU")
                        .font(.system(size: 12, weight: .medium, design: .rounded))
                        .monospacedDigit()
                        .foregroundStyle(.secondary)

                    if !isSel {
                        Button("选择") {
                            store.settings.selectedAccountId = acc.id
                        }
                        .buttonStyle(.bordered)
                        .controlSize(.mini)
                    }

                    if store.settings.accounts.count > 1 || acc.authMode == .isolatedKeychainADC {
                        Button {
                            store.removeAccount(acc.id)
                        } label: {
                            Image(systemName: "trash")
                                .font(.system(size: 11))
                                .foregroundStyle(ColabTheme.coral)
                        }
                        .buttonStyle(.borderless)
                        .help("移除此账号并清除对应钥匙串凭证")
                    }
                }
                .padding(.vertical, 2)
            }
        }

        if let acc = selectedAccount {
            settingsSectionCard(title: "当前账号资料", trailing: acc.displayName) {
                HStack {
                    Text("账号备注")
                        .font(.system(size: 12))
                        .frame(width: 72, alignment: .leading)
                    TextField("账号名称", text: binding(for: acc, \.displayName))
                        .textFieldStyle(.roundedBorder)
                        .controlSize(.small)
                        .frame(maxWidth: 240)
                    Spacer()
                }

                HStack {
                    Text("订阅档位")
                        .font(.system(size: 12))
                        .frame(width: 72, alignment: .leading)
                    Picker("", selection: binding(for: acc, \.tier)) {
                        ForEach(SubscriptionTier.allCases) { tier in
                            Text(tier.displayName).tag(tier)
                        }
                    }
                    .labelsHidden()
                    .controlSize(.small)
                    .frame(width: 180)

                    Button("管理订阅 / 购点…") {
                        store.openPayPage()
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                    Spacer()
                }

                HStack(spacing: 10) {
                    Text("颜色标记")
                        .font(.system(size: 12))
                        .frame(width: 72, alignment: .leading)
                    ForEach(AccountColorTag.allCases) { tag in
                        Button {
                            var copy = acc
                            copy.colorTag = tag
                            store.updateAccount(copy)
                        } label: {
                            Circle()
                                .fill(ColabTheme.color(for: tag))
                                .frame(width: 16, height: 16)
                                .overlay {
                                    if acc.colorTag == tag {
                                        Circle().strokeBorder(Color.primary, lineWidth: 1.5).padding(-2.5)
                                    }
                                }
                        }
                        .buttonStyle(.plain)
                    }
                    Spacer()
                }
            }
        }
    }

    // MARK: - Page 5: Advanced (Power-user parameters tucked away cleanly)
    @ViewBuilder
    private var advancedPage: some View {
        pageHeader(title: "高级", subtitle: "面向进阶用户的负载端口、启动脚本、轮询间隔与维护选项，普通用户保持默认即可。")

        if let acc = selectedAccount {
            let rt = store.state(for: acc.id)

            settingsSectionCard(title: "工作负载、端口与远端脚本", trailing: acc.displayName) {
                HStack {
                    Text("负载模式")
                        .font(.system(size: 12))
                        .frame(width: 88, alignment: .leading)
                    Picker("", selection: binding(for: acc, \.workloadKind)) {
                        ForEach(WorkloadKind.allCases) { k in
                            Text("\(k.displayName) (默认 :\(String(k.defaultRemotePort)))").tag(k)
                        }
                    }
                    .labelsHidden()
                    .controlSize(.small)
                    .frame(width: 240)
                    Spacer()
                }

                HStack {
                    Text("默认会话名")
                        .font(.system(size: 12))
                        .frame(width: 88, alignment: .leading)
                    TextField("llm-coder", text: binding(for: acc, \.sessionName))
                        .textFieldStyle(.roundedBorder)
                        .controlSize(.small)
                        .frame(width: 200)
                    Spacer()
                }

                HStack(spacing: 10) {
                    Text("端口映射")
                        .font(.system(size: 12))
                        .frame(width: 88, alignment: .leading)

                    Text("远端")
                        .font(.system(size: 11.5))
                        .foregroundStyle(.secondary)
                    TextField("18080", text: $remotePortDraft)
                        .textFieldStyle(.roundedBorder)
                        .frame(width: 76)
                        .multilineTextAlignment(.trailing)
                        .controlSize(.small)
                        .onSubmit { commitPortDraft(isRemote: true, account: acc) }

                    Text("本地 SSH 隧道")
                        .font(.system(size: 11.5))
                        .foregroundStyle(.secondary)
                    TextField("18080", text: $localPortDraft)
                        .textFieldStyle(.roundedBorder)
                        .frame(width: 76)
                        .multilineTextAlignment(.trailing)
                        .controlSize(.small)
                        .onSubmit { commitPortDraft(isRemote: false, account: acc) }

                    Text("回车生效 (1024–65535)")
                        .font(.system(size: 10.5))
                        .foregroundStyle(.tertiary)
                    Spacer()
                }

                if rt.sshTunnelActive, let bound = rt.activeSSHTunnelPort {
                    Text("当前 SSH 隧道已绑定 127.0.0.1:\(String(bound))，修改本地端口需断开并重连隧道后生效。")
                        .font(.system(size: 10.5))
                        .foregroundStyle(ColabTheme.sky)
                }

                VStack(alignment: .leading, spacing: 5) {
                    Text(acc.workloadKind == .llm ? "Drive 远端 LLM 启动脚本路径" : "Drive 远端工作负载脚本（可选，留空仅分配算力）")
                        .font(.system(size: 11.5))
                        .foregroundStyle(.secondary)
                    TextField(
                        acc.workloadKind.defaultScriptHint.isEmpty ? "/content/drive/MyDrive/colab-tools/start.sh" : acc.workloadKind.defaultScriptHint,
                        text: binding(for: acc, \.driveScriptPath)
                    )
                    .textFieldStyle(.roundedBorder)
                    .font(.system(size: 11, design: .monospaced))
                    .controlSize(.small)
                }
            }

            settingsSectionCard(title: "轮询节奏、维护与数据目录") {
                HStack {
                    VStack(alignment: .leading, spacing: 2) {
                        Text("后台自动同步间隔")
                            .font(.system(size: 12))
                        Text("多账号自动错峰串行查询，避免高频请求触发限流")
                            .font(.system(size: 11))
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
                    Stepper(value: $store.settings.backgroundRefreshSeconds, in: 60...900, step: 60) {
                        Text("\(store.settings.backgroundRefreshSeconds / 60) 分钟")
                            .font(.system(size: 12))
                            .monospacedDigit()
                    }
                    .controlSize(.small)
                }

                Divider().opacity(0.35)

                HStack(spacing: 8) {
                    Button("打开配置与备份目录") {
                        NSWorkspace.shared.open(AppSettings.configDir)
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)

                    Button("清空本机时段统计") {
                        store.clearAllocationInsights()
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)

                    if rt.lastError != nil {
                        Button("清除错误提示") {
                            store.clearDisplayedError(for: acc.id)
                        }
                        .buttonStyle(.bordered)
                        .controlSize(.small)
                    }

                    if let until = rt.queueCircuitBreakUntil, until > Date() {
                        let remMin = max(1, Int(ceil(until.timeIntervalSinceNow / 60.0)))
                        CardBadge(text: "防风控冷却剩余 \(remMin)m（不可跳过）", tint: ColabTheme.coral)
                    }
                    Spacer()
                }
            }
        }
    }

    // MARK: - Page 6: About
    @ViewBuilder
    private var aboutPage: some View {
        VStack(spacing: 12) {
            VStack(spacing: 8) {
                appIconView(size: 72, radius: 17)
                    .shadow(color: .black.opacity(0.18), radius: 8, y: 3)

                Text("Colab Tools")
                    .font(.system(size: 18, weight: .semibold))
                Text("v0.3.5 · MIT Open Source")
                    .font(.system(size: 11.5))
                    .foregroundStyle(.secondary)
                Text("面向全体 Google AI / Gemini 与 Colab 订阅用户的原生 macOS 菜单栏伴侣。基于官方 google-colab-cli，支持多订阅隔离、合规排队与错峰预约、全模态负载监控及多客户端生态接入。")
                    .font(.system(size: 11.5))
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .frame(maxWidth: 440)
                    .padding(.top, 2)
            }
            .frame(maxWidth: .infinity)
            .padding(20)
            .hudCard(cornerRadius: 12)

            settingsSectionCard(title: "开源仓库与作者") {
                aboutLinkRow(title: "GitHub 开源主页", subtitle: "github.com/LJMStark/Colab-Tools", url: "https://github.com/LJMStark/Colab-Tools")
                Divider().opacity(0.35)
                aboutLinkRow(title: "作者 X (Twitter)", subtitle: "x.com/starkljm", url: "https://x.com/starkljm")
                Divider().opacity(0.35)
                aboutLinkRow(title: "联系作者", subtitle: "X DM: @starkljm", url: "https://x.com/starkljm")
            }
        }
    }

    private func aboutLinkRow(title: String, subtitle: String, url: String) -> some View {
        HStack {
            VStack(alignment: .leading, spacing: 1) {
                Text(title).font(.system(size: 12, weight: .medium))
                Text(subtitle).font(.system(size: 11)).foregroundStyle(.secondary)
            }
            Spacer()
            if let u = URL(string: url) {
                Button("打开") {
                    NSWorkspace.shared.open(u)
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
            }
        }
    }

    @ViewBuilder
    private func appIconView(size: CGFloat, radius: CGFloat) -> some View {
        let iconImage: NSImage? = {
            if let iconPath = Bundle.main.path(forResource: "AppIconPreview", ofType: "png"),
               let img = NSImage(contentsOfFile: iconPath) {
                return img
            }
            let devAsset = URL(fileURLWithPath: FileManager.default.currentDirectoryPath)
                .appendingPathComponent("assets/AppIcon.png")
            if let img = NSImage(contentsOfFile: devAsset.path) {
                return img
            }
            return nil
        }()
        if let img = iconImage {
            Image(nsImage: img)
                .resizable()
                .scaledToFit()
                .frame(width: size, height: size)
                .clipShape(RoundedRectangle(cornerRadius: radius, style: .continuous))
        } else {
            ZStack {
                LinearGradient(
                    colors: [ColabTheme.amber, Color(red: 0.92, green: 0.44, blue: 0.12)],
                    startPoint: .topLeading,
                    endPoint: .bottomTrailing
                )
                Image(systemName: "cpu.fill")
                    .font(.system(size: size * 0.48, weight: .semibold))
                    .foregroundStyle(.white)
            }
            .frame(width: size, height: size)
            .clipShape(RoundedRectangle(cornerRadius: radius, style: .continuous))
        }
    }

    private func refreshLoginItemStatus() {
        if isPreviewMode { return }
        loginItemStatus = SMAppService.mainApp.status
    }

    private func syncPortDrafts() {
        guard let acc = selectedAccount else { return }
        remotePortDraft = String(acc.remoteServicePort)
        localPortDraft = String(acc.localSSHTunnelPort)
    }

    private func commitPortDraft(isRemote: Bool, account: AccountProfile) {
        let raw = (isRemote ? remotePortDraft : localPortDraft).trimmingCharacters(in: .whitespacesAndNewlines)
        guard let val = Int(raw), (1024...65535).contains(val) else {
            store.showToast("端口范围须为 1024–65535，已保留原设置")
            syncPortDrafts()
            return
        }
        var copy = account
        if isRemote {
            copy.remoteServicePort = val
        } else {
            copy.localSSHTunnelPort = val
        }
        store.updateAccount(copy)
        store.showToast(isRemote ? "已更新远端服务端口为 :\(String(val))" : "已更新本地隧道端口为 :\(String(val))")
    }

    private func toggleLaunchAtLogin(_ enabled: Bool) {
        if isPreviewMode {
            loginItemStatus = enabled ? .enabled : .notRegistered
            return
        }
        do {
            if enabled {
                try SMAppService.mainApp.register()
            } else {
                try SMAppService.mainApp.unregister()
            }
            loginItemStatus = SMAppService.mainApp.status
            switch loginItemStatus {
            case .enabled:
                store.showToast("已开启开机自动启动")
            case .requiresApproval:
                store.showToast("已请求开机自启，请在系统设置 → 登录项中批准")
            default:
                store.showToast("已关闭开机自动启动")
            }
        } catch {
            loginItemStatus = SMAppService.mainApp.status
            store.showToast("切换开机自启失败（请确认应用位于 /Applications 目录）")
        }
    }

    private func binding<Value>(for account: AccountProfile, _ keyPath: WritableKeyPath<AccountProfile, Value>) -> Binding<Value> {
        Binding(
            get: {
                store.settings.accounts.first(where: { $0.id == account.id })?[keyPath: keyPath] ?? account[keyPath: keyPath]
            },
            set: { newValue in
                guard var cur = store.settings.accounts.first(where: { $0.id == account.id }) else { return }
                cur[keyPath: keyPath] = newValue
                store.updateAccount(cur)
            }
        )
    }
}
