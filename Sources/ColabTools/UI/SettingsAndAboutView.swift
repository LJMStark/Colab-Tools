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
                    .font(.system(size: 11.5, weight: .semibold))

                Picker("显示模式", selection: $store.settings.menuBarMode) {
                    ForEach(MenuBarDisplayMode.allCases) { mode in
                        Text(mode.label).tag(mode)
                    }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .controlSize(.small)

                HStack {
                    Text("后台自动同步间隔")
                        .font(.system(size: 11))
                    Spacer()
                    Stepper(value: $store.settings.backgroundRefreshSeconds, in: 60...900, step: 60) {
                        Text("\(store.settings.backgroundRefreshSeconds / 60) 分钟")
                            .font(.system(size: 11))
                            .monospacedDigit()
                    }
                    .controlSize(.small)
                }

                Text("多账号查询采用错峰串行与最低冷却保护，严格遵守官方 CLI 规范不触发风控。")
                    .font(.system(size: 10))
                    .foregroundStyle(.secondary)
            }
            .padding(12)
            .hudCard()

            // 2. Selected Account Detailed Settings
            if let acc = store.selectedAccount {
                VStack(alignment: .leading, spacing: 9) {
                    HStack {
                        Text("当前账号配置 · \(acc.displayName)")
                            .font(.system(size: 11.5, weight: .semibold))
                        Spacer()
                        Text(acc.authMode == .systemDefaultOAuth2 ? "默认 OAuth2" : "独立钥匙串 ADC")
                            .font(.system(size: 9.5))
                            .foregroundStyle(.secondary)
                            .padding(.horizontal, 6)
                            .padding(.vertical, 2)
                            .background(Color.primary.opacity(0.06), in: Capsule())
                    }

                    HStack {
                        Text("账号备注")
                            .font(.system(size: 11))
                            .frame(width: 68, alignment: .leading)
                        TextField("账号名称", text: binding(for: acc, \.displayName))
                            .textFieldStyle(.roundedBorder)
                            .controlSize(.small)
                    }

                    HStack {
                        Text("默认会话名")
                            .font(.system(size: 11))
                            .frame(width: 68, alignment: .leading)
                        TextField("llm-coder", text: binding(for: acc, \.sessionName))
                            .textFieldStyle(.roundedBorder)
                            .controlSize(.small)
                    }

                    HStack {
                        Text("空闲超时")
                            .font(.system(size: 11))
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
                            .font(.system(size: 11))
                            .frame(width: 68, alignment: .leading)
                        Stepper(value: binding(for: acc, \.warnBeforeMinutes), in: 0...15) {
                            Text("\(acc.warnBeforeMinutes) 分钟")
                                .font(.system(size: 11))
                                .monospacedDigit()
                        }
                        .controlSize(.small)
                        Spacer()
                    }

                    Divider().opacity(0.35)

                    Toggle("开机自动同步公网地址到 CC Switch（不改动官方订阅）", isOn: binding(for: acc, \.autoSyncCCSwitch))
                        .font(.system(size: 11))
                        .controlSize(.mini)

                    HStack {
                        Text("渠道显示名")
                            .font(.system(size: 11))
                            .frame(width: 68, alignment: .leading)
                        TextField("Colab Tools", text: binding(for: acc, \.ccSwitchProviderName))
                            .textFieldStyle(.roundedBorder)
                            .controlSize(.small)
                    }

                    VStack(alignment: .leading, spacing: 3) {
                        Text("Drive 远端 LLM 启动脚本路径")
                            .font(.system(size: 10.5))
                            .foregroundStyle(.secondary)
                        TextField("/content/drive/MyDrive/colab-llm/scripts/start_server.sh", text: binding(for: acc, \.driveScriptPath))
                            .textFieldStyle(.roundedBorder)
                            .font(.system(size: 10.5, design: .monospaced))
                            .controlSize(.small)
                    }
                }
                .padding(12)
                .hudCard()
            }

            // 3. Multi-Account Management List
            VStack(alignment: .leading, spacing: 8) {
                HStack {
                    Text("已绑定账号 (\(store.settings.accounts.count))")
                        .font(.system(size: 11.5, weight: .semibold))
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
                                .font(.system(size: 11, weight: .medium))
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
            .padding(12)
            .hudCard()
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
                Text("v0.3.0 · MIT Open Source")
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
                AboutLinkRow(title: "联系作者", subtitle: "X DM: @starkljm（二维码失效可私信入群）", url: "https://x.com/starkljm")

                if let qrPath = Bundle.main.path(forResource: "wechat-qr", ofType: "png"),
                   let img = NSImage(contentsOfFile: qrPath) {
                    VStack(spacing: 6) {
                        Divider().opacity(0.35)
                        Text("扫码加入微信交流群")
                            .font(.system(size: 11))
                            .foregroundStyle(.secondary)
                        Image(nsImage: img)
                            .resizable()
                            .interpolation(.none)
                            .scaledToFit()
                            .frame(width: 144, height: 144)
                            .clipShape(RoundedRectangle(cornerRadius: 8))
                    }
                    .frame(maxWidth: .infinity)
                    .padding(.top, 4)
                } else {
                    HStack(spacing: 6) {
                        Image(systemName: "qrcode").foregroundStyle(.secondary)
                        Text("放入 assets/wechat-qr.png 后自动展示交流群二维码")
                            .font(.system(size: 10.5))
                            .foregroundStyle(.secondary)
                    }
                    .padding(.top, 2)
                }
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
