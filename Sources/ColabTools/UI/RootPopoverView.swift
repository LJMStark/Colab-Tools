import AppKit
import ColabToolsCore
import SwiftUI

struct RootPopoverView: View {
    @ObservedObject var store: WorkspaceStore

    var body: some View {
        VStack(spacing: 0) {
            headerBar
            Divider().opacity(0.4)

            if store.selectedTab == .dashboard && !store.showingAddAccount && store.isMultiAccount {
                multiAccountRail
                Divider().opacity(0.35)
            }

            ScrollView(.vertical, showsIndicators: false) {
                VStack(spacing: 10) {
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
                .padding(12)
            }
            .frame(maxHeight: 560)

            if let toast = store.toastMessage {
                HStack(spacing: 6) {
                    Image(systemName: "checkmark.circle")
                        .font(.system(size: 11))
                        .foregroundStyle(.secondary)
                    Text(toast)
                        .font(.system(size: 11))
                        .lineLimit(1)
                    Spacer()
                }
                .padding(.horizontal, 12)
                .padding(.vertical, 7)
                .background(.ultraThinMaterial)
                .overlay(alignment: .top) { Divider().opacity(0.35) }
                .transition(.opacity)
            }

            Divider().opacity(0.4)
            footerBar
        }
        .frame(width: 384)
        .background(.regularMaterial)
    }

    // MARK: - Header
    private var headerBar: some View {
        HStack(spacing: 8) {
            HStack(spacing: 7) {
                Circle()
                    .fill(globalDotColor)
                    .frame(width: 8, height: 8)
                Text("Colab Tools")
                    .font(.system(size: 13.5, weight: .semibold))
                headerStatusPill
            }

            Spacer()

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
            .frame(width: 126)
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
        if store.settings.accounts.contains(where: { store.state(for: $0.id).phase.isBusy }) {
            return ColabTheme.amber
        }
        return Color(nsColor: .tertiaryLabelColor)
    }

    @ViewBuilder
    private var headerStatusPill: some View {
        let running = store.runningAccounts
        if running.count >= 2 {
            Text("\(running.count) 实例运行中")
                .font(.system(size: 10, weight: .medium))
                .foregroundStyle(.secondary)
                .padding(.horizontal, 6)
                .padding(.vertical, 2)
                .background(Color.primary.opacity(0.06), in: Capsule())
        } else if let first = running.first {
            Text("\(first.1.shortGPUName) · \(Formatters.cu(first.1.cuBalance)) CU")
                .font(.system(size: 10, weight: .medium))
                .monospacedDigit()
                .foregroundStyle(.secondary)
                .padding(.horizontal, 6)
                .padding(.vertical, 2)
                .background(Color.primary.opacity(0.06), in: Capsule())
        } else if let acc = store.selectedAccount {
            let bal = store.state(for: acc.id).cuBalance
            Text("\(Formatters.cu(bal)) CU")
                .font(.system(size: 10, weight: .medium))
                .monospacedDigit()
                .foregroundStyle(.secondary)
                .padding(.horizontal, 6)
                .padding(.vertical, 2)
                .background(Color.primary.opacity(0.05), in: Capsule())
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
                    HStack(spacing: 4) {
                        Image(systemName: "square.grid.2x2")
                            .font(.system(size: 10))
                        Text("总览")
                            .font(.system(size: 11, weight: store.selectedViewMode == .overview ? .semibold : .regular))
                    }
                    .padding(.horizontal, 8)
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
                        HStack(spacing: 5) {
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
                        .padding(.horizontal, 8)
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
        .padding(12)
        .refinedCard()
    }

    private var emptyAccountOnboardingCard: some View {
        VStack(spacing: 10) {
            Image(systemName: "person.crop.circle.badge.plus")
                .font(.system(size: 28, weight: .light))
                .foregroundStyle(.secondary)
            Text("尚未绑定 Google Colab 账号")
                .font(.system(size: 13, weight: .semibold))
            Text("点击下方按钮通过官方 Google OAuth2 完成授权，即可实时查看点数、一键启停 GPU/TPU 并开启自动空闲关机。")
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
            Button("绑定第一个账号") {
                withAnimation(.snappy(duration: 0.18)) {
                    store.showingAddAccount = true
                }
            }
            .buttonStyle(.borderedProminent)
            .controlSize(.small)
        }
        .frame(maxWidth: .infinity)
        .padding(18)
        .refinedCard()
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
