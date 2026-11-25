import Darwin
import Foundation

public struct ClientDetectionSnapshot: Equatable, Sendable {
    public let ccSwitchInstalled: Bool
    public let openCodeInstalled: Bool
    public let codexInstalled: Bool
    public let claudeCodeInstalled: Bool

    public init(
        ccSwitchInstalled: Bool = false,
        openCodeInstalled: Bool = false,
        codexInstalled: Bool = false,
        claudeCodeInstalled: Bool = false
    ) {
        self.ccSwitchInstalled = ccSwitchInstalled
        self.openCodeInstalled = openCodeInstalled
        self.codexInstalled = codexInstalled
        self.claudeCodeInstalled = claudeCodeInstalled
    }
}

public struct ClientAccountIdentifiers: Equatable, Sendable {
    public let openCodeProviderKey: String
    public let codexTableKey: String
    public let envKey: String

    public init(accountId: UUID) {
        let compact = accountId.uuidString.replacingOccurrences(of: "-", with: "").lowercased()
        self.openCodeProviderKey = "colab-tools-\(compact)"
        self.codexTableKey = "colab_tools_\(compact)"
        self.envKey = "COLAB_TOOLS_API_KEY_\(compact.uppercased())"
    }
}

public enum ClientSyncError: Error, LocalizedError, Equatable {
    case invalidEndpointURL
    case invalidModelAlias
    case invalidIdentifier(String)
    case unmanagedConflict(String)
    case malformedExistingConfig(String)
    case unsafeFilePath(String)
    case ioError(String)

    public var errorDescription: String? {
        switch self {
        case .invalidEndpointURL: return "服务端点地址格式无效"
        case .invalidModelAlias: return "模型别名包含非法字符"
        case .invalidIdentifier(let msg): return "配置标识符不合法：\(msg)"
        case .unmanagedConflict(let msg): return "检测到非本应用托管的同名配置，为安全已取消写入：\(msg)"
        case .malformedExistingConfig(let msg): return "现有配置文件格式异常或读取失败，已保留原文件：\(msg)"
        case .unsafeFilePath(let msg): return "目标路径或备份目录不安全，已取消写入：\(msg)"
        case .ioError(let msg): return "读写配置文件失败：\(msg)"
        }
    }
}

public enum CopyProtocolKind: String, CaseIterable, Identifiable, Sendable {
    case openAI = "OpenAI (/v1)"
    case anthropic = "Anthropic"
    public var id: String { rawValue }
}

/// Safe, non-destructive multi-client integration engine.
/// - Per-account isolated keys derived from full account UUID so multiple accounts never collide or cross-send credentials.
/// - Serialized in-process writes, strict read/UTF-8 failure handling, symlink/ownership verification, and `0700`/`0600` private backups.
/// - NEVER modifies existing official default providers and NEVER persists plaintext API keys into OpenCode/Codex config files.
public enum ClientIntegrations {
    public static let openCodeConfigURL = FileManager.default.homeDirectoryForCurrentUser
        .appendingPathComponent(".config/opencode/opencode.json")
    public static let codexConfigURL = FileManager.default.homeDirectoryForCurrentUser
        .appendingPathComponent(".codex/config.toml")
    public static let integrationBackupDir = AppSettings.configDir
        .appendingPathComponent("backups", isDirectory: true)

    private static let keyRegex = try! NSRegularExpression(pattern: "^[A-Za-z0-9_-]{1,64}$")
    private static let envRegex = try! NSRegularExpression(pattern: "^[A-Z0-9_]{1,80}$")
    private static let writeQueue = DispatchQueue(label: "io.github.ljmstark.colabtools.client-integrations")

    public static func identifiers(for accountId: UUID) -> ClientAccountIdentifiers {
        ClientAccountIdentifiers(accountId: accountId)
    }

    public static func detectInstalledTools() -> ClientDetectionSnapshot {
        let fm = FileManager.default
        let home = fm.homeDirectoryForCurrentUser.path
        let binDirs = ["\(home)/.local/bin", "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin"]

        func hasBinary(_ name: String) -> Bool {
            binDirs.contains { fm.isExecutableFile(atPath: "\($0)/\(name)") }
        }

        let ccSwitch = CCSwitchSync.isInstalled
        let openCode = fm.fileExists(atPath: openCodeConfigURL.path)
            || fm.fileExists(atPath: "\(home)/.config/opencode")
            || hasBinary("opencode")
        let codex = fm.fileExists(atPath: codexConfigURL.path)
            || fm.fileExists(atPath: "\(home)/.codex")
            || hasBinary("codex")
        let claude = hasBinary("claude") || fm.fileExists(atPath: "\(home)/.claude")

        return ClientDetectionSnapshot(
            ccSwitchInstalled: ccSwitch,
            openCodeInstalled: openCode,
            codexInstalled: codex,
            claudeCodeInstalled: claude
        )
    }

    /// Ensures trailing `/v1` for OpenAI-compatible clients without duplicating `/v1`.
    public static func openAIBaseURL(from baseURL: String) -> String {
        let trimmed = baseURL.trimmingCharacters(in: .whitespacesAndNewlines)
            .trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        if trimmed.hasSuffix("/v1") { return trimmed }
        return "\(trimmed)/v1"
    }

    /// Safely merges an isolated standby provider into `~/.config/opencode/opencode.json`.
    /// - Uses `{env:<envKey>}` so secrets are NEVER written in plaintext to disk.
    /// - Refuses to overwrite an existing unmanaged provider with the same key.
    /// - Preserves any user-added models/options inside a managed provider entry.
    /// - Serializes writes and creates a `0600` timestamped backup inside a `0700` backup directory.
    @discardableResult
    public static func syncOpenCode(
        baseURL: String,
        modelAlias: String,
        providerKey: String = "colab-tools",
        envKey: String = "COLAB_TOOLS_API_KEY",
        displayName: String = "Colab Tools",
        configURL: URL = openCodeConfigURL,
        backupDir: URL = integrationBackupDir
    ) throws -> URL {
        guard InputValidator.isValidEndpointURL(baseURL) else { throw ClientSyncError.invalidEndpointURL }
        guard InputValidator.isValidModelAlias(modelAlias) else { throw ClientSyncError.invalidModelAlias }
        guard isValidKey(providerKey) else { throw ClientSyncError.invalidIdentifier("providerKey") }
        guard isValidEnvKey(envKey) else { throw ClientSyncError.invalidIdentifier("envKey") }

        return try writeQueue.sync {
            let target = try resolveSafeWritableTarget(configURL)
            let fm = FileManager.default
            var rootObj: [String: Any] = [:]

            if target.exists {
                let raw: Data
                do {
                    raw = try Data(contentsOf: target.resolvedURL)
                } catch {
                    throw ClientSyncError.malformedExistingConfig("无法读取 opencode.json：\(error.localizedDescription)")
                }
                if !raw.isEmpty {
                    let obj: Any
                    do {
                        obj = try JSONSerialization.jsonObject(with: raw)
                    } catch {
                        throw ClientSyncError.malformedExistingConfig("opencode.json 解析失败：\(error.localizedDescription)")
                    }
                    guard let parsed = obj as? [String: Any] else {
                        throw ClientSyncError.malformedExistingConfig("opencode.json 顶层不是 JSON 对象")
                    }
                    rootObj = parsed
                }
                try createPrivateBackup(of: target.resolvedURL, prefix: "opencode", ext: "json", backupDir: backupDir)
            } else {
                try fm.createDirectory(at: target.resolvedURL.deletingLastPathComponent(), withIntermediateDirectories: true)
                rootObj["$schema"] = "https://opencode.ai/config.json"
            }

            var providers: [String: Any]
            if let existingProviders = rootObj["provider"] {
                guard let dict = existingProviders as? [String: Any] else {
                    throw ClientSyncError.malformedExistingConfig("opencode.json 的 provider 字段不是对象")
                }
                providers = dict
            } else {
                providers = [:]
            }

            var entry: [String: Any]
            if let existingEntry = providers[providerKey] {
                guard let dict = existingEntry as? [String: Any], isManagedOpenCodeEntry(dict) else {
                    throw ClientSyncError.unmanagedConflict("opencode.json 中已存在非本应用创建的 provider.\(providerKey)")
                }
                entry = dict
            } else {
                entry = [:]
            }

            var options = (entry["options"] as? [String: Any]) ?? [:]
            options["baseURL"] = openAIBaseURL(from: baseURL)
            options["apiKey"] = "{env:\(envKey)}"

            var models = (entry["models"] as? [String: Any]) ?? [:]
            var modelEntry = (models[modelAlias] as? [String: Any]) ?? [:]
            modelEntry["name"] = "\(modelAlias) (Colab)"
            models[modelAlias] = modelEntry

            entry["npm"] = "@ai-sdk/openai-compatible"
            entry["name"] = displayName
            entry["options"] = options
            entry["models"] = models
            entry["x_colab_tools_managed"] = true

            providers[providerKey] = entry
            rootObj["provider"] = providers

            let outData: Data
            do {
                outData = try JSONSerialization.data(withJSONObject: rootObj, options: [.prettyPrinted, .sortedKeys])
            } catch {
                throw ClientSyncError.ioError("JSON 序列化失败：\(error.localizedDescription)")
            }
            try writeFilePreservingPermissions(outData, to: target.resolvedURL, existingPermissions: target.posixPermissions)
            return target.resolvedURL
        }
    }

    /// Safely writes or updates an isolated `[model_providers.<providerTableKey>]` managed block in `~/.codex/config.toml`.
    /// - Refuses to proceed if an existing file cannot be read or is not valid UTF-8 (NEVER overwrites as empty).
    /// - Validates table/env identifiers, requires unique standalone marker lines, rejects unmanaged duplicate tables,
    ///   and creates a `0600` private backup inside a `0700` directory before atomic write.
    @discardableResult
    public static func syncCodexCLI(
        baseURL: String,
        modelAlias: String,
        providerTableKey: String = "colab_tools",
        envKey: String = "COLAB_TOOLS_API_KEY",
        displayName: String = "Colab Tools",
        configURL: URL = codexConfigURL,
        backupDir: URL = integrationBackupDir
    ) throws -> URL {
        guard InputValidator.isValidEndpointURL(baseURL) else { throw ClientSyncError.invalidEndpointURL }
        guard InputValidator.isValidModelAlias(modelAlias) else { throw ClientSyncError.invalidModelAlias }
        guard isValidKey(providerTableKey) else { throw ClientSyncError.invalidIdentifier("providerTableKey") }
        guard isValidEnvKey(envKey) else { throw ClientSyncError.invalidIdentifier("envKey") }

        return try writeQueue.sync {
            let target = try resolveSafeWritableTarget(configURL)
            let fm = FileManager.default
            var existing = ""

            if target.exists {
                let raw: Data
                do {
                    raw = try Data(contentsOf: target.resolvedURL)
                } catch {
                    throw ClientSyncError.malformedExistingConfig("无法读取 config.toml：\(error.localizedDescription)")
                }
                guard let decoded = String(data: raw, encoding: .utf8) else {
                    throw ClientSyncError.malformedExistingConfig("config.toml 包含无效 UTF-8 字节")
                }
                existing = decoded
                try createPrivateBackup(of: target.resolvedURL, prefix: "codex-config", ext: "toml", backupDir: backupDir)
            } else {
                try fm.createDirectory(at: target.resolvedURL.deletingLastPathComponent(), withIntermediateDirectories: true)
            }

            let beginMarker = "# BEGIN COLAB-TOOLS MANAGED \(providerTableKey)"
            let endMarker = "# END COLAB-TOOLS MANAGED \(providerTableKey)"
            let legacyBegin = "# BEGIN COLAB-TOOLS MANAGED"
            let legacyEnd = "# END COLAB-TOOLS MANAGED"

            let lines = existing.components(separatedBy: "\n")
            let normalizedLines = lines.map { $0.replacingOccurrences(of: "\r", with: "") }

            // Track standalone marker lines only when NOT inside a TOML multi-line string
            var beginIndices: [Int] = []
            var endIndices: [Int] = []
            var inTripleDouble = false
            var inTripleSingle = false

            for (i, line) in normalizedLines.enumerated() {
                let trimmed = line.trimmingCharacters(in: .whitespaces)
                if !inTripleDouble && !inTripleSingle {
                    if trimmed == beginMarker || (providerTableKey == "colab_tools" && trimmed == legacyBegin) {
                        beginIndices.append(i)
                    } else if trimmed == endMarker || (providerTableKey == "colab_tools" && trimmed == legacyEnd) {
                        endIndices.append(i)
                    }
                }
                updateMultilineTOMLState(line: line, inTripleDouble: &inTripleDouble, inTripleSingle: &inTripleSingle)
            }

            if inTripleDouble || inTripleSingle {
                throw ClientSyncError.malformedExistingConfig("config.toml 存在未闭合的多行字符串")
            }
            if beginIndices.count != endIndices.count || beginIndices.count > 1 {
                throw ClientSyncError.malformedExistingConfig("config.toml 中 Colab Tools 托管标记不成对或重复")
            }
            if let b = beginIndices.first, let e = endIndices.first, b >= e {
                throw ClientSyncError.malformedExistingConfig("config.toml 中 Colab Tools 托管标记顺序错乱")
            }

            let managedRange: ClosedRange<Int>? = (beginIndices.first != nil && endIndices.first != nil)
                ? (beginIndices[0]...endIndices[0])
                : nil

            // Scan all unmanaged lines to guarantee no unmanaged `[model_providers.<providerTableKey>]` already exists
            inTripleDouble = false
            inTripleSingle = false
            for (i, line) in normalizedLines.enumerated() {
                let inManaged = managedRange?.contains(i) ?? false
                if !inManaged && !inTripleDouble && !inTripleSingle {
                    if isTOMLModelProviderHeader(line: line, tableKey: providerTableKey) {
                        throw ClientSyncError.unmanagedConflict("config.toml 已存在未托管的 [model_providers.\(providerTableKey)]")
                    }
                }
                updateMultilineTOMLState(line: line, inTripleDouble: &inTripleDouble, inTripleSingle: &inTripleSingle)
            }

            let safeName = escapeTOMLString(displayName)
            let safeURL = escapeTOMLString(openAIBaseURL(from: baseURL))
            let blockLines = [
                beginMarker,
                "[model_providers.\(providerTableKey)]",
                "name = \"\(safeName)\"",
                "base_url = \"\(safeURL)\"",
                "wire_api = \"responses\"",
                "env_key = \"\(envKey)\"",
                endMarker
            ]

            let updated: String
            if let range = managedRange {
                var outLines = normalizedLines
                outLines.replaceSubrange(range, with: blockLines)
                updated = outLines.joined(separator: "\n")
            } else if existing.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                updated = blockLines.joined(separator: "\n") + "\n"
            } else {
                let trimmedEnd = existing.replacingOccurrences(of: "\r\n", with: "\n")
                let sep = trimmedEnd.hasSuffix("\n\n") ? "" : (trimmedEnd.hasSuffix("\n") ? "\n" : "\n\n")
                updated = trimmedEnd + sep + blockLines.joined(separator: "\n") + "\n"
            }

            guard let data = updated.data(using: .utf8) else {
                throw ClientSyncError.ioError("UTF-8 编码失败")
            }
            try writeFilePreservingPermissions(data, to: target.resolvedURL, existingPermissions: target.posixPermissions)
            return target.resolvedURL
        }
    }

    public static func exportEnvSnippet(
        protocolKind: CopyProtocolKind,
        baseURL: String,
        modelAlias: String,
        apiKey: String = "",
        envKey: String = "COLAB_TOOLS_API_KEY",
        includeRawSecret: Bool = false
    ) -> String {
        let safeEnvKey = isValidEnvKey(envKey) ? envKey : "COLAB_TOOLS_API_KEY"
        let useSecret = includeRawSecret && !apiKey.isEmpty
        switch protocolKind {
        case .openAI:
            let keyLine = useSecret
                ? "export \(safeEnvKey)=\(ColabCLIEngine.shellEscape(apiKey))"
                : "export \(safeEnvKey)='<your-colab-api-key>'"
            return """
            \(keyLine)
            export OPENAI_BASE_URL=\(ColabCLIEngine.shellEscape(openAIBaseURL(from: baseURL)))
            export OPENAI_API_KEY="$\(safeEnvKey)"
            # Model: \(modelAlias)
            """
        case .anthropic:
            let cleanURL = baseURL.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
            let tokenLine = useSecret
                ? "export ANTHROPIC_AUTH_TOKEN=\(ColabCLIEngine.shellEscape(apiKey))"
                : "export ANTHROPIC_AUTH_TOKEN=\"${\(safeEnvKey):-your-colab-api-key}\""
            return """
            export ANTHROPIC_BASE_URL=\(ColabCLIEngine.shellEscape(cleanURL))
            \(tokenLine)
            export ANTHROPIC_MODEL=\(ColabCLIEngine.shellEscape(modelAlias))
            """
        }
    }

    public static func exportJSONSnippet(
        protocolKind: CopyProtocolKind,
        baseURL: String,
        modelAlias: String,
        apiKey: String = "",
        envKey: String = "COLAB_TOOLS_API_KEY",
        includeRawSecret: Bool = false
    ) -> String {
        let safeEnvKey = isValidEnvKey(envKey) ? envKey : "COLAB_TOOLS_API_KEY"
        let keyVal = (includeRawSecret && !apiKey.isEmpty) ? apiKey : "${\(safeEnvKey)}"
        let obj: [String: Any]
        switch protocolKind {
        case .openAI:
            obj = [
                "name": "Colab Tools",
                "type": "openai-compatible",
                "baseURL": openAIBaseURL(from: baseURL),
                "apiKey": keyVal,
                "model": modelAlias
            ]
        case .anthropic:
            obj = [
                "name": "Colab Tools",
                "type": "anthropic",
                "baseURL": baseURL.trimmingCharacters(in: CharacterSet(charactersIn: "/")),
                "authToken": keyVal,
                "model": modelAlias
            ]
        }
        guard let data = try? JSONSerialization.data(withJSONObject: obj, options: [.prettyPrinted, .sortedKeys]),
              let str = String(data: data, encoding: .utf8) else {
            return "{}"
        }
        return str
    }

    // MARK: - Private Safety & Validation Helpers

    private static func isValidKey(_ value: String) -> Bool {
        let range = NSRange(value.startIndex..., in: value)
        return keyRegex.firstMatch(in: value, range: range) != nil
    }

    private static func isValidEnvKey(_ value: String) -> Bool {
        let range = NSRange(value.startIndex..., in: value)
        return envRegex.firstMatch(in: value, range: range) != nil
    }

    private static func isManagedOpenCodeEntry(_ dict: [String: Any]) -> Bool {
        if let flag = dict["x_colab_tools_managed"] as? Bool, flag {
            return true
        }
        guard let npm = dict["npm"] as? String, npm == "@ai-sdk/openai-compatible",
              let opts = dict["options"] as? [String: Any],
              let apiKey = opts["apiKey"] as? String else {
            return false
        }
        return apiKey.hasPrefix("{env:COLAB_TOOLS_API_KEY") && apiKey.hasSuffix("}")
    }

    private static func isTOMLModelProviderHeader(line: String, tableKey: String) -> Bool {
        let trimmed = line.trimmingCharacters(in: .whitespaces)
        guard trimmed.hasPrefix("[") else { return false }
        let noComment = trimmed.split(separator: "#", maxSplits: 1, omittingEmptySubsequences: false).first.map(String.init) ?? trimmed
        let header = noComment.trimmingCharacters(in: .whitespaces)
        let exact = "[model_providers.\(tableKey)]"
        let quotedDouble = "[model_providers.\"\(tableKey)\"]"
        let quotedSingle = "[model_providers.'\(tableKey)']"
        return header == exact || header == quotedDouble || header == quotedSingle
    }

    private static func updateMultilineTOMLState(line: String, inTripleDouble: inout Bool, inTripleSingle: inout Bool) {
        let chars = Array(line)
        var i = 0
        while i < chars.count {
            if !inTripleDouble && !inTripleSingle && chars[i] == "#" {
                return
            }
            if !inTripleSingle && i + 2 < chars.count && chars[i] == "\"" && chars[i + 1] == "\"" && chars[i + 2] == "\"" {
                let escaped = i > 0 && chars[i - 1] == "\\"
                if !escaped {
                    inTripleDouble.toggle()
                    i += 3
                    continue
                }
            }
            if !inTripleDouble && i + 2 < chars.count && chars[i] == "'" && chars[i + 1] == "'" && chars[i + 2] == "'" {
                inTripleSingle.toggle()
                i += 3
                continue
            }
            i += 1
        }
    }

    private struct ResolvedTarget {
        let resolvedURL: URL
        let exists: Bool
        let posixPermissions: Int?
    }

    private static func resolveSafeWritableTarget(_ url: URL) throws -> ResolvedTarget {
        var st = stat()
        let currentUID = getuid()

        if lstat(url.path, &st) == 0 {
            let mode = st.st_mode & S_IFMT
            if mode == S_IFLNK {
                // Dotfiles symlink: resolve target, reject broken symlinks, directories, or foreign-owned targets
                let resolved = url.resolvingSymlinksInPath()
                var targetStat = stat()
                guard stat(resolved.path, &targetStat) == 0 else {
                    throw ClientSyncError.unsafeFilePath("符号链接目标不存在：\(url.path)")
                }
                guard (targetStat.st_mode & S_IFMT) == S_IFREG else {
                    throw ClientSyncError.unsafeFilePath("符号链接未指向普通文件：\(url.path)")
                }
                guard targetStat.st_uid == currentUID else {
                    throw ClientSyncError.unsafeFilePath("符号链接目标不属于当前用户：\(resolved.path)")
                }
                return ResolvedTarget(resolvedURL: resolved, exists: true, posixPermissions: Int(targetStat.st_mode & 0o777))
            }
            guard mode == S_IFREG else {
                throw ClientSyncError.unsafeFilePath("目标路径不是普通文件：\(url.path)")
            }
            guard st.st_uid == currentUID else {
                throw ClientSyncError.unsafeFilePath("目标文件不属于当前用户：\(url.path)")
            }
            return ResolvedTarget(resolvedURL: url, exists: true, posixPermissions: Int(st.st_mode & 0o777))
        }

        if errno == ENOENT {
            let parent = url.deletingLastPathComponent()
            var pStat = stat()
            if lstat(parent.path, &pStat) == 0 && (pStat.st_mode & S_IFMT) == S_IFLNK {
                let resolvedParent = parent.resolvingSymlinksInPath()
                var rpStat = stat()
                guard stat(resolvedParent.path, &rpStat) == 0,
                      (rpStat.st_mode & S_IFMT) == S_IFDIR,
                      rpStat.st_uid == currentUID else {
                    throw ClientSyncError.unsafeFilePath("父目录符号链接不安全：\(parent.path)")
                }
                return ResolvedTarget(resolvedURL: resolvedParent.appendingPathComponent(url.lastPathComponent), exists: false, posixPermissions: nil)
            }
            return ResolvedTarget(resolvedURL: url, exists: false, posixPermissions: nil)
        }

        throw ClientSyncError.ioError("无法检查目标文件状态（errno=\(errno)）")
    }

    private static func writeFilePreservingPermissions(_ data: Data, to url: URL, existingPermissions: Int?) throws {
        let fm = FileManager.default
        let dir = url.deletingLastPathComponent()
        let tmp = dir.appendingPathComponent(".\(url.lastPathComponent).colabtools.\(UUID().uuidString).tmp")
        let perm = existingPermissions ?? 0o600
        do {
            try data.write(to: tmp, options: [])
            try fm.setAttributes([.posixPermissions: perm], ofItemAtPath: tmp.path)
            if rename(tmp.path, url.path) != 0 {
                let err = errno
                try? fm.removeItem(at: tmp)
                throw ClientSyncError.ioError("原子替换文件失败（errno=\(err)）")
            }
        } catch let e as ClientSyncError {
            throw e
        } catch {
            try? fm.removeItem(at: tmp)
            throw ClientSyncError.ioError(error.localizedDescription)
        }
    }

    private static func escapeTOMLString(_ value: String) -> String {
        value
            .replacingOccurrences(of: "\\", with: "\\\\")
            .replacingOccurrences(of: "\"", with: "\\\"")
            .replacingOccurrences(of: "\n", with: " ")
            .replacingOccurrences(of: "\r", with: " ")
    }

    private static func createPrivateBackup(of src: URL, prefix: String, ext: String, backupDir: URL) throws {
        let fm = FileManager.default
        let currentUID = getuid()

        var dirStat = stat()
        if lstat(backupDir.path, &dirStat) == 0 {
            if (dirStat.st_mode & S_IFMT) == S_IFLNK || (dirStat.st_mode & S_IFMT) != S_IFDIR {
                throw ClientSyncError.unsafeFilePath("备份目录不能为符号链接或非目录：\(backupDir.path)")
            }
            guard dirStat.st_uid == currentUID else {
                throw ClientSyncError.unsafeFilePath("备份目录所有者异常：\(backupDir.path)")
            }
        } else {
            try fm.createDirectory(at: backupDir, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        }
        try? fm.setAttributes([.posixPermissions: 0o700], ofItemAtPath: backupDir.path)

        let df = DateFormatter()
        df.dateFormat = "yyyyMMdd-HHmmss-SSS"
        let nonce = UUID().uuidString.prefix(8).lowercased()
        let dst = backupDir.appendingPathComponent("\(prefix)-\(df.string(from: Date()))-\(nonce).\(ext).bak")

        var dstStat = stat()
        if lstat(dst.path, &dstStat) == 0 {
            throw ClientSyncError.unsafeFilePath("备份目标已存在：\(dst.path)")
        }

        let raw = try Data(contentsOf: src)
        let fd = open(dst.path, O_WRONLY | O_CREAT | O_EXCL | O_NOFOLLOW, 0o600)
        guard fd >= 0 else {
            throw ClientSyncError.ioError("无法安全创建私有备份文件（errno=\(errno)）")
        }
        defer { close(fd) }
        try raw.withUnsafeBytes { ptr in
            guard let base = ptr.baseAddress else { return }
            var remaining = raw.count
            var offset = 0
            while remaining > 0 {
                let n = Darwin.write(fd, base.advanced(by: offset), remaining)
                if n <= 0 {
                    throw ClientSyncError.ioError("写入备份文件失败（errno=\(errno)）")
                }
                offset += n
                remaining -= n
            }
        }
        try? fm.setAttributes([.posixPermissions: 0o600], ofItemAtPath: dst.path)

        // Retain the 10 most recent backups per prefix to avoid unbounded growth
        if let all = try? fm.contentsOfDirectory(at: backupDir, includingPropertiesForKeys: nil) {
            let matching = all
                .filter { $0.lastPathComponent.hasPrefix("\(prefix)-") && $0.lastPathComponent.hasSuffix(".\(ext).bak") }
                .sorted { $0.lastPathComponent < $1.lastPathComponent }
            if matching.count > 10 {
                for old in matching.prefix(matching.count - 10) {
                    try? fm.removeItem(at: old)
                }
            }
        }
    }
}
