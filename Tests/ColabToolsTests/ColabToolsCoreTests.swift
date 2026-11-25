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

    func testPoliteQueueEqualJitterCapsAndTierFiltering() {
        // 1. Tier filtering strips illegal accelerators and preserves high-to-low order
        let proFiltered = PoliteQueuePolicy.sanitizedTargets(selected: [.l4, .g4, .h100, .a100], for: .colabPro)
        XCTAssertEqual(proFiltered, [.l4, .a100])

        let freeFiltered = PoliteQueuePolicy.sanitizedTargets(selected: [.h100, .a100], for: .free)
        XCTAssertEqual(freeFiltered, [.t4])

        // 2. Equal Jitter stays strictly within [base/2, base] for rounds 1..4
        let start = Date()
        let d1Min = PoliteQueuePolicy.nextDelay(completedAttempts: 1, startedAt: start, now: start, unitRandom: 0.0)
        let d1Max = PoliteQueuePolicy.nextDelay(completedAttempts: 1, startedAt: start, now: start, unitRandom: 1.0)
        XCTAssertEqual(d1Min, 90)
        XCTAssertEqual(d1Max, 180)

        let d4Min = PoliteQueuePolicy.nextDelay(completedAttempts: 4, startedAt: start, now: start, unitRandom: 0.0)
        let d4Max = PoliteQueuePolicy.nextDelay(completedAttempts: 4, startedAt: start, now: start, unitRandom: 1.0)
        XCTAssertEqual(d4Min, 360)
        XCTAssertEqual(d4Max, 720)

        // 3. Hard ceiling: round 5 completed -> nil; elapsed near 40m -> nil
        XCTAssertNil(PoliteQueuePolicy.nextDelay(completedAttempts: 5, startedAt: start, now: start, unitRandom: 0.5))
        let nearCap = start.addingTimeInterval(39 * 60)
        XCTAssertNil(PoliteQueuePolicy.nextDelay(completedAttempts: 1, startedAt: start, now: nearCap, unitRandom: 0.5))

        // 4. Inter-target delay lies in [12s, 20s]
        XCTAssertEqual(PoliteQueuePolicy.interTargetDelaySeconds(unitRandom: 0.0), 12)
        XCTAssertEqual(PoliteQueuePolicy.interTargetDelaySeconds(unitRandom: 1.0), 20)
    }

    func testPoliteQueueCircuitBreakerClassification() {
        // Only explicit 503 allows continuing the queue
        if case .capacityUnavailable503 = PoliteQueuePolicy.classifyFailure(
            exitCode: 1, timedOut: false, stdout: "", stderr: "HTTP 503 Service Unavailable", targetDisplay: "H100"
        ) {} else {
            XCTFail("Expected 503 capacityUnavailable503")
        }

        // 412, 429, 403, abuse message, timeout, and unknown errors MUST hard circuit-break
        for (stderr, timedOut) in [
            ("412 Precondition Failed: TooManyAssignmentsError", false),
            ("HTTP 429 Too Many Requests", false),
            ("403 Forbidden", false),
            ("This account has been blocked from accessing Colab runtimes due to suspected abusive activity", false),
            ("", true),
            ("Unexpected crash", false)
        ] {
            if case .circuitBroken = PoliteQueuePolicy.classifyFailure(
                exitCode: 1, timedOut: timedOut, stdout: "", stderr: stderr, targetDisplay: "G4"
            ) {} else {
                XCTFail("Expected circuitBroken for stderr='\(stderr)' timedOut=\(timedOut)")
            }
        }
    }

    func testLegacyAccountProfileDecodesWithoutQueueFields() throws {
        let legacyJSON = """
        {
          "id": "11111111-1111-1111-1111-111111111111",
          "displayName": "Legacy",
          "email": "user@example.com",
          "colorTag": "#F59E0B",
          "tier": "ai_ultra",
          "authMode": "system_oauth2",
          "sessionName": "llm-coder",
          "preferredAccelerator": "AUTO_BEST",
          "highMem": false,
          "enableLLMModule": true,
          "autoShutdownEnabled": true,
          "idleTimeoutMinutes": 40,
          "warnBeforeMinutes": 2,
          "driveScriptPath": "/content/drive/MyDrive/colab-llm/scripts/start_server.sh",
          "autoSyncCCSwitch": true,
          "ccSwitchProviderId": "colab-qwen",
          "ccSwitchProviderName": "Colab Qwen",
          "localEnvPath": ""
        }
        """
        let decoded = try JSONDecoder().decode(AccountProfile.self, from: Data(legacyJSON.utf8))
        XCTAssertFalse(decoded.enablePoliteQueue)
        XCTAssertEqual(decoded.queueTargets, [.g4, .h100])
        XCTAssertEqual(decoded.workloadKind, .llm)
        // Legacy configs missing `enableHardwareIdleFallback` MUST stay `false` to preserve safe paused behavior
        XCTAssertFalse(decoded.enableHardwareIdleFallback)
        XCTAssertEqual(decoded.remoteServicePort, 18080)
    }

    func testWorkloadStatusJSONAndComfyQueueParsing() throws {
        let videoJSON = """
        {
          "kind": "video",
          "name": "Wan2.2-14B",
          "ui_url": "https://wan-demo.trycloudflare.com",
          "port": 8188,
          "metric_label": "渲染进度",
          "metric_value": "18/30",
          "metric_unit": "steps",
          "metric_ratio": 0.6,
          "busy": true,
          "queue_running": 1,
          "queue_pending": 2,
          "detail": "720p · 48fps"
        }
        """
        let payload = try XCTUnwrap(WorkloadStatusParser.parseStatusJSON(videoJSON))
        XCTAssertEqual(payload.parsedKind, .video)
        XCTAssertEqual(payload.name, "Wan2.2-14B")
        XCTAssertEqual(payload.port, 8188)
        XCTAssertEqual(payload.isWorkloadBusy, true)
        XCTAssertEqual(payload.queuePending, 2)

        let comfyRaw = #"{"queue_running":[["job-1"]],"queue_pending":[["job-2"],["job-3"]]}"#
        let comfy = try XCTUnwrap(WorkloadStatusParser.parseComfyQueueJSON(comfyRaw))
        XCTAssertEqual(comfy.running, 1)
        XCTAssertEqual(comfy.pending, 2)
        XCTAssertTrue(comfy.isBusy)

        let comfyIdle = try XCTUnwrap(WorkloadStatusParser.parseComfyQueueJSON(#"{"queue_running":[],"queue_pending":[]}"#))
        XCTAssertFalse(comfyIdle.isBusy)
    }

    func testHardwareDualLowLoadPreventsFalseShutdownDuringDownloadOrTranscode() {
        let now = Date()

        // GPU 0% while CPU 62% (e.g. downloading 20GB video weights or FFmpeg transcoding) MUST be `.busy`
        let downloading = IdleWatchdogEvaluator.evaluateHardwareActivity(gpuUtilPercent: 0, cpuUtilPercent: 62, now: now)
        XCTAssertTrue(downloading.isBusy)

        // GPU 18% while CPU 8% MUST be `.busy`
        let gpuActive = IdleWatchdogEvaluator.evaluateHardwareActivity(gpuUtilPercent: 18, cpuUtilPercent: 8, now: now)
        XCTAssertTrue(gpuActive.isBusy)

        // Only BOTH GPU < 5% AND CPU < 25% classifies as `.idle`
        let trulyIdle = IdleWatchdogEvaluator.evaluateHardwareActivity(gpuUtilPercent: 2, cpuUtilPercent: 14, now: now)
        if case .idle = trulyIdle {} else {
            XCTFail("Expected .idle when both GPU<5% and CPU<25%")
        }

        // Missing readings (nil) or TPU runtimes MUST NEVER map to 0 / `.idle`
        XCTAssertEqual(IdleWatchdogEvaluator.evaluateHardwareActivity(gpuUtilPercent: nil, cpuUtilPercent: 5, now: now), .unknown)
        XCTAssertEqual(IdleWatchdogEvaluator.evaluateHardwareActivity(gpuUtilPercent: 0, cpuUtilPercent: nil, now: now), .unknown)
        XCTAssertEqual(IdleWatchdogEvaluator.evaluateHardwareActivity(gpuUtilPercent: 0, cpuUtilPercent: 5, isTPU: true, now: now), .unknown)
    }

    func testMultiSlotLLMAggregationPreventsFalseIdleWhenSecondSlotBusy() {
        let now = Date()
        let busySlot2 = [
            SlotInfo(id: 0, n_ctx: 262144, is_processing: false, id_task: 10, n_prompt_tokens: 1000),
            SlotInfo(id: 1, n_ctx: 262144, is_processing: true, id_task: 11, n_prompt_tokens: 5000)
        ]
        let aggBusy = IdleWatchdogEvaluator.evaluateLLMSlots(busySlot2, now: now)
        XCTAssertTrue(aggBusy.signal.isBusy)
        XCTAssertEqual(aggBusy.maxPromptTokens, 5000)

        let missingFlag = [
            SlotInfo(id: 0, n_ctx: 262144, is_processing: false, id_task: 10, n_prompt_tokens: 1000),
            SlotInfo(id: 1, n_ctx: 262144, is_processing: nil, id_task: 11, n_prompt_tokens: 2000)
        ]
        XCTAssertEqual(IdleWatchdogEvaluator.evaluateLLMSlots(missingFlag, now: now).signal, .unknown)

        let allIdle = [
            SlotInfo(id: 0, n_ctx: 262144, is_processing: false, id_task: 10, n_prompt_tokens: 1000),
            SlotInfo(id: 1, n_ctx: 262144, is_processing: false, id_task: 12, n_prompt_tokens: 800)
        ]
        if case .idle = IdleWatchdogEvaluator.evaluateLLMSlots(allIdle, now: now).signal {} else {
            XCTFail("Expected .idle only when ALL slots are explicitly idle")
        }
        XCTAssertEqual(IdleWatchdogEvaluator.evaluateLLMSlots([], now: now).signal, .unknown)
    }

    func testFreeTierWithoutPaidCUIsRestrictedFromSSHAndWebUI() {
        let freeAcc = AccountProfile(tier: .free)
        var rt = AccountRuntimeState()
        rt.cuBalance = 0
        XCTAssertTrue(rt.isFreeRuntimeRestricted(for: freeAcc))

        rt.cuBalance = 50
        XCTAssertFalse(rt.isFreeRuntimeRestricted(for: freeAcc))

        let proAcc = AccountProfile(tier: .colabPro)
        rt.cuBalance = 0
        XCTAssertFalse(rt.isFreeRuntimeRestricted(for: proAcc))
    }

    func testNonLLMHardwareFallbackWatchdogLifecycle() {
        let now = Date()
        let freshIdle = ActivitySignal.idle(observedAt: now.addingTimeInterval(-120))

        // Within 210s hardware probe staleness window -> triggers shutdown at timeout
        XCTAssertEqual(
            IdleWatchdogEvaluator.evaluate(
                isRunning: true, enableLLMModule: false, activitySignal: freshIdle,
                autoShutdownEnabled: true, idleSeconds: 40 * 60, timeoutMinutes: 40,
                warnBeforeMinutes: 2, alreadyWarned: true, now: now, allowHardwareFallback: true
            ),
            .shutdown
        )

        // Stale > 210s -> safely pauses without shutting down
        let staleIdle = ActivitySignal.idle(observedAt: now.addingTimeInterval(-260))
        XCTAssertEqual(
            IdleWatchdogEvaluator.evaluate(
                isRunning: true, enableLLMModule: false, activitySignal: staleIdle,
                autoShutdownEnabled: true, idleSeconds: 50 * 60, timeoutMinutes: 40,
                warnBeforeMinutes: 2, alreadyWarned: true, now: now, allowHardwareFallback: true
            ),
            .pausedNoSignal
        )
    }

    func testOpenCodeSyncPreservesExistingProvidersAndNeverWritesRawKey() throws {
        let tmp = FileManager.default.temporaryDirectory.appendingPathComponent("colab-opencode-test-\(UUID().uuidString)", isDirectory: true)
        defer { try? FileManager.default.removeItem(at: tmp) }
        let cfgURL = tmp.appendingPathComponent("opencode.json")
        let backupDir = tmp.appendingPathComponent("backups", isDirectory: true)
        try FileManager.default.createDirectory(at: tmp, withIntermediateDirectories: true)

        let existingJSON = """
        {
          "model": "anthropic/claude-sonnet-4",
          "provider": {
            "anthropic": { "name": "Official Anthropic" }
          }
        }
        """
        try Data(existingJSON.utf8).write(to: cfgURL)

        _ = try ClientIntegrations.syncOpenCode(
            baseURL: "https://demo-node.trycloudflare.com",
            modelAlias: "qwen3.8-27b-uncensored",
            configURL: cfgURL,
            backupDir: backupDir
        )

        let outRaw = try String(contentsOf: cfgURL, encoding: .utf8)
        let parsed = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(outRaw.utf8)) as? [String: Any])
        XCTAssertEqual(parsed["model"] as? String, "anthropic/claude-sonnet-4")

        let providers = try XCTUnwrap(parsed["provider"] as? [String: Any])
        XCTAssertNotNil(providers["anthropic"])
        let colab = try XCTUnwrap(providers["colab-tools"] as? [String: Any])
        let opts = try XCTUnwrap(colab["options"] as? [String: Any])
        XCTAssertEqual(opts["baseURL"] as? String, "https://demo-node.trycloudflare.com/v1")
        XCTAssertEqual(opts["apiKey"] as? String, "{env:COLAB_TOOLS_API_KEY}")
        XCTAssertFalse(outRaw.contains("sk-"))

        let backups = try FileManager.default.contentsOfDirectory(atPath: backupDir.path)
        XCTAssertEqual(backups.filter { $0.hasSuffix(".json.bak") }.count, 1)
    }

    func testCodexTOMLSyncIsIdempotentAndPreservesDefaultProvider() throws {
        let tmp = FileManager.default.temporaryDirectory.appendingPathComponent("colab-codex-test-\(UUID().uuidString)", isDirectory: true)
        defer { try? FileManager.default.removeItem(at: tmp) }
        let cfgURL = tmp.appendingPathComponent("config.toml")
        let backupDir = tmp.appendingPathComponent("backups", isDirectory: true)
        try FileManager.default.createDirectory(at: tmp, withIntermediateDirectories: true)

        let originalTOML = """
        model_provider = "openai"
        model = "o3"
        """
        try originalTOML.write(to: cfgURL, atomically: true, encoding: .utf8)

        _ = try ClientIntegrations.syncCodexCLI(
            baseURL: "https://node-1.trycloudflare.com",
            modelAlias: "qwen3.8-27b",
            configURL: cfgURL,
            backupDir: backupDir
        )
        _ = try ClientIntegrations.syncCodexCLI(
            baseURL: "https://node-2.trycloudflare.com/",
            modelAlias: "qwen3.8-27b",
            configURL: cfgURL,
            backupDir: backupDir
        )

        let out = try String(contentsOf: cfgURL, encoding: .utf8)
        XCTAssertTrue(out.contains(#"model_provider = "openai""#))
        XCTAssertTrue(out.contains(#"model = "o3""#))
        XCTAssertTrue(out.contains(#"base_url = "https://node-2.trycloudflare.com/v1""#))
        XCTAssertTrue(out.contains(#"env_key = "COLAB_TOOLS_API_KEY""#))
        XCTAssertFalse(out.contains("node-1.trycloudflare.com"))
        let markerCount = out.components(separatedBy: "# BEGIN COLAB-TOOLS MANAGED").count - 1
        XCTAssertEqual(markerCount, 1)
    }

    func testTwoStageQueueFallbackAndScheduledFireDate() throws {
        let round1 = PoliteQueuePolicy.targetsForRound(
            attempt: 1,
            primaryTargets: [.g4, .h100],
            fallbackEnabled: true,
            fallbackAfterRounds: 2,
            fallbackTarget: .l4,
            tier: .aiUltra
        )
        XCTAssertEqual(round1, [.g4, .h100])

        let round2 = PoliteQueuePolicy.targetsForRound(
            attempt: 2,
            primaryTargets: [.g4, .h100],
            fallbackEnabled: true,
            fallbackAfterRounds: 2,
            fallbackTarget: .l4,
            tier: .aiUltra
        )
        XCTAssertEqual(round2, [.g4, .h100])

        let round3 = PoliteQueuePolicy.targetsForRound(
            attempt: 3,
            primaryTargets: [.g4, .h100],
            fallbackEnabled: true,
            fallbackAfterRounds: 2,
            fallbackTarget: .l4,
            tier: .aiUltra
        )
        XCTAssertEqual(round3, [.g4, .h100, .l4])

        // Free tier must never inject paid fallback card
        let freeRound3 = PoliteQueuePolicy.targetsForRound(
            attempt: 3,
            primaryTargets: [.t4],
            fallbackEnabled: true,
            fallbackAfterRounds: 1,
            fallbackTarget: .h100,
            tier: .free
        )
        XCTAssertEqual(freeRound3, [.t4])

        // Scheduled fire date: before target -> today; after target -> tomorrow
        var cal = Calendar(identifier: .gregorian)
        cal.timeZone = TimeZone(secondsFromGMT: 8 * 3600)!
        let morning6 = try XCTUnwrap(cal.date(from: DateComponents(year: 2026, month: 5, day: 10, hour: 6, minute: 30)))
        let fireToday = PoliteQueuePolicy.nextScheduledFireDate(hour: 7, minute: 0, now: morning6, calendar: cal)
        XCTAssertEqual(PoliteQueuePolicy.dayStamp(for: fireToday, calendar: cal), "2026-05-10")
        XCTAssertEqual(cal.component(.hour, from: fireToday), 7)

        let morning8 = try XCTUnwrap(cal.date(from: DateComponents(year: 2026, month: 5, day: 10, hour: 8, minute: 15)))
        let fireTomorrow = PoliteQueuePolicy.nextScheduledFireDate(hour: 7, minute: 0, now: morning8, calendar: cal)
        XCTAssertEqual(PoliteQueuePolicy.dayStamp(for: fireTomorrow, calendar: cal), "2026-05-11")
    }

    func testAllocationInsightsSixSlotHeatmapAndRecommendation() throws {
        var cal = Calendar(identifier: .gregorian)
        cal.timeZone = TimeZone(secondsFromGMT: 8 * 3600)!
        let h06 = try XCTUnwrap(cal.date(from: DateComponents(year: 2026, month: 5, day: 10, hour: 6, minute: 15)))
        let h21 = try XCTUnwrap(cal.date(from: DateComponents(year: 2026, month: 5, day: 10, hour: 21, minute: 0)))

        var insights = AllocationInsights()
        insights.record(hardware: "NVIDIA RTX PRO 6000", succeeded: true, at: h06, calendar: cal)
        insights.record(hardware: "G4", succeeded: true, at: h06, calendar: cal)
        insights.record(hardware: "G4", succeeded: false, at: h06, calendar: cal)
        insights.record(hardware: "G4", succeeded: false, at: h21, calendar: cal)
        insights.record(hardware: "G4", succeeded: false, at: h21, calendar: cal)

        let slots = insights.sixSlotHeatmap(for: "G4")
        XCTAssertEqual(slots.count, 6)
        XCTAssertEqual(slots[1].label, "04–08")
        XCTAssertEqual(slots[1].attempts, 3)
        XCTAssertEqual(slots[1].successes, 2)
        XCTAssertEqual(slots[5].attempts, 2)
        XCTAssertEqual(slots[5].successes, 0)

        let summary = try XCTUnwrap(insights.recommendedWindowSummary(for: "G4"))
        XCTAssertTrue(summary.contains("04–08"))
        XCTAssertTrue(summary.contains("67%"))
    }

    func testPerAccountIsolationAndUnmanagedConflictProtection() throws {
        let tmp = FileManager.default.temporaryDirectory.appendingPathComponent("colab-iso-\(UUID().uuidString)", isDirectory: true)
        defer { try? FileManager.default.removeItem(at: tmp) }
        let ocURL = tmp.appendingPathComponent("opencode.json")
        let cxURL = tmp.appendingPathComponent("config.toml")
        let bakDir = tmp.appendingPathComponent("backups", isDirectory: true)
        try FileManager.default.createDirectory(at: tmp, withIntermediateDirectories: true)

        let idA = ClientIntegrations.identifiers(for: UUID())
        let idB = ClientIntegrations.identifiers(for: UUID())
        XCTAssertNotEqual(idA.openCodeProviderKey, idB.openCodeProviderKey)
        XCTAssertNotEqual(idA.codexTableKey, idB.codexTableKey)
        XCTAssertNotEqual(idA.envKey, idB.envKey)

        // Sync both A and B into the same files without clobbering each other
        _ = try ClientIntegrations.syncOpenCode(
            baseURL: "https://node-a.trycloudflare.com", modelAlias: "model-a",
            providerKey: idA.openCodeProviderKey, envKey: idA.envKey,
            configURL: ocURL, backupDir: bakDir
        )
        _ = try ClientIntegrations.syncOpenCode(
            baseURL: "https://node-b.trycloudflare.com", modelAlias: "model-b",
            providerKey: idB.openCodeProviderKey, envKey: idB.envKey,
            configURL: ocURL, backupDir: bakDir
        )
        let ocParsed = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: ocURL)) as? [String: Any])
        let providers = try XCTUnwrap(ocParsed["provider"] as? [String: Any])
        XCTAssertNotNil(providers[idA.openCodeProviderKey])
        XCTAssertNotNil(providers[idB.openCodeProviderKey])

        // Unmanaged OpenCode provider with same key must be rejected
        var manualRoot = ocParsed
        var manualProviders = providers
        manualProviders["custom-user"] = ["name": "User Manual", "options": ["apiKey": "user-key"]]
        manualRoot["provider"] = manualProviders
        try JSONSerialization.data(withJSONObject: manualRoot).write(to: ocURL)
        XCTAssertThrowsError(try ClientIntegrations.syncOpenCode(
            baseURL: "https://node-a.trycloudflare.com", modelAlias: "model-a",
            providerKey: "custom-user", envKey: idA.envKey,
            configURL: ocURL, backupDir: bakDir
        ))

        // Unmanaged Codex table conflict & invalid table key must both throw
        try "[model_providers.\(idA.codexTableKey)]\nname = \"Manual\"\n".write(to: cxURL, atomically: true, encoding: .utf8)
        XCTAssertThrowsError(try ClientIntegrations.syncCodexCLI(
            baseURL: "https://node-a.trycloudflare.com", modelAlias: "model-a",
            providerTableKey: idA.codexTableKey, envKey: idA.envKey,
            configURL: cxURL, backupDir: bakDir
        ))
        XCTAssertThrowsError(try ClientIntegrations.syncCodexCLI(
            baseURL: "https://node-a.trycloudflare.com", modelAlias: "model-a",
            providerTableKey: "bad]\n[evil", envKey: idA.envKey,
            configURL: cxURL, backupDir: bakDir
        ))
    }

    func testCodexInvalidUTF8RefusesOverwriteAndCreatesPrivate0600Backup() throws {
        let tmp = FileManager.default.temporaryDirectory.appendingPathComponent("colab-utf8-\(UUID().uuidString)", isDirectory: true)
        defer { try? FileManager.default.removeItem(at: tmp) }
        let cxURL = tmp.appendingPathComponent("config.toml")
        let bakDir = tmp.appendingPathComponent("backups", isDirectory: true)
        try FileManager.default.createDirectory(at: tmp, withIntermediateDirectories: true)

        // Invalid UTF-8 bytes must abort without clobbering file
        let badBytes = Data([0xFF, 0xFE, 0x00, 0x41])
        try badBytes.write(to: cxURL)
        XCTAssertThrowsError(try ClientIntegrations.syncCodexCLI(
            baseURL: "https://node-ok.trycloudflare.com", modelAlias: "qwen",
            configURL: cxURL, backupDir: bakDir
        ))
        XCTAssertEqual(try Data(contentsOf: cxURL), badBytes)

        // Valid config sync creates 0700 backup dir and 0600 backup file
        try "model_provider = \"openai\"\n".write(to: cxURL, atomically: true, encoding: .utf8)
        _ = try ClientIntegrations.syncCodexCLI(
            baseURL: "https://node-ok.trycloudflare.com", modelAlias: "qwen",
            configURL: cxURL, backupDir: bakDir
        )
        let dirAttrs = try FileManager.default.attributesOfItem(atPath: bakDir.path)
        XCTAssertEqual((dirAttrs[.posixPermissions] as? NSNumber)?.intValue, 0o700)
        let bakFiles = try FileManager.default.contentsOfDirectory(atPath: bakDir.path).filter { $0.hasSuffix(".bak") }
        XCTAssertFalse(bakFiles.isEmpty)
        let bakAttrs = try FileManager.default.attributesOfItem(atPath: bakDir.appendingPathComponent(bakFiles[0]).path)
        XCTAssertEqual((bakAttrs[.posixPermissions] as? NSNumber)?.intValue, 0o600)
    }

    func testScheduleEvaluationGraceWindowAndMissedOneShotExpiry() throws {
        var cal = Calendar(identifier: .gregorian)
        cal.timeZone = TimeZone(secondsFromGMT: 8 * 3600)!
        let fireAt = try XCTUnwrap(cal.date(from: DateComponents(year: 2026, month: 5, day: 10, hour: 7, minute: 0)))

        // Before target -> waiting
        let early = fireAt.addingTimeInterval(-30)
        XCTAssertEqual(PoliteQueuePolicy.evaluateSchedule(fireAt: fireAt, repeatDaily: false, hour: 7, minute: 0, now: early, calendar: cal), .waiting)

        // 3 minutes late (within 10m grace window) -> triggerNow
        let withinGrace = fireAt.addingTimeInterval(180)
        XCTAssertEqual(PoliteQueuePolicy.evaluateSchedule(fireAt: fireAt, repeatDaily: false, hour: 7, minute: 0, now: withinGrace, calendar: cal), .triggerNow(nextFireAt: nil))

        // 20 minutes late (Mac woke up late) -> one-shot expires cleanly with nil nextFireAt
        let missed = fireAt.addingTimeInterval(20 * 60)
        XCTAssertEqual(PoliteQueuePolicy.evaluateSchedule(fireAt: fireAt, repeatDaily: false, hour: 7, minute: 0, now: missed, calendar: cal), .missedExpired(nextFireAt: nil))

        // Daily repeat missed -> advances to next day without firing today
        if case .missedExpired(let next) = PoliteQueuePolicy.evaluateSchedule(fireAt: fireAt, repeatDaily: true, hour: 7, minute: 0, now: missed, calendar: cal) {
            let nextUnwrapped = try XCTUnwrap(next)
            XCTAssertEqual(PoliteQueuePolicy.dayStamp(for: nextUnwrapped, calendar: cal), "2026-05-11")
        } else {
            XCTFail("Expected missedExpired with next day fire date")
        }
    }

    func testCorruptedAllocationInsightsAndUnknownEnumDecodeSafely() throws {
        // Malformed extreme/negative/inverted counts must clamp safely without overflow
        let corruptJSON = """
        {
          "bucketsByHardware": {
            "G4": {
              "6": { "attempts": -5, "successes": 999 },
              "7": { "attempts": 9223372036854775807, "successes": 9223372036854775807 },
              "99": { "attempts": 10, "successes": 5 }
            }
          }
        }
        """
        let decodedInsights = try JSONDecoder().decode(AllocationInsights.self, from: Data(corruptJSON.utf8))
        let slots = decodedInsights.sixSlotHeatmap(for: "G4")
        XCTAssertEqual(slots[1].attempts, AllocationBucket.maxSafeCount)
        XCTAssertEqual(slots[1].successes, AllocationBucket.maxSafeCount)
        XCTAssertNotNil(decodedInsights.recommendedWindowSummary(for: "G4"))

        // Unknown queueFallbackTarget enum string must fall back per-field instead of failing entire AccountProfile decode
        let futureConfigJSON = """
        {
          "id": "22222222-2222-2222-2222-222222222222",
          "displayName": "FutureProof",
          "email": "user@example.com",
          "colorTag": "#F59E0B",
          "tier": "colab_pro",
          "authMode": "system_oauth2",
          "sessionName": "llm-coder",
          "preferredAccelerator": "AUTO_PRO",
          "highMem": false,
          "enableLLMModule": true,
          "queueTargets": ["A100", "L4"],
          "queueFallbackEnabled": true,
          "queueFallbackTarget": "B200_FUTURE_UNKNOWN",
          "autoShutdownEnabled": true,
          "idleTimeoutMinutes": 40,
          "warnBeforeMinutes": 2,
          "driveScriptPath": "/content/drive/MyDrive/colab-llm/scripts/start_server.sh",
          "autoSyncCCSwitch": true,
          "ccSwitchProviderId": "colab-qwen",
          "ccSwitchProviderName": "Colab Qwen",
          "localEnvPath": ""
        }
        """
        let decodedAcc = try JSONDecoder().decode(AccountProfile.self, from: Data(futureConfigJSON.utf8))
        XCTAssertEqual(decodedAcc.displayName, "FutureProof")
        // Colab Pro primary [A100, L4] -> default fallback excludes primary cards -> T4
        XCTAssertEqual(decodedAcc.queueFallbackTarget, .t4)
    }

    func testDefaultCopySnippetsNeverLeakRawSecretUnlessExplicitlyRequested() {
        let safeEnv = ClientIntegrations.exportEnvSnippet(
            protocolKind: .openAI,
            baseURL: "https://demo.trycloudflare.com",
            modelAlias: "qwen",
            apiKey: "sk-real-secret-token",
            envKey: "COLAB_TOOLS_API_KEY_TEST",
            includeRawSecret: false
        )
        XCTAssertFalse(safeEnv.contains("sk-real-secret-token"))
        XCTAssertTrue(safeEnv.contains("COLAB_TOOLS_API_KEY_TEST"))

        let secretEnv = ClientIntegrations.exportEnvSnippet(
            protocolKind: .openAI,
            baseURL: "https://demo.trycloudflare.com",
            modelAlias: "qwen",
            apiKey: "sk-real-secret-token",
            envKey: "COLAB_TOOLS_API_KEY_TEST",
            includeRawSecret: true
        )
        XCTAssertTrue(secretEnv.contains("sk-real-secret-token"))
    }
}
