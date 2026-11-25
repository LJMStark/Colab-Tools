import Darwin
import Foundation

public enum CLIInstallMethod: String, Equatable, Sendable {
    case uvTool
    case pipx
    case homebrewThenUv
    case officialUvScript

    public var displayTitle: String {
        switch self {
        case .uvTool: return "使用本机 uv 安装（最快推荐）"
        case .pipx: return "使用本机 pipx 安装"
        case .homebrewThenUv: return "通过 Homebrew 安装 uv 与 Colab CLI"
        case .officialUvScript: return "通过 Astral 官方脚本安装 uv 与 Colab CLI"
        }
    }

    public var shortButtonTitle: String {
        switch self {
        case .uvTool: return "一键安装 google-colab-cli"
        case .pipx: return "使用 pipx 一键安装"
        case .homebrewThenUv: return "一键安装 uv 与 Colab CLI"
        case .officialUvScript: return "同意并自动安装 uv 与 Colab CLI"
        }
    }

    public var requiresRemoteScriptConsent: Bool {
        self == .officialUvScript
    }

    public var manualShellCommand: String {
        switch self {
        case .uvTool:
            return "uv tool install google-colab-cli"
        case .pipx:
            return "pipx install google-colab-cli"
        case .homebrewThenUv:
            return "brew install uv && uv tool install google-colab-cli"
        case .officialUvScript:
            return "curl -LsSf https://astral.sh/uv/install.sh | sh && \"$HOME/.local/bin/uv\" tool install google-colab-cli"
        }
    }
}

public struct EnvironmentCheckReport: Equatable, Sendable {
    public let colabInstalled: Bool
    public let colabPath: String?
    public let colabVersion: String?
    public let colabBrokenReason: String?
    public let uvPath: String?
    public let pipxPath: String?
    public let brewPath: String?
    public let python3Path: String?
    public let recommendedMethod: CLIInstallMethod
    public let checkedAt: Date

    public init(
        colabInstalled: Bool = false,
        colabPath: String? = nil,
        colabVersion: String? = nil,
        colabBrokenReason: String? = nil,
        uvPath: String? = nil,
        pipxPath: String? = nil,
        brewPath: String? = nil,
        python3Path: String? = nil,
        recommendedMethod: CLIInstallMethod = .uvTool,
        checkedAt: Date = Date()
    ) {
        self.colabInstalled = colabInstalled
        self.colabPath = colabPath
        self.colabVersion = colabVersion
        self.colabBrokenReason = colabBrokenReason
        self.uvPath = uvPath
        self.pipxPath = pipxPath
        self.brewPath = brewPath
        self.python3Path = python3Path
        self.recommendedMethod = recommendedMethod
        self.checkedAt = checkedAt
    }

    public var packageManagerSummary: String {
        if uvPath != nil { return "已检测到 uv" }
        if pipxPath != nil { return "已检测到 pipx" }
        if brewPath != nil { return "已检测到 Homebrew" }
        if python3Path != nil { return "仅检测到 Python 3（建议自动安装 uv）" }
        return "未检测到 uv / pipx / Homebrew"
    }
}

public struct CLIInstallResult: Equatable, Sendable {
    public let succeeded: Bool
    public let report: EnvironmentCheckReport
    public let message: String

    public init(succeeded: Bool, report: EnvironmentCheckReport, message: String) {
        self.succeeded = succeeded
        self.report = report
        self.message = message
    }
}

/// Inspects local CLI prerequisites (`google-colab-cli`, `uv`, `pipx`, `brew`, `python3`)
/// and performs transparent, cancellable one-click installation with POSIX process-group cleanup.
public enum EnvironmentDoctor {
    public static func chooseInstallMethod(
        uvPath: String?,
        pipxPath: String?,
        brewPath: String?
    ) -> CLIInstallMethod {
        if let uvPath, !uvPath.isEmpty { return .uvTool }
        if let pipxPath, !pipxPath.isEmpty { return .pipx }
        if let brewPath, !brewPath.isEmpty { return .homebrewThenUv }
        return .officialUvScript
    }

    public static func parseVersionString(from output: String) -> String? {
        let lines = output
            .components(separatedBy: .newlines)
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
        guard !lines.isEmpty else { return nil }
        let regex = try? NSRegularExpression(pattern: #"\b\d+\.\d+(?:\.\d+)*(?:[A-Za-z0-9._-]*)?\b"#)
        for line in lines {
            let range = NSRange(line.startIndex..., in: line)
            if let match = regex?.firstMatch(in: line, range: range),
               let swiftRange = Range(match.range, in: line) {
                return String(line[swiftRange])
            }
        }
        return nil
    }

    public static func findExecutable(_ name: String) -> String? {
        let fm = FileManager.default
        let env = ColabCLIEngine.baseEnvironment()
        let pathStr = env["PATH"] ?? ""
        for dir in pathStr.split(separator: ":").map(String.init) where !dir.isEmpty {
            let candidate = URL(fileURLWithPath: dir, isDirectory: true)
                .appendingPathComponent(name).path
            if fm.isExecutableFile(atPath: candidate) {
                return candidate
            }
        }
        return nil
    }

    /// Probes the official `colab version` subcommand (with `--version` fallback for future releases),
    /// distinguishing healthy installations from broken/non-functional binaries in PATH.
    public static func inspect() async -> EnvironmentCheckReport {
        let colabPath = findExecutable("colab")
        let uvPath = findExecutable("uv")
        let pipxPath = findExecutable("pipx")
        let brewPath = findExecutable("brew")
        let python3Path = findExecutable("python3")

        var healthy = false
        var version: String? = nil
        var brokenReason: String? = nil

        if let colabPath {
            let primary = await ColabCLIEngine.runProcess(colabPath, arguments: ["version"], timeoutSeconds: 8)
            let primaryOut = primary.stdout.isEmpty ? primary.stderr : primary.stdout
            if primary.exitCode == 0, let parsed = parseVersionString(from: primaryOut) {
                healthy = true
                version = parsed
            } else {
                let fallback = await ColabCLIEngine.runProcess(colabPath, arguments: ["--version"], timeoutSeconds: 8)
                let fallbackOut = fallback.stdout.isEmpty ? fallback.stderr : fallback.stdout
                if fallback.exitCode == 0, let parsed = parseVersionString(from: fallbackOut) {
                    healthy = true
                    version = parsed
                } else {
                    let code = primary.exitCode != 0 ? primary.exitCode : fallback.exitCode
                    let detail = primary.timedOut ? "执行超时" : "退出码 \(code)"
                    brokenReason = "检测到 \(colabPath) 但无法正常运行 `colab version`（\(detail)），请一键重新安装修复"
                }
            }
        }

        let method = chooseInstallMethod(uvPath: uvPath, pipxPath: pipxPath, brewPath: brewPath)
        return EnvironmentCheckReport(
            colabInstalled: healthy,
            colabPath: colabPath,
            colabVersion: version,
            colabBrokenReason: brokenReason,
            uvPath: uvPath,
            pipxPath: pipxPath,
            brewPath: brewPath,
            python3Path: python3Path,
            recommendedMethod: method,
            checkedAt: Date()
        )
    }

    /// Runs a transparent, cancellable CLI installation streaming stdout/stderr lines back to the caller,
    /// then re-verifies that `colab version` succeeds.
    public static func installColabCLI(
        method: CLIInstallMethod,
        timeoutSeconds: Double = 240,
        onOutputLine: @escaping @Sendable (String) -> Void
    ) async -> CLIInstallResult {
        let shellCommand: String
        switch method {
        case .uvTool:
            shellCommand = "uv tool install --upgrade google-colab-cli"
        case .pipx:
            shellCommand = "pipx install --force google-colab-cli"
        case .homebrewThenUv:
            shellCommand = "brew install uv && uv tool install --upgrade google-colab-cli"
        case .officialUvScript:
            shellCommand = "curl -LsSf https://astral.sh/uv/install.sh | sh && \"$HOME/.local/bin/uv\" tool install --upgrade google-colab-cli"
        }

        onOutputLine("$ \(shellCommand)")
        let res = await runCancellableProcessGroupCommand(
            command: shellCommand,
            timeoutSeconds: timeoutSeconds,
            onLine: onOutputLine
        )
        let refreshed = await inspect()

        if Task.isCancelled {
            return CLIInstallResult(succeeded: false, report: refreshed, message: "已取消安装并清理进程")
        }
        if res.timedOut {
            return CLIInstallResult(succeeded: false, report: refreshed, message: "安装超时（超过 \(Int(timeoutSeconds)) 秒），请检查网络连接或复制命令到终端执行")
        }
        if res.exitCode == 0 && refreshed.colabInstalled {
            let ver = refreshed.colabVersion.map { "v\($0)" } ?? ""
            return CLIInstallResult(succeeded: true, report: refreshed, message: "google-colab-cli \(ver) 安装完成并已验证可用")
        }
        if res.exitCode == 0 && !refreshed.colabInstalled {
            let reason = refreshed.colabBrokenReason ?? "安装命令已退出，但未在 PATH (~/.local/bin) 检测到可用的 colab 命令，请查看日志"
            return CLIInstallResult(succeeded: false, report: refreshed, message: reason)
        }
        return CLIInstallResult(succeeded: false, report: refreshed, message: "安装未成功（退出码 \(res.exitCode)），请展开日志排查或复制命令在终端执行")
    }

    /// Spawns `/bin/zsh -c <command>` inside its own POSIX process group (`POSIX_SPAWN_SETPGROUP`)
    /// so timeout or Task cancellation reliably kills the entire descendant tree (`brew`, `curl`, `sh`, `uv`)
    /// and waits for process-group termination before returning.
    public static func runCancellableProcessGroupCommand(
        command: String,
        timeoutSeconds: Double,
        onLine: @escaping @Sendable (String) -> Void
    ) async -> ShellResult {
        final class ControlState: @unchecked Sendable {
            let lock = NSLock()
            var pgid: pid_t = 0
            var cancelRequested = false
            var timedOut = false
            var finished = false
        }
        let state = ControlState()

        return await withTaskCancellationHandler {
            await withCheckedContinuation { continuation in
                DispatchQueue.global(qos: .userInitiated).async {
                    var pipeFds: [Int32] = [0, 0]
                    guard Darwin.pipe(&pipeFds) == 0 else {
                        onLine("[ERROR] 无法创建输出管道")
                        continuation.resume(returning: ShellResult(exitCode: -1, stdout: "", stderr: "pipe failed"))
                        return
                    }
                    let readFd = pipeFds[0]
                    let writeFd = pipeFds[1]
                    _ = fcntl(readFd, F_SETFD, FD_CLOEXEC)
                    _ = fcntl(writeFd, F_SETFD, FD_CLOEXEC)

                    var fileActions: posix_spawn_file_actions_t? = nil
                    posix_spawn_file_actions_init(&fileActions)
                    defer { posix_spawn_file_actions_destroy(&fileActions) }
                    posix_spawn_file_actions_adddup2(&fileActions, writeFd, STDOUT_FILENO)
                    posix_spawn_file_actions_adddup2(&fileActions, writeFd, STDERR_FILENO)
                    posix_spawn_file_actions_addclose(&fileActions, readFd)
                    posix_spawn_file_actions_addclose(&fileActions, writeFd)

                    var attr: posix_spawnattr_t? = nil
                    posix_spawnattr_init(&attr)
                    defer { posix_spawnattr_destroy(&attr) }
                    posix_spawnattr_setflags(&attr, Int16(POSIX_SPAWN_SETPGROUP))
                    posix_spawnattr_setpgroup(&attr, 0)

                    let envDict = ColabCLIEngine.baseEnvironment()
                    let argvStrings = ["/bin/zsh", "-c", command]
                    let envStrings = envDict.map { "\($0.key)=\($0.value)" }
                    var cArgv: [UnsafeMutablePointer<CChar>?] = argvStrings.map { strdup($0) } + [nil]
                    var cEnvp: [UnsafeMutablePointer<CChar>?] = envStrings.map { strdup($0) } + [nil]
                    defer {
                        for ptr in cArgv { free(ptr) }
                        for ptr in cEnvp { free(ptr) }
                    }

                    var childPid: pid_t = 0
                    let spawnStatus = posix_spawn(&childPid, "/bin/zsh", &fileActions, &attr, &cArgv, &cEnvp)
                    Darwin.close(writeFd)

                    guard spawnStatus == 0, childPid > 0 else {
                        Darwin.close(readFd)
                        let errMsg = String(cString: strerror(spawnStatus))
                        onLine("[ERROR] 启动安装进程失败：\(errMsg)")
                        continuation.resume(returning: ShellResult(exitCode: -1, stdout: "", stderr: errMsg))
                        return
                    }

                    var alreadyCancelled = false
                    state.lock.lock()
                    state.pgid = childPid
                    alreadyCancelled = state.cancelRequested
                    state.lock.unlock()

                    if alreadyCancelled {
                        terminateProcessGroupAndConfirmExit(pgid: childPid)
                    }

                    // Switch read end to non-blocking so poll loop remains responsive to cancellation & timeout
                    let curFlags = fcntl(readFd, F_GETFL)
                    if curFlags >= 0 {
                        _ = fcntl(readFd, F_SETFL, curFlags | O_NONBLOCK)
                    }

                    let deadline = Date().addingTimeInterval(max(0.2, timeoutSeconds))
                    var partialBuffer = ""
                    var readBuf = [UInt8](repeating: 0, count: 4096)
                    var pipeEOF = false
                    var reaped = false
                    var exitStatus: Int32 = 0
                    var killedByControl = alreadyCancelled

                    func flushLines(forceRemaining: Bool) {
                        let normalized = partialBuffer
                            .replacingOccurrences(of: "\r\n", with: "\n")
                            .replacingOccurrences(of: "\r", with: "\n")
                        var lines = normalized.components(separatedBy: "\n")
                        if forceRemaining {
                            partialBuffer = ""
                        } else {
                            partialBuffer = lines.popLast() ?? ""
                        }
                        for raw in lines {
                            let clean = raw.trimmingCharacters(in: .whitespacesAndNewlines)
                            if !clean.isEmpty { onLine(clean) }
                        }
                    }

                    while true {
                        // 1. Check cancellation or deadline
                        state.lock.lock()
                        let cancelNow = state.cancelRequested
                        state.lock.unlock()

                        let timedOutNow = Date() >= deadline
                        if (cancelNow || timedOutNow) && !killedByControl {
                            killedByControl = true
                            if timedOutNow && !cancelNow {
                                state.lock.lock()
                                state.timedOut = true
                                state.lock.unlock()
                            }
                            terminateProcessGroupAndConfirmExit(pgid: childPid)
                        }

                        // 2. Drain any ready bytes from pipe
                        if !pipeEOF {
                            var pfd = pollfd(fd: readFd, events: Int16(POLLIN | POLLHUP), revents: 0)
                            let pollRet = Darwin.poll(&pfd, 1, 50)
                            if pollRet > 0 {
                                while true {
                                    let n = Darwin.read(readFd, &readBuf, readBuf.count)
                                    if n > 0 {
                                        let chunk = String(decoding: readBuf[0..<n], as: UTF8.self)
                                        partialBuffer.append(chunk)
                                        flushLines(forceRemaining: false)
                                    } else if n == 0 {
                                        pipeEOF = true
                                        break
                                    } else {
                                        if errno == EAGAIN || errno == EWOULDBLOCK {
                                            break
                                        }
                                        pipeEOF = true
                                        break
                                    }
                                }
                            }
                        } else {
                            usleep(30_000)
                        }

                        // 3. Reap leader process non-blockingly
                        if !reaped {
                            let w = waitpid(childPid, &exitStatus, WNOHANG)
                            if w == childPid || (w == -1 && errno == ECHILD) {
                                reaped = true
                            }
                        }

                        // 4. Exit loop once leader is reaped and either pipe closed or process group fully gone
                        if reaped {
                            let groupGone = (Darwin.kill(-childPid, 0) == -1 && errno == ESRCH)
                            if pipeEOF || (killedByControl && groupGone) {
                                break
                            }
                            // If leader exited normally while a stray background child still holds stdout open after control kill,
                            // ensure the whole group is gone before exiting.
                            if killedByControl {
                                terminateProcessGroupAndConfirmExit(pgid: childPid)
                                break
                            }
                        }
                    }

                    Darwin.close(readFd)
                    if !reaped {
                        _ = waitpid(childPid, &exitStatus, 0)
                    }
                    // Final safety sweep on cancellation/timeout so no descendant survives
                    if killedByControl {
                        terminateProcessGroupAndConfirmExit(pgid: childPid)
                    }

                    flushLines(forceRemaining: true)

                    state.lock.lock()
                    state.finished = true
                    let didTimeout = state.timedOut
                    let wasCancelled = state.cancelRequested
                    state.lock.unlock()

                    let code: Int32
                    if wasCancelled {
                        code = -2
                    } else if didTimeout {
                        code = -15
                    } else if (exitStatus & 0x7f) == 0 {
                        code = (exitStatus >> 8) & 0xff
                    } else {
                        code = -(exitStatus & 0x7f)
                    }

                    continuation.resume(returning: ShellResult(
                        exitCode: code,
                        stdout: "",
                        stderr: "",
                        timedOut: didTimeout
                    ))
                }
            }
        } onCancel: {
            state.lock.lock()
            state.cancelRequested = true
            let pgid = state.pgid
            let alive = !state.finished
            state.lock.unlock()
            if alive && pgid > 0 {
                // Immediately send SIGTERM to entire process group; worker thread completes SIGKILL escalation & wait confirmation.
                _ = Darwin.kill(-pgid, SIGTERM)
            }
        }
    }

    private static func terminateProcessGroupAndConfirmExit(pgid: pid_t) {
        guard pgid > 0 else { return }
        _ = Darwin.kill(-pgid, SIGTERM)
        let termDeadline = Date().addingTimeInterval(1.2)
        while Date() < termDeadline {
            if Darwin.kill(-pgid, 0) == -1 && errno == ESRCH {
                return
            }
            var status: Int32 = 0
            _ = waitpid(pgid, &status, WNOHANG)
            usleep(40_000)
        }
        _ = Darwin.kill(-pgid, SIGKILL)
        let killDeadline = Date().addingTimeInterval(1.5)
        while Date() < killDeadline {
            var status: Int32 = 0
            _ = waitpid(pgid, &status, WNOHANG)
            if Darwin.kill(-pgid, 0) == -1 && errno == ESRCH {
                return
            }
            usleep(40_000)
        }
    }
}
