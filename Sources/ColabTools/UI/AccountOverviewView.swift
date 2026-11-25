import ColabToolsCore
import SwiftUI

/// Multi-account parallel overview card: shows aggregate Compute Units, active GPUs, hourly burn,
/// and per-account independent one-click controls.
struct AccountOverviewView: View {
    @ObservedObject var store: WorkspaceStore

    var body: some View {
        VStack(spacing: 10) {
            // Global 3-column summary
            HStack(spacing: 8) {
                SummaryStat(title: "总剩余点数", value: "\(Formatters.cu(store.totalBalanceCU)) CU")
                SummaryStat(title: "运行中实例", value: "\(store.runningAccounts.count) / \(store.settings.accounts.count)")
                SummaryStat(title: "合计消耗", value: String(format: "%.2f/hr", store.totalHourlyRateCU))
            }

            // Per-account parallel rows
            VStack(spacing: 7) {
                ForEach(store.settings.accounts) { acc in
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

            HStack(spacing: 6) {
                Image(systemName: "shield.checkered")
                    .font(.system(size: 10.5))
                    .foregroundStyle(.secondary)
                Text("各账号使用独立官方会话与钥匙串凭证隔离，并行互不干扰。")
                    .font(.system(size: 10.5))
                    .foregroundStyle(.secondary)
                Spacer()
            }
            .padding(.horizontal, 4)
        }
    }
}

private struct SummaryStat: View {
    let title: String
    let value: String

    var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            Text(title)
                .font(.system(size: 10))
                .foregroundStyle(.secondary)
            Text(value)
                .font(.system(size: 13, weight: .semibold))
                .monospacedDigit()
                .lineLimit(1)
                .minimumScaleFactor(0.8)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(9)
        .refinedCard()
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
                .fill(ColabTheme.color(for: account.colorTag))
                .frame(width: 8, height: 8)

            Button(action: onInspect) {
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 5) {
                        Text(account.displayName)
                            .font(.system(size: 12, weight: .medium))
                            .foregroundStyle(.primary)
                            .lineLimit(1)
                        Text(account.tier.shortBadge)
                            .font(.system(size: 9.5, weight: .medium))
                            .foregroundStyle(.secondary)
                            .padding(.horizontal, 5)
                            .padding(.vertical, 1.5)
                            .background(Color.primary.opacity(0.06), in: Capsule())
                    }
                    HStack(spacing: 6) {
                        Text("\(Formatters.cu(state.cuBalance)) CU")
                            .monospacedDigit()
                        Text("·")
                        if state.phase.isRunning {
                            Text("\(state.shortGPUName) · 剩\(Formatters.shortMinutes(state.remainingIdleSeconds(timeoutMinutes: account.idleTimeoutMinutes)))")
                                .foregroundStyle(Color(nsColor: .systemGreen))
                                .monospacedDigit()
                        } else if case .starting(let step) = state.phase {
                            Text(step).foregroundStyle(ColabTheme.amber)
                        } else {
                            Text("已关闭")
                        }
                    }
                    .font(.system(size: 10.5))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)

            if state.phase.isBusy {
                ProgressView().controlSize(.small)
            } else {
                Button(action: onTogglePower) {
                    Image(systemName: state.phase.isRunning ? "power" : "play.fill")
                        .font(.system(size: 10.5, weight: .semibold))
                        .foregroundStyle(state.phase.isRunning ? Color(nsColor: .systemRed) : Color.primary)
                        .frame(width: 26, height: 24)
                        .background(
                            state.phase.isRunning ? Color(nsColor: .systemRed).opacity(0.1) : Color.primary.opacity(0.07),
                            in: RoundedRectangle(cornerRadius: 6)
                        )
                }
                .buttonStyle(.plain)
                .help(state.phase.isRunning ? "关闭此账号 GPU" : "启动此账号 GPU")
            }

            Button(action: onInspect) {
                Image(systemName: "chevron.right")
                    .font(.system(size: 10, weight: .medium))
                    .foregroundStyle(.tertiary)
            }
            .buttonStyle(.plain)
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 8)
        .refinedCard()
    }
}
