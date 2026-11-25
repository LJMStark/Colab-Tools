import AppKit
import ColabToolsCore
import SwiftUI

/// 3-step first-launch onboarding & environment doctor wizard.
/// Guides new users through CLI inspection, transparent one-click `google-colab-cli` installation,
/// official Google OAuth2 sign-in, and a 30-second feature overview.
struct OnboardingGuideView: View {
    @ObservedObject var store: WorkspaceStore
    var isPreviewMode: Bool = false

    @State private var step: Int = 1
    @State private var showInstallLogs: Bool = true
    @State private var agreedRemoteScript: Bool = false

    private var env: EnvironmentCheckReport { store.envReport }

    var body: some View {
        ScrollView(.vertical, showsIndicators: !isPreviewMode) {
            VStack(spacing: 20) {
                // Header with skip button
                HStack(alignment: .center) {
                    HStack(spacing: 12) {
                        ColabAppIconView(size: 46, cornerRadius: 12)
                            .shadow(color: .black.opacity(0.18), radius: 6, y: 2)
                        VStack(alignment: .leading, spacing: 3) {
                            Text("欢迎使用 Colab Tools")
                                .font(ColabTypography.pageTitle)
                            Text("3 步完成环境体检与 Google 账号绑定，即可一键管控云端 GPU / TPU")
                                .font(ColabTypography.body)
                                .foregroundStyle(.secondary)
                        }
                    }
                    Spacer()
                    Button("先跳过，直接看界面") {
                        store.completeOnboarding()
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.regular)
                }

                // 3-step pill stepper
                HStack(spacing: 10) {
                    stepPill(index: 1, title: "1. 环境体检与安装", done: store.cliInstalled)
                    stepPill(index: 2, title: "2. 绑定 Google 账号", done: !store.settings.accounts.isEmpty)
                    stepPill(index: 3, title: "3. 30 秒快速上手", done: false)
                }

                Group {
                    if step == 1 {
                        step1EnvironmentCard
                    } else if step == 2 {
                        step2AccountCard
                    } else {
                        step3TourCard
                    }
                }
            }
            .frame(maxWidth: 780)
            .padding(.horizontal, 36)
            .padding(.top, isPreviewMode ? 28 : 42)
            .padding(.bottom, 32)
            .frame(maxWidth: .infinity)
        }
        .onAppear {
            if !isPreviewMode {
                if store.cliInstalled && store.settings.accounts.isEmpty {
                    step = 2
                } else if store.cliInstalled && !store.settings.accounts.isEmpty {
                    step = 3
                }
                Task { await store.refreshEnvironmentCheck() }
            }
        }
    }

    private func stepPill(index: Int, title: String, done: Bool) -> some View {
        let active = step == index
        return Button {
            withAnimation(.snappy(duration: 0.16)) { step = index }
        } label: {
            HStack(spacing: 7) {
                Image(systemName: done ? "checkmark.circle.fill" : "\(index).circle.fill")
                    .font(.system(size: 14, weight: .semibold))
                    .foregroundStyle(done ? ColabTheme.emerald : (active ? ColabTheme.amber : .secondary))
                Text(title)
                    .font(.system(size: 13, weight: active ? .semibold : .medium))
                    .foregroundStyle(active ? .primary : .secondary)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 10)
            .padding(.horizontal, 12)
            .background(
                active ? Color.primary.opacity(0.09) : Color.primary.opacity(0.035),
                in: RoundedRectangle(cornerRadius: 11, style: .continuous)
            )
            .overlay(
                RoundedRectangle(cornerRadius: 11, style: .continuous)
                    .strokeBorder(active ? ColabTheme.amber.opacity(0.55) : Color.primary.opacity(0.08), lineWidth: 0.8)
            )
        }
        .buttonStyle(.plain)
    }

    // MARK: - Step 1: Environment Doctor & One-Click CLI Installer
    private var step1EnvironmentCard: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack {
                VStack(alignment: .leading, spacing: 3) {
                    Text("本机运行环境体检")
                        .font(.system(size: 16, weight: .semibold))
                    Text("Colab Tools 完全基于 Google 官方 google-colab-cli 驱动，不私自绕过任何官方接口。")
                        .font(ColabTypography.subheadline)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Button {
                    Task { await store.refreshEnvironmentCheck() }
                } label: {
                    Label(store.isCheckingEnvironment ? "检测中…" : "重新体检", systemImage: "arrow.clockwise")
                }
                .buttonStyle(.bordered)
                .controlSize(.regular)
                .disabled(store.isCheckingEnvironment || store.isInstallingCLI)
            }

            Divider().opacity(0.35)

            VStack(spacing: 12) {
                doctorCheckRow(
                    title: "官方 google-colab-cli",
                    ok: store.cliInstalled,
                    detail: store.cliInstalled
                        ? "已安装\(env.colabVersion.map { " v\($0)" } ?? "") · \(env.colabPath ?? "PATH 可用")"
                        : (env.colabBrokenReason ?? "未检测到 colab 命令，可点击下方按钮自动为你安装")
                )
                doctorCheckRow(
                    title: "包管理环境（uv / pipx / Homebrew）",
                    ok: env.uvPath != nil || env.pipxPath != nil || env.brewPath != nil,
                    detail: env.packageManagerSummary
                )
                doctorCheckRow(
                    title: "Google Colab 账号授权",
                    ok: !store.settings.accounts.isEmpty,
                    detail: store.settings.accounts.isEmpty
                        ? "尚未绑定账号（下一步可一键通过 Google 官方授权登录）"
                        : "已绑定 \(store.settings.accounts.count) 个账号"
                )
            }

            if !store.cliInstalled {
                Divider().opacity(0.35)
                VStack(alignment: .leading, spacing: 12) {
                    HStack(spacing: 8) {
                        Image(systemName: "wand.and.stars")
                            .font(.system(size: 15, weight: .semibold))
                            .foregroundStyle(ColabTheme.amber)
                        Text("自动安装方案：\(env.recommendedMethod.displayTitle)")
                            .font(ColabTypography.bodySemibold)
                    }

                    Text(env.recommendedMethod.manualShellCommand)
                        .font(ColabTypography.mono)
                        .textSelection(.enabled)
                        .padding(12)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(Color.primary.opacity(0.05), in: RoundedRectangle(cornerRadius: 9, style: .continuous))

                    if env.recommendedMethod.requiresRemoteScriptConsent {
                        Toggle(isOn: $agreedRemoteScript) {
                            Text("我知晓并将从 Astral 官方地址（astral.sh/uv/install.sh）安装 uv 及 google-colab-cli")
                                .font(ColabTypography.subheadline)
                                .foregroundStyle(.secondary)
                        }
                        .toggleStyle(.checkbox)
                    }

                    HStack(spacing: 10) {
                        if store.isInstallingCLI {
                            HStack(spacing: 8) {
                                ProgressView().controlSize(.small)
                                Text(store.isCancellingCLIInstall ? "正在终止安装进程组…" : "正在安装 google-colab-cli，请稍候…")
                                    .font(ColabTypography.bodyMedium)
                            }
                            Button(store.isCancellingCLIInstall ? "正在取消…" : "取消安装") {
                                store.cancelCLIInstall()
                            }
                            .buttonStyle(.bordered)
                            .controlSize(.regular)
                            .disabled(store.isCancellingCLIInstall)
                        } else {
                            let installDisabled = env.recommendedMethod.requiresRemoteScriptConsent && !agreedRemoteScript
                            Button {
                                showInstallLogs = true
                                store.installColabCLIAutomatically()
                            } label: {
                                Label(env.recommendedMethod.shortButtonTitle, systemImage: "arrow.down.circle.fill")
                                    .font(ColabTypography.bodySemibold)
                                    .foregroundStyle(installDisabled ? Color.secondary : Color.black.opacity(0.88))
                                    .padding(.horizontal, 18)
                                    .padding(.vertical, 9)
                                    .background(
                                        installDisabled ? Color.primary.opacity(0.08) : ColabTheme.amber,
                                        in: RoundedRectangle(cornerRadius: 9, style: .continuous)
                                    )
                            }
                            .buttonStyle(.plain)
                            .disabled(installDisabled)

                            Button {
                                store.copyToClipboard(env.recommendedMethod.manualShellCommand, label: "CLI 安装命令")
                            } label: {
                                Label("复制命令自己装", systemImage: "doc.on.doc")
                                    .font(ColabTypography.bodyMedium)
                                    .foregroundStyle(.primary)
                                    .padding(.horizontal, 14)
                                    .padding(.vertical, 9)
                                    .background(Color.primary.opacity(0.07), in: RoundedRectangle(cornerRadius: 9, style: .continuous))
                                    .overlay(
                                        RoundedRectangle(cornerRadius: 9, style: .continuous)
                                            .strokeBorder(Color.primary.opacity(0.12), lineWidth: 0.7)
                                    )
                            }
                            .buttonStyle(.plain)
                        }
                        Spacer()
                    }

                    if let err = store.cliInstallError, !err.isEmpty {
                        HStack(spacing: 8) {
                            Image(systemName: "exclamationmark.triangle.fill")
                                .foregroundStyle(ColabTheme.coral)
                            Text(err)
                                .font(ColabTypography.subheadline)
                                .foregroundStyle(ColabTheme.coral)
                        }
                    }

                    if !store.cliInstallLogs.isEmpty {
                        DisclosureGroup("安装实时输出日志（\(store.cliInstallLogs.count) 行）", isExpanded: $showInstallLogs) {
                            ScrollView {
                                Text(store.cliInstallLogs.joined(separator: "\n"))
                                    .font(ColabTypography.monoSmall)
                                    .foregroundStyle(.secondary)
                                    .textSelection(.enabled)
                                    .frame(maxWidth: .infinity, alignment: .leading)
                                    .padding(10)
                            }
                            .frame(height: 120)
                            .background(Color.black.opacity(0.22), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
                        }
                        .font(ColabTypography.subheadlineMedium)
                    }
                }
            } else {
                HStack {
                    Label("环境已就绪，google-colab-cli 可正常工作", systemImage: "checkmark.seal.fill")
                        .font(ColabTypography.bodyMedium)
                        .foregroundStyle(ColabTheme.emerald)
                    Spacer()
                    Button {
                        withAnimation(.snappy(duration: 0.16)) { step = 2 }
                    } label: {
                        Label("下一步：绑定 Google 账号", systemImage: "arrow.right")
                            .font(ColabTypography.bodySemibold)
                            .foregroundStyle(Color.black.opacity(0.88))
                            .padding(.horizontal, 18)
                            .padding(.vertical, 9)
                            .background(ColabTheme.amber, in: RoundedRectangle(cornerRadius: 9, style: .continuous))
                    }
                    .buttonStyle(.plain)
                }
                .padding(.top, 4)
            }
        }
        .padding(22)
        .bentoCard(cornerRadius: 16)
    }

    private func doctorCheckRow(title: String, ok: Bool, detail: String) -> some View {
        HStack(spacing: 12) {
            Image(systemName: ok ? "checkmark.circle.fill" : "exclamationmark.circle.fill")
                .font(.system(size: 18))
                .foregroundStyle(ok ? ColabTheme.emerald : ColabTheme.amber)
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(ColabTypography.bodySemibold)
                Text(detail)
                    .font(ColabTypography.subheadline)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            CardBadge(text: ok ? "已就绪" : "待完成", tint: ok ? ColabTheme.emerald : ColabTheme.amber)
        }
        .padding(12)
        .background(Color.primary.opacity(0.035), in: RoundedRectangle(cornerRadius: 11, style: .continuous))
    }

    // MARK: - Step 2: Account Binding
    private var step2AccountCard: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack {
                VStack(alignment: .leading, spacing: 3) {
                    Text("绑定你的第一个 Google Colab 账号")
                        .font(.system(size: 16, weight: .semibold))
                    Text("凭证仅加密存于本机 macOS 钥匙串；支持同时绑定多个 AI Ultra / Pro+ / Pro / Free 账号。")
                        .font(ColabTypography.subheadline)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Button {
                    Task {
                        let ok = await store.tryImportSystemDefaultAccount()
                        if !ok { store.showToast("未检测到终端默认 colab 登录，请用下方按钮走官方授权") }
                    }
                } label: {
                    Label("自动导入终端已登账号", systemImage: "person.badge.key")
                }
                .buttonStyle(.bordered)
                .controlSize(.regular)
                .disabled(!store.cliInstalled || store.isImportingSystemAccount)
            }

            if !store.settings.accounts.isEmpty {
                VStack(alignment: .leading, spacing: 10) {
                    ForEach(store.settings.accounts) { acc in
                        let st = store.state(for: acc.id)
                        HStack(spacing: 10) {
                            Circle()
                                .fill(ColabTheme.color(for: acc.colorTag))
                                .frame(width: 10, height: 10)
                            VStack(alignment: .leading, spacing: 2) {
                                Text("\(acc.displayName) · \(acc.tier.displayName)")
                                    .font(ColabTypography.bodySemibold)
                                Text(acc.email.isEmpty ? "默认官方 CLI 账号" : acc.email)
                                    .font(ColabTypography.subheadline)
                                    .foregroundStyle(.secondary)
                            }
                            Spacer()
                            Text("\(Formatters.cu(st.cuBalance)) CU")
                                .font(.system(size: 14, weight: .semibold, design: .rounded))
                                .monospacedDigit()
                            CardBadge(text: "已绑定", tint: ColabTheme.emerald)
                        }
                        .padding(12)
                        .background(ColabTheme.emerald.opacity(0.08), in: RoundedRectangle(cornerRadius: 11, style: .continuous))
                    }

                    HStack {
                        Spacer()
                        Button {
                            withAnimation(.snappy(duration: 0.16)) { step = 3 }
                        } label: {
                            Label("下一步：30 秒快速上手", systemImage: "arrow.right")
                                .font(ColabTypography.bodySemibold)
                                .foregroundStyle(Color.black.opacity(0.88))
                                .padding(.horizontal, 18)
                                .padding(.vertical, 9)
                                .background(ColabTheme.amber, in: RoundedRectangle(cornerRadius: 9, style: .continuous))
                        }
                        .buttonStyle(.plain)
                    }
                    .padding(.top, 4)
                }
            } else {
                Divider().opacity(0.35)
                AddAccountSheetView(store: store, embedded: true) {
                    withAnimation(.snappy(duration: 0.16)) { step = 3 }
                }
            }
        }
        .padding(22)
        .bentoCard(cornerRadius: 16)
    }

    // MARK: - Step 3: 30-Second Feature Tour
    private var step3TourCard: some View {
        VStack(alignment: .leading, spacing: 18) {
            VStack(alignment: .leading, spacing: 4) {
                Text("一切就绪！30 秒掌握三个最实用的功能")
                    .font(.system(size: 16, weight: .semibold))
                Text("平时关闭主窗口后，Colab Tools 会安静常驻在顶部菜单栏；随时点击菜单栏展开图标或按 ⌘, 呼出大屏。")
                    .font(ColabTypography.subheadline)
                    .foregroundStyle(.secondary)
            }

            HStack(spacing: 14) {
                tourFeatureTile(
                    icon: "clock.badge.checkmark",
                    tint: ColabTheme.amber,
                    title: "顺位抢卡与错峰预约",
                    desc: "勾选想要的显卡并排好顺位，设好两阶段保底卡；推荐约清晨 06:30–07:30 欧美低峰自动拿卡。"
                )
                tourFeatureTile(
                    icon: "timer",
                    tint: ColabTheme.emerald,
                    title: "空闲自动关机省点数",
                    desc: "支持 LLM、视频、生图与训练负载；优先读应用状态，兜底需 GPU+CPU 双低载才会关机，防误杀不烧钱。"
                )
                tourFeatureTile(
                    icon: "puzzlepiece.extension",
                    tint: ColabTheme.sky,
                    title: "全生态客户端接入",
                    desc: "自动检测本机 CC Switch、OpenCode、Codex CLI 与 Claude Code；其余工具支持 OpenAI / Anthropic 双协议一键复制。"
                )
            }

            HStack {
                Button("返回上一步") {
                    withAnimation(.snappy(duration: 0.16)) { step = 2 }
                }
                .buttonStyle(.bordered)
                .controlSize(.large)

                Spacer()

                Button {
                    store.completeOnboarding()
                } label: {
                    Label("进入 Colab Tools 主控制台", systemImage: "sparkles")
                        .font(ColabTypography.bodySemibold)
                        .foregroundStyle(Color.black.opacity(0.88))
                        .padding(.horizontal, 18)
                        .padding(.vertical, 9)
                        .background(ColabTheme.amber, in: RoundedRectangle(cornerRadius: 9, style: .continuous))
                }
                .buttonStyle(.plain)
            }
        }
        .padding(22)
        .bentoCard(cornerRadius: 16)
    }

    private func tourFeatureTile(icon: String, tint: Color, title: String, desc: String) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            Image(systemName: icon)
                .font(.system(size: 20, weight: .semibold))
                .foregroundStyle(tint)
                .frame(width: 38, height: 38)
                .background(tint.opacity(0.14), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
            Text(title)
                .font(ColabTypography.sectionTitle)
            Text(desc)
                .font(ColabTypography.subheadline)
                .foregroundStyle(.secondary)
                .lineSpacing(3)
                .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 0)
        }
        .padding(16)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .background(Color.primary.opacity(0.038), in: RoundedRectangle(cornerRadius: 13, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 13, style: .continuous).strokeBorder(Color.primary.opacity(0.08), lineWidth: 0.6))
    }
}
