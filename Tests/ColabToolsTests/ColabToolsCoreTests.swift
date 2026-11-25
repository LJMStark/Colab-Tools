import Foundation
import SQLite3
import XCTest
@testable import ColabToolsCore

final class ColabToolsCoreTests: XCTestCase {
    func testSubscriptionTierSuggestionsAndCaps() {
        XCTAssertEqual(SubscriptionTier.suggested(forBalance: 2400), .aiUltra)
        XCTAssertEqual(SubscriptionTier.suggested(forBalance: 580), .colabProPlus)
        XCTAssertEqual(SubscriptionTier.suggested(forBalance: 98.5), .colabPro)
        XCTAssertEqual(SubscriptionTier.suggested(forBalance: 0), .free)

        XCTAssertEqual(SubscriptionTier.free.maxSessionHours, 12)
        XCTAssertFalse(SubscriptionTier.free.supportsHighMem)
        XCTAssertEqual(SubscriptionTier.free.allowedAccelerators, [.t4, .cpu])

        XCTAssertEqual(SubscriptionTier.aiUltra.maxSessionHours, 24)
        XCTAssertTrue(SubscriptionTier.aiUltra.supportsHighMem)
        XCTAssertTrue(SubscriptionTier.aiUltra.allowedAccelerators.contains(.g4))
        XCTAssertTrue(SubscriptionTier.aiUltra.allowedAccelerators.contains(.h100))
    }

    func testStrictUsageParsingRejectsPartialOutput() {
        let valid = """
        Current balance: 2374.78 compute units
        Usage rate: 2.33/hr
        Active assignments: 1
        """
        let parsed = ColabCLIEngine.parseUsage(valid)
        XCTAssertNotNil(parsed)
        XCTAssertEqual(parsed!.balance, 2374.78, accuracy: 0.001)
        XCTAssertEqual(parsed!.ratePerHour, 2.33, accuracy: 0.001)
        XCTAssertEqual(parsed!.activeAssignments, 1)

        // Partial/malformed output must return nil rather than silent zeros
        XCTAssertNil(ColabCLIEngine.parseUsage("Current balance: 100 compute units\n"))
        XCTAssertNil(ColabCLIEngine.parseUsage("Error 503"))
    }

    func testStrictSessionsParsingHandlesColabNamedSessionAndIgnoresLogs() {
        let sample = """
        [llm-coder] gpu-l4-s-ass1a0-14xzn5nc0wm0e | Hardware: L4 | Shape: Standard | Variant: GPU
        [colab] gpu-t4-s-validname | Hardware: T4 | Shape: Standard | Variant: GPU
        [?] gpu-t4-s-orphan999 | Hardware: T4 | Shape: High-RAM | Variant: GPU
        [colab] Pruned 1 stale local session(s).
        [random-log] something else without hardware column
        """
        let list = ColabCLIEngine.parseSessions(sample)
        XCTAssertEqual(list.count, 3)
        XCTAssertEqual(list[0].name, "llm-coder")
        XCTAssertEqual(list[1].name, "colab")
        XCTAssertFalse(list[1].isOrphan)
        XCTAssertEqual(list[2].name, "?")
        XCTAssertTrue(list[2].isOrphan)
    }

    func testInputValidatorBlocksShellInjection() {
        XCTAssertTrue(InputValidator.isValidSessionName("llm-coder_01"))
        XCTAssertFalse(InputValidator.isValidSessionName("foo; rm -rf /"))
        XCTAssertFalse(InputValidator.isValidSessionName("foo'bar"))
        XCTAssertFalse(InputValidator.isValidSessionName(""))

        XCTAssertTrue(InputValidator.isValidEndpointURL("https://abc-123.trycloudflare.com"))
        XCTAssertTrue(InputValidator.isValidEndpointURL("http://127.0.0.1:18080"))
        XCTAssertFalse(InputValidator.isValidEndpointURL("https://evil.com/' ; id #"))
        XCTAssertFalse(InputValidator.isValidEndpointURL("http://example.com"))
        XCTAssertFalse(InputValidator.isValidEndpointURL("https://evil.com/\necho pwn"))
    }

    func testWatchdogNeverShutsDownWithoutFreshVerifiedIdleSignal() {
        let now = Date()

        // 1. Non-LLM notebook / training session: must pause, never kill
        XCTAssertEqual(
            IdleWatchdogEvaluator.evaluate(
                isRunning: true, enableLLMModule: false,
                activitySignal: .idle(observedAt: now),
                autoShutdownEnabled: true, idleSeconds: 9999,
                timeoutMinutes: 40, warnBeforeMinutes: 2, alreadyWarned: false, now: now
            ),
            .pausedNoSignal
        )

        // 2. Unknown signal (/slots failed or never polled): must pause, never kill
        XCTAssertEqual(
            IdleWatchdogEvaluator.evaluate(
                isRunning: true, enableLLMModule: true,
                activitySignal: .unknown,
                autoShutdownEnabled: true, idleSeconds: 9999,
                timeoutMinutes: 40, warnBeforeMinutes: 2, alreadyWarned: false, now: now
            ),
            .pausedNoSignal
        )

        // 3. Stale idle signal (> 90s old): must pause, never kill
        XCTAssertEqual(
            IdleWatchdogEvaluator.evaluate(
                isRunning: true, enableLLMModule: true,
                activitySignal: .idle(observedAt: now.addingTimeInterval(-120)),
                autoShutdownEnabled: true, idleSeconds: 9999,
                timeoutMinutes: 40, warnBeforeMinutes: 2, alreadyWarned: false, now: now
            ),
            .pausedNoSignal
        )

        // 4. Fresh busy signal: pauses timer (.none)
        XCTAssertEqual(
            IdleWatchdogEvaluator.evaluate(
                isRunning: true, enableLLMModule: true,
                activitySignal: .busy(observedAt: now),
                autoShutdownEnabled: true, idleSeconds: 9999,
                timeoutMinutes: 40, warnBeforeMinutes: 2, alreadyWarned: false, now: now
            ),
            .none
        )

        // 5. Fresh idle signal at warn threshold -> .warn(2)
        XCTAssertEqual(
            IdleWatchdogEvaluator.evaluate(
                isRunning: true, enableLLMModule: true,
                activitySignal: .idle(observedAt: now),
                autoShutdownEnabled: true, idleSeconds: 38 * 60,
                timeoutMinutes: 40, warnBeforeMinutes: 2, alreadyWarned: false, now: now
            ),
            .warn(remainingMinutes: 2)
        )

        // 6. Fresh idle signal reaching custom 15m timeout -> .shutdown
        XCTAssertEqual(
            IdleWatchdogEvaluator.evaluate(
                isRunning: true, enableLLMModule: true,
                activitySignal: .idle(observedAt: now),
                autoShutdownEnabled: true, idleSeconds: 15 * 60,
                timeoutMinutes: 15, warnBeforeMinutes: 2, alreadyWarned: true, now: now
            ),
            .shutdown
        )
    }

    func testProcessRunnerDrainsHeavyStderrWithoutDeadlockAndEnforcesTimeout() async {
        // Flood stderr (> 200KB, far larger than 64KB OS pipe buffer) while printing stdout
        let heavy = await ColabCLIEngine.runProcess(
            "/bin/zsh",
            arguments: ["-c", "python3 -c 'import sys; sys.stderr.write(\"E\"*262144); sys.stderr.flush(); print(\"DONE_OK\")'"],
            timeoutSeconds: 10
        )
        XCTAssertEqual(heavy.exitCode, 0)
        XCTAssertFalse(heavy.timedOut)
        XCTAssertTrue(heavy.stdout.contains("DONE_OK"))
        XCTAssertGreaterThan(heavy.stderr.count, 200_000)

        // Timeout must terminate hung process promptly
        let hung = await ColabCLIEngine.runProcess(
            "/bin/zsh",
            arguments: ["-c", "sleep 30"],
            timeoutSeconds: 1.0
        )
        XCTAssertTrue(hung.timedOut)
    }

    func testCCSwitchWALBackupAndProtectsActiveProvider() throws {
        let tmp = FileManager.default.temporaryDirectory.appendingPathComponent("colabtools-test-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: tmp, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: tmp) }

        let dbURL = tmp.appendingPathComponent("cc-switch.db")
        let bakDir = tmp.appendingPathComponent("backups", isDirectory: true)

        // Create a real WAL-mode SQLite DB matching CC Switch schema
        var db: OpaquePointer?
        XCTAssertEqual(sqlite3_open(dbURL.path, &db), SQLITE_OK)
        let setupSQL = """
        PRAGMA journal_mode=WAL;
        CREATE TABLE providers (
            id TEXT NOT NULL, app_type TEXT NOT NULL, name TEXT NOT NULL,
            settings_config TEXT NOT NULL, website_url TEXT, category TEXT,
            created_at INTEGER, sort_index INTEGER, notes TEXT, icon TEXT,
            icon_color TEXT, meta TEXT NOT NULL DEFAULT '{}',
            is_current BOOLEAN NOT NULL DEFAULT 0,
            in_failover_queue BOOLEAN NOT NULL DEFAULT 0,
            cost_multiplier TEXT NOT NULL DEFAULT '1.0',
            provider_type TEXT,
            PRIMARY KEY (id, app_type)
        );
        INSERT INTO providers (id, app_type, name, settings_config, category, sort_index, is_current)
        VALUES ('claude-official', 'claude', 'Claude Official', '{"env":{"KEEP":"1"}}', 'official', 0, 1);
        INSERT INTO providers (id, app_type, name, settings_config, category, sort_index, is_current)
        VALUES ('colab-standby', 'claude', 'Old Name', '{"env":{"CUSTOM_FLAG":"yes"},"extra":42}', 'custom', 1, 0);
        """
        XCTAssertEqual(sqlite3_exec(db, setupSQL, nil, nil, nil), SQLITE_OK)
        sqlite3_close(db)

        // 1. Refuses to touch active official provider
        XCTAssertThrowsError(try CCSwitchSync.syncProvider(
            providerId: "claude-official", providerName: "Hijack",
            baseURL: "https://a.trycloudflare.com", apiKey: "sk-1",
            modelAlias: "qwen3.8", contextLimit: 262144, gpuLabel: "L4",
            accountLabel: "main", dbPath: dbURL, backupDir: bakDir
        )) { err in
            XCTAssertEqual(err as? CCSwitchSync.SyncError, .refusesToOverwriteActiveProvider)
        }

        // 2. Updates standby provider while preserving unrelated JSON keys & creating WAL-safe backup
        _ = try CCSwitchSync.syncProvider(
            providerId: "colab-standby", providerName: "Colab Updated",
            baseURL: "https://demo.trycloudflare.com", apiKey: "sk-secret",
            modelAlias: "qwen3.8", contextLimit: 262144, gpuLabel: "L4",
            accountLabel: "main", dbPath: dbURL, backupDir: bakDir
        )

        let backups = try FileManager.default.contentsOfDirectory(atPath: bakDir.path)
        XCTAssertEqual(backups.count, 1)

        var verifyDB: OpaquePointer?
        XCTAssertEqual(sqlite3_open(dbURL.path, &verifyDB), SQLITE_OK)
        defer { sqlite3_close(verifyDB) }
        var stmt: OpaquePointer?
        XCTAssertEqual(sqlite3_prepare_v2(verifyDB, "SELECT is_current, settings_config FROM providers WHERE id='colab-standby'", -1, &stmt, nil), SQLITE_OK)
        defer { sqlite3_finalize(stmt) }
        XCTAssertEqual(sqlite3_step(stmt), SQLITE_ROW)
        XCTAssertEqual(sqlite3_column_int(stmt, 0), 0)
        let cfgStr = String(cString: sqlite3_column_text(stmt, 1))
        let cfgObj = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(cfgStr.utf8)) as? [String: Any])
        XCTAssertEqual(cfgObj["extra"] as? Int, 42)
        let envObj = try XCTUnwrap(cfgObj["env"] as? [String: String])
        XCTAssertEqual(envObj["CUSTOM_FLAG"], "yes")
        XCTAssertEqual(envObj["ANTHROPIC_BASE_URL"], "https://demo.trycloudflare.com")
    }

    func testKeychainCredentialNormalization() throws {
        let raw = #"{"client_id":"cid","client_secret":"sec","refresh_token":"rtok"}"#
        let normalized = try KeychainVault.normalizeToAuthorizedUserJSON(raw)
        let obj = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(normalized.utf8)) as? [String: Any])
        XCTAssertEqual(obj["type"] as? String, "authorized_user")
        XCTAssertEqual(obj["refresh_token"] as? String, "rtok")

        XCTAssertThrowsError(try KeychainVault.normalizeToAuthorizedUserJSON(#"{"foo":"bar"}"#))
    }
}
