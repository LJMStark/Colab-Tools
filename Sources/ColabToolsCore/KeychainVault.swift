import Darwin
import Foundation
import Security

/// Encrypted macOS Keychain vault for isolated multi-account Colab OAuth2 credentials and LLM API keys.
/// Never leaves persistent plaintext tokens on disk.
public enum KeychainVault {
    public static let serviceName = "io.github.ljmstark.colabtools"
    public static let ephemeralDir = AppSettings.configDir.appendingPathComponent(".ephemeral", isDirectory: true)

    public enum VaultError: LocalizedError {
        case encodingFailed
        case keychainStatus(OSStatus)
        case invalidCredentialJSON
        case ephemeralCreationFailed(String)

        public var errorDescription: String? {
            switch self {
            case .encodingFailed: return "凭证编码失败"
            case .keychainStatus(let s): return "钥匙串操作失败 (OSStatus: \(s))"
            case .invalidCredentialJSON: return "凭证 JSON 格式无效，缺少 refresh_token / client_id"
            case .ephemeralCreationFailed(let reason): return "创建受限临时凭证失败: \(reason)"
            }
        }
    }

    private static func oauthAccountKey(for profileId: UUID) -> String { "account:\(profileId.uuidString)" }
    private static func apiKeyAccountKey(for profileId: UUID) -> String { "apikey:\(profileId.uuidString)" }

    /// Normalizes a Google OAuth2 credential JSON into official ADC `authorized_user` format
    /// so `colab --auth=adc` honors it via `GOOGLE_APPLICATION_CREDENTIALS`.
    public static func normalizeToAuthorizedUserJSON(_ rawJSON: String) throws -> String {
        guard let data = rawJSON.data(using: .utf8),
              var obj = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            throw VaultError.invalidCredentialJSON
        }
        guard obj["refresh_token"] != nil, obj["client_id"] != nil else {
            throw VaultError.invalidCredentialJSON
        }
        if obj["type"] == nil {
            obj["type"] = "authorized_user"
        }
        let out = try JSONSerialization.data(withJSONObject: obj, options: [.sortedKeys])
        guard let str = String(data: out, encoding: .utf8) else {
            throw VaultError.encodingFailed
        }
        return str
    }

    public static func saveCredentialJSON(_ rawJSON: String, for profileId: UUID) throws {
        let normalized = try normalizeToAuthorizedUserJSON(rawJSON)
        try saveSecret(normalized, account: oauthAccountKey(for: profileId))
    }

    public static func loadCredentialJSON(for profileId: UUID) -> String? {
        loadSecret(account: oauthAccountKey(for: profileId))
    }

    public static func deleteCredential(for profileId: UUID) {
        deleteSecret(account: oauthAccountKey(for: profileId))
        deleteSecret(account: apiKeyAccountKey(for: profileId))
    }

    public static func saveAPIKey(_ apiKey: String, for profileId: UUID) {
        guard !apiKey.isEmpty else { return }
        try? saveSecret(apiKey, account: apiKeyAccountKey(for: profileId))
    }

    public static func loadAPIKey(for profileId: UUID) -> String? {
        loadSecret(account: apiKeyAccountKey(for: profileId))
    }

    private static func saveSecret(_ value: String, account: String) throws {
        guard let data = value.data(using: .utf8) else { throw VaultError.encodingFailed }
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: serviceName,
            kSecAttrAccount as String: account
        ]
        SecItemDelete(query as CFDictionary)

        var addQuery = query
        addQuery[kSecValueData as String] = data
        addQuery[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlock
        let status = SecItemAdd(addQuery as CFDictionary, nil)
        guard status == errSecSuccess else {
            throw VaultError.keychainStatus(status)
        }
    }

    private static func loadSecret(account: String) -> String? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: serviceName,
            kSecAttrAccount as String: account,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne
        ]
        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        guard status == errSecSuccess, let data = item as? Data else { return nil }
        return String(data: data, encoding: .utf8)
    }

    private static func deleteSecret(account: String) {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: serviceName,
            kSecAttrAccount as String: account
        ]
        SecItemDelete(query as CFDictionary)
    }

    // MARK: - Ephemeral 0600 Files (Crash-Safe Cleanup + O_EXCL Creation)

    public static func ensurePrivateEphemeralDir() throws {
        let fm = FileManager.default
        if !fm.fileExists(atPath: ephemeralDir.path) {
            try fm.createDirectory(at: ephemeralDir, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        }
        try fm.setAttributes([.posixPermissions: 0o700], ofItemAtPath: ephemeralDir.path)
    }

    /// Creates an exclusive owner-only (`0600`, `O_CREAT | O_EXCL | O_NOFOLLOW`) temporary file.
    public static func writeEphemeralFile(prefix: String, ext: String, content: String, executable: Bool = false) throws -> URL {
        try ensurePrivateEphemeralDir()
        let pid = ProcessInfo.processInfo.processIdentifier
        let fileURL = ephemeralDir.appendingPathComponent("\(prefix)-\(pid)-\(UUID().uuidString).\(ext)")
        let mode: mode_t = executable ? 0o700 : 0o600
        let fd = open(fileURL.path, O_WRONLY | O_CREAT | O_EXCL | O_NOFOLLOW, mode)
        guard fd >= 0 else {
            throw VaultError.ephemeralCreationFailed(String(cString: strerror(errno)))
        }
        defer { close(fd) }

        let data = Data(content.utf8)
        let written: Int = data.withUnsafeBytes { ptr in
            guard let base = ptr.baseAddress else { return 0 }
            return write(fd, base, data.count)
        }
        guard written == data.count else {
            unlink(fileURL.path)
            throw VaultError.ephemeralCreationFailed("写入字节数不完整")
        }
        return fileURL
    }

    public static func shredAndRemove(_ url: URL) {
        if let attrs = try? FileManager.default.attributesOfItem(atPath: url.path),
           let size = attrs[.size] as? Int, size > 0,
           let fh = try? FileHandle(forWritingTo: url) {
            try? fh.write(contentsOf: Data(repeating: 0, count: min(size, 65536)))
            try? fh.close()
        }
        try? FileManager.default.removeItem(at: url)
    }

    /// Removes stale ephemeral files left behind by dead PIDs (e.g. after crashes or force-quit).
    public static func cleanupStaleEphemeralFiles() {
        let fm = FileManager.default
        guard let entries = try? fm.contentsOfDirectory(at: ephemeralDir, includingPropertiesForKeys: [.contentModificationDateKey]) else {
            return
        }
        let myPid = ProcessInfo.processInfo.processIdentifier
        let now = Date()
        for url in entries {
            let name = url.lastPathComponent
            let parts = name.split(separator: "-")
            if parts.count >= 2, let filePid = Int32(parts[1]) {
                if filePid == myPid { continue }
                // kill(pid, 0) == 0 means process still alive; ESRCH means dead
                if kill(filePid, 0) == 0 || errno == EPERM { continue }
                shredAndRemove(url)
            } else if let mtime = (try? url.resourceValues(forKeys: [.contentModificationDateKey]))?.contentModificationDate,
                      now.timeIntervalSince(mtime) > 300 {
                shredAndRemove(url)
            }
        }
    }

    /// Creates an exclusive `0600` ephemeral ADC file for a single official `colab --auth=adc` invocation,
    /// executes `operation`, and securely shreds + deletes the file in `defer`.
    public static func withEphemeralADCFile<T>(
        for profileId: UUID,
        operation: (String) async throws -> T
    ) async throws -> T {
        guard let json = loadCredentialJSON(for: profileId) else {
            throw VaultError.invalidCredentialJSON
        }
        let fileURL = try writeEphemeralFile(prefix: "adc", ext: "json", content: json)
        defer { shredAndRemove(fileURL) }
        return try await operation(fileURL.path)
    }
}
