import Foundation
import SQLite3

/// WAL-safe transactional SQLite synchronization for CC Switch (farion1231/cc-switch).
/// Guarantees:
/// 1. Uses SQLite online backup API (`sqlite3_backup_*`) so WAL commits are never missed; aborts if backup fails.
/// 2. Runs inside a short `BEGIN IMMEDIATE` transaction and verifies `is_current = 0` before updating.
/// 3. Merges only managed `env` keys inside `settings_config` without clobbering unrelated user fields.
public enum CCSwitchSync {
    public static let defaultDBPath = FileManager.default.homeDirectoryForCurrentUser
        .appendingPathComponent(".cc-switch/cc-switch.db")
    public static let defaultBackupDir = FileManager.default.homeDirectoryForCurrentUser
        .appendingPathComponent(".cc-switch/backups", isDirectory: true)

    public enum SyncError: LocalizedError, Equatable {
        case databaseNotFound
        case invalidEndpoint
        case backupFailed(String)
        case refusesToOverwriteActiveProvider
        case sqliteFailed(String)

        public var errorDescription: String? {
            switch self {
            case .databaseNotFound: return "未检测到 CC Switch 数据库 (~/.cc-switch/cc-switch.db)"
            case .invalidEndpoint: return "公网地址或 API Key 无效，已拒绝写入 CC Switch"
            case .backupFailed(let msg): return "数据库在线备份失败，已中止写入：\(msg)"
            case .refusesToOverwriteActiveProvider: return "目标渠道当前处于激活状态，已跳过覆写以保护配置"
            case .sqliteFailed(let msg): return "SQLite 写入失败: \(msg)"
            }
        }
    }

    public static var isInstalled: Bool {
        FileManager.default.fileExists(atPath: defaultDBPath.path)
    }

    /// Performs a WAL-safe SQLite online backup.
    public static func onlineBackup(sourceDB: URL, destinationDB: URL) throws {
        var src: OpaquePointer?
        var dst: OpaquePointer?
        guard sqlite3_open_v2(sourceDB.path, &src, SQLITE_OPEN_READONLY, nil) == SQLITE_OK, let src else {
            let msg = src.flatMap { String(cString: sqlite3_errmsg($0)) } ?? "open source failed"
            sqlite3_close(src)
            throw SyncError.backupFailed(msg)
        }
        defer { sqlite3_close(src) }
        sqlite3_busy_timeout(src, 5000)

        guard sqlite3_open_v2(destinationDB.path, &dst, SQLITE_OPEN_READWRITE | SQLITE_OPEN_CREATE, nil) == SQLITE_OK, let dst else {
            let msg = dst.flatMap { String(cString: sqlite3_errmsg($0)) } ?? "open destination failed"
            sqlite3_close(dst)
            throw SyncError.backupFailed(msg)
        }
        defer { sqlite3_close(dst) }

        guard let backup = sqlite3_backup_init(dst, "main", src, "main") else {
            throw SyncError.backupFailed(String(cString: sqlite3_errmsg(dst)))
        }
        let stepRC = sqlite3_backup_step(backup, -1)
        let finishRC = sqlite3_backup_finish(backup)
        guard (stepRC == SQLITE_DONE || stepRC == SQLITE_OK) && finishRC == SQLITE_OK else {
            try? FileManager.default.removeItem(at: destinationDB)
            throw SyncError.backupFailed(String(cString: sqlite3_errmsg(dst)))
        }
    }

    public static func syncProvider(
        providerId: String,
        providerName: String,
        baseURL: String,
        apiKey: String,
        modelAlias: String,
        contextLimit: Int,
        gpuLabel: String,
        accountLabel: String,
        dbPath: URL = defaultDBPath,
        backupDir: URL = defaultBackupDir
    ) throws -> String {
        guard FileManager.default.fileExists(atPath: dbPath.path) else {
            throw SyncError.databaseNotFound
        }
        guard InputValidator.isValidEndpointURL(baseURL),
              !apiKey.isEmpty,
              InputValidator.isValidModelAlias(modelAlias) else {
            throw SyncError.invalidEndpoint
        }

        // Daily WAL-safe online backup (must succeed before any write)
        try FileManager.default.createDirectory(at: backupDir, withIntermediateDirectories: true)
        let dayFmt = DateFormatter()
        dayFmt.dateFormat = "yyyyMMdd"
        let prefix = "cc-switch.db.bak-colabtools-\(dayFmt.string(from: Date()))"
        let existing = (try? FileManager.default.contentsOfDirectory(atPath: backupDir.path)) ?? []
        if !existing.contains(where: { $0.hasPrefix(prefix) }) {
            let fullFmt = DateFormatter()
            fullFmt.dateFormat = "yyyyMMdd-HHmmss"
            let bak = backupDir.appendingPathComponent("cc-switch.db.bak-colabtools-\(fullFmt.string(from: Date()))")
            try onlineBackup(sourceDB: dbPath, destinationDB: bak)
        }

        let autoCompact = max(32768, contextLimit - 16384)
        let managedEnv: [String: String] = [
            "ANTHROPIC_BASE_URL": baseURL,
            "ANTHROPIC_AUTH_TOKEN": apiKey,
            "ANTHROPIC_MODEL": modelAlias,
            "ANTHROPIC_SMALL_FAST_MODEL": modelAlias,
            "ANTHROPIC_DEFAULT_HAIKU_MODEL": modelAlias,
            "ANTHROPIC_DEFAULT_SONNET_MODEL": modelAlias,
            "ANTHROPIC_DEFAULT_OPUS_MODEL": modelAlias,
            "CLAUDE_CODE_DISABLE_1M_CONTEXT": "1",
            "CLAUDE_CODE_MAX_CONTEXT_TOKENS": "\(contextLimit)",
            "CLAUDE_CODE_AUTO_COMPACT_WINDOW": "\(autoCompact)",
            "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"
        ]
        let notes = "Colab Tools [\(accountLabel)] · \(gpuLabel.isEmpty ? "GPU" : gpuLabel) (\(contextLimit / 1024)K) · 自动同步"

        var db: OpaquePointer?
        guard sqlite3_open_v2(dbPath.path, &db, SQLITE_OPEN_READWRITE, nil) == SQLITE_OK, let db else {
            let msg = db.flatMap { String(cString: sqlite3_errmsg($0)) } ?? "open error"
            sqlite3_close(db)
            throw SyncError.sqliteFailed(msg)
        }
        defer { sqlite3_close(db) }
        sqlite3_busy_timeout(db, 5000)

        guard sqlite3_exec(db, "BEGIN IMMEDIATE;", nil, nil, nil) == SQLITE_OK else {
            throw SyncError.sqliteFailed(String(cString: sqlite3_errmsg(db)))
        }
        var committed = false
        defer {
            if !committed { _ = sqlite3_exec(db, "ROLLBACK;", nil, nil, nil) }
        }

        let transient = unsafeBitCast(-1, to: sqlite3_destructor_type.self)

        // Inspect existing row inside the exclusive transaction
        var existingRow: (isCurrent: Bool, rawConfig: String, category: String)?
        var checkStmt: OpaquePointer?
        if sqlite3_prepare_v2(
            db,
            "SELECT is_current, settings_config, COALESCE(category, '') FROM providers WHERE id=? AND app_type='claude'",
            -1, &checkStmt, nil
        ) == SQLITE_OK {
            sqlite3_bind_text(checkStmt, 1, providerId, -1, transient)
            if sqlite3_step(checkStmt) == SQLITE_ROW {
                let cur = sqlite3_column_int(checkStmt, 0) != 0
                let cfg = sqlite3_column_text(checkStmt, 1).map { String(cString: $0) } ?? "{}"
                let cat = sqlite3_column_text(checkStmt, 2).map { String(cString: $0) } ?? ""
                existingRow = (cur, cfg, cat)
            }
        }
        sqlite3_finalize(checkStmt)

        if let row = existingRow {
            // Protect active provider or official provider from unintended mutation
            if row.isCurrent || row.category == "official" {
                throw SyncError.refusesToOverwriteActiveProvider
            }

            // Merge managed env keys into existing settings_config JSON
            var rootObj: [String: Any] = [:]
            if let data = row.rawConfig.data(using: .utf8),
               let parsed = try? JSONSerialization.jsonObject(with: data) as? [String: Any] {
                rootObj = parsed
            }
            var envObj = (rootObj["env"] as? [String: Any]) ?? [:]
            for (k, v) in managedEnv { envObj[k] = v }
            rootObj["env"] = envObj
            let mergedData = try JSONSerialization.data(withJSONObject: rootObj, options: [.sortedKeys])
            let mergedJSON = String(data: mergedData, encoding: .utf8) ?? "{}"

            let sql = "UPDATE providers SET name=?, settings_config=?, notes=? WHERE id=? AND app_type='claude' AND is_current=0"
            var stmt: OpaquePointer?
            guard sqlite3_prepare_v2(db, sql, -1, &stmt, nil) == SQLITE_OK else {
                throw SyncError.sqliteFailed(String(cString: sqlite3_errmsg(db)))
            }
            defer { sqlite3_finalize(stmt) }
            sqlite3_bind_text(stmt, 1, providerName, -1, transient)
            sqlite3_bind_text(stmt, 2, mergedJSON, -1, transient)
            sqlite3_bind_text(stmt, 3, notes, -1, transient)
            sqlite3_bind_text(stmt, 4, providerId, -1, transient)
            guard sqlite3_step(stmt) == SQLITE_DONE, sqlite3_changes(db) == 1 else {
                throw SyncError.sqliteFailed("未更新任何行（可能已被切换为激活状态）")
            }
            guard sqlite3_exec(db, "COMMIT;", nil, nil, nil) == SQLITE_OK else {
                throw SyncError.sqliteFailed(String(cString: sqlite3_errmsg(db)))
            }
            committed = true
            return "已安全更新 CC Switch 待机渠道（官方订阅未受影响）"
        } else {
            let newData = try JSONSerialization.data(withJSONObject: ["env": managedEnv], options: [.sortedKeys])
            let newJSON = String(data: newData, encoding: .utf8) ?? "{}"

            var maxSort: Int32 = 0
            var sortStmt: OpaquePointer?
            if sqlite3_prepare_v2(db, "SELECT COALESCE(MAX(sort_index), 0) FROM providers WHERE app_type='claude'", -1, &sortStmt, nil) == SQLITE_OK {
                if sqlite3_step(sortStmt) == SQLITE_ROW { maxSort = sqlite3_column_int(sortStmt, 0) }
            }
            sqlite3_finalize(sortStmt)

            let sql = """
            INSERT INTO providers (
                id, app_type, name, settings_config, website_url, category,
                created_at, sort_index, notes, icon, icon_color, meta,
                is_current, in_failover_queue, cost_multiplier, provider_type
            ) VALUES (?, 'claude', ?, ?, 'https://colab.research.google.com', 'custom', ?, ?, ?, 'google', '#F59E0B', '{}', 0, 0, '0.0', NULL)
            """
            var stmt: OpaquePointer?
            guard sqlite3_prepare_v2(db, sql, -1, &stmt, nil) == SQLITE_OK else {
                throw SyncError.sqliteFailed(String(cString: sqlite3_errmsg(db)))
            }
            defer { sqlite3_finalize(stmt) }
            sqlite3_bind_text(stmt, 1, providerId, -1, transient)
            sqlite3_bind_text(stmt, 2, providerName, -1, transient)
            sqlite3_bind_text(stmt, 3, newJSON, -1, transient)
            sqlite3_bind_int64(stmt, 4, Int64(Date().timeIntervalSince1970 * 1000))
            sqlite3_bind_int(stmt, 5, maxSort + 1)
            sqlite3_bind_text(stmt, 6, notes, -1, transient)
            guard sqlite3_step(stmt) == SQLITE_DONE, sqlite3_changes(db) == 1 else {
                throw SyncError.sqliteFailed(String(cString: sqlite3_errmsg(db)))
            }
            guard sqlite3_exec(db, "COMMIT;", nil, nil, nil) == SQLITE_OK else {
                throw SyncError.sqliteFailed(String(cString: sqlite3_errmsg(db)))
            }
            committed = true
            return "已新增 CC Switch 待机渠道（官方订阅保持激活）"
        }
    }
}
