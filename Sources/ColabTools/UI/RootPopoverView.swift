import AppKit
import ColabToolsCore
import SwiftUI

private struct PanelContentHeightKey: PreferenceKey {
    static let defaultValue: CGFloat = 0
    static func reduce(value: inout CGFloat, nextValue: () -> CGFloat) {
        let next = nextValue()
        if next > 0 { value = next }
    }
}

struct RootPopoverView: View {
    static let panelWidth: CGFloat = 396

    @ObservedObject var store: WorkspaceStore
    var isPreviewMode: Bool = false

    @State private var measuredContentHeight: CGFloat = 710

    private var maxScrollHeight: CGFloat {
        let visibleH = NSScreen.main?.visibleFrame.height ?? 920
        let chromeH: CGFloat = (store.selectedTab == .dashboard && !store.showingAddAccount && store.isMultiAccount) ? 148 : 112
        return min(860, max(540, visibleH - chromeH))
    }

    @ViewBuilder
    private var mainContent: some View {
        VStack(alignment: .leading, spacing: 9) {
            if !store.cliInstalled {
                missingCLICard
            } else if store.showingAddAccount {
                AddAccountSheetView(store: store) {
                    withAnimation(.snappy(duration: 0.18)) {
                        store.showingAddAccount = false
                    }
                }
            } else {
                switch store.selectedTab {
                case .dashboard:
                    if store.settings.accounts.isEmpty {
                        emptyAccountOnboardingCard
                    } else if store.isMultiAccount && store.selectedViewMode == .overview {
                        AccountOverviewView(store: store)
                    } else if let acc = store.selectedAccount {
                        AccountDetailView(store: store, account: acc)
                    }
                case .settings:
                    SettingsTabView(store: store)
                case .about:
                    AboutTabView()
                }
            }
        }
        .frame(width: Self.panelWidth - 24, alignment: .topLeading)
        .padding(.horizontal, 12)
        .padding(.vertical, 11)
        .background {
            GeometryReader { geo in
                Color.clear.preference(key: PanelContentHeightKey.self, value: geo.size.height)
            }
        }
    }

    var body: some View {
        VStack(spacing: 0) {
            headerBar
            Divider().opacity(0.4)

            if store.selectedTab == .dashboard && !store.showingAddAccount && store.isMultiAccount {
                multiAccountRail
                Divider().opacity(0.35)
            }

            if isPreviewMode {
                mainContent
            } else {
                let fittedHeight = min(max(measuredContentHeight, 260), maxScrollHeight)
                ScrollView(.vertical, showsIndicators: measuredContentHeight > maxScrollHeight + 2) {
                    mainContent
                }
                .frame(width: Self.panelWidth, height: fittedHeight)
                .onPreferenceChange(PanelContentHeightKey.self) { h in
                    let rounded = ceil(h)
                    if rounded > 0 && abs(rounded - measuredContentHeight) > 0.5 {
                        measuredContentHeight = rounded
                    }
                }
            }

            if let toast = store.toastMessage {
                HStack(spacing: 6) {
                    Image(systemName: "checkmark.circle.fill")
                        .font(.system(size: 11.5))
                        .foregroundStyle(ColabTheme.emerald)
                    Text(toast)
                        .font(.system(size: 11.5))
                        .lineLimit(1)
                    Spacer()
                }
                .padding(.horizontal, 14)
                .padding(.vertical, 8)
                .background(.ultraThinMaterial)
                .overlay(alignment: .top) { Divider().opacity(0.35) }
                .transition(.opacity)
            }

            Divider().opacity(0.4)
            footerBar
        }
        .frame(width: Self.panelWidth)
        .clipped()
        .background {
            if isPreviewMode {
                Color.clear
            } else {
                Rectangle().fill(.regularMaterial)
            }
        }
    }

    // MARK: - Header
    private var headerBar: some View {
        HStack(spacing: 7) {
            HStack(spacing: 6) {
                Circle()
                    .fill(globalDotColor)
                    .frame(width: 8, height: 8)
                Text("Colab Tools")
                    .font(.system(size: 13.5, weight: .semibold))
                    .lineLimit(1)
                    .fixedSize(horizontal: true, vertical: false)
                headerStatusPill
            }

            Spacer(minLength: 4)

            Picker("", selection: Binding(
                get: { store.selectedTab },
                set: { store.switchTab(to: $0) }
            )) {
                ForEach(WorkspaceStore.PanelTab.allCases) { tab in
                    Text(tab.rawValue).tag(tab)
                }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .frame(width: 124)
            .controlSize(.small)

            Button {
                if store.showingAddAccount {
                    withAnimation(.snappy(duration: 0.18)) { store.showingAddAccount = false }
                } else {
                    store.openAddAccountFlow()
                }
            } label: {
                Image(systemName: store.showingAddAccount ? "xmark" : "person.badge.plus")
                    .font(.system(size: 11.5, weight: .medium))
            }
            .buttonStyle(.borderless)
            .help("添加新的 Colab 订阅账号")

            Button {
                Task {
                    if store.isMultiAccount && store.selectedViewMode == .overview {
                        await store.refreshAllAccounts(force: true, silent: false)
                    } else if let acc = store.selectedAccount {
                        await store.refreshAccount(acc.id, force: true, silent: false)
                    }
                }
            } label: {
                Image(systemName: "arrow.clockwise")
                    .font(.system(size: 11.5, weight: .medium))
            }
            .buttonStyle(.borderless)
            .help("立即同步官方 CLI 状态（5 秒防抖）")
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 10)
    }

    private var globalDotColor: Color {
        let running = store.runningAccounts.count
        if running > 0 { return Color(nsColor: .systemGreen) }
        if store.settings.accounts.contains(where: {
            let ph = store.state(for: $0.id).phase
            return ph.isBusy || ph.isQueuing
        }) {
            return ColabTheme.amber
        }
        return Color(nsColor: .tertiaryLabelColor)
    }

    @ViewBuilder
    private var headerStatusPill: some View {
        let running = store.runningAccounts
        let queuingItem = store.settings.accounts.compactMap { acc -> (Int, Int)? in
            if case .queuing(let a, let m, _, _) = store.state(for: acc.id).phase { return (a, m) }
            return nil
        }.first
        if let q = queuingItem, running.isEmpty {
            Text("排队 \(q.0)/\(q.1)")
                .font(.system(size: 10, weight: .semibold))
                .monospacedDigit()
                .foregroundStyle(ColabTheme.amber)
                .lineLimit(1)
                .fixedSize(horizontal: true, vertical: false)
                .padding(.horizontal, 6.5)
                .padding(.vertical, 2)
                .background(ColabTheme.amber.opacity(0.14), in: Capsule())
        } else if running.count >= 2 {
            Text("\(running.count) 运行中")
                .font(.system(size: 10, weight: .medium))
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .fixedSize(horizontal: true, vertical: false)
                .padding(.horizontal, 6.5)
                .padding(.vertical, 2)
                .background(Color.primary.opacity(0.07), in: Capsule())
        } else if let first = running.first {
            let label = first.0.enableLLMModule
                ? "\(first.1.shortGPUName) · \(first.1.contextLimit / 1024)K"
                : first.1.shortGPUName
            Text(label)
                .font(.system(size: 10, weight: .medium))
                .monospacedDigit()
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .fixedSize(horizontal: true, vertical: false)
                .padding(.horizontal, 6.5)
                .padding(.vertical, 2)
                .background(Color.primary.opacity(0.07), in: Capsule())
        } else if let acc = store.selectedAccount {
            let bal = store.state(for: acc.id).cuBalance
            Text("\(Formatters.cu(bal)) CU")
                .font(.system(size: 10, weight: .medium))
                .monospacedDigit()
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .fixedSize(horizontal: true, vertical: false)
                .padding(.horizontal, 6.5)
                .padding(.vertical, 2)
                .background(Color.primary.opacity(0.06), in: Capsule())
        }
    }

    // MARK: - Multi-Account Pill Switcher (Hidden when single account for clean minimalism)
    private var multiAccountRail: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 5) {
                Button {
                    withAnimation(.snappy(duration: 0.18)) {
                        store.selectedViewMode = .overview
                    }
                } label: {
                    HStack(spacing: 4) {
                        Image(systemName: "square.grid.2x2")
                            .font(.system(size: 10))
                        Text("总览")
                            .font(.system(size: 11, weight: store.selectedViewMode == .overview ? .semibold : .regular))
                    }
                    .padding(.horizontal, 7.5)
                    .padding(.vertical, 4)
                    .background(
                        store.selectedViewMode == .overview ? Color.primary.opacity(0.13) : Color.primary.opacity(0.04),
                        in: Capsule()
                    )
                }
                .buttonStyle(.plain)

                ForEach(store.settings.accounts) { acc in
                    let st = store.state(for: acc.id)
                    let isSelected = store.selectedViewMode == .singleAccount && store.settings.selectedAccountId == acc.id
                    Button {
                        withAnimation(.snappy(duration: 0.18)) {
                            store.settings.selectedAccountId = acc.id
                            store.selectedViewMode = .singleAccount
                        }
                    } label: {
                        HStack(spacing: 4.5) {
                            Circle()
                                .fill(st.phase.isRunning ? Color(nsColor: .systemGreen) : ColabTheme.color(for: acc.colorTag))
                                .frame(width: 6.5, height: 6.5)
                            Text(acc.displayName)
                                .font(.system(size: 11, weight: isSelected ? .semibold : .regular))
                                .lineLimit(1)
                            Text("\(Formatters.cu(st.cuBalance))")
                                .font(.system(size: 10))
                                .monospacedDigit()
                                .foregroundStyle(.secondary)
                        }
                        .padding(.horizontal, 7.5)
                        .padding(.vertical, 4)
                        .background(
                            isSelected ? Color.primary.opacity(0.13) : Color.primary.opacity(0.04),
                            in: Capsule()
                        )
                    }
                    .buttonStyle(.plain)
                }
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 7)
        }
    }

    // MARK: - Onboarding / Empty States
    private var missingCLICard: some View {
        VStack(alignment: .leading, spacing: 8) {
            Label("未检测到官方 google-colab-cli", systemImage: "exclamationmark.triangle")
                .font(.system(size: 12.5, weight: .semibold))
            Text("Colab Tools 100% 基于 Google 官方 CLI 工作。请先在终端安装：")
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
            Text("uv tool install google-colab-cli")
                .font(.system(size: 11, design: .monospaced))
                .padding(8)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Color.primary.opacity(0.05), in: RoundedRectangle(cornerRadius: 6))
            Button("复制安装命令") {
                store.copyToClipboard("uv tool install google-colab-cli", label: "安装命令")
            }
            .buttonStyle(.bordered)
            .controlSize(.small)
        }
        .padding(14)
        .hudCard()
    }

    private var emptyAccountOnboardingCard: some View {
        VStack(spacing: 10) {
            Image(systemName: "person.crop.circle.badge.plus")
                .font(.system(size: 30, weight: .light))
                .foregroundStyle(.secondary)
            Text("尚未绑定 Google Colab 账号")
                .font(.system(size: 13.5, weight: .semibold))
            Text("点击下方按钮通过官方 Google OAuth2 完成授权，即可实时查看点数、一键启停 GPU/TPU 并开启自动空闲关机。")
                .font(.system(size: 11.5))
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
            Button("绑定第一个账号") {
                store.openAddAccountFlow()
            }
            .buttonStyle(.borderedProminent)
            .controlSize(.small)
        }
        .frame(maxWidth: .infinity)
        .padding(18)
        .hudCard()
    }

    // MARK: - Footer
    private var footerBar: some View {
        HStack {
            if let acc = store.selectedAccount, let ts = store.state(for: acc.id).lastUsageFetchAt {
                Text("同步于 \(ts.formatted(date: .omitted, time: .standard))")
                    .font(.system(size: 10))
                    .monospacedDigit()
                    .foregroundStyle(.tertiary)
            } else {
                Text("100% 官方 Colab CLI 驱动")
                    .font(.system(size: 10))
                    .foregroundStyle(.tertiary)
            }
            Spacer()
            Button("退出") {
                store.cleanup()
                NSApplication.shared.terminate(nil)
            }
            .font(.system(size: 11))
            .buttonStyle(.borderless)
            .foregroundStyle(.secondary)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }
}
