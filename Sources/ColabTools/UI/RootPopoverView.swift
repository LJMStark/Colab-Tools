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
    static let panelWidth: CGFloat = 420

    @ObservedObject var store: WorkspaceStore
    var isPreviewMode: Bool = false

    @State private var measuredContentHeight: CGFloat = 760

    private var maxScrollHeight: CGFloat {
        let visibleH = NSScreen.main?.visibleFrame.height ?? 920
        let chromeH: CGFloat = (!store.showingAddAccount && store.isMultiAccount) ? 148 : 112
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
            } else if store.settings.accounts.isEmpty {
                emptyAccountOnboardingCard
            } else if store.isMultiAccount && store.selectedViewMode == .overview {
                AccountOverviewView(store: store)
            } else if let acc = store.selectedAccount {
                AccountDetailView(store: store, account: acc)
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

            if !store.showingAddAccount && store.isMultiAccount {
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
                HStack(spacing: 7) {
                    Image(systemName: "checkmark.circle.fill")
                        .font(.system(size: 13))
                        .foregroundStyle(ColabTheme.emerald)
                    Text(toast)
                        .font(ColabTypography.popoverSubMedium)
                        .lineLimit(1)
                    Spacer()
                }
                .padding(.horizontal, 14)
                .padding(.vertical, 9)
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
        HStack(spacing: 8) {
            HStack(spacing: 7) {
                Circle()
                    .fill(globalDotColor)
                    .frame(width: 8.5, height: 8.5)
                Text("Colab Tools")
                    .font(ColabTypography.popoverTitle)
                    .lineLimit(1)
                    .fixedSize(horizontal: true, vertical: false)
                headerStatusPill
            }

            Spacer(minLength: 4)

            HStack(spacing: 12) {
                Button {
                    store.openMainWindow(section: .status, onboarding: false)
                } label: {
                    Image(systemName: "macwindow")
                        .font(.system(size: 13.5, weight: .medium))
                }
                .buttonStyle(.borderless)
                .help("打开大屏主控制台 (⌘0)")

                Button {
                    if store.showingAddAccount {
                        withAnimation(.snappy(duration: 0.18)) { store.showingAddAccount = false }
                    } else {
                        store.openAddAccountFlow()
                    }
                } label: {
                    Image(systemName: store.showingAddAccount ? "xmark" : "person.badge.plus")
                        .font(.system(size: 13.5, weight: .medium))
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
                        .font(.system(size: 13.5, weight: .medium))
                }
                .buttonStyle(.borderless)
                .help("立即同步官方 CLI 状态（5 秒防抖）")

                Button {
                    store.openMainWindow(section: .settings, onboarding: false)
                } label: {
                    Image(systemName: "gearshape")
                        .font(.system(size: 13.5, weight: .medium))
                }
                .buttonStyle(.borderless)
                .help("打开偏好设置 (⌘,)")
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 11)
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
                .font(.system(size: 11, weight: .semibold))
                .monospacedDigit()
                .foregroundStyle(ColabTheme.amber)
                .lineLimit(1)
                .fixedSize(horizontal: true, vertical: false)
                .padding(.horizontal, 7.5)
                .padding(.vertical, 2.5)
                .background(ColabTheme.amber.opacity(0.14), in: Capsule())
        } else if running.count >= 2 {
            Text("\(running.count) 运行中")
                .font(ColabTypography.popoverCaptionMedium)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .fixedSize(horizontal: true, vertical: false)
                .padding(.horizontal, 7.5)
                .padding(.vertical, 2.5)
                .background(Color.primary.opacity(0.07), in: Capsule())
        } else if let first = running.first {
            let effectiveKind = first.0.workloadKind == .general ? (first.1.detectedWorkloadKind ?? .general) : first.0.workloadKind
            let label: String = {
                if effectiveKind == .llm {
                    return "\(first.1.shortGPUName) · \(first.1.contextLimit / 1024)K"
                }
                return "\(first.1.shortGPUName) · \(effectiveKind.shortBadge)"
            }()
            Text(label)
                .font(ColabTypography.popoverCaptionMedium)
                .monospacedDigit()
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .fixedSize(horizontal: true, vertical: false)
                .padding(.horizontal, 7.5)
                .padding(.vertical, 2.5)
                .background(Color.primary.opacity(0.07), in: Capsule())
        } else if let acc = store.selectedAccount {
            let bal = store.state(for: acc.id).cuBalance
            Text("\(Formatters.cu(bal)) CU")
                .font(ColabTypography.popoverCaptionMedium)
                .monospacedDigit()
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .fixedSize(horizontal: true, vertical: false)
                .padding(.horizontal, 7.5)
                .padding(.vertical, 2.5)
                .background(Color.primary.opacity(0.06), in: Capsule())
        }
    }

    // MARK: - Multi-Account Pill Switcher (Hidden when single account for clean minimalism)
    private var multiAccountRail: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 6) {
                Button {
                    withAnimation(.snappy(duration: 0.18)) {
                        store.selectedViewMode = .overview
                    }
                } label: {
                    HStack(spacing: 5) {
                        Image(systemName: "square.grid.2x2")
                            .font(.system(size: 11))
                        Text("总览")
                            .font(.system(size: 12, weight: store.selectedViewMode == .overview ? .semibold : .regular))
                    }
                    .padding(.horizontal, 9)
                    .padding(.vertical, 5)
                    .background(
                        store.selectedViewMode == .overview ? Color.primary.opacity(0.13) : Color.primary.opacity(0.045),
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
                        HStack(spacing: 5) {
                            Circle()
                                .fill(st.phase.isRunning ? Color(nsColor: .systemGreen) : ColabTheme.color(for: acc.colorTag))
                                .frame(width: 7, height: 7)
                            Text(acc.displayName)
                                .font(.system(size: 12, weight: isSelected ? .semibold : .regular))
                                .lineLimit(1)
                            Text("\(Formatters.cu(st.cuBalance))")
                                .font(ColabTypography.popoverCaption)
                                .monospacedDigit()
                                .foregroundStyle(.secondary)
                        }
                        .padding(.horizontal, 9)
                        .padding(.vertical, 5)
                        .background(
                            isSelected ? Color.primary.opacity(0.13) : Color.primary.opacity(0.045),
                            in: Capsule()
                        )
                    }
                    .buttonStyle(.plain)
                }
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
        }
    }

    // MARK: - Onboarding / Empty States
    private var missingCLICard: some View {
        VStack(alignment: .leading, spacing: 10) {
            Label("未检测到官方 google-colab-cli", systemImage: "exclamationmark.triangle.fill")
                .font(ColabTypography.sectionTitle)
                .foregroundStyle(ColabTheme.amber)
            Text("Colab Tools 基于 Google 官方 CLI 工作。点击下方按钮打开环境体检向导，可一键为你自动安装并绑定账号。")
                .font(ColabTypography.popoverSub)
                .foregroundStyle(.secondary)
            HStack(spacing: 8) {
                Button("打开一键安装向导") {
                    store.reopenOnboarding()
                }
                .buttonStyle(.borderedProminent)
                .tint(ColabTheme.amber)

                Button("复制安装命令") {
                    store.copyToClipboard(store.envReport.recommendedMethod.manualShellCommand, label: "安装命令")
                }
                .buttonStyle(.bordered)
            }
        }
        .padding(16)
        .hudCard()
    }

    private var emptyAccountOnboardingCard: some View {
        VStack(spacing: 12) {
            Image(systemName: "person.crop.circle.badge.plus")
                .font(.system(size: 32, weight: .light))
                .foregroundStyle(.secondary)
            Text("尚未绑定 Google Colab 账号")
                .font(.system(size: 15, weight: .semibold))
            Text("通过官方 Google OAuth2 完成授权，即可实时查看点数、一键启停 GPU/TPU 并开启自动空闲关机。")
                .font(ColabTypography.popoverSub)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
            HStack(spacing: 8) {
                Button("打开开箱引导") {
                    store.reopenOnboarding()
                }
                .buttonStyle(.borderedProminent)

                Button("直接绑定账号") {
                    store.openAddAccountFlow()
                }
                .buttonStyle(.bordered)
            }
        }
        .frame(maxWidth: .infinity)
        .padding(20)
        .hudCard()
    }

    // MARK: - Footer
    private var footerBar: some View {
        HStack(spacing: 8) {
            if let acc = store.selectedAccount, let ts = store.state(for: acc.id).lastUsageFetchAt {
                Text("同步于 \(ts.formatted(date: .omitted, time: .standard))")
                    .font(ColabTypography.popoverCaption)
                    .monospacedDigit()
                    .foregroundStyle(.tertiary)
            } else {
                Text("官方 Colab CLI 驱动")
                    .font(ColabTypography.popoverCaption)
                    .foregroundStyle(.tertiary)
            }
            Spacer()
            Button("主控制台") {
                store.openMainWindow(section: .status, onboarding: false)
            }
            .font(ColabTypography.popoverSub)
            .buttonStyle(.borderless)
            .foregroundStyle(.secondary)

            Text("·").font(.system(size: 11)).foregroundStyle(.quaternary)

            Button("设置…") {
                store.openMainWindow(section: .settings, onboarding: false)
            }
            .font(ColabTypography.popoverSub)
            .buttonStyle(.borderless)
            .foregroundStyle(.secondary)

            Text("·").font(.system(size: 11)).foregroundStyle(.quaternary)

            Button("退出") {
                store.cleanup()
                NSApplication.shared.terminate(nil)
            }
            .font(ColabTypography.popoverSub)
            .buttonStyle(.borderless)
            .foregroundStyle(.secondary)
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 9)
    }
}
