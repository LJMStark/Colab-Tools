import AppKit
import ColabToolsCore
import SwiftUI

/// Guided official Google OAuth2 account onboarding flow.
/// Uses the installed official `google-colab-cli` OAuth client & scopes, stores credentials
/// exclusively in macOS Keychain under an isolated profile ID without overwriting the default CLI login.
struct AddAccountSheetView: View {
    @ObservedObject var store: WorkspaceStore
    let onClose: () -> Void

    @State private var displayName: String = ""
    @State private var selectedTier: SubscriptionTier = .colabPro
    @State private var selectedColor: AccountColorTag = .emerald
    @State private var sessionName: String = "colab-\(String(UUID().uuidString.prefix(4)).lowercased())"
    @State private var enableLLM: Bool = false

    @State private var ticket: OAuthSessionTicket? = nil
    @State private var authCode: String = ""
    @State private var isBusy: Bool = false
    @State private var errorMsg: String? = nil

    var body: some View {
        VStack(alignment: .leading, spacing: 11) {
            HStack {
                Text("添加新的 Colab 订阅账号")
                    .font(.system(size: 13, weight: .semibold))
                Spacer()
                Button {
                    onClose()
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .foregroundStyle(.tertiary)
                        .font(.system(size: 14))
                }
                .buttonStyle(.plain)
            }

            Text("全程走官方 Google OAuth2 授权流程，凭证加密存于 macOS 钥匙串独立条目，绝不覆盖终端默认 colab 登录状态。")
                .font(.system(size: 10.5))
                .foregroundStyle(.secondary)

            VStack(alignment: .leading, spacing: 8) {
                HStack {
                    Text("账号备注")
                        .font(.system(size: 11))
                        .frame(width: 60, alignment: .leading)
                    TextField("如：工作 Pro+ / 副号 AI Pro", text: $displayName)
                        .textFieldStyle(.roundedBorder)
                        .controlSize(.small)
                }

                HStack {
                    Text("订阅档位")
                        .font(.system(size: 11))
                        .frame(width: 60, alignment: .leading)
                    Picker("", selection: $selectedTier) {
                        ForEach(SubscriptionTier.allCases) { t in
                            Text(t.displayName).tag(t)
                        }
                    }
                    .labelsHidden()
                    .controlSize(.small)
                }

                HStack {
                    Text("会话名称")
                        .font(.system(size: 11))
                        .frame(width: 60, alignment: .leading)
                    TextField("colab-work", text: $sessionName)
                        .textFieldStyle(.roundedBorder)
                        .controlSize(.small)
                }

                HStack(spacing: 8) {
                    Text("颜色标记")
                        .font(.system(size: 11))
                        .frame(width: 60, alignment: .leading)
                    ForEach(AccountColorTag.allCases) { tag in
                        Button {
                            selectedColor = tag
                        } label: {
                            Circle()
                                .fill(ColabTheme.color(for: tag))
                                .frame(width: 15, height: 15)
                                .overlay {
                                    if selectedColor == tag {
                                        Circle().strokeBorder(Color.primary, lineWidth: 1.5).padding(-2)
                                    }
                                }
                        }
                        .buttonStyle(.plain)
                    }
                    Spacer()
                }

                Toggle("启用 LLM 推理与 CC Switch 独立渠道", isOn: $enableLLM)
                    .font(.system(size: 11))
                    .controlSize(.mini)
            }
            .padding(12)
            .hudCard()

            // Step 1 & 2: Official OAuth2 Browser Consent
            VStack(alignment: .leading, spacing: 8) {
                if let t = ticket {
                    Text("第 2 步：在浏览器完成登录授权后，将页面显示的授权码粘贴到下方：")
                        .font(.system(size: 11, weight: .medium))

                    HStack(spacing: 6) {
                        Button("重新打开授权页") {
                            if let u = URL(string: t.authURL) { NSWorkspace.shared.open(u) }
                        }
                        .buttonStyle(.bordered)
                        .controlSize(.small)

                        Button("复制链接") {
                            store.copyToClipboard(t.authURL, label: "官方授权链接")
                        }
                        .buttonStyle(.bordered)
                        .controlSize(.small)
                    }

                    TextField("粘贴 Google Authorization Code (4/0A...)", text: $authCode)
                        .textFieldStyle(.roundedBorder)
                        .font(.system(size: 11, design: .monospaced))
                        .controlSize(.small)

                    Button {
                        Task { await finalizeOAuth(ticket: t) }
                    } label: {
                        HStack {
                            if isBusy { ProgressView().controlSize(.small) }
                            Text(isBusy ? "验证并识别点数中…" : "完成绑定并识别点数")
                                .font(.system(size: 11.5, weight: .medium))
                        }
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 6)
                        .foregroundStyle(Color(nsColor: .windowBackgroundColor))
                        .background(Color.primary, in: RoundedRectangle(cornerRadius: 7))
                    }
                    .buttonStyle(.plain)
                    .disabled(isBusy || authCode.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                } else {
                    Button {
                        Task { await startOAuth() }
                    } label: {
                        HStack {
                            if isBusy { ProgressView().controlSize(.small) }
                            Label(isBusy ? "正在生成官方授权链接…" : "打开 Google 官方授权页面", systemImage: "safari")
                                .font(.system(size: 11.5, weight: .medium))
                        }
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 7)
                        .foregroundStyle(Color(nsColor: .windowBackgroundColor))
                        .background(Color.primary, in: RoundedRectangle(cornerRadius: 7))
                    }
                    .buttonStyle(.plain)
                    .disabled(isBusy)
                }

                if let err = errorMsg {
                    Text(err)
                        .font(.system(size: 10.5))
                        .foregroundStyle(Color(nsColor: .systemRed))
                }
            }
            .padding(12)
            .hudCard()
        }
    }

    private func startOAuth() async {
        isBusy = true
        errorMsg = nil
        defer { isBusy = false }

        let result = await ColabCLIEngine.beginOfficialOAuthFlow()
        switch result {
        case .success(let t):
            ticket = t
            if let u = URL(string: t.authURL) {
                NSWorkspace.shared.open(u)
            }
        case .failure(let err):
            errorMsg = err.localizedDescription
        }
    }

    private func finalizeOAuth(ticket: OAuthSessionTicket) async {
        isBusy = true
        errorMsg = nil
        defer { isBusy = false }

        let exchanged = await ColabCLIEngine.completeOfficialOAuthFlow(
            ticket: ticket,
            authorizationCode: authCode.trimmingCharacters(in: .whitespacesAndNewlines)
        )
        switch exchanged {
        case .failure(let err):
            errorMsg = err.localizedDescription
        case .success(let credJSON):
            let newId = UUID()
            do {
                try KeychainVault.saveCredentialJSON(credJSON, for: newId)
            } catch {
                errorMsg = error.localizedDescription
                return
            }

            let shortSlug = newId.uuidString.prefix(6).lowercased()
            let cleanSession = sessionName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                ? "colab-\(shortSlug)"
                : sessionName.trimmingCharacters(in: .whitespacesAndNewlines)

            var profile = AccountProfile(
                id: newId,
                displayName: displayName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? "账号 \(store.settings.accounts.count + 1)" : displayName,
                email: "",
                colorTag: selectedColor,
                tier: selectedTier,
                authMode: .isolatedKeychainADC,
                sessionName: cleanSession,
                preferredAccelerator: selectedTier.defaultAccelerator,
                enableLLMModule: enableLLM,
                ccSwitchProviderId: "colab-tools-\(shortSlug)",
                ccSwitchProviderName: "Colab Tools · \(displayName.isEmpty ? "账号\(store.settings.accounts.count + 1)" : displayName)",
                localSSHTunnelPort: 18080 + store.settings.accounts.count
            )

            // Strictly verify credentials via official `colab --auth=adc whoami` before persisting; rollback on failure
            guard let who = await ColabCLIEngine.whoami(profile: profile) else {
                KeychainVault.deleteCredential(for: newId)
                errorMsg = "官方 colab whoami 校验失败，已回滚凭证，请确认授权码有效并重试"
                return
            }
            profile.email = who.email
            if displayName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                profile.displayName = who.email.split(separator: "@").first.map(String.init) ?? profile.displayName
                profile.ccSwitchProviderName = "Colab Tools · \(profile.displayName)"
            }

            let usageRes = await ColabCLIEngine.fetchUsageAndSessions(profile: profile)
            if usageRes.ok, let balance = usageRes.usage?.balance, balance > 0 {
                let suggested = SubscriptionTier.suggested(forBalance: balance)
                if selectedTier == .colabPro && suggested != .free {
                    profile.tier = suggested
                    profile.preferredAccelerator = suggested.defaultAccelerator
                }
            }

            await store.addAccountProfile(profile)
            onClose()
        }
    }
}
