import ColabToolsCore
import SwiftUI

/// Multi-account parallel overview: large aggregate hero metrics + per-account parallel table + one-click stop-all.
struct AccountOverviewView: View {
    @ObservedObject var store: WorkspaceStore

    var body: some View {
        VStack(spacing: 11) {
            // Hero aggregate strip
            HStack(alignment: .center) {
                HStack(alignment: .firstTextBaseline, spacing: 7) {
                    Image(systemName: store.runningAccounts.isEmpty ? "square.grid.2x2" : "bolt.horizontal.circle.fill")
                        .font(.system(size: 20, weight: .semibold))
                        .foregroundStyle(store.runningAccounts.isEmpty ? .secondary : ColabTheme.emerald)
                    Text(Formatters.cu(store.totalBalanceCU))
                        .font(.system(size: 27, weight: .bold, design: .rounded))
                        .monospacedDigit()
                    VStack(alignment: .leading, spacing: 1) {
                        Text("\(store.runningAccounts.count) 个运行中")
                            .font(.system(size: 12.5, weight: .semibold))
                        Text(String(format: "总计 %.2f CU/hr", store.totalHourlyRateCU))
                            .font(.system(size: 10.5))
                            .monospacedDigit()
                            .foregroundStyle(.secondary)
                    }
                }
                Spacer()
                if !store.runningAccounts.isEmpty {
                    Button {
                        let ids = store.runningAccounts.map(\.0.id)
                        Task {
                            for id in ids { await store.stopAccountSession(id) }
                        }
                    } label: {
                        Label("全停止损", systemImage: "power")
                            .font(.system(size: 11.5, weight: .semibold))
                            .foregroundStyle(ColabTheme.coral)
                            .padding(.horizontal, 10)
                            .padding(.vertical, 6)
                            .background(ColabTheme.coral.opacity(0.12), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
                    }
                    .buttonStyle(.plain)
                }
            }
            .padding(.horizontal, 2)

            // Per-account parallel list card
            VStack(spacing: 0) {
                HStack {
                    Text("多账号并行工作区 (\(store.settings.accounts.count))")
                        .font(.system(size: 11.5, weight: .medium))
                        .foregroundStyle(.secondary)
                    Spacer()
                    Text("点数")
                        .font(.system(size: 11))
                        .foregroundStyle(.tertiary)
                        .frame(width: 64, alignment: .trailing)
                    Text("控制")
                        .font(.system(size: 11))
                        .foregroundStyle(.tertiary)
                        .frame(width: 46, alignment: .trailing)
                }
                .padding(.bottom, 8)

                ForEach(Array(store.settings.accounts.enumerated()), id: \.element.id) { idx, acc in
                    if idx > 0 { Divider().opacity(0.35).padding(.vertical, 6) }
                    let st = store.state(for: acc.id)
                    AccountParallelRow(account: acc, state: st) {
                        store.settings.selectedAccountId = acc.id
                        withAnimation(.snappy(duration: 0.18)) {
                            store.selectedViewMode = .singleAccount
                        }
                    } onTogglePower: {
                        Task {
                            if st.phase.isRunning {
                                await store.stopAccountSession(acc.id)
                            } else {
                                await store.startAccountSession(acc.id)
                            }
                        }
                    }
                }
            }
            .padding(12)
            .hudCard()

            HStack(spacing: 6) {
                Image(systemName: "lock.shield")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                Text("各账号使用独立 macOS 钥匙串凭证与会话索引隔离，支持并行启停。")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                Spacer()
            }
            .padding(.horizontal, 4)
        }
    }
}

private struct AccountParallelRow: View {
    let account: AccountProfile
    let state: AccountRuntimeState
    let onInspect: () -> Void
    let onTogglePower: () -> Void

    var body: some View {
        HStack(spacing: 9) {
            Circle()
                .fill(state.phase.isRunning ? ColabTheme.emerald : ColabTheme.color(for: account.colorTag))
                .frame(width: 8, height: 8)

            Button(action: onInspect) {
                VStack(alignment: .leading, spacing: 2.5) {
                    HStack(spacing: 6) {
                        Text(account.displayName)
                            .font(.system(size: 12.5, weight: .semibold))
                            .foregroundStyle(.primary)
                            .lineLimit(1)
                        CardBadge(text: account.tier.shortBadge)
                    }
                    Text(
                        state.phase.isRunning
                            ? "\(state.shortGPUName) · \(String(format: "%.1f/%.0fG", Double(state.vramUsedMB)/1024.0, Double(state.vramTotalMB)/1024.0))"
                            : (state.phase.isBusy ? "操作中…" : (account.email.isEmpty ? "待机" : account.email))
                    )
                    .font(.system(size: 11))
                    .monospacedDigit()
                    .foregroundStyle(state.phase.isRunning ? ColabTheme.emerald : .secondary)
                    .lineLimit(1)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)

            Text(Formatters.cu(state.cuBalance))
                .font(.system(size: 12.5, weight: .semibold, design: .rounded))
                .monospacedDigit()
                .frame(width: 64, alignment: .trailing)

            HStack(spacing: 6) {
                if state.phase.isBusy {
                    ProgressView().controlSize(.small)
                } else {
                    Button(action: onTogglePower) {
                        Image(systemName: state.phase.isRunning ? "power" : "play.fill")
                            .font(.system(size: 11, weight: .semibold))
                            .foregroundStyle(state.phase.isRunning ? ColabTheme.coral : Color.primary)
                            .frame(width: 28, height: 24)
                            .background(
                                state.phase.isRunning ? ColabTheme.coral.opacity(0.12) : Color.primary.opacity(0.08),
                                in: RoundedRectangle(cornerRadius: 6.5, style: .continuous)
                            )
                    }
                    .buttonStyle(.plain)
                }
                Image(systemName: "chevron.right")
                    .font(.system(size: 10, weight: .medium))
                    .foregroundStyle(.tertiary)
            }
            .frame(width: 46, alignment: .trailing)
        }
    }
}
