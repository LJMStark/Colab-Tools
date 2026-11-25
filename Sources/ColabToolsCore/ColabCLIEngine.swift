import Darwin
import Foundation

public struct ShellResult: Sendable {
    public let exitCode: Int32
    public let stdout: String
    public let stderr: String
    public let timedOut: Bool

    public init(exitCode: Int32, stdout: String, stderr: String, timedOut: Bool = false) {
        self.exitCode = exitCode
        self.stdout = stdout
        self.stderr = stderr
        self.timedOut = timedOut
    }
}

public struct UsageSnapshot: Equatable, Sendable {
    public let balance: Double
    public let ratePerHour: Double
    public let activeAssignments: Int

    public init(balance: Double, ratePerHour: Double, activeAssignments: Int) {
        self.balance = balance
        self.ratePerHour = ratePerHour
        self.activeAssignments = activeAssignments
    }
}

public struct WhoamiInfo: Equatable, Sendable {
    public let provider: String
    public let email: String
    public let expiresIn: String

    public init(provider: String, email: String, expiresIn: String) {
        self.provider = provider
        self.email = email
        self.expiresIn = expiresIn
    }
}

public struct RemoteTelemetry: Equatable, Sendable {
    public var gpuName: String = ""
    public var vramUsedMB: Int = 0
    public var vramTotalMB: Int = 0
    public var gpuUtil: Int = 0
    public var publicURL: String = ""
    public var modelFile: String = ""
    public var modelAlias: String = ""
    public var contextLimit: Int = 262144
    public var kvQuant: String = "q4_0"
    public var healthy: Bool = false
    public var uptimeSeconds: Int = 0

    public init() {}
}

public struct OAuthSessionTicket: Sendable {
    public let authURL: String
    public let codeVerifier: String
    public let colabPythonPath: String
}

/// 100% official `google-colab-cli` command engine with deadlock-free process management and polite error backoff.
public enum ColabCLIEngine {
    private static let maxPipeBytes = 2 * 1024 * 1024

    public static func baseEnvironment(extra: [String: String] = [:]) -> [String: String] {
        var env = ProcessInfo.processInfo.environment
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        let extraPaths = [
            "\(home)/.local/bin",
            "/opt/homebrew/bin",
            "/usr/local/bin",
            "/usr/bin",
            "/bin",
            "/usr/sbin",
            "/sbin"
        ]
        let existing = env["PATH"] ?? ""
        let merged = (extraPaths + existing.split(separator: ":").map(String.init))
            .reduce(into: [String]()) { acc, item in
                if !item.isEmpty && !acc.contains(item) { acc.append(item) }
            }
        env["PATH"] = merged.joined(separator: ":")
        // Strip socks5 all_proxy which can stall urllib3/requests; keep http_proxy/https_proxy intact
        env.removeValue(forKey: "all_proxy")
        env.removeValue(forKey: "ALL_PROXY")
        // Polite backoff: never hammer /tun/m/assign with exponential retry loops
        env["COLAB_CLI_ASSIGN_RETRIES"] = "0"
        for (k, v) in extra { env[k] = v }
        return env
    }

    /// Deadlock-free async subprocess runner:
    /// - Starts the process BEFORE writing stdin or draining pipes
    /// - Concurrently drains stdout and stderr (capped at 2MB each) so full buffers never block
    /// - Escalates timeout/cancellation from SIGTERM -> SIGKILL after 1.5s grace period
    public static func runProcess(
        _ launchPath: String = "/bin/zsh",
        arguments: [String],
        stdinText: String? = nil,
        extraEnv: [String: String] = [:],
        timeoutSeconds: Double = 60
    ) async -> ShellResult {
        final class ProcessBox: @unchecked Sendable {
            let lock = NSLock()
            var process: Process?
            var finished = false
            var timedOut = false
        }
        let box = ProcessBox()

        return await withTaskCancellationHandler {
            await withCheckedContinuation { continuation in
                DispatchQueue.global(qos: .userInitiated).async {
                    let proc = Process()
                    proc.executableURL = URL(fileURLWithPath: launchPath)
                    proc.arguments = arguments
                    proc.environment = baseEnvironment(extra: extraEnv)

                    let outPipe = Pipe()
                    let errPipe = Pipe()
                    let inPipe = stdinText != nil ? Pipe() : nil
                    proc.standardOutput = outPipe
                    proc.standardError = errPipe
                    if let inPipe { proc.standardInput = inPipe }

                    box.lock.lock()
                    box.process = proc
                    box.lock.unlock()

                    let killSequence: @Sendable (Bool) -> Void = { isTimeout in
                        box.lock.lock()
                        let shouldKill = !box.finished
                        if isTimeout { box.timedOut = true }
                        let target = box.process
                        box.lock.unlock()
                        guard shouldKill, let target, target.isRunning else { return }
                        let pid = target.processIdentifier
                        target.terminate()
                        DispatchQueue.global().asyncAfter(deadline: .now() + 1.5) {
                            box.lock.lock()
                            let stillAlive = !box.finished && target.isRunning
                            box.lock.unlock()
                            if stillAlive && pid > 0 {
                                kill(pid, SIGKILL)
                            }
                        }
                    }

                    let timer = DispatchSource.makeTimerSource(queue: DispatchQueue.global())
                    timer.schedule(deadline: .now() + max(0.5, timeoutSeconds))
                    timer.setEventHandler { killSequence(true) }

                    do {
                        try proc.run()
                        timer.resume()

                        // Drain stdout, stderr, and write stdin concurrently to guarantee zero pipe deadlock
                        let ioGroup = DispatchGroup()
                        final class DataBox: @unchecked Sendable {
                            var data = Data()
                        }
                        let outBox = DataBox()
                        let errBox = DataBox()

                        if let inPipe, let stdinText {
                            ioGroup.enter()
                            DispatchQueue.global(qos: .userInitiated).async {
                                defer {
                                    try? inPipe.fileHandleForWriting.close()
                                    ioGroup.leave()
                                }
                                if let d = stdinText.data(using: .utf8) {
                                    try? inPipe.fileHandleForWriting.write(contentsOf: d)
                                }
                            }
                        }

                        ioGroup.enter()
                        DispatchQueue.global(qos: .userInitiated).async {
                            outBox.data = Self.readBounded(from: outPipe.fileHandleForReading, limit: maxPipeBytes)
                            ioGroup.leave()
                        }

                        ioGroup.enter()
                        DispatchQueue.global(qos: .userInitiated).async {
                            errBox.data = Self.readBounded(from: errPipe.fileHandleForReading, limit: maxPipeBytes)
                            ioGroup.leave()
                        }

                        proc.waitUntilExit()
                        // Wait briefly for IO drains after exit (never hang if a stray descendant inherited an fd)
                        _ = ioGroup.wait(timeout: .now() + 2.0)

                        box.lock.lock()
                        box.finished = true
                        let didTimeout = box.timedOut
                        box.lock.unlock()
                        timer.cancel()

                        continuation.resume(returning: ShellResult(
                            exitCode: didTimeout ? -15 : proc.terminationStatus,
                            stdout: String(data: outBox.data, encoding: .utf8) ?? "",
                            stderr: String(data: errBox.data, encoding: .utf8) ?? "",
                            timedOut: didTimeout
                        ))
                    } catch {
                        box.lock.lock()
                        box.finished = true
                        box.lock.unlock()
                        timer.cancel()
                        continuation.resume(returning: ShellResult(exitCode: -1, stdout: "", stderr: error.localizedDescription))
                    }
                }
            }
        } onCancel: {
            box.lock.lock()
            let target = box.process
            let alive = !box.finished
            box.lock.unlock()
            if alive, let target, target.isRunning {
                let pid = target.processIdentifier
                target.terminate()
                DispatchQueue.global().asyncAfter(deadline: .now() + 1.0) {
                    if target.isRunning && pid > 0 { kill(pid, SIGKILL) }
                }
            }
        }
    }

    private static func readBounded(from handle: FileHandle, limit: Int) -> Data {
        var result = Data()
        while true {
            let chunk: Data
            do {
                guard let read = try handle.read(upToCount: 32768), !read.isEmpty else { break }
                chunk = read
            } catch {
                break
            }
            if result.count < limit {
                let remaining = limit - result.count
                result.append(chunk.prefix(remaining))
            }
            // Continue draining even past limit so the child never blocks on SIGPIPE/full buffer
        }
        return result
    }

    public static func colabPrefixArgs(for profile: AccountProfile) -> [String] {
        var args: [String] = []
        switch profile.authMode {
        case .systemDefaultOAuth2:
            args.append("--auth=oauth2")
        case .isolatedKeychainADC:
            args.append("--auth=adc")
            if let cfg = profile.isolatedConfigFilePath {
                let dir = URL(fileURLWithPath: cfg).deletingLastPathComponent()
                try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
                args.append(contentsOf: ["--config", cfg])
            }
        }
        return args
    }

    public static func runColab(
        profile: AccountProfile,
        subcommandArgs: [String],
        stdinText: String? = nil,
        timeoutSeconds: Double = 60
    ) async -> ShellResult {
        let fullCmd = (["colab"] + colabPrefixArgs(for: profile) + subcommandArgs)
            .map(shellEscape)
            .joined(separator: " ")

        switch profile.authMode {
        case .systemDefaultOAuth2:
            return await runProcess(arguments: ["-c", fullCmd], stdinText: stdinText, timeoutSeconds: timeoutSeconds)
        case .isolatedKeychainADC:
            do {
                return try await KeychainVault.withEphemeralADCFile(for: profile.id) { adcPath in
                    await runProcess(
                        arguments: ["-c", fullCmd],
                        stdinText: stdinText,
                        extraEnv: ["GOOGLE_APPLICATION_CREDENTIALS": adcPath],
                        timeoutSeconds: timeoutSeconds
                    )
                }
            } catch {
                return ShellResult(exitCode: -1, stdout: "", stderr: error.localizedDescription)
            }
        }
    }

    // MARK: - Strict Official CLI Output Parsers

    public static func parseWhoami(_ stdout: String) -> WhoamiInfo? {
        var provider = ""
        var email = ""
        var expires = ""
        for line in stdout.components(separatedBy: .newlines) {
            let t = line.trimmingCharacters(in: .whitespaces)
            if t.hasPrefix("Auth provider:") {
                provider = t.replacingOccurrences(of: "Auth provider:", with: "").trimmingCharacters(in: .whitespaces)
            } else if t.hasPrefix("Email:") {
                email = t.replacingOccurrences(of: "Email:", with: "").trimmingCharacters(in: .whitespaces)
            } else if t.hasPrefix("Expires in:") {
                expires = t.replacingOccurrences(of: "Expires in:", with: "").trimmingCharacters(in: .whitespaces)
            }
        }
        guard !email.isEmpty, email.contains("@") else { return nil }
        return WhoamiInfo(provider: provider, email: email, expiresIn: expires)
    }

    /// Strictly requires all 3 official `colab usage` fields; returns `nil` on malformed/partial output instead of silent zeros.
    public static func parseUsage(_ stdout: String) -> UsageSnapshot? {
        var balance: Double?
        var rate: Double?
        var active: Int?
        for line in stdout.components(separatedBy: .newlines) {
            let t = line.trimmingCharacters(in: .whitespaces)
            if t.hasPrefix("Current balance:") {
                let num = t.replacingOccurrences(of: "Current balance:", with: "")
                    .replacingOccurrences(of: "compute units", with: "")
                    .trimmingCharacters(in: .whitespaces)
                balance = Double(num)
            } else if t.hasPrefix("Usage rate:") {
                let num = t.replacingOccurrences(of: "Usage rate:", with: "")
                    .replacingOccurrences(of: "/hr", with: "")
                    .trimmingCharacters(in: .whitespaces)
                rate = Double(num)
            } else if t.hasPrefix("Active assignments:") {
                let num = t.replacingOccurrences(of: "Active assignments:", with: "")
                    .trimmingCharacters(in: .whitespaces)
                active = Int(num)
            }
        }
        guard let b = balance, let r = rate, let a = active else { return nil }
        return UsageSnapshot(balance: b, ratePerHour: r, activeAssignments: a)
    }

    /// Strictly parses official `colab sessions` rows containing `| Hardware:` metadata.
    /// Does not blindly skip a valid session named `colab`, and ignores log lines like `[colab] Pruned ...`.
    public static func parseSessions(_ stdout: String) -> [ColabSessionItem] {
        var items: [ColabSessionItem] = []
        for raw in stdout.components(separatedBy: .newlines) {
            let line = raw.trimmingCharacters(in: .whitespaces)
            guard line.hasPrefix("["),
                  let closeBracket = line.firstIndex(of: "]"),
                  line.contains("| Hardware:") else {
                continue
            }
            let name = String(line[line.index(after: line.startIndex)..<closeBracket])
            guard !name.isEmpty else { continue }

            let rest = String(line[line.index(after: closeBracket)...]).trimmingCharacters(in: .whitespaces)
            let parts = rest.split(separator: "|").map { $0.trimmingCharacters(in: .whitespaces) }
            guard parts.count >= 2 else { continue }
            let endpoint = parts[0]
            guard !endpoint.isEmpty else { continue }

            var hw = ""
            var shape = "Standard"
            var variant = "DEFAULT"
            for p in parts.dropFirst() {
                if p.hasPrefix("Hardware:") {
                    hw = p.replacingOccurrences(of: "Hardware:", with: "").trimmingCharacters(in: .whitespaces)
                } else if p.hasPrefix("Shape:") {
                    shape = p.replacingOccurrences(of: "Shape:", with: "").trimmingCharacters(in: .whitespaces)
                } else if p.hasPrefix("Variant:") {
                    variant = p.replacingOccurrences(of: "Variant:", with: "").trimmingCharacters(in: .whitespaces)
                }
            }
            guard !hw.isEmpty else { continue }
            items.append(ColabSessionItem(
                name: name,
                endpoint: endpoint,
                hardware: hw,
                shape: shape,
                variant: variant,
                isOrphan: name == "?"
            ))
        }
        return items
    }

    // MARK: - High-Level Official Operations

    public static func isColabCLIInstalled() async -> Bool {
        let r = await runProcess(arguments: ["-c", "which colab"], timeoutSeconds: 5)
        return r.exitCode == 0 && !r.stdout.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    public static func whoami(profile: AccountProfile) async -> WhoamiInfo? {
        let r = await runColab(profile: profile, subcommandArgs: ["whoami"], timeoutSeconds: 20)
        guard r.exitCode == 0 else { return nil }
        return parseWhoami(r.stdout)
    }

    /// Strict dual check: BOTH `colab usage` and `colab sessions` must succeed and parse cleanly.
    public static func fetchUsageAndSessions(profile: AccountProfile) async -> (usage: UsageSnapshot?, sessions: [ColabSessionItem]?, ok: Bool, error: String?) {
        let u = await runColab(profile: profile, subcommandArgs: ["usage"], timeoutSeconds: 25)
        guard u.exitCode == 0, let parsedUsage = parseUsage(u.stdout) else {
            let err = (u.stderr.isEmpty ? u.stdout : u.stderr).trimmingCharacters(in: .whitespacesAndNewlines)
            return (nil, nil, false, err.isEmpty ? "colab usage 查询失败" : err)
        }
        let s = await runColab(profile: profile, subcommandArgs: ["sessions"], timeoutSeconds: 25)
        guard s.exitCode == 0 else {
            let err = (s.stderr.isEmpty ? s.stdout : s.stderr).trimmingCharacters(in: .whitespacesAndNewlines)
            return (parsedUsage, nil, false, err.isEmpty ? "colab sessions 查询失败" : err)
        }
        return (parsedUsage, parseSessions(s.stdout), true, nil)
    }

    /// Fetches sanitized telemetry ONLY (never prints API keys or raw process cmdlines with secrets into CLI history).
    public static func fetchRemoteTelemetry(profile: AccountProfile) async -> RemoteTelemetry? {
        let port = profile.remoteLLMPort
        let py = """
        import subprocess, json, pathlib
        def sh(cmd):
            r = subprocess.run(["bash", "-c", cmd], text=True, capture_output=True)
            return r.stdout.strip()
        print("NV=" + sh("nvidia-smi --query-gpu=name,memory.used,memory.total,utilization.gpu --format=csv,noheader,nounits 2>/dev/null | head -1 || true"))
        print("URL=" + sh("cat /tmp/public_url.txt 2>/dev/null || true"))
        print("HEALTH=" + sh("curl -sf --max-time 3 http://127.0.0.1:\(port)/health || true"))
        print("UPTIME=" + sh("awk '{print int($1)}' /proc/uptime 2>/dev/null || echo 0"))
        meta_p = pathlib.Path("/tmp/llama-server.meta")
        if meta_p.exists():
            print("META=" + meta_p.read_text(errors="ignore").strip())
        else:
            # Fallback: extract only -m / -c / -ctk / --alias without printing --api-key
            cmd = sh("ps -eo args | grep '[l]lama-server' | head -1 || true")
            import re
            m = re.search(r'-m\\s+(\\S+)', cmd)
            c = re.search(r'-c\\s+(\\d+)', cmd)
            kv = re.search(r'-ctk\\s+(\\S+)', cmd)
            al = re.search(r'--alias\\s+(\\S+)', cmd)
            print("META=" + json.dumps({
                "model": pathlib.Path(m.group(1)).name if m else "",
                "ctx": int(c.group(1)) if c else 262144,
                "kv": kv.group(1) if kv else "q4_0",
                "alias": al.group(1) if al else ""
            }))
        """
        let r = await runColab(profile: profile, subcommandArgs: ["exec", "-s", profile.sessionName], stdinText: py, timeoutSeconds: 30)
        guard r.exitCode == 0 else { return nil }

        var t = RemoteTelemetry()
        for line in r.stdout.components(separatedBy: .newlines) {
            if line.hasPrefix("NV=") {
                let parts = line.dropFirst(3).split(separator: ",").map { $0.trimmingCharacters(in: .whitespaces) }
                if parts.count >= 4 {
                    t.gpuName = parts[0]
                    t.vramUsedMB = Int(parts[1]) ?? 0
                    t.vramTotalMB = Int(parts[2]) ?? 0
                    t.gpuUtil = Int(parts[3]) ?? 0
                }
            } else if line.hasPrefix("URL=") {
                let raw = String(line.dropFirst(4)).trimmingCharacters(in: .whitespaces)
                if InputValidator.isValidEndpointURL(raw) {
                    t.publicURL = raw
                }
            } else if line.hasPrefix("HEALTH=") {
                t.healthy = line.contains("ok")
            } else if line.hasPrefix("UPTIME=") {
                t.uptimeSeconds = Int(line.dropFirst(7).trimmingCharacters(in: .whitespaces)) ?? 0
            } else if line.hasPrefix("META="),
                      let d = String(line.dropFirst(5)).data(using: .utf8),
                      let obj = try? JSONSerialization.jsonObject(with: d) as? [String: Any] {
                if let m = obj["model"] as? String { t.modelFile = m }
                if let c = obj["ctx"] as? Int, c > 0 { t.contextLimit = c }
                if let kv = obj["kv"] as? String, !kv.isEmpty { t.kvQuant = kv }
                if let al = obj["alias"] as? String, InputValidator.isValidModelAlias(al) { t.modelAlias = al }
            }
        }
        return t
    }

    /// Reads the persisted remote LLM API key via official `colab download` into a temporary file,
    /// keeping the secret completely out of `colab exec` history logs.
    public static func fetchRemoteAPIKeySafely(profile: AccountProfile) async -> String? {
        guard let tmpURL = try? KeychainVault.writeEphemeralFile(prefix: "keydl", ext: "txt", content: "") else {
            return nil
        }
        defer { KeychainVault.shredAndRemove(tmpURL) }
        // Remove empty placeholder so `colab download` creates it cleanly
        try? FileManager.default.removeItem(at: tmpURL)
        let r = await runColab(
            profile: profile,
            subcommandArgs: ["download", "-s", profile.sessionName, "/content/drive/MyDrive/colab-llm/api_key.txt", tmpURL.path],
            timeoutSeconds: 25
        )
        guard r.exitCode == 0,
              let key = try? String(contentsOf: tmpURL, encoding: .utf8).trimmingCharacters(in: .whitespacesAndNewlines),
              !key.isEmpty,
              key.rangeOfCharacter(from: .whitespacesAndNewlines) == nil else {
            return nil
        }
        return key
    }

    public static func fetchSlots(baseURL: String, apiKey: String) async -> SlotInfo? {
        guard InputValidator.isValidEndpointURL(baseURL), let url = URL(string: "\(baseURL)/slots") else { return nil }
        var req = URLRequest(url: url, timeoutInterval: 10)
        if !apiKey.isEmpty { req.setValue(apiKey, forHTTPHeaderField: "x-api-key") }
        do {
            let (data, resp) = try await URLSession.shared.data(for: req)
            guard let http = resp as? HTTPURLResponse, http.statusCode == 200 else { return nil }
            let slots = try JSONDecoder().decode([SlotInfo].self, from: data)
            return slots.first
        } catch {
            return nil
        }
    }

    /// Polite accelerator allocation: ONLY falls back to the next tier on explicit capacity unavailability (503).
    /// Immediately stops on rate-limit (429), assignment cap (412 / precondition failed), auth errors (401/403), or uncertain timeouts.
    public static func provisionSession(
        profile: AccountProfile,
        onProgress: @escaping @Sendable (String) -> Void
    ) async -> (allocated: String?, lastError: String?) {
        guard InputValidator.isValidSessionName(profile.sessionName) else {
            return (nil, "会话名不合法（仅允许字母、数字、._-）")
        }
        var lastErr: String? = nil
        for target in profile.preferredAccelerator.probeTargets {
            onProgress("申请 \(target.display)…")
            var args = ["new", "-s", profile.sessionName]
            if let flag = target.flag, let val = target.value {
                args.append(contentsOf: [flag, val])
            }
            if profile.highMem && profile.tier.supportsHighMem {
                args.append("--high-mem")
            }
            let r = await runColab(profile: profile, subcommandArgs: args, timeoutSeconds: 50)
            if r.exitCode == 0 {
                return (target.display, nil)
            }
            if r.timedOut {
                return (nil, "申请 \(target.display) 超时，结果不确定，已停止继续申请以防重复计费")
            }
            let combined = (r.stderr + "\n" + r.stdout).lowercased()
            if combined.contains("429") || combined.contains("too many requests") || combined.contains("rate limit") {
                return (nil, "触发官方限流 (429)，已停止申请，请稍后再试")
            }
            if combined.contains("412") || combined.contains("precondition failed") || combined.contains("too many assignments") {
                return (nil, "活跃会话已达官方上限 (412)，请先关闭已有会话")
            }
            if combined.contains("401") || combined.contains("403") || combined.contains("unauthorized") {
                return (nil, "认证失效或无权限 (401/403)，请重新授权该账号")
            }
            if combined.contains("503") || combined.contains("service unavailable") {
                lastErr = "\(target.display) 当前无可用库存 (503)"
                continue // Only explicit capacity shortage triggers fallback
            }
            if combined.contains("400") || combined.contains("bad request") {
                lastErr = "当前订阅不支持 \(target.display) (400)"
                continue
            }
            // Unknown error: stop immediately rather than looping
            return (nil, "\(target.display) 申请异常，已停止继续尝试")
        }
        return (nil, lastErr)
    }

    public static func stopSession(profile: AccountProfile, sessionName: String? = nil) async -> ShellResult {
        let target = sessionName ?? profile.sessionName
        guard InputValidator.isValidSessionName(target) else {
            return ShellResult(exitCode: -1, stdout: "", stderr: "无效的会话名")
        }
        return await runColab(profile: profile, subcommandArgs: ["stop", "-s", target], timeoutSeconds: 30)
    }

    /// Uses explicit stdout sentinel `DRIVE_MOUNTED=1` instead of relying on Python `sys.exit()` under Jupyter kernel.
    public static func ensureDriveMounted(profile: AccountProfile) async -> Bool {
        let checkPy = "import os\nprint('DRIVE_MOUNTED=1' if os.path.isdir('/content/drive/MyDrive') else 'DRIVE_MOUNTED=0')\n"
        let check = await runColab(profile: profile, subcommandArgs: ["exec", "-s", profile.sessionName], stdinText: checkPy, timeoutSeconds: 20)
        if check.exitCode == 0 && check.stdout.contains("DRIVE_MOUNTED=1") {
            return true
        }
        let m = await runColab(profile: profile, subcommandArgs: ["drivemount", "-s", profile.sessionName, "/content/drive"], timeoutSeconds: 90)
        guard m.exitCode == 0 else { return false }
        let verify = await runColab(profile: profile, subcommandArgs: ["exec", "-s", profile.sessionName], stdinText: checkPy, timeoutSeconds: 20)
        return verify.exitCode == 0 && verify.stdout.contains("DRIVE_MOUNTED=1")
    }

    /// Streams output incrementally so `colab exec` never hits idle-output timeout, and validates explicit sentinel `SERVER_READY=1`.
    public static func runDriveStartScript(profile: AccountProfile) async -> (ok: Bool, stdout: String, error: String?) {
        let escapedPath = profile.driveScriptPath
            .replacingOccurrences(of: "\\", with: "\\\\")
            .replacingOccurrences(of: "\"", with: "\\\"")
        let port = profile.remoteLLMPort
        let py = """
        import subprocess, os
        env = os.environ.copy()
        env["LLAMA_PORT"] = "\(port)"
        p = subprocess.Popen(["bash", "\(escapedPath)"], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, bufsize=1, env=env)
        for line in p.stdout:
            print(line, end="", flush=True)
        rc = p.wait()
        print(f"SCRIPT_EXIT_CODE={rc}", flush=True)
        """
        let r = await runColab(profile: profile, subcommandArgs: ["exec", "-s", profile.sessionName], stdinText: py, timeoutSeconds: 240)
        guard r.exitCode == 0,
              r.stdout.contains("SCRIPT_EXIT_CODE=0"),
              r.stdout.contains("SERVER_READY=1") else {
            let reason = r.timedOut ? "启动脚本超时" : "远端启动脚本未返回就绪标志"
            return (false, r.stdout, reason)
        }
        return (true, r.stdout, nil)
    }

    public static func openColabWebURL(profile: AccountProfile, sessionName: String? = nil) async -> ShellResult {
        let target = sessionName ?? profile.sessionName
        guard InputValidator.isValidSessionName(target) else {
            return ShellResult(exitCode: -1, stdout: "", stderr: "无效的会话名")
        }
        return await runColab(profile: profile, subcommandArgs: ["url", "-s", target, "--open"], timeoutSeconds: 15)
    }

    public static func openColabPayPage() async -> ShellResult {
        await runProcess(arguments: ["-c", "colab pay"], timeoutSeconds: 15)
    }

    // MARK: - Official OAuth2 Flow for Adding Isolated Multi-Account Profiles

    public static func resolveColabPythonPath() async -> String? {
        let res = await runProcess(arguments: ["-c", "head -n 1 \"$(which colab)\" | sed 's/^#!//'"], timeoutSeconds: 5)
        let path = res.stdout.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !path.isEmpty, FileManager.default.isExecutableFile(atPath: path) else { return nil }
        return path
    }

    public static func beginOfficialOAuthFlow() async -> Result<OAuthSessionTicket, Error> {
        guard let pyPath = await resolveColabPythonPath() else {
            return .failure(NSError(domain: "ColabTools", code: 1, userInfo: [NSLocalizedDescriptionKey: "未找到官方 google-colab-cli，请先执行 uv tool install google-colab-cli"]))
        }
        let script = """
        import json
        from importlib import resources
        from google_auth_oauthlib.flow import InstalledAppFlow
        from colab_cli.auth import PUBLIC_SCOPES, REMOTE_REDIRECT_URI

        cfg = json.loads(resources.files("colab_cli").joinpath("oauth_config.json").read_text())
        flow = InstalledAppFlow.from_client_config(cfg, PUBLIC_SCOPES)
        flow.redirect_uri = REMOTE_REDIRECT_URI
        auth_url, _ = flow.authorization_url(prompt="consent", token_usage="remote")
        print(json.dumps({"auth_url": auth_url, "code_verifier": flow.code_verifier or ""}))
        """
        let r = await runProcess(pyPath, arguments: ["-c", script], timeoutSeconds: 15)
        guard r.exitCode == 0,
              let data = r.stdout.trimmingCharacters(in: .whitespacesAndNewlines).data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: String],
              let authURL = obj["auth_url"] else {
            let msg = r.stderr.isEmpty ? "无法初始化官方 OAuth2 流程（需要 google-colab-cli 0.8.x）" : r.stderr
            return .failure(NSError(domain: "ColabTools", code: 2, userInfo: [NSLocalizedDescriptionKey: msg]))
        }
        return .success(OAuthSessionTicket(authURL: authURL, codeVerifier: obj["code_verifier"] ?? "", colabPythonPath: pyPath))
    }

    public static func completeOfficialOAuthFlow(ticket: OAuthSessionTicket, authorizationCode: String) async -> Result<String, Error> {
        let script = """
        import json, sys
        from importlib import resources
        from google_auth_oauthlib.flow import InstalledAppFlow
        from colab_cli.auth import PUBLIC_SCOPES, REMOTE_REDIRECT_URI

        payload = json.loads(sys.stdin.read())
        cfg = json.loads(resources.files("colab_cli").joinpath("oauth_config.json").read_text())
        flow = InstalledAppFlow.from_client_config(cfg, PUBLIC_SCOPES)
        flow.redirect_uri = REMOTE_REDIRECT_URI
        if payload.get("code_verifier"):
            flow.code_verifier = payload["code_verifier"]
        flow.fetch_token(code=payload["code"].strip())
        print(flow.credentials.to_json())
        """
        let inputObj: [String: String] = ["code": authorizationCode, "code_verifier": ticket.codeVerifier]
        guard let inData = try? JSONSerialization.data(withJSONObject: inputObj),
              let inStr = String(data: inData, encoding: .utf8) else {
            return .failure(NSError(domain: "ColabTools", code: 3, userInfo: [NSLocalizedDescriptionKey: "编码授权码失败"]))
        }
        let r = await runProcess(ticket.colabPythonPath, arguments: ["-c", script], stdinText: inStr, timeoutSeconds: 25)
        guard r.exitCode == 0 else {
            let msg = r.stderr.trimmingCharacters(in: .whitespacesAndNewlines)
            return .failure(NSError(domain: "ColabTools", code: 4, userInfo: [NSLocalizedDescriptionKey: msg.isEmpty ? "授权码无效或已过期" : msg]))
        }
        return .success(r.stdout.trimmingCharacters(in: .whitespacesAndNewlines))
    }

    public static func sendNotification(title: String, body: String) {
        let t = title.replacingOccurrences(of: "\"", with: "\\\"")
        let b = body.replacingOccurrences(of: "\"", with: "\\\"")
        let script = "display notification \"\(b)\" with title \"\(t)\" sound name \"Submarine\""
        Task.detached {
            _ = await runProcess("/usr/bin/osascript", arguments: ["-e", script], timeoutSeconds: 5)
        }
    }

    public static func shellEscape(_ s: String) -> String {
        "'" + s.replacingOccurrences(of: "'", with: "'\\''") + "'"
    }
}
