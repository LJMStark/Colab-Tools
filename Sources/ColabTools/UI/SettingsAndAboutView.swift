import AppKit
import ColabToolsCore
import SwiftUI

struct SettingsTabView: View {
    @ObservedObject var store: WorkspaceStore

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            // 1. Menu Bar & Anti-Risk-Control Polling
            VStack(alignment: .leading, spacing: 8) {
                Text("菜单栏与合规节流")
                    .font(.system(size: 12, weight: .semibold))
                    .lineLimit(1)

                Picker("显示模式", selection: $store.settings.menuBarMode) {
                    ForEach(MenuBarDisplayMode.allCases) { mode in
                        Text(mode.label).tag(mode)
                    }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .controlSize(.small)
                .frame(maxWidth: .infinity)

                HStack {
                    Text("后台自动同步间隔")
                        .font(.system(size: 11.5))
                    Spacer()
                    Stepper(value: $store.settings.backgroundRefreshSeconds, in: 60...900, step: 60) {
                        Text("\(store.settings.backgroundRefreshSeconds / 60) 分钟")
                            .font(.system(size: 11.5))
                            .monospacedDigit()
                    }
                    .controlSize(.small)
                }

                Text("多账号查询采用错峰串行与最低冷却保护，遵循官方 CLI 规范不触发风控。")
                    .font(.system(size: 10.5))
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(12)
            .hudCard()

            // 2. Selected Account Detailed Settings
            if let acc = store.selectedAccount {
                VStack(alignment: .leading, spacing: 9) {
                    HStack {
                        Text("当前账号配置 · \(acc.displayName)")
                            .font(.system(size: 12, weight: .semibold))
                            .lineLimit(1)
                        Spacer()
                        CardBadge(text: acc.authMode == .systemDefaultOAuth2 ? "默认 OAuth2" : "独立钥匙串 ADC")
                    }

                    HStack {
                        Text("账号备注")
                            .font(.system(size: 11.5))
                            .frame(width: 68, alignment: .leading)
                        TextField("账号名称", text: binding(for: acc, \.displayName))
                            .textFieldStyle(.roundedBorder)
                            .controlSize(.small)
                            .frame(maxWidth: .infinity)
                    }

                    HStack {
                        Text("默认会话名")
                            .font(.system(size: 11.5))
                            .frame(width: 68, alignment: .leading)
                        TextField("llm-coder", text: binding(for: acc, \.sessionName))
                            .textFieldStyle(.roundedBorder)
                            .controlSize(.small)
                            .frame(maxWidth: .infinity)
                    }

                    HStack {
                        Text("空闲超时")
                            .font(.system(size: 11.5))
                            .frame(width: 68, alignment: .leading)
                        TextField("", value: binding(for: acc, \.idleTimeoutMinutes), format: .number)
                            .textFieldStyle(.roundedBorder)
                            .frame(width: 60)
                            .multilineTextAlignment(.trailing)
                            .controlSize(.small)
                        Text("分钟（5–720）")
                            .font(.system(size: 10.5))
                            .foregroundStyle(.secondary)
                        Spacer()
                    }

                    HStack {
                        Text("关机前提醒")
                            .font(.system(size: 11.5))
                            .frame(width: 68, alignment: .leading)
                        Stepper(value: binding(for: acc, \.warnBeforeMinutes), in: 0...15) {
                            Text("\(acc.warnBeforeMinutes) 分钟")
                                .font(.system(size: 11.5))
                                .monospacedDigit()
                        }
                        .controlSize(.small)
                        Spacer()
                    }

                    HStack {
                        Text("负载模式")
                            .font(.system(size: 11.5))
                            .frame(width: 68, alignment: .leading)
                        Picker("", selection: binding(for: acc, \.workloadKind)) {
                            ForEach(WorkloadKind.allCases) { k in
                                Text("\(k.displayName) (:\(String(k.defaultRemotePort)))").tag(k)
                            }
                        }
                        .labelsHidden()
                        .controlSize(.small)
                        .frame(maxWidth: .infinity)
                    }

                    HStack {
                        Text("服务端口")
                            .font(.system(size: 11.5))
                            .frame(width: 68, alignment: .leading)
                        TextField("", value: binding(for: acc, \.remoteServicePort), format: .number.grouping(.never))
                            .textFieldStyle(.roundedBorder)
                            .frame(width: 72)
                            .multilineTextAlignment(.trailing)
                            .controlSize(.small)
                        Text("隧道本地 :\(String(acc.localSSHTunnelPort))")
                            .font(.system(size: 10.5))
                            .monospacedDigit()
                            .foregroundStyle(.secondary)
                        Spacer()
                    }

                    Toggle(isOn: binding(for: acc, \.enableHardwareIdleFallback)) {
                        VStack(alignment: .leading, spacing: 1) {
                            Text("非 LLM 负载启用 GPU+CPU 双低载防误杀关机")
                                .font(.system(size: 11.5))
                            Text("须同时满足 GPU < 5% 且整机 CPU < 25%，防下载权重或转码误停")
                                .font(.system(size: 10))
                                .foregroundStyle(.secondary)
                        }
                    }
                    .toggleStyle(.checkbox)
                    .controlSize(.small)
                    .frame(maxWidth: .infinity, alignment: .leading)

                    Divider().opacity(0.35)

                    if acc.workloadKind == .llm {
                        Toggle(isOn: binding(for: acc, \.autoSyncCCSwitch)) {
                            VStack(alignment: .leading, spacing: 1) {
                                Text("开机自动同步公网地址到 CC Switch")
                                    .font(.system(size: 11.5))
                                Text("仅写入独立待机渠道，不改动官方订阅")
                                    .font(.system(size: 10))
                                    .foregroundStyle(.secondary)
                            }
                        }
                        .toggleStyle(.checkbox)
                        .controlSize(.small)
                        .frame(maxWidth: .infinity, alignment: .leading)

                        HStack {
                            Text("渠道显示名")
                                .font(.system(size: 11.5))
                                .frame(width: 68, alignment: .leading)
                            TextField("Colab Tools", text: binding(for: acc, \.ccSwitchProviderName))
                                .textFieldStyle(.roundedBorder)
                                .controlSize(.small)
                                .frame(maxWidth: .infinity)
                        }
                    }

                    VStack(alignment: .leading, spacing: 4) {
                        Text(acc.workloadKind == .llm ? "Drive 远端 LLM 启动脚本路径" : "Drive 远端工作负载启动脚本（可选，留空仅分配算力）")
                            .font(.system(size: 10.5))
                            .foregroundStyle(.secondary)
                        TextField(acc.workloadKind.defaultScriptHint.isEmpty ? "/content/drive/MyDrive/colab-tools/start.sh" : acc.workloadKind.defaultScriptHint, text: binding(for: acc, \.driveScriptPath))
                            .textFieldStyle(.roundedBorder)
                            .font(.system(size: 10.5, design: .monospaced))
                            .controlSize(.small)
                            .frame(maxWidth: .infinity)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(12)
                .hudCard()
            }

            // 3. Multi-Account Management List
            VStack(alignment: .leading, spacing: 8) {
                HStack {
                    Text("已绑定账号 (\(store.settings.accounts.count))")
                        .font(.system(size: 12, weight: .semibold))
                    Spacer()
                    Button {
                        store.openAddAccountFlow()
                    } label: {
                        Label("添加账号", systemImage: "plus")
                            .font(.system(size: 10.5, weight: .medium))
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.mini)
                }

                ForEach(store.settings.accounts) { acc in
                    HStack(spacing: 8) {
                        Circle()
                            .fill(ColabTheme.color(for: acc.colorTag))
                            .frame(width: 7, height: 7)
                        VStack(alignment: .leading, spacing: 1) {
                            Text("\(acc.displayName) · \(acc.tier.shortBadge)")
                                .font(.system(size: 11.5, weight: .medium))
                                .lineLimit(1)
                            Text(acc.email.isEmpty ? "默认官方 CLI 账号" : acc.email)
                                .font(.system(size: 10))
                                .foregroundStyle(.secondary)
                                .lineLimit(1)
                        }
                        Spacer()
                        if store.settings.accounts.count > 1 || acc.authMode == .isolatedKeychainADC {
                            Button {
                                store.removeAccount(acc.id)
                            } label: {
                                Image(systemName: "trash")
                                    .font(.system(size: 10.5))
                                    .foregroundStyle(Color(nsColor: .systemRed).opacity(0.8))
                            }
                            .buttonStyle(.borderless)
                            .help("移除此账号并清除对应钥匙串凭证")
                        }
                    }
                    .padding(.vertical, 2)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(12)
            .hudCard()
        }
        .frame(maxWidth: .infinity, alignment: .leading)
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

struct AboutTabView: View {
    var body: some View {
        VStack(spacing: 10) {
            VStack(spacing: 7) {
                if let iconPath = Bundle.main.path(forResource: "AppIconPreview", ofType: "png"),
                   let img = NSImage(contentsOfFile: iconPath) {
                    Image(nsImage: img)
                        .resizable()
                        .scaledToFit()
                        .frame(width: 64, height: 64)
                        .clipShape(RoundedRectangle(cornerRadius: 15, style: .continuous))
                        .shadow(color: .black.opacity(0.18), radius: 6, y: 2)
                } else {
                    Image(nsImage: NSApp.applicationIconImage)
                        .resizable()
                        .scaledToFit()
                        .frame(width: 60, height: 60)
                }

                Text("Colab Tools")
                    .font(.system(size: 16, weight: .semibold))
                Text("v0.3.3 · MIT Open Source")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                Text("面向全体 Google AI / Gemini 与 Colab 订阅用户的原生 macOS 菜单栏伴侣。100% 基于官方 google-colab-cli，支持多订阅并行隔离管理、点数监控、自定义空闲自动关机与 Claude Code / CC Switch 无感联动。")
                    .font(.system(size: 10.5))
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .padding(.top, 2)
            }
            .frame(maxWidth: .infinity)
            .padding(14)
            .hudCard()

            VStack(alignment: .leading, spacing: 8) {
                Text("开源仓库与社区")
                    .font(.system(size: 11.5, weight: .semibold))

                AboutLinkRow(title: "GitHub 开源主页", subtitle: "github.com/LJMStark/Colab-Tools", url: "https://github.com/LJMStark/Colab-Tools")
                AboutLinkRow(title: "作者 X (Twitter)", subtitle: "x.com/starkljm", url: "https://x.com/starkljm")
                AboutLinkRow(title: "联系作者", subtitle: "X DM: @starkljm", url: "https://x.com/starkljm")
            }
            .padding(12)
            .hudCard()
        }
    }
}

private struct AboutLinkRow: View {
    let title: String
    let subtitle: String
    let url: String

    var body: some View {
        HStack {
            VStack(alignment: .leading, spacing: 1) {
                Text(title).font(.system(size: 11.5, weight: .medium))
                Text(subtitle).font(.system(size: 10.5)).foregroundStyle(.secondary)
            }
            Spacer()
            if !url.isEmpty, let u = URL(string: url) {
                Button("打开") {
                    NSWorkspace.shared.open(u)
                }
                .buttonStyle(.bordered)
                .controlSize(.mini)
            }
        }
    }
}
