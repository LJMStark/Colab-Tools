import Foundation

public struct AllocationBucket: Codable, Equatable, Sendable {
    public static let maxSafeCount = 1_000_000

    public var attempts: Int = 0
    public var successes: Int = 0

    public init(attempts: Int = 0, successes: Int = 0) {
        let clampedAttempts = max(0, min(Self.maxSafeCount, attempts))
        self.attempts = clampedAttempts
        self.successes = max(0, min(clampedAttempts, successes))
    }

    private enum CodingKeys: String, CodingKey {
        case attempts, successes
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        let rawAttempts = (try? c.decode(Int.self, forKey: .attempts)) ?? 0
        let rawSuccesses = (try? c.decode(Int.self, forKey: .successes)) ?? 0
        self.init(attempts: rawAttempts, successes: rawSuccesses)
    }
}

public struct TimeSlotHitRate: Identifiable, Equatable, Sendable {
    public let id: Int // 0..5 representing 00-04 .. 20-24
    public let label: String
    public let attempts: Int
    public let successes: Int

    public var rate: Double? {
        guard attempts > 0 else { return nil }
        return min(1.0, max(0.0, Double(successes) / Double(attempts)))
    }

    public init(id: Int, label: String, attempts: Int, successes: Int) {
        self.id = id
        self.label = label
        let safeAtt = max(0, min(AllocationBucket.maxSafeCount, attempts))
        self.attempts = safeAtt
        self.successes = max(0, min(safeAtt, successes))
    }
}

/// Strictly local, privacy-preserving allocation hit-rate recorder.
/// Tracks per-hardware 24-hour bucket counts in `~/.config/colab-tools/allocation-stats.json`
/// so users can identify off-peak golden hours for scarce GPUs (G4 / H100 / A100) without telemetry upload.
public struct AllocationInsights: Codable, Equatable, Sendable {
    /// Map of normalized hardware key (e.g. "G4", "H100") -> hour string ("0"..."23") -> bucket.
    public var bucketsByHardware: [String: [String: AllocationBucket]] = [:]

    public static let defaultFileURL = AppSettings.configDir
        .appendingPathComponent("allocation-stats.json")

    public init(bucketsByHardware: [String: [String: AllocationBucket]] = [:]) {
        self.bucketsByHardware = bucketsByHardware
    }

    private enum CodingKeys: String, CodingKey {
        case bucketsByHardware
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        let rawMap = (try? c.decode([String: [String: AllocationBucket]].self, forKey: .bucketsByHardware)) ?? [:]
        var sanitized: [String: [String: AllocationBucket]] = [:]
        for (hw, hourMap) in rawMap {
            let normHW = Self.normalizeHardwareKey(hw)
            guard !normHW.isEmpty else { continue }
            var cleanHours: [String: AllocationBucket] = sanitized[normHW] ?? [:]
            for (hKey, bucket) in hourMap {
                guard let hInt = Int(hKey), (0...23).contains(hInt) else { continue }
                let existing = cleanHours[String(hInt)] ?? AllocationBucket()
                let mergedAtt = min(AllocationBucket.maxSafeCount, existing.attempts + bucket.attempts)
                let mergedSuc = min(mergedAtt, existing.successes + bucket.successes)
                cleanHours[String(hInt)] = AllocationBucket(attempts: mergedAtt, successes: mergedSuc)
            }
            if !cleanHours.isEmpty {
                sanitized[normHW] = cleanHours
            }
        }
        self.bucketsByHardware = sanitized
    }

    public static func normalizeHardwareKey(_ raw: String) -> String {
        let u = raw.uppercased()
        if u.contains("G4") || u.contains("6000") { return "G4" }
        if u.contains("H100") { return "H100" }
        if u.contains("A100") { return "A100" }
        if u.contains("L4") { return "L4" }
        if u.contains("T4") { return "T4" }
        if u.contains("V6E") { return "v6e-1" }
        if u.contains("V5E") { return "v5e-1" }
        return raw.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    public mutating func record(hardware: String, succeeded: Bool, at date: Date = Date(), calendar: Calendar = .current) {
        let key = Self.normalizeHardwareKey(hardware)
        guard !key.isEmpty else { return }
        let hour = calendar.component(.hour, from: date)
        let hourKey = String(max(0, min(23, hour)))
        var hwMap = bucketsByHardware[key] ?? [:]
        let old = hwMap[hourKey] ?? AllocationBucket()
        let nextAtt = min(AllocationBucket.maxSafeCount, old.attempts + 1)
        let nextSuc = min(nextAtt, old.successes + (succeeded ? 1 : 0))
        hwMap[hourKey] = AllocationBucket(attempts: nextAtt, successes: nextSuc)
        bucketsByHardware[key] = hwMap
    }

    public func totalAttempts(for hardware: String) -> Int {
        let key = Self.normalizeHardwareKey(hardware)
        return (bucketsByHardware[key]?.values ?? [:].values).reduce(0) { acc, b in
            min(AllocationBucket.maxSafeCount, acc + max(0, b.attempts))
        }
    }

    /// Aggregates 24 hourly buckets into 6 crisp 4-hour blocks (`00–04` .. `20–24`).
    public func sixSlotHeatmap(for hardware: String) -> [TimeSlotHitRate] {
        let key = Self.normalizeHardwareKey(hardware)
        let hwMap = bucketsByHardware[key] ?? [:]
        let labels = ["00–04", "04–08", "08–12", "12–16", "16–20", "20–24"]
        return (0..<6).map { slot in
            var att = 0
            var suc = 0
            for h in (slot * 4)..<(slot * 4 + 4) {
                if let b = hwMap[String(h)] {
                    att = min(AllocationBucket.maxSafeCount, att + max(0, b.attempts))
                    suc = min(att, suc + max(0, min(b.attempts, b.successes)))
                }
            }
            return TimeSlotHitRate(id: slot, label: labels[slot], attempts: att, successes: suc)
        }
    }

    /// Returns a friendly Chinese recommendation when enough local observations (>= 3) exist.
    /// Uses a strict total ordering (rate desc -> attempts desc -> slot id asc) to guarantee transitivity.
    public func recommendedWindowSummary(for hardware: String) -> String? {
        let key = Self.normalizeHardwareKey(hardware)
        let slots = sixSlotHeatmap(for: key)
        let total = slots.reduce(0) { min(AllocationBucket.maxSafeCount, $0 + $1.attempts) }
        guard total >= 3 else { return nil }

        let ranked = slots
            .filter { $0.attempts > 0 && ($0.rate ?? 0) > 0 }
            .sorted { lhs, rhs in
                let lr = lhs.rate ?? 0
                let rr = rhs.rate ?? 0
                if lr != rr { return lr > rr }
                if lhs.attempts != rhs.attempts { return lhs.attempts > rhs.attempts }
                return lhs.id < rhs.id
            }
        guard let best = ranked.first, let rate = best.rate else {
            return "近期 \(key) 本地尚无命中记录，建议尝试清晨低峰预约"
        }
        let pct = Int((rate * 100).rounded())
        return "本机历史 \(key) 在 \(best.label) 命中率最高（\(pct)% · \(best.successes)/\(best.attempts)）"
    }

    public static func load(from url: URL = defaultFileURL) -> AllocationInsights {
        guard let data = try? Data(contentsOf: url),
              let decoded = try? JSONDecoder().decode(AllocationInsights.self, from: data) else {
            return AllocationInsights()
        }
        return decoded
    }

    public func save(to url: URL = defaultFileURL) {
        try? FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        let enc = JSONEncoder()
        enc.outputFormatting = [.prettyPrinted, .sortedKeys]
        if let data = try? enc.encode(self) {
            try? data.write(to: url, options: .atomic)
        }
    }
}
