import ColabToolsCore
import SwiftUI

/// Multi-account parallel overview: large aggregate hero metrics + per-account parallel table + one-click stop-all.
struct AccountOverviewView: View {
    @ObservedObject var store: WorkspaceStore

    var body: some View {
        VStack(spacing: 11) {
            // Hero aggregate strip
            HStack(alignment: .center) {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Image(systemName: store.runningAccounts.isEmpty ? "square.grid.2x2" : "bolt.horizontal.circle.fill")
                        .font(.system(size: 22, weight: .semibold))
                        .foregroundStyle(store.runningAccounts.isEmpty ? .secondary : ColabTheme.emerald)
                    Text(Formatters.cu(store.totalBalanceCU))
                        .font(.system(size: 29, weight: .bold, design: .rounded))
                        .monospacedDigit()
                    VStack(alignment: .leading, spacing: 2) {
                        Text("\(store.runningAccounts.count) 个运行中")
                            .font(.system(size: 13.5, weight: .semibold))
                        Text(String(format: "总计 %.2f CU/hr", store.totalHourlyRateCU))
                            .font(ColabTypography.popoverCaption)
                            .monospacedDigit()
                            .foregroundStyle(.secondary)
                    }
                }
                Spacer()
                if store.totalManagedRunningSessionCount > 0 {
                    Button {
                        Task { await store.stopAllManagedSessions() }
                    } label: {
                        Label("全停止损", systemImage: "power")
                            .font(.system(size: 12.5, weight: .semibold))
                            .foregroundStyle(ColabTheme.coral)
                            .padding(.horizontal, 11)
                            .padding(.vertical, 6.5)
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
                        .font(ColabTypography.popoverSubMedium)
                        .foregroundStyle(.secondary)
                    Spacer()
                    Text("点数")
                        .font(ColabTypography.popoverCaption)
                        .foregroundStyle(.tertiary)
                        .frame(width: 68, alignment: .trailing)
                    Text("控制")
                        .font(ColabTypography.popoverCaption)
                        .foregroundStyle(.tertiary)
                        .frame(width: 50, alignment: .trailing)
                }
                .padding(.bottom, 8)

                ForEach(Array(store.settings.accounts.enumerated()), id: \.element.id) { idx, acc in
                    if idx > 0 { Divider().opacity(0.35).padding(.vertical, 7) }
                    let st = store.state(for: acc.id)
                    AccountParallelRow(account: acc, state: st) {
                        store.settings.selectedAccountId = acc.id
                        withAnimation(.snappy(duration: 0.18)) {
                            store.selectedViewMode = .singleAccount
                        }
                    } onTogglePower: {
                        Task {
                            if st.phase.isQueuing {
                                store.cancelQueue(for: acc.id)
                            } else if st.phase.isRunning {
                                await store.stopAccountSession(acc.id)
                            } else {
                                await store.startAccountSession(acc.id)
                            }
                        }
                    }
                }
            }
            .padding(13)
            .hudCard()

            HStack(spacing: 6) {
                Image(systemName: "lock.shield")
                    .font(.system(size: 12))
                    .foregroundStyle(.secondary)
                Text("各账号使用独立 macOS 钥匙串凭证与会话索引隔离，支持并行启停。")
                    .font(ColabTypography.popoverSub)
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
        HStack(spacing: 10) {
            Circle()
                .fill(state.phase.isRunning ? ColabTheme.emerald : (state.phase.isQueuing ? ColabTheme.amber : ColabTheme.color(for: account.colorTag)))
                .frame(width: 8.5, height: 8.5)

            Button(action: onInspect) {
                VStack(alignment: .leading, spacing: 3) {
                    HStack(spacing: 6) {
                        Text(account.displayName)
                            .font(.system(size: 13.5, weight: .semibold))
                            .foregroundStyle(.primary)
                            .lineLimit(1)
                        CardBadge(text: account.tier.shortBadge)
                    }
                    Text(rowSubtitle)
                    .font(ColabTypography.popoverSub)
                    .monospacedDigit()
                    .foregroundStyle(state.phase.isRunning ? ColabTheme.emerald : (state.phase.isQueuing ? ColabTheme.amber : .secondary))
                    .lineLimit(1)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)

            Text(Formatters.cu(state.cuBalance))
                .font(.system(size: 13.5, weight: .semibold, design: .rounded))
                .monospacedDigit()
                .frame(width: 68, alignment: .trailing)

            HStack(spacing: 6) {
                if state.phase.isBusy {
                    ProgressView().controlSize(.small)
                } else {
                    Button(action: onTogglePower) {
                        Image(systemName: state.phase.isQueuing ? "xmark" : (state.phase.isRunning ? "power" : "play.fill"))
                            .font(.system(size: 12, weight: .semibold))
                            .foregroundStyle((state.phase.isRunning || state.phase.isQueuing) ? ColabTheme.coral : Color.primary)
                            .frame(width: 30, height: 26)
                            .background(
                                (state.phase.isRunning || state.phase.isQueuing) ? ColabTheme.coral.opacity(0.12) : Color.primary.opacity(0.08),
                                in: RoundedRectangle(cornerRadius: 7, style: .continuous)
                            )
                    }
                    .buttonStyle(.plain)
                }
                Image(systemName: "chevron.right")
                    .font(.system(size: 11, weight: .medium))
                    .foregroundStyle(.tertiary)
            }
            .frame(width: 50, alignment: .trailing)
        }
    }

    private var rowSubtitle: String {
        if case .queuing(let a, let m, _, let summary) = state.phase {
            return "排队 \(a)/\(m) · \(summary)"
        }
        if state.phase.isRunning {
            return "\(state.shortGPUName) · \(String(format: "%.1f/%.0fG", Double(state.vramUsedMB)/1024.0, Double(state.vramTotalMB)/1024.0))"
        }
        if state.phase.isBusy { return "操作中…" }
        return account.email.isEmpty ? "待机" : account.email
    }
}
